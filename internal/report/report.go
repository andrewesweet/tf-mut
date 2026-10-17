// Package report holds the value the engine returns and the renderings of it:
// the terminal renderer and the `WriteJSON`, `WriteSARIF`, `WriteMTE`,
// `WriteHTML`, `WriteJUnit` and `WriteMarkdown` reporters.
//
// Every reporter is derived from this single
// value, so no two of them can disagree about a verdict.
package report

// SchemaVersion is the version of the machine-readable report schema.
// See docs/schema/report-2.15.0.json for the versioned consumer contract.
const SchemaVersion = "2.15.0"

// Command names what produced a report.
type Command string

// The commands that produce a report.
const (
	// CommandRun executed the mutant population.
	CommandRun Command = "run"
	// CommandPreview generated the population without executing it.
	CommandPreview Command = "preview"
	// CommandSuggest executed the population and generated assertions for its
	// survivors.
	CommandSuggest Command = "suggest"
	// CommandCharacterise scaffolded, harvested and pinned a suite.
	CommandCharacterise Command = "characterise"
	// CommandCurate reported redundancy over an authoritative population.
	CommandCurate Command = "curate"
	// CommandTodos listed the open judgement points.
	CommandTodos Command = "todos"
)

// State is the aggregate verdict for one mutant.
type State string

// The mutant states, in precedence order. Every state is assigned by the
// normative table of the milestone spec: they are overlapping predicates
// without one, and file order must never change a verdict.
const (
	// Invalid marks a mutant that fails terraform validate. Excluded from scores.
	Invalid State = "Invalid"
	// Killed marks a mutant an assertion caught.
	Killed State = "Killed"
	// KilledByError marks a mutant Terraform's own evaluation caught.
	KilledByError State = "KilledByError"
	// Timeout marks a mutant that exceeded its execution budget.
	Timeout State = "Timeout"
	// Survived marks a mutant every executed run passed. It carries exactly one
	// diagnosis.
	Survived State = "Survived"
	// StructurallyUnassertable marks a fingerprint-identical mutant of a
	// construct with no plan or state projection. In the denominator, with a fix.
	StructurallyUnassertable State = "StructurallyUnassertable"
	// Unobservable marks a fingerprint-identical mutant of a construct that does
	// project, proven over a payload with no unknown value in the mutation's
	// forward cone (M3a.2; the whole-payload rule remains the floor wherever
	// the address mapping fails), or proven statically. See the Unobservable
	// state contract in docs/design/product-design.md. Excluded.
	Unobservable State = "Unobservable"
	// NoCoverage marks a mutant in a module no run block instantiates, assigned
	// statically. The claim is module-level absence and nothing finer.
	NoCoverage State = "NoCoverage"
	// Ignored marks a mutant a reasoned suppression or a configured exclusion
	// removed from the population. Excluded, counted.
	Ignored State = "Ignored"
	// Pending marks a mutant without a verdict. See the mutant-state table in
	// docs/design/product-design.md for its reporting and scoring contract.
	Pending State = "Pending"
)

// Diagnosis names why a survivor survived. Exactly one is assigned to every
// `Survived` mutant, by the precedence below.
type Diagnosis string

