package report

// The characterisation entities of report-2.3.0.
//
// Every entity carries a stable, content-derived identifier with a stated
// dedup key, and every status is a closed vocabulary. Extending one of these
// enumerations is a minor schema version with the consumer contract
// documented — not a silently additive change, because a consumer switching
// over a closed set breaks on a value it has never seen.

// SensitiveWithheld is what every artefact carries in place of a value
// Terraform marks sensitive: the report, the terminal, a generated file, a
// TODO's constraint evidence and a failed attempt's diagnostic alike.
const SensitiveWithheld = "(sensitive value withheld)"

// PinStatus is the closed status vocabulary of a pin.
type PinStatus string

// The pin statuses. Only Pinned carries an expression; every skipped status
// carries a reason and no executable content.
const (
	// Pinned marks a harvested value expressed as an assert condition.
	Pinned PinStatus = "pinned"
	// PinSkippedSensitive marks a value Terraform marks sensitive. Neither the
	// value nor any rendering of it reaches an artefact.
	PinSkippedSensitive PinStatus = "skipped-sensitive"
	// PinSkippedUnrenderable marks a value no type-correct Terraform equality
	// expresses, as the M4 rendering contract decides it.
	PinSkippedUnrenderable PinStatus = "skipped-unrenderable"
	// PinSkippedVolatile marks a value the double run proved varies between
	// two runs of the same configuration.
	PinSkippedVolatile PinStatus = "skipped-volatile"
	// PinSkippedMockInvented marks a schema-computed value the mock invented
	// rather than the configuration determined.
	PinSkippedMockInvented PinStatus = "skipped-mock-invented"
)

// TodoStatus is the closed status vocabulary of a TODO.
type TodoStatus string

// The TODO statuses.
const (
	// TodoOpen marks a judgement point nobody has answered.
	TodoOpen TodoStatus = "open"
	// TodoAnswered marks an answer supplied and not yet verified.
	TodoAnswered TodoStatus = "answered"
	// TodoPromoted marks an answer that verified and became test content.
	TodoPromoted TodoStatus = "promoted"
	// TodoRejected marks an answer verification refuted.
	TodoRejected TodoStatus = "rejected"
)

// ScaffoldStatus is the closed status vocabulary of a scaffold.
type ScaffoldStatus string

// The scaffold statuses.
const (
	// Scaffolded marks non-executable material awaiting an answer.
	Scaffolded ScaffoldStatus = "scaffolded"
	// ScaffoldPromoted marks a scaffold whose behaviour verified and which
	// became test content.
	ScaffoldPromoted ScaffoldStatus = "promoted"
)

// CurateKind is the closed vocabulary of curate finding kinds.
type CurateKind string

// The curate finding kinds.
const (
	// EmptyKillSet marks an assertion no mutant's death depended on.
	EmptyKillSet CurateKind = "empty-kill-set"
	// Subsumed marks an assertion whose kill set is contained in another's.
	Subsumed CurateKind = "subsumed"
	// CrossScenarioRedundant marks two scenarios pinning the same behaviour
	// under inputs that do not discriminate it.
	CrossScenarioRedundant CurateKind = "cross-scenario-redundant"
)

// AssertionProvenance classifies an assertion against the generated-assertion
// registry.
type AssertionProvenance string

// The provenance classes, decided mechanically against the registry.
const (
	// GeneratedUnmodified marks an assertion this tool wrote and nobody edited.
	GeneratedUnmodified AssertionProvenance = "generated-unmodified"
	// GeneratedEdited marks an assertion this tool wrote and somebody changed.
	GeneratedEdited AssertionProvenance = "generated-edited"
	// PreExisting marks an assertion no registry entry claims.
	PreExisting AssertionProvenance = "pre-existing"
)

// InputProvenance names where a scenario's variable assignment came from.
type InputProvenance string

// The synthesis preference order, in the order it is tried.
const (
	// FromDefault is the variable's own default.
	FromDefault InputProvenance = "default"
	// FromValidation is a value mined from a validation condition.
	FromValidation InputProvenance = "mined"
	// FromType is a value synthesised from the declared type.
	FromType InputProvenance = "typed"
	// FromAnswer is a value a TODO answer supplied.
	FromAnswer InputProvenance = "answered"
)

// Input is one variable assignment in a scenario.
type Input struct {
	Name string `json:"name"`
	// Expression is the assignment as written into the run block. A sensitive
	// or ephemeral variable carries the withheld marker instead.
	Expression string          `json:"expression"`
	Provenance InputProvenance `json:"provenance"`
}

// Scenario is one generated harvest point.
//
// Identity: a hash over the module, the input assignment set and the state
// key. Two scenarios with the same inputs in the same module are the same
// scenario, whatever they are named.
type Scenario struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// StateKey isolates the scenario's Terraform state from every other
	// generated scenario, so its pins describe creates rather than updates.
	StateKey string `json:"state_key"`
	// File is the generated test file, relative to the module directory.
	File   string  `json:"file"`
	Inputs []Input `json:"inputs"`
}

// Pin is one harvested value at the chosen granularity.
//
// Identity: a hash over the scenario, the address and the expression. The
// address is the dedup key — one pin per address per run.
type Pin struct {
	ID string `json:"id"`
	// Scenario is the identifier of the scenario the value was harvested from.
	Scenario string `json:"scenario"`
	// Address is the Terraform address the pin is about.
	Address string `json:"address"`
	// Expression is the generated assert condition. Empty for every skipped
	// status: a skipped pin carries no executable content.
	Expression string    `json:"expression,omitempty"`
	Status     PinStatus `json:"status"`
	// Reason states why a skipped pin was skipped. Empty when pinned.
	Reason string `json:"reason,omitempty"`
	// Rung is the ladder level the pin belongs to.
	Rung string `json:"rung"`
}

