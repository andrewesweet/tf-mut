// Package report holds the value the engine returns and the renderings of it.
//
// The terminal and JSON reporters are both derived from this single value, so
// the two can never disagree about a verdict.
package report

// SchemaVersion is the version of the machine-readable report schema.
//
// It changes whenever a consumer could break: removed fields, renamed fields,
// or changed semantics of an existing field.
const SchemaVersion = "1.0.0"

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

// The M1 mutant states, in precedence order.
const (
	// Invalid marks a mutant that fails terraform validate. Excluded from scores.
	Invalid State = "Invalid"
	// Killed marks a mutant an assertion caught.
	Killed State = "Killed"
	// KilledByError marks a mutant Terraform's own evaluation caught.
	KilledByError State = "KilledByError"
	// Timeout marks a mutant that exceeded its execution budget.
	Timeout State = "Timeout"
	// Survived marks a mutant every executed run passed.
	Survived State = "Survived"
	// NoCoverage marks a mutant no run block instantiates, assigned statically.
	NoCoverage State = "NoCoverage"
	// Pending marks a mutant that was generated but not executed, which is the
	// state every mutant carries in a preview.
	Pending State = "Pending"
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
	File   string `json:"file"`
	Run    string `json:"run"`
	Status string `json:"status"`
}

// Mutant is one mutation and everything learned about it.
type Mutant struct {
	ID       string `json:"id"`
	Operator string `json:"operator"`
	// Module is the closure-relative directory of the mutated module.
	Module string `json:"module"`
	// Site is the semantic address of the mutation site.
	Site string `json:"site"`
	// Resource is the resource address the site belongs to, when it has one.
	Resource string `json:"resource,omitempty"`
	Range    Range  `json:"range"`
	Diff     string `json:"diff"`
	State    State  `json:"state"`
	// Runs records the per-run outcomes orthogonally to the aggregate state.
	Runs []RunOutcome `json:"runs"`
	// Diagnostics are the Terraform diagnostics the mutant produced.
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	// ExecutedRuns is the number of run blocks that actually executed.
	ExecutedRuns int `json:"executed_runs"`
	// Validated records whether lazy validation ran for this mutant.
	Validated bool `json:"validated"`
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

// Metrics are the three headline numbers plus the counts they derive from.
type Metrics struct {
	MutationScore  float64 `json:"mutation_score"`
	AssertionScore float64 `json:"assertion_score"`
	Reachability   float64 `json:"reachability"`
	// Incomplete marks a score that a timeout made untrustworthy.
	Incomplete bool `json:"incomplete"`
	// Counts holds the number of mutants in each state.
	Counts map[State]int `json:"counts"`
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
	Warnings      []string  `json:"warnings"`
	// Errors lists mutants that could not be evaluated at all.
	Errors []ExecutionError `json:"errors"`
}

// Count returns the number of mutants in the given state.
func (r Report) Count(state State) int {
	return r.Metrics.Counts[state]
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

// ComputeMetrics derives the state counts and the three headline metrics.
//
// The scored set is Killed + KilledByError + Survived + NoCoverage + Timeout.
// Invalid mutants are excluded, and any timeout marks the score incomplete.
func ComputeMetrics(mutants []Mutant) Metrics {
	counts := map[State]int{}
	for _, mutant := range mutants {
		counts[mutant.State]++
	}

	killed := counts[Killed]
	killedByError := counts[KilledByError]
	survived := counts[Survived]
	noCoverage := counts[NoCoverage]
	timeout := counts[Timeout]

	scored := killed + killedByError + survived + noCoverage + timeout
	assertionDenominator := killed + survived + timeout

	return Metrics{
		MutationScore:  ratio(killed+killedByError, scored),
		AssertionScore: ratio(killed, assertionDenominator),
		Reachability:   ratio(killed+killedByError+survived+timeout, scored),
		Incomplete:     timeout > 0,
		Counts:         counts,
		Scored:         scored,
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}

	return float64(numerator) / float64(denominator)
}