// The survivor diagnoses, in precedence order — first match wins.
const (
	// IndeterminateUnknownValues marks a fingerprint-identical survivor whose
	// payload carries an unknown value, so equality cannot be proven.
	IndeterminateUnknownValues Diagnosis = "indeterminate-unknown-values"
	// IndeterminateVolatility marks a survivor whose delta remained undecidable
	// after the mutant was re-run.
	IndeterminateVolatility Diagnosis = "indeterminate-volatility"
	// MockMasked is withdrawn (M3, issue #50, prove-or-withdraw): its positive
	// case cannot fire — a stable apply-mode delta in an optional-computed
	// attribute is attributable to the module, and a computed-only attribute's
	// mock value is either deterministic and identical on both sides or random
	// and masked by the volatility re-run, measured against hashicorp/aws
	// (docs/research/09-m3-real-provider-gate.md). The value stays declared so
	// the 2.1.0 schema remains additive over 2.0 documents; the oracle never
	// emits it again.
	//
	// Deprecated: never emitted since M3.
	MockMasked Diagnosis = "mock-masked"
	// WeakAssertion marks a survivor an assertion reads yet does not catch.
	WeakAssertion Diagnosis = "weak-assertion"
	// NoAssertion marks a survivor the output and local closure proves no
	// assertion reads.
	NoAssertion Diagnosis = "no-assertion"
	// Unasserted marks a survivor whose closure was defeated, so weak and absent
	// assertions cannot be honestly told apart.
	Unasserted Diagnosis = "unasserted"
)

// Position is a point in a source file.
type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Range is a source range, relative to the module closure root.
type Range struct {
	File  string   `json:"file"`
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Diagnostic is a Terraform diagnostic attached to a mutant.
type Diagnostic struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
	Range    *Range `json:"range,omitempty"`
	TestFile string `json:"test_file,omitempty"`
	TestRun  string `json:"test_run,omitempty"`
}

// RunOutcome is the result of one run block under one mutant.
type RunOutcome struct {
	File string `json:"file"`
	Run  string `json:"run"`
	// Phase is one (the plain classification run) or two (the verbose
	// fingerprint run), so a reader can see that phase two ran only where the
	// design says it should.
	Phase  int    `json:"phase"`
	Status string `json:"status"`
}

// Change is one masked observable difference between baseline and mutant.
type Change struct {
	Run     string `json:"run"`
	Path    string `json:"path"`
	Address string `json:"address,omitempty"`
	// Baseline and Mutant are canonical renderings; empty means the path was
	// absent from that side.
	Baseline string `json:"baseline"`
	Mutant   string `json:"mutant"`
	// Sensitive reports that Terraform marks this value — or an ancestor of it
	// — sensitive (2.2.0). Retention is not permission to render the value.
	Sensitive bool `json:"sensitive,omitempty"`
}

// Evidence is what a diagnosis carries, per the normative table. Every field is
// optional because each diagnosis names its own required subset.
type Evidence struct {
	// Delta is the masked observable difference.
	Delta []Change `json:"delta,omitempty"`
	// UnknownPaths names the addresses whose value the payload left unknown.
	UnknownPaths []string `json:"unknown_paths,omitempty"`
	// VolatileComponents names the payload paths the baseline mask removed.
	VolatileComponents []string `json:"volatile_components,omitempty"`
	// UnstableAttributes names the attributes that differed across the two runs
	// of the mutant itself.
	UnstableAttributes []string `json:"unstable_attributes,omitempty"`
	// Assertion locates the assertion that read the delta but did not catch it.
	Assertion string `json:"assertion,omitempty"`
	// ClosureVerdict records what the output and local closure concluded.
	ClosureVerdict string `json:"closure_verdict,omitempty"`
	// DefeatedBy names the construct that defeated the closure computation.
	DefeatedBy string `json:"defeated_by,omitempty"`
	// MockResource was carried only by the withdrawn mock-masked diagnosis;
	// the field stays declared so 2.0 documents remain readable.
	//
	// Deprecated: never emitted since M3.
	MockResource string `json:"mock_resource,omitempty"`
}

// Verdict is the classification of one mutant: the diagnosis where it has one,
// the evidence behind it, and the fix it names.
type Verdict struct {
	// Diagnosis is set only for `Survived`: the normative table gives diagnoses
	// to survivors and to nothing else. The excluded and unassertable states
	// still carry a message and a fix, because they are still findings.
	Diagnosis Diagnosis `json:"diagnosis,omitempty"`
	// Message states the finding in the reader's terms.
	Message string `json:"message"`
	// Fix names the change that would resolve it.
	Fix      string   `json:"fix"`
	Evidence Evidence `json:"evidence"`
}

