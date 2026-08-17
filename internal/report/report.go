// Package report holds the value the engine returns and the renderings of it.
//
// The terminal, JSON and SARIF reporters are all derived from this single
// value, so no two of them can disagree about a verdict.
package report

import (
	"cmp"
	"slices"
)

// SchemaVersion is the version of the machine-readable report schema.
//
// It changes whenever a consumer could break: removed fields, renamed fields,
// or changed semantics of an existing field. Version 2 is the M2 break, taken
// once at the start of the milestone rather than twice during it. 2.1.0 is the
// M3 additive revision (review M3): cache provenance per mutant, sampling
// metadata, baseline acceptance and staleness, the population split, and the
// gate table's outcomes.
const SchemaVersion = "2.1.0"

// Command names what produced a report.
type Command string

// The two commands that produce a report.
const (
	// CommandRun executed the mutant population.
	CommandRun Command = "run"
	// CommandPreview generated the population without executing it.
	CommandPreview Command = "preview"
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
	// the address mapping fails) — or classified statically, where the cone
	// reaches nothing observable at all. Excluded.
	Unobservable State = "Unobservable"
	// NoCoverage marks a mutant in a module no run block instantiates, assigned
	// statically. The claim is module-level absence and nothing finer.
	NoCoverage State = "NoCoverage"
	// Ignored marks a mutant a reasoned suppression or a configured exclusion
	// removed from the population. Excluded, counted.
	Ignored State = "Ignored"
	// Pending marks a mutant that was generated but not executed, which is the
	// state every mutant carries in a preview.
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
	// MockMasked marks an apply-mode survivor whose delta is confined to
	// schema-computed attributes the mock invented.
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

// Actionable reports whether the diagnosis names something the reader can fix,
// as opposed to something the oracle could not decide. It selects the SARIF
// level, so the distinction is a published contract.
func (d Diagnosis) Actionable() bool {
	return d != IndeterminateUnknownValues && d != IndeterminateVolatility
}

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
	// MockResource names the mock default that would pin a mock-masked value.
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
	// Diagnostics are the Terraform diagnostics the mutant produced.
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	// ExecutedRuns is the number of run blocks that actually executed.
	ExecutedRuns int `json:"executed_runs"`
	// Validated records whether lazy validation ran for this mutant.
	Validated bool `json:"validated"`
	// Suppression records the directive or exclusion that made the mutant
	// Ignored.
	Suppression *Suppression `json:"suppression,omitempty"`
	// Provenance records why the mutant is in this run and how its verdict
	// was obtained (2.1.0).
	Provenance *Provenance `json:"provenance,omitempty"`
}

// The selection modes a mutant's provenance can name.
const (
	// SelectionFull marks a mutant selected because the whole population ran.
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
)

// Provenance records why a mutant is in this run and how its verdict was
// obtained — the facts the gate table's population split is audited from.
type Provenance struct {
	// Selection is full, since or sample.
	Selection string `json:"selection"`
	// Reason states, in the reader's terms, why the selection chose this
	// mutant.
	Reason string `json:"reason,omitempty"`
	// Execution is fresh or cached.
	Execution string `json:"execution"`
	// CacheKey is the cache key basis hash for a cached verdict.
	CacheKey string `json:"cache_key,omitempty"`
	// BaselineStatus is new, accepted or unobserved once a baseline gate has
	// judged the mutant; empty where no baseline was involved.
	BaselineStatus string `json:"baseline_status,omitempty"`
}

// Population is the count split every scoped, cached or sampled run must
// report distinctly, so a reader always knows what this run actually proved.
type Population struct {
	// Selected is the number of mutants this run selected.
	Selected int `json:"selected"`
	// Omitted is the number generated but left out by --since or --sample.
	Omitted int `json:"omitted"`
	// Cached is the number of selected mutants whose verdicts were replayed.
	Cached int `json:"cached"`
	// Fresh is the number of selected mutants executed by this run.
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
}

// GateOutcome is one row outcome of the normative gate table.
type GateOutcome struct {
	// Evaluated reports whether the gate ran at all.
	Evaluated bool `json:"evaluated"`
	// Scope is full or selected — the population the gate was evaluated over.
	Scope string `json:"scope,omitempty"`
	// Partial marks an evaluation over less than the full population.
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
	// Unobserved lists accepted identifiers outside a scoped population,
	// which are not stale: this run says nothing about them.
	Unobserved []string `json:"unobserved,omitempty"`
	// StalenessReported is true only on a full population.
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
	// Incomplete marks a score that a timeout made untrustworthy.
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
	// Command is run or preview.
	Command Command `json:"command"`
	// Module is the absolute directory of the module under test.
	Module string `json:"module"`
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
	// Selection records how the population was chosen (2.1.0).
	Selection Selection `json:"selection"`
	// Sampling is present only on a sampled run (2.1.0).
	Sampling *Sampling `json:"sampling,omitempty"`
	// Gates is the gate table's outcomes (2.1.0). Absent in a preview.
	Gates *Gates `json:"gates,omitempty"`
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

// scoredStates is the scored set: everything the population can be graded on.
//
// Invalid, Unobservable and Ignored are excluded and reported as counts, so a
// mutant nobody could have caught never lowers a score.
//
//nolint:gochecknoglobals // an immutable lookup table.
var scoredStates = []State{Killed, KilledByError, Survived, StructurallyUnassertable, NoCoverage, Timeout}

// ComputeMetrics derives the state counts and the three headline metrics.
func ComputeMetrics(mutants []Mutant) Metrics {
	counts := map[State]int{}
	diagnoses := map[Diagnosis]int{}

	for _, mutant := range mutants {
		counts[mutant.State]++

		if mutant.State == Survived && mutant.Verdict != nil {
			diagnoses[mutant.Verdict.Diagnosis]++
		}
	}

	scored := 0
	for _, state := range scoredStates {
		scored += counts[state]
	}

	killed := counts[Killed]
	killedByError := counts[KilledByError]
	survived := counts[Survived]
	unassertable := counts[StructurallyUnassertable]
	timeout := counts[Timeout]

	return Metrics{
		MutationScore:  ratio(killed+killedByError, scored),
		AssertionScore: ratio(killed, killed+survived+unassertable+timeout),
		Reachability:   ratio(killed+killedByError+survived+timeout, scored),
		Incomplete:     timeout > 0,
		Counts:         counts,
		Diagnoses:      diagnoses,
		Scored:         scored,
	}
}

// ComputeOperatorErrors summarises generation quality per operator.
func ComputeOperatorErrors(mutants []Mutant) []OperatorErrors {
	byOperator := map[string]*OperatorErrors{}

	for _, mutant := range mutants {
		entry, found := byOperator[mutant.Operator]
		if !found {
			entry = &OperatorErrors{
				Operator: mutant.Operator, Generated: 0, Invalid: 0, KilledByError: 0, ErrorRate: 0,
			}
			byOperator[mutant.Operator] = entry
		}

		entry.Generated++

		//nolint:exhaustive // only the two error states contribute to the counts.
		switch mutant.State {
		case Invalid:
			entry.Invalid++
		case KilledByError:
			entry.KilledByError++
		default:
		}
	}

	counts := make([]OperatorErrors, 0, len(byOperator))

	for _, entry := range byOperator {
		entry.ErrorRate = ratio(entry.Invalid, entry.Generated)
		counts = append(counts, *entry)
	}

	slices.SortFunc(counts, func(left, right OperatorErrors) int {
		return cmp.Compare(left.Operator, right.Operator)
	})

	return counts
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}

	return float64(numerator) / float64(denominator)
}
