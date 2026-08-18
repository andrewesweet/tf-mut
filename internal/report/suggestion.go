package report

// The suggestion outcome model (report-2.2.0).
//
// It is the whole normative table of the M4 spec's M2 disposition, expressed
// once: a suggestion is an object with a stable identity, exactly one status
// from a closed vocabulary, and presence rules that make an artefact's absence
// mean something. A `skipped-*` outcome carries no patch, and carries no
// expression where its own reason forbids one — a generator limit is reported
// as a limit, never dressed as a refutation, and a sensitive value appears in
// no artefact at all.

// SuggestionStatus is one row of the outcome table.
type SuggestionStatus string

// The complete status vocabulary. Nothing else may be assigned.
const (
	// SuggestionCandidate is a generated assertion nothing has verified yet.
	SuggestionCandidate SuggestionStatus = "candidate"
	// SuggestionVerified passed both verification legs: the full suite stayed
	// green with the candidate batch applied, and the suggestion alone failed
	// against its re-materialised mutant.
	SuggestionVerified SuggestionStatus = "verified"
	// SuggestionRefuted failed a verification leg. It is a tool finding: the
	// generator produced an assertion that does not do what it claims.
	SuggestionRefuted SuggestionStatus = "refuted"
	// SuggestionSkippedSensitive marks a delta whose value — or an ancestor of
	// it — Terraform marks sensitive. No artefact contains the value.
	SuggestionSkippedSensitive SuggestionStatus = "skipped-sensitive"
	// SuggestionSkippedUnaddressable marks a delta the address adapter could
	// not express as a legal traversal in the selected run's target module.
	SuggestionSkippedUnaddressable SuggestionStatus = "skipped-unaddressable"
	// SuggestionSkippedUnrenderable marks a value the rendering contract
	// cannot express type-correctly for Terraform equality.
	SuggestionSkippedUnrenderable SuggestionStatus = "skipped-unrenderable"
	// SuggestionSkippedUnsupportedTarget marks a survivor whose target test
	// file is JSON. No JSON test writer is built, and `--apply` never touches
	// one.
	SuggestionSkippedUnsupportedTarget SuggestionStatus = "skipped-unsupported-target"
)

// Skipped reports whether a status is one of the skipped family, which is what
// the "no patch" rule turns on.
func (s SuggestionStatus) Skipped() bool {
	switch s {
	case SuggestionSkippedSensitive, SuggestionSkippedUnaddressable,
		SuggestionSkippedUnrenderable, SuggestionSkippedUnsupportedTarget:
		return true
	case SuggestionCandidate, SuggestionVerified, SuggestionRefuted:
		return false
	default:
		return false
	}
}

// VerificationLeg is the outcome of one half of the verification contract.
type VerificationLeg struct {
	// Passed reports whether the leg met its requirement: green for the
	// baseline leg, failing for the isolated mutant leg.
	Passed bool `json:"passed"`
	// Runs are the run references the leg executed, so a reader can see what
	// the claim was made over.
	Runs []RunOutcome `json:"runs"`
	// Detail states what the leg observed, in the reader's terms.
	Detail string `json:"detail"`
}

// Verification is the evidence a verified or refuted suggestion carries.
type Verification struct {
	// Baseline is the full-suite run over the target file's candidate batch,
	// which must be green.
	Baseline VerificationLeg `json:"baseline"`
	// Mutant is the isolated check of this suggestion alone against its
	// re-materialised mutant, which must fail.
	Mutant VerificationLeg `json:"mutant"`
}

// Suggestion is one generated assertion and everything known about it.
type Suggestion struct {
	// ID is a content hash over the mutant identifier, the target run and the
	// expression. It is stable across runs and across unrelated edits.
	ID string `json:"id"`
	// MutantID is the survivor the suggestion would kill.
	MutantID string `json:"mutant_id"`
	// TargetFile is the test file the assertion belongs in, relative to the
	// module directory.
	TargetFile string `json:"target_file"`
	// TargetRun is the run block the assertion belongs in.
	TargetRun string `json:"target_run"`
	// Status is the outcome, from the closed vocabulary.
	Status SuggestionStatus `json:"status"`
	// Expression is the generated assert condition. It is absent for every
	// status whose reason forbids one.
	Expression string `json:"expression,omitempty"`
	// Patch is the unified diff that would add the assertion. Present only for
	// candidate, verified and refuted: every skipped status carries no patch.
	Patch string `json:"patch,omitempty"`
	// VerifiedDigest is the SHA-256 of the target file's bytes as verified,
	// which binds an apply to what was proven. Present for verified.
	VerifiedDigest string `json:"verified_digest,omitempty"`
	// Verification is the two-leg evidence. Present for verified and refuted.
	Verification *Verification `json:"verification,omitempty"`
	// StatusReason states why, and is required for every refuted and every
	// skipped status.
	StatusReason string `json:"status_reason,omitempty"`
}

// SuggestionsByStatus counts the suggestions carrying each status.
func (r Report) SuggestionsByStatus() map[SuggestionStatus]int {
	counts := map[SuggestionStatus]int{}
	for _, suggestion := range r.Suggestions {
		counts[suggestion.Status]++
	}

	return counts
}

// SuggestionByID returns the suggestion with the given identifier.
func (r Report) SuggestionByID(id string) (Suggestion, bool) {
	for _, suggestion := range r.Suggestions {
		if suggestion.ID == id {
			return suggestion, true
		}
	}

	return Suggestion{}, false
}

// AppliedSuggestions records what an `--apply` invocation did (2.2.0).
//
// It exists so that a refusal and a partial application are both readable
// facts rather than a message on standard error: the protocol aborts with zero
// writes on any preflight mismatch, and a failure part-way through a multi-file
// application has to leave a state the reader can recover from.
type AppliedSuggestions struct {
	// Requested lists the suggestion identifiers the invocation selected.
	Requested []string `json:"requested"`
	// Written lists the target files written, in write order.
	Written []string `json:"written"`
	// Pending lists the target files a failure left unwritten.
	Pending []string `json:"pending,omitempty"`
	// Aborted states why the protocol refused or stopped. Empty on success.
	Aborted string `json:"aborted,omitempty"`
	// Partial reports that some files were written and others were not.
	Partial bool `json:"partial,omitempty"`
}