// Suppression records an inline directive or configured exclusion.
type Suppression struct {
	// Kind is comment, config-operator, config-path or config-resource.
	Kind string `json:"kind"`
	// Operators lists the operator identifiers the directive named.
	Operators []string `json:"operators,omitempty"`
	// Reason is the mandatory justification; empty on a rejected directive.
	Reason string `json:"reason,omitempty"`
	// Accepted reports whether the directive suppressed anything. A directive
	// without a reason does not suppress: the finding stands and the directive
	// is reported here.
	Accepted bool   `json:"accepted"`
	Range    *Range `json:"range,omitempty"`
	// Mutants lists the identifiers the suppression applied to.
	Mutants []string `json:"mutants,omitempty"`
	// Rejection explains why a directive did not suppress.
	Rejection string `json:"rejection,omitempty"`
}

// Mutant is one mutation and everything learned about it.
type Mutant struct {
	ID       string `json:"id"`
	Operator string `json:"operator"`
	// Tier is the catalogue tier the operator belongs to.
	Tier string `json:"tier"`
	// Module is the closure-relative directory of the mutated module.
	Module string `json:"module"`
	// Site is the semantic address of the mutation site.
	Site string `json:"site"`
	// Resource is the resource address the site belongs to, when it has one.
	Resource string `json:"resource,omitempty"`
	Range    Range  `json:"range"`
	Diff     string `json:"diff"`
	State    State  `json:"state"`
	// Verdict carries the diagnosis and its evidence, for states that have one.
	Verdict *Verdict `json:"verdict,omitempty"`
	// Runs records the per-run outcomes orthogonally to the aggregate state.
	Runs []RunOutcome `json:"runs"`
	// Diagnostics are the Terraform diagnostics the mutant produced, in the
	// engine's canonical order (test file, run, severity, summary, detail,
	// range) rather than Terraform's nondeterministic stream order.
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	// ExecutedRuns is the number of run blocks that actually executed.
	ExecutedRuns int `json:"executed_runs"`
	// Validated records whether lazy validation ran for this mutant.
	Validated bool `json:"validated"`
	// DeadlineReached records whether this mutant's Terraform test invocation
	// reached its execution budget, whatever verdict the runs that finished
	// first earned: the verdict precedence is unchanged, so the deadline is
	// visible behind a Killed or KilledByError verdict too (2.9.0).
	DeadlineReached bool `json:"deadline_reached"`
	// Suppression records the directive or exclusion that made the mutant
	// Ignored.
	Suppression *Suppression `json:"suppression,omitempty"`
	// Provenance records why the mutant is in this run and how its verdict
	// was obtained (2.1.0).
	Provenance *Provenance `json:"provenance,omitempty"`
	// Origins is the pack provenance (2.4.0): every (operator, pack, entry)
	// whose rewrite produced this mutant's bytes, sorted and deduplicated by
	// (pack, entry). Present exactly when at least one pack entry produced
	// the bytes — never empty, and always present on a pack-tier mutant.
	Origins []Origin `json:"origins,omitempty"`
	// Telemetry records this invocation's evaluation of the mutant. See
	// MutantTelemetry for availability and fields.
	Telemetry *MutantTelemetry `json:"telemetry,omitempty"`
}

// Origin is one pack entry whose rewrite produced a mutant's bytes: the form
// operator the entry parameterised, the pack's name and the entry's
// author-supplied label. The pair (pack, entry) is the origin's stable wire
// identity.
type Origin struct {
	Operator string `json:"operator"`
	Pack     string `json:"pack"`
	Entry    string `json:"entry"`
}

// The selection modes a mutant's provenance can name.
const (
	// SelectionFull marks a mutant selected as part of the whole population.
	SelectionFull = "full"
	// SelectionSince marks a mutant selected by `--since`.
	SelectionSince = "since"
	// SelectionSample marks a mutant selected by `--sample`.
	SelectionSample = "sample"
)

