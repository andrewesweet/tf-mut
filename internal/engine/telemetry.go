package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// The named phases of the grading pipeline and of one mutant's evaluation.
// Each name is a step the code actually has, in the order a sequential run
// executes them; a phase is recorded only where the step ran.
const (
	// Run-level phases.
	phaseVersion             = "version"
	phaseDiscovery           = "discovery"
	phaseWarmMaterialise     = "warm-materialise"
	phaseWarmBackend         = "warm-backend"
	phaseWarmInit            = "warm-init"
	phaseWarmSchema          = "warm-schema"
	phaseWarmFmt             = "warm-fmt"
	phaseGates               = "gates"
	phaseBaselineTest        = "baseline-test"
	phaseBaselineFingerprint = "baseline-fingerprint"
	phaseGeneration          = "generation"
	phaseSelection           = "selection"
	phaseCacheLoad           = "cache-load"
	phaseCacheStore          = "cache-store"
	phaseExecution           = "execution"
	phaseSuggest             = "suggest"
	phaseFinalise            = "finalise"

	// Per-mutant phases.
	phaseSandbox         = "sandbox"
	phaseTest            = "test"
	phaseValidate        = "validate"
	phaseFingerprint     = "fingerprint"
	phaseVolatilityRerun = "volatility-rerun"
	phaseOracle          = "oracle"
)

// Span names, one per level: the run, each mutant it graded, and each phase
// either recorded.
const (
	spanRun    = "tf-mut.run"
	spanMutant = "tf-mut.mutant"
	spanPhase  = "tf-mut.phase"

	// Span attributes.
	attrCommand  = "tf-mut.command"
	attrMutantID = "tf-mut.mutant_id"
	attrOperator = "tf-mut.operator"
	attrPhase    = "tf-mut.phase"
)

// childScopeStatement is what the report says the child-process numbers
// cover, so no consumer has to know the rusage rule to read them honestly.
const childScopeStatement = "each entry covers the terraform process and the descendants it " +
	"waited for before exiting: Terraform reaps its provider plugins, so their CPU and peak " +
	"memory are included. user_cpu_ms and system_cpu_ms are processor times; peak_rss_kb is " +
	"the maximum resident set size in kibibytes, the kernel's ru_maxrss unit on Linux"

// runCollector records one run's phase timings and child-process usage.
//
// It is always on: the numbers cost two clock reads per phase bracket, and the
// cost of the recording itself is measured and published next to the numbers,
// so the reader can judge the instrumentation by its own account. It decides
// nothing about any verdict. When the run configured an OTLP endpoint, the
// same brackets drive spans over the tracer; without one the tracer is
// disabled and every span call is a no-op.
type runCollector struct {
	started time.Time
	tracer  *tracing.Tracer
	mutex   sync.Mutex
	phases  []report.PhaseTiming
	// children aggregates by subcommand; order remembers first-use order so
	// the published list is deterministic.
	children map[string]*report.ProcessUsage
	order    []string
	overhead atomic.Int64
	runSpan  *tracing.Span
	// base parents every phase span to the run span; it is the collector's
	// parenting chain, not request state captured for later.
	base context.Context //nolint:containedctx // the parenting chain, not stored request state
}

// mutantCollector records one mutant's evaluation. Its phases and children
// are local to the worker goroutine that owns it, so they need no lock; the
// child usage is merged into the run aggregate under the run collector's
// lock.
type mutantCollector struct {
	run        *runCollector
	started    time.Time
	base       context.Context //nolint:containedctx // the parenting chain, not stored request state
	phases     []report.PhaseTiming
	children   []report.ProcessUsage
	mutantSpan *tracing.Span
}

func newRunCollector(ctx context.Context, tracer *tracing.Tracer) *runCollector {
	return &runCollector{
		started:  time.Now(),
		tracer:   tracer,
		children: map[string]*report.ProcessUsage{},
		phases:   []report.PhaseTiming{},
		base:     ctx,
	}
}

// beginRun starts the run span and returns the context its phases nest under.
func (c *runCollector) beginRun(command string) context.Context {
	c.base, c.runSpan = c.tracer.Start(c.base, spanRun, attribute.String(attrCommand, command))

	return c.base
}