// Todo is one judgement point the deterministic pipeline could not resolve.
//
// Identity: a hash over the variable and the constraint range, so the
// identifier survives a resume.
type Todo struct {
	ID string `json:"id"`
	// Variable is the module input awaiting a value.
	Variable string     `json:"variable"`
	Status   TodoStatus `json:"status"`
	// Constraint is the validation or precondition expression verbatim, or
	// empty where the variable carries none.
	Constraint string `json:"constraint,omitempty"`
	// Range is where the constraint is declared.
	Range Range `json:"range"`
	// Diagnostic is the last attempt's failure, redacted.
	Diagnostic string `json:"diagnostic,omitempty"`
	// Attempted lists the values already tried, redacted.
	Attempted []string `json:"attempted,omitempty"`
	// Artefact is the non-executable file the TODO is written into, relative
	// to the module directory.
	Artefact string `json:"artefact,omitempty"`
}

// Scaffold is non-executable material awaiting an answer before it can become
// test content.
//
// Identity: a hash over the construct address.
type Scaffold struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Address string         `json:"address"`
	Status  ScaffoldStatus `json:"status"`
	// Artefact is the non-executable file the scaffold lives in.
	Artefact string `json:"artefact,omitempty"`
}

// CurateFinding is one redundancy report, with the evidence attached.
//
// Identity: a hash over the kind and the member set.
type CurateFinding struct {
	ID   string     `json:"id"`
	Kind CurateKind `json:"kind"`
	// Members are the assertion identifiers the finding is about.
	Members []string `json:"members"`
	// Provenance classifies each member against the generated-assertion
	// registry, in the same order.
	Provenance []AssertionProvenance `json:"provenance"`
	// Mutants lists the mutants the evidence rests on.
	Mutants []string `json:"mutants"`
	// PopulationAuthoritative is always true: curate refuses to evaluate a
	// partial population, and the flag is published so no consumer has to
	// know the rule to apply it.
	PopulationAuthoritative bool `json:"population_authoritative"`
	// Message states the finding in the reader's terms.
	Message string `json:"message"`
}

// GeneratedFile is one file the scaffold consists of.
type GeneratedFile struct {
	// Path is relative to the module directory.
	Path string `json:"path"`
	// Content is the file's bytes. It travels in the report whenever a JSON
	// reporter owns standard output, so the caller never has to reconstruct
	// what would have been printed.
	Content string `json:"content"`
	// Digest is the content digest the write protocol commits against.
	Digest string `json:"digest"`
	// Executable is false for the non-executable artefact class — the files
	// `terraform test` never reads.
	Executable bool `json:"executable"`
	// Written reports that this invocation placed the file on disk.
	Written bool `json:"written"`
}

// CharacteriseWrite records what a --write invocation did.
type CharacteriseWrite struct {
	// Requested reports that --write was given.
	Requested bool `json:"requested"`
	// InputDigest is the digest of the input closure that made the scaffold
	// green, re-checked immediately before each rename.
	InputDigest string `json:"input_digest"`
	// Written lists the module-relative paths this invocation placed.
	Written []string `json:"written,omitempty"`
	// Refused states why no write happened, where one was requested.
	Refused string `json:"refused,omitempty"`
	// Partial names the files written before an abort, where the commit could
	// not complete as a whole.
	Partial []string `json:"partial,omitempty"`
}

// Convergence is the until-dry loop's evidence.
type Convergence struct {
	Rounds int `json:"rounds"`
	// NewPinsPerRound records how many new pins each round produced.
	NewPinsPerRound []int `json:"new_pins_per_round"`
	// StopReason is dry, bounded or refused.
	StopReason string `json:"stop_reason"`
}

// Characterisation is the characterise and curate result block (2.3.0).
type Characterisation struct {
	// Rung is the granularity the pins were taken at.
	Rung string `json:"rung"`
	// RungRequested is the rung the caller asked for, present only when the
	// zero-output contract escalated away from it.
	RungRequested string `json:"rung_requested,omitempty"`
	// Escalated reports the zero-output auto-escalation.
	Escalated bool `json:"escalated,omitempty"`
	// EscalationReason states why, in the reader's terms.
	EscalationReason string `json:"escalation_reason,omitempty"`
	// Complete is false whenever the selected rung produced no pins: a
	// characterisation that pinned nothing may never report complete.
	Complete    bool               `json:"complete"`
	Scenarios   []Scenario         `json:"scenarios"`
	Pins        []Pin              `json:"pins"`
	Todos       []Todo             `json:"todos,omitempty"`
	Scaffolds   []Scaffold         `json:"scaffolds,omitempty"`
	Files       []GeneratedFile    `json:"files"`
	Findings    []CurateFinding    `json:"curate_findings,omitempty"`
	Write       *CharacteriseWrite `json:"write,omitempty"`
	Convergence *Convergence       `json:"convergence,omitempty"`
	// Staged reports that the suite existed only as an overlay: no byte of the
	// source tree was changed.
	Staged bool `json:"staged"`
}

// PinsByStatus counts the pins in each status.
func (c Characterisation) PinsByStatus() map[PinStatus]int {
	counts := map[PinStatus]int{}
	for _, pin := range c.Pins {
		counts[pin.Status]++
	}

	return counts
}

// OpenTodos counts the TODOs still awaiting an answer.
func (c Characterisation) OpenTodos() int {
	open := 0

	for _, todo := range c.Todos {
		if todo.Status == TodoOpen {
			open++
		}
	}

	return open
}