// The execution provenances a mutant can carry.
const (
	// ExecutionFresh marks a verdict computed by this run.
	ExecutionFresh = "fresh"
	// ExecutionCached marks a verdict replayed from the incremental cache,
	// with evidence rehydrated against the current tree.
	ExecutionCached = "cached"
	// ExecutionUnexecuted marks a selected mutant never dispatched.
	ExecutionUnexecuted = "unexecuted"
)

// Provenance records why a mutant is in this run and how its verdict was
// obtained — the facts the gate table's population split is audited from.
type Provenance struct {
	// Selection is full, since or sample.
	Selection string `json:"selection"`
	// Reason states, in the reader's terms, why the selection chose this
	// mutant.
	Reason string `json:"reason,omitempty"`
	// Execution is fresh, cached or unexecuted.
	Execution string `json:"execution"`
	// CacheKey is the cache key basis hash for a cached verdict.
	CacheKey string `json:"cache_key,omitempty"`
	// BaselineStatus is new, accepted or unobserved once a baseline gate has
	// judged the mutant; empty where no baseline was involved.
	BaselineStatus string `json:"baseline_status,omitempty"`
}

// CacheMiss carries the optional environment-change explanation (2.12.0).
// Its wire contract is defined in docs/schema/report-2.12.0.json.
type CacheMiss struct {
	// Added names variables the current environment carries that the stored
	// generation did not.
	Added []string `json:"added,omitempty"`
	// Removed names variables the stored generation carried that the current
	// environment does not.
	Removed []string `json:"removed,omitempty"`
	// Changed names variables both carry under different values.
	Changed []string `json:"changed,omitempty"`
	// Truncated reports that the bound cut the lists.
	Truncated bool `json:"truncated,omitempty"`
}

// MaxCacheMissNames bounds the variable names one CacheMiss carries across
// its three lists. A session-to-session diff is a handful of names; the bound
// exists for a wholesale environment replacement, which would otherwise turn
// the explanation into the environment listing the field exists to avoid.
const MaxCacheMissNames = 32

// Population is the count split every scoped, cached or sampled run must
// report distinctly, so a reader always knows what this run actually proved.
type Population struct {
	// Selected is the number of mutants this run selected.
	Selected int `json:"selected"`
	// Omitted is the number generated but left out by --since or --sample.
	Omitted int `json:"omitted"`
	// Cached is the number of selected mutants whose verdicts were replayed.
	Cached int `json:"cached"`
	// Fresh is the number of selected mutants whose verdicts were computed by
	// this run, including statically decided mutants.
	Fresh int `json:"fresh"`
}

// Selection records how the population was chosen.
type Selection struct {
	// Mode is full or since.
	Mode string `json:"mode"`
	// Ref is the --since ref, where one was given.
	Ref string `json:"ref,omitempty"`
	// ForcedFull states why a --since run fell back to the full population,
	// naming the changed file class that forced it.
	ForcedFull string `json:"forced_full,omitempty"`
}

// Sampling records the sampling metadata. A sampled run is never
// authoritative: no gate may consume it without the named unsafe opt-in.
type Sampling struct {
	RatePercent float64 `json:"rate_percent"`
	Seed        int64   `json:"seed"`
	// Authoritative is always false: it is published so that no consumer has
	// to know the rule to apply it.
	Authoritative bool `json:"authoritative"`
	// GateOptIn reports that --allow-sampled-gate was used: the run's gates
	// were satisfied by a sample, and the report says so.
	GateOptIn bool `json:"gate_opt_in,omitempty"`
}