// endRun closes the run span. It is safe to call once, at the very end of the
// run, after every phase has ended.
func (c *runCollector) endRun() {
	c.runSpan.End()
}

// phase times one named step of the run. The returned function ends the step
// and records its wall time; a step that panics never records.
func (c *runCollector) phase(name string) func() {
	start := time.Now()
	_, span := c.tracer.Start(c.base, spanPhase, attribute.String(attrPhase, name))

	return func() {
		elapsed := time.Since(start)
		span.End()

		marked := time.Now()
		c.mutex.Lock()
		c.phases = append(c.phases, report.PhaseTiming{Phase: name, DurationMS: elapsed.Milliseconds()})
		c.mutex.Unlock()
		c.overhead.Add(int64(time.Since(marked)))
	}
}

// child records one child Terraform process's usage under its subcommand.
func (c *runCollector) child(command string, usage tfexec.Usage) {
	marked := time.Now()
	c.mutex.Lock()

	aggregate, found := c.children[command]
	if !found {
		aggregate = &report.ProcessUsage{Command: command, Invocations: 0}
		c.children[command] = aggregate
		c.order = append(c.order, command)
	}

	aggregate.Invocations++
	aggregate.UserCPUMS += usage.UserCPU.Milliseconds()
	aggregate.SystemCPUMS += usage.SystemCPU.Milliseconds()
	if usage.PeakRSSKB > aggregate.PeakRSSKB {
		aggregate.PeakRSSKB = usage.PeakRSSKB
	}

	c.mutex.Unlock()
	c.overhead.Add(int64(time.Since(marked)))
}

// finish completes the run-level block.
func (c *runCollector) finish() *report.Telemetry {
	children := make([]report.ProcessUsage, 0, len(c.order))
	for _, command := range c.order {
		children = append(children, *c.children[command])
	}

	return &report.Telemetry{
		WallMS:     time.Since(c.started).Milliseconds(),
		Phases:     c.phases,
		Children:   children,
		OverheadMS: c.overhead.Load() / int64(time.Millisecond),
		ChildScope: childScopeStatement,
	}
}

// mutant begins one mutant's evaluation record. The id and operator name the
// mutant's span; the phases and children it records nest under it.
func (c *runCollector) mutant(ctx context.Context, id, operator string) *mutantCollector {
	base, span := c.tracer.Start(ctx, spanMutant,
		attribute.String(attrMutantID, id), attribute.String(attrOperator, operator))

	return &mutantCollector{
		run:        c,
		started:    time.Now(),
		base:       base,
		phases:     []report.PhaseTiming{},
		children:   []report.ProcessUsage{},
		mutantSpan: span,
	}
}

// phase times one named step of the mutant's evaluation. It runs on the
// worker goroutine that owns the collector, so the only lock it takes is the
// run aggregate's, through the overhead ledger.
func (m *mutantCollector) phase(name string) func() {
	start := time.Now()
	_, span := m.run.tracer.Start(m.base, spanPhase, attribute.String(attrPhase, name))

	return func() {
		elapsed := time.Since(start)
		span.End()

		marked := time.Now()
		m.phases = append(m.phases, report.PhaseTiming{Phase: name, DurationMS: elapsed.Milliseconds()})
		m.run.overhead.Add(int64(time.Since(marked)))
	}
}

// child records one child process of this evaluation, per invocation, and
// merges it into the run aggregate.
func (m *mutantCollector) child(command string, usage tfexec.Usage) {
	marked := time.Now()

	m.children = append(m.children, report.ProcessUsage{
		Command:     command,
		Invocations: 1,
		UserCPUMS:   usage.UserCPU.Milliseconds(),
		SystemCPUMS: usage.SystemCPU.Milliseconds(),
		PeakRSSKB:   usage.PeakRSSKB,
	})

	m.run.child(command, usage)
	m.run.overhead.Add(int64(time.Since(marked)))
}

// finish completes the mutant's record and closes its span.
func (m *mutantCollector) finish() *report.MutantTelemetry {
	m.mutantSpan.End()

	return &report.MutantTelemetry{
		WallMS:   time.Since(m.started).Milliseconds(),
		Phases:   m.phases,
		Children: m.children,
	}
}
