package report

// The performance-telemetry entities of report-2.7.0. They record where a
// run's time went — the wall time of the named phases the pipeline executed,
// and the operating system's accounting for every child Terraform process —
// so that a follow-up measurement can compare reality against the cost model.
// Telemetry decides nothing: no verdict, score or gate reads it.

// PhaseTiming is one named phase of the run or of one mutant's evaluation.
// The names are the engine's own steps, spelled in internal/engine.
type PhaseTiming struct {
	// Phase is the step's name, e.g. warm-init or sandbox.
	Phase string `json:"phase"`
	// DurationMS is the step's wall time in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// StartedAt is the step's start as an RFC 3339 timestamp in UTC
	// (2.16.0) — the moment the duration is measured from, published beside
	// it so a phase can be tied to everything else the wall clock recorded.
	StartedAt string `json:"started_at"`
}

// ProcessUsage is the operating system's accounting for one child Terraform
// process, or the aggregate of several under one subcommand.
// For accounting scope, see process_usage in docs/schema/report-2.13.0.json.
type ProcessUsage struct {
	// Command is the Terraform subcommand: version, init, providers, fmt,
	// validate or test.
	Command string `json:"command"`
	// Invocations is the number of child processes aggregated here: one in a
	// mutant's own record, possibly many in the run-level aggregate.
	Invocations int `json:"invocations"`
	// UserCPUMS and SystemCPUMS are the child's user and system processor
	// time in milliseconds.
	UserCPUMS   int64 `json:"user_cpu_ms"`
	SystemCPUMS int64 `json:"system_cpu_ms"`
	// PeakRSSKB is the maximum resident set size in kibibytes — the kernel's
	// ru_maxrss unit on Linux.
	// Aggregates keep the largest peak across invocations, rather than a sum.
	PeakRSSKB int64 `json:"peak_rss_kb"`
}

// MutantTelemetry is one mutant's execution record: where its own evaluation
// spent wall time and what its child Terraform processes cost. Present on
// every mutant this run executed; absent for a verdict replayed from the
// verdict cache, assigned statically, or not executed at all.
type MutantTelemetry struct {
	// StartedAt is the evaluation's start as an RFC 3339 timestamp in UTC
	// (2.16.0): the instant WallMS is measured from, so an observation made
	// outside this process — an orphaned process, a log line — can be tied to
	// the mutant it belongs to.
	StartedAt string `json:"started_at"`
	// WallMS is the mutant's whole evaluation wall time in milliseconds.
	WallMS int64 `json:"wall_ms"`
	// Phases are the steps its evaluation executed, in execution order:
	// sandbox, test, and — where the verdict needed them — validate,
	// fingerprint, volatility-rerun and oracle.
	Phases []PhaseTiming `json:"phases"`
	// Children carries one entry per child Terraform process the evaluation
	// started, in start order.
	Children []ProcessUsage `json:"children"`
}

// Telemetry is the run-level performance record (2.7.0). Present for the
// commands that execute Terraform through the grading pipeline: run, preview
// and suggest, and the graded run behind curate. Characterise and todos do
// not publish this block.
type Telemetry struct {
	// RunID is the run's one identifier (2.11.0): the run span's trace ID when
	// tracing is enabled, a random identifier of the same 32-hex-character
	// shape otherwise. Every stderr line the tool writes during the run
	// carries it as a `[run_id] ` prefix, so stderr, report and trace can be
	// tied together.
	RunID string `json:"run_id"`
	// WallMS is the engine invocation's wall time in milliseconds, including
	// workspace cleanup, trace flushing and profile finalization.
	WallMS int64 `json:"wall_ms"`
	// Phases are the command's named steps. Ordering and overlap are
	// documented in docs/schema/report-2.7.0.json.
	Phases []PhaseTiming `json:"phases"`
	// Children aggregates every child Terraform process the command started,
	// by subcommand, including the processes each mutant's evaluation started.
	Children []ProcessUsage `json:"children"`
	// OverheadMS is synchronous recording time: timing and child bookkeeping,
	// span creation, attribute recording and span completion. It excludes
	// asynchronous export, shutdown and the clock reads that bracket recording.
	OverheadMS int64 `json:"overhead_ms"`
	// ChildScope states what the per-child numbers cover, so no consumer has
	// to know the rusage rule to read them honestly.
	ChildScope string `json:"child_scope"`
}