// GateOutcome is one row outcome of the normative gate table.
type GateOutcome struct {
	// Evaluated reports whether the gate ran at all.
	Evaluated bool `json:"evaluated"`
	// Scope is full or selected — the population the gate was evaluated over.
	Scope string `json:"scope,omitempty"`
	// Partial marks an evaluation under the partial-population rules in
	// docs/design/product-design.md, section 10.
	Partial bool `json:"partial,omitempty"`
	// Passed is the outcome where the gate was evaluated.
	Passed bool `json:"passed,omitempty"`
	// Refused states why the gate refused to evaluate, where it did.
	Refused string `json:"refused,omitempty"`
}

// BaselineGate records what the baseline file contributed to the run.
type BaselineGate struct {
	// Path is the baseline file, relative to the module.
	Path string `json:"path"`
	// Accepted is the number of baseline entries.
	Accepted int `json:"accepted"`
	// Matched is the number of current findings accepted by the baseline.
	Matched int `json:"matched"`
	// New lists the mutant identifiers of findings the baseline does not
	// accept.
	New []string `json:"new"`
	// Stale lists accepted identifiers with no current finding — only a full
	// population may report these.
	Stale []string `json:"stale,omitempty"`
	// Unobserved lists accepted identifiers outside the observed population,
	// which are not stale: this run says nothing about them.
	Unobserved []string `json:"unobserved,omitempty"`
	// StalenessReported is true only on a full, fully observed population.
	StalenessReported bool `json:"staleness_reported"`
	// Write records whether a baseline write was permitted or refused, where
	// one was requested.
	Write string `json:"write,omitempty"`
}

// BaselineWritten records a permitted, completed baseline write.
const BaselineWritten = "written"

// Gates is the gate table's outcomes for this run.
type Gates struct {
	MinScore  GateOutcome   `json:"min_score"`
	FailOnNew GateOutcome   `json:"fail_on_new"`
	Baseline  *BaselineGate `json:"baseline,omitempty"`
}

// Finding is an actionable result addressed to a place in the module.
type Finding struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Address is the Terraform address the finding is about.
	Address string `json:"address"`
	Module  string `json:"module"`
	Range   Range  `json:"range"`
	Message string `json:"message"`
	// Mutants lists the mutant identifiers that support the finding.
	Mutants []string `json:"mutants"`
}

// PseudoTested is the kind of the milestone's headline finding.
const PseudoTested = "pseudo-tested"

// OperatorErrors counts what one operator produced, so that the selective
// validation question can be answered from data rather than from opinion.
type OperatorErrors struct {
	Operator string `json:"operator"`
	// Generated is the number of mutants the operator produced.
	Generated int `json:"generated"`
	// Invalid is the number terraform validate rejected.
	Invalid int `json:"invalid"`
	// KilledByError is the number Terraform's evaluation caught.
	KilledByError int `json:"killed_by_error"`
	// ErrorRate is Invalid ÷ Generated.
	ErrorRate float64 `json:"error_rate"`
}

// Metrics are the three headline numbers plus the counts they derive from.
type Metrics struct {
	MutationScore  float64 `json:"mutation_score"`
	AssertionScore float64 `json:"assertion_score"`
	Reachability   float64 `json:"reachability"`
	// Incomplete marks an untrustworthy score: a trip, a timeout, an execution
	// error, a non-empty unscored population, or a high share of judged
	// mutants rejected by terraform validate.
	Incomplete bool `json:"incomplete"`
	// Counts holds the number of mutants in each state.
	Counts map[State]int `json:"counts"`
	// Diagnoses holds the number of survivors carrying each diagnosis.
	Diagnoses map[Diagnosis]int `json:"diagnoses"`
	// Scored is the size of the scored set.
	Scored int `json:"scored"`
}

// Baseline records what the unmutated suite did.
type Baseline struct {
	// Runs is the number of run blocks the baseline executed.
	Runs int `json:"runs"`
	// Assertions is the number of assert blocks in the suite.
	Assertions int `json:"assertions"`
	// DurationMS is the baseline wall time, used for timeout calibration.
	DurationMS int64 `json:"duration_ms"`
	// Fingerprint is the composed fingerprint of the unmutated suite.
	Fingerprint string `json:"fingerprint"`
	// VolatileComponents names the payload paths the volatile mask removes.
	VolatileComponents []string `json:"volatile_components"`
}

