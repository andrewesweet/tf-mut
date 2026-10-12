package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// The named phases of the grading pipeline and of one mutant's evaluation.
// Each name identifies a step the code executes; a phase is recorded only
// where the step ran.
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
	phaseSuggestVerification = "suggest-verification"
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
// It is always on. Synchronous recording overhead is measured and published
// next to the numbers so consumers can account for instrumentation. Verdicts
// never read this record. When an OTLP endpoint is configured, the same
// brackets drive spans; without one the tracer exports nothing.
type runCollector struct {
	tracer *tracing.Tracer
	mutex  sync.Mutex
	phases []report.PhaseTiming
	// children aggregates by subcommand; order remembers first-use order so
	// the published list is deterministic.
	children map[string]*report.ProcessUsage
	order    []string
	overhead atomic.Int64
	runSpan  *tracing.Span
	trip     *tripOutcome
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
		tracer:   tracer,
		children: map[string]*report.ProcessUsage{},
		phases:   []report.PhaseTiming{},
		base:     ctx,
	}
}

// beginRun starts the run span and returns the context its phases nest under.
func (c *runCollector) beginRun(command string) context.Context {
	marked := time.Now()
	c.base, c.runSpan = c.tracer.Start(c.base, spanRun, tracing.String(attrCommand, command))
	c.overhead.Add(int64(time.Since(marked)))

	return c.base
}

// endRun closes the run span. It is safe to call once, at the very end of the
// run, after every phase has ended.
func (c *runCollector) endRun() {
	marked := time.Now()
	c.runSpan.End()
	c.overhead.Add(int64(time.Since(marked)))
}

// phase times one named step of the run. The returned function ends the step
// and records its wall time; a step that panics never records.
func (c *runCollector) phase(name string) func(...tracing.Attribute) {
	start := time.Now()
	_, span := c.tracer.Start(c.base, spanPhase, tracing.String(attrPhase, name))
	c.overhead.Add(int64(time.Since(start)))

	return func(attributes ...tracing.Attribute) {
		elapsed := time.Since(start)

		marked := time.Now()
		span.End(attributes...)
		c.mutex.Lock()
		c.phases = append(c.phases, report.PhaseTiming{Phase: name, DurationMS: elapsed.Milliseconds()})
		c.mutex.Unlock()
		c.overhead.Add(int64(time.Since(marked)))
	}
}

// child records one child Terraform process's usage under its subcommand.
func (c *runCollector) child(command string, usage tfexec.Usage, started bool) []tracing.Attribute {
	if !started {
		return nil
	}

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
	attributes := []tracing.Attribute{
		tracing.String("tf-mut.child.command", command),
		tracing.Int("tf-mut.child.user_cpu_ms", usage.UserCPU.Milliseconds()),
		tracing.Int("tf-mut.child.system_cpu_ms", usage.SystemCPU.Milliseconds()),
		tracing.Int("tf-mut.child.peak_rss_kb", usage.PeakRSSKB),
	}
	c.overhead.Add(int64(time.Since(marked)))

	return attributes
}

// finish completes the run-level block.
func (c *runCollector) finish() *report.Telemetry {
	children := make([]report.ProcessUsage, 0, len(c.order))
	for _, command := range c.order {
		children = append(children, *c.children[command])
	}

	return &report.Telemetry{
		Phases:     c.phases,
		Children:   children,
		OverheadMS: c.overhead.Load() / int64(time.Millisecond),
		ChildScope: childScopeStatement,
	}
}

// mutant begins one mutant's evaluation record. The id and operator name the
// mutant's span; the phases and children it records nest under it.
func (c *runCollector) mutant(ctx context.Context, id, operator string) *mutantCollector {
	marked := time.Now()
	base, span := c.tracer.Start(ctx, spanMutant,
		tracing.String(attrMutantID, id), tracing.String(attrOperator, operator))

	collector := &mutantCollector{
		run:        c,
		started:    marked,
		base:       base,
		phases:     []report.PhaseTiming{},
		children:   []report.ProcessUsage{},
		mutantSpan: span,
	}
	c.overhead.Add(int64(time.Since(marked)))

	return collector
}

// phase times one named step of the mutant's evaluation. It runs on the
// worker goroutine that owns the collector, so the only lock it takes is the
// run aggregate's, through the overhead ledger.
func (m *mutantCollector) phase(name string) func(...tracing.Attribute) {
	start := time.Now()
	_, span := m.run.tracer.Start(m.base, spanPhase, tracing.String(attrPhase, name))
	m.run.overhead.Add(int64(time.Since(start)))

	return func(attributes ...tracing.Attribute) {
		elapsed := time.Since(start)

		marked := time.Now()
		span.End(attributes...)
		m.phases = append(m.phases, report.PhaseTiming{Phase: name, DurationMS: elapsed.Milliseconds()})
		m.run.overhead.Add(int64(time.Since(marked)))
	}
}

// child records one child process of this evaluation, per invocation, and
// merges it into the run aggregate.
func (m *mutantCollector) child(command string, usage tfexec.Usage, started bool) []tracing.Attribute {
	if !started {
		return nil
	}

	marked := time.Now()

	m.children = append(m.children, report.ProcessUsage{
		Command:     command,
		Invocations: 1,
		UserCPUMS:   usage.UserCPU.Milliseconds(),
		SystemCPUMS: usage.SystemCPU.Milliseconds(),
		PeakRSSKB:   usage.PeakRSSKB,
	})

	m.run.overhead.Add(int64(time.Since(marked)))
	return m.run.child(command, usage, true)
}

// finish completes the mutant's record and closes its span.
func (m *mutantCollector) finish() *report.MutantTelemetry {
	marked := time.Now()
	m.mutantSpan.End()

	telemetry := &report.MutantTelemetry{
		WallMS:   time.Since(m.started).Milliseconds(),
		Phases:   m.phases,
		Children: m.children,
	}
	m.run.overhead.Add(int64(time.Since(marked)))

	return telemetry
}
