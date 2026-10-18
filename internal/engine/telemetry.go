package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
const childScopeStatement = "each entry covers the Terraform process and only the descendants it " +
	"waited for before exiting. Provider plugins left running at normal exit or timeout are " +
	"excluded, including those later killed by tf-mut. user_cpu_ms and system_cpu_ms are processor " +
	"times; peak_rss_kb is the maximum resident set size in kibibytes, the kernel's ru_maxrss " +
	"unit on Linux"

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
	// runID is the run's one identifier, minted once where the run span
	// starts: the report's telemetry block, the progress lines and the
	// warnings all carry it.
	runID string
	trip  *tripOutcome
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

// identify mints the run's one identifier, immediately after the run span
// starts: the span's trace ID when tracing is enabled, a random identifier of
// the same 32-hex-character shape otherwise. The report's telemetry block,
// the progress lines and the warnings all carry it.
func (c *runCollector) identify() string {
	marked := time.Now()
	c.runID = runIdentifier(c.runSpan)
	c.overhead.Add(int64(time.Since(marked)))

	return c.runID
}

// runIdentifier is the run span's trace ID when the span carries one —
// tracing enabled — and a random identifier of the same shape otherwise.
func runIdentifier(span *tracing.Span) string {
	if id := span.TraceID(); id != "" {
		return id
	}

	return randomRunID()
}

// randomRunID mints 16 random bytes — the 128-bit OpenTelemetry trace-ID
// shape — hex-encoded. crypto/rand.Read always fills the buffer without error.
func randomRunID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])

	return hex.EncodeToString(bytes[:])
}

// endRun closes the run span. It is safe to call once, at the very end of the
// run, after every phase has ended.
func (c *runCollector) endRun() {
	marked := time.Now()
	c.runSpan.End()
	c.overhead.Add(int64(time.Since(marked)))
}

// stampStart renders an instant as the RFC 3339 UTC timestamp the telemetry
// publishes beside every duration.
func stampStart(at time.Time) string {
	return at.UTC().Format(time.RFC3339Nano)
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
		c.phases = append(c.phases, report.PhaseTiming{
			Phase:      name,
			DurationMS: elapsed.Milliseconds(),
			StartedAt:  stampStart(start),
		})
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
		RunID:      c.runID,
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
		m.phases = append(m.phases, report.PhaseTiming{
			Phase:      name,
			DurationMS: elapsed.Milliseconds(),
			StartedAt:  stampStart(start),
		})
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
		StartedAt: stampStart(m.started),
		WallMS:    time.Since(m.started).Milliseconds(),
		Phases:    m.phases,
		Children:  m.children,
	}
	m.run.overhead.Add(int64(time.Since(marked)))

	return telemetry
}

// recordError notes the operational failure the report carries on the
// mutant's span, so the trace shows the same failure. It runs while the span
// is still open, before finish ends it.
func (m *mutantCollector) recordError(message string) {
	marked := time.Now()
	m.mutantSpan.RecordError(message)
	m.run.overhead.Add(int64(time.Since(marked)))
}