// ExecutionError records a mutant that could not be evaluated. It is never a
// verdict: an unevaluated mutant is an operational failure, not a survivor.
type ExecutionError struct {
	MutantID string `json:"mutant_id"`
	Site     string `json:"site"`
	Message  string `json:"message"`
}

// Report is the engine's complete result.
type Report struct {
	SchemaVersion string `json:"schema_version"`
	// Command identifies the report-producing invocation; see the Command constants.
	Command Command `json:"command"`
	// Module is the absolute directory of the module under test.
	Module string `json:"module"`
	// ClosureRoot is the absolute directory containing every local module;
	// mutant file paths are relative to it (2.1.0).
	ClosureRoot string `json:"closure_root,omitempty"`
	// TerraformVersion is the version of the Terraform binary used.
	TerraformVersion string `json:"terraform_version"`
	// TestDirectory is the test directory relative to the module.
	TestDirectory string    `json:"test_directory"`
	Baseline      Baseline  `json:"baseline"`
	Mutants       []Mutant  `json:"mutants"`
	Findings      []Finding `json:"findings"`
	Metrics       Metrics   `json:"metrics"`
	// OperatorErrors reports per-operator generation quality.
	OperatorErrors []OperatorErrors `json:"operator_errors"`
	// Suppressions lists every directive and exclusion, applied and rejected.
	Suppressions []Suppression `json:"suppressions"`
	Warnings     []string      `json:"warnings"`
	// Errors lists mutants that could not be evaluated at all.
	Errors []ExecutionError `json:"errors"`
	// Population is the selected/omitted/cached/fresh split (2.1.0).
	Population Population `json:"population"`
	// CacheMiss is the optional cache-miss explanation (2.12.0).
	CacheMiss *CacheMiss `json:"cache_miss,omitempty"`
	// Selection records how the population was chosen (2.1.0).
	Selection Selection `json:"selection"`
	// Sampling is present only on a sampled run (2.1.0).
	Sampling *Sampling `json:"sampling,omitempty"`
	// Gates is the gate table's outcomes (2.1.0). Present only for the
	// commands that execute a population: run, suggest and curate.
	Gates *Gates `json:"gates,omitempty"`
	// Suggestions is the generated assertion outcome table (2.2.0). Present
	// only for the suggest command.
	Suggestions []Suggestion `json:"suggestions,omitempty"`
	// Apply records what an --apply invocation wrote (2.2.0).
	Apply *AppliedSuggestions `json:"apply,omitempty"`
	// Characterisation is the scaffold, pin, TODO and curate block (2.3.0).
	// Present only for the characterise, todos and curate commands.
	Characterisation *Characterisation `json:"characterisation,omitempty"`
	// Telemetry is the command's performance record. See Telemetry for
	// availability and fields.
	Telemetry *Telemetry `json:"telemetry,omitempty"`
}

// Count returns the number of mutants in the given state.
func (r Report) Count(state State) int {
	return r.Metrics.Counts[state]
}

// CountDiagnosis returns the number of survivors carrying the diagnosis.
func (r Report) CountDiagnosis(diagnosis Diagnosis) int {
	return r.Metrics.Diagnoses[diagnosis]
}

// Survivors lists the surviving mutants in report order.
func (r Report) Survivors() []Mutant {
	survivors := []Mutant{}

	for _, mutant := range r.Mutants {
		if mutant.State == Survived {
			survivors = append(survivors, mutant)
		}
	}

	return survivors
}

// MutantByID returns the mutant with the given identifier.
func (r Report) MutantByID(id string) (Mutant, bool) {
	for _, mutant := range r.Mutants {
		if mutant.ID == id {
			return mutant, true
		}
	}

	return Mutant{}, false
}
