package suggest

import "slices"

// The suggestion lifecycle.
//
// A candidate is a generated assertion nothing has verified: it is the only
// value that can be verified or refuted, and the only one a patch is spelled
// for. A suggestion is a terminal outcome, constructible only from a
// candidate transition or from Skipped. The M4 outcome table's presence rules
// are the constructors' signatures, so the combinations the table forbids
// have no spelling: a verified suggestion cannot be built without its digest
// and both verification legs, and a skipped one cannot be built with a patch
// or an expression at all.

// SkipReason is why the generator refused a survivor before any candidate
// existed. It is the Suggestion context's own closed vocabulary — the
// adapters refuse with it, not with a publication status — and the
// application layer projects it onto the published wire spelling.
type SkipReason string

// The complete skip vocabulary. Nothing else may be assigned.
const (
	// SkipSensitive marks a delta whose value — or an ancestor of it —
	// Terraform marks sensitive. No artefact contains the value.
	SkipSensitive SkipReason = "skipped-sensitive"
	// SkipUnaddressable marks a delta the address adapter could not express
	// as a legal traversal in the selected run's target module.
	SkipUnaddressable SkipReason = "skipped-unaddressable"
	// SkipUnrenderable marks a value the rendering contract cannot express
	// type-correctly for Terraform equality.
	SkipUnrenderable SkipReason = "skipped-unrenderable"
	// SkipUnsupportedTarget marks a survivor whose target test file is JSON.
	// No JSON test writer is built, and `--apply` never touches one.
	SkipUnsupportedTarget SkipReason = "skipped-unsupported-target"
)

// RunRecord is one run block a verification leg executed, so a reader can see
// what the leg's claim was made over.
type RunRecord struct {
	file   string
	run    string
	phase  int
	status string
}

// NewRunRecord records one executed run reference.
func NewRunRecord(file, run string, phase int, status string) RunRecord {
	return RunRecord{file: file, run: run, phase: phase, status: status}
}

// File is the test file the run block was executed from.
func (r RunRecord) File() string { return r.file }

// Run is the run block's name.
func (r RunRecord) Run() string { return r.run }

// Phase is one (the plain classification run) or two (the verbose
// fingerprint run).
func (r RunRecord) Phase() int { return r.phase }

// Status is the run's outcome as Terraform reported it.
func (r RunRecord) Status() string { return r.status }

// Leg is the outcome of one half of the verification contract.
type Leg struct {
	// passed reports whether the leg met its requirement: green for the
	// baseline leg, failing for the isolated mutant leg.
	passed bool
	// runs are the run references the leg executed.
	runs []RunRecord
	// detail states what the leg observed, in the reader's terms.
	detail string
}

// NewLeg records one leg's outcome: whether it met its requirement, the run
// references it executed, and what it observed.
func NewLeg(passed bool, runs []RunRecord, detail string) Leg {
	return Leg{passed: passed, runs: slices.Clone(runs), detail: detail}
}

// Passed reports whether the leg met its requirement.
func (l Leg) Passed() bool { return l.passed }

// Runs are the run references the leg executed.
func (l Leg) Runs() []RunRecord { return l.runs }

// Detail states what the leg observed, in the reader's terms.
func (l Leg) Detail() string { return l.detail }

// Verification is the two-leg evidence a verified or refuted suggestion
// carries. The baseline leg is the full-suite run over the target file's
// candidate batch, which must be green; the mutant leg is the isolated check
// of this suggestion alone against its re-materialised mutant, which must
// fail. Neither alone is enough, and there is no spelling for a conclusion
// without both.
type Verification struct {
	baseline Leg
	mutant   Leg
}

// NewVerification binds the two legs into one evidence value.
func NewVerification(baseline, mutant Leg) Verification {
	return Verification{baseline: baseline, mutant: mutant}
}

// Baseline is the full-suite leg over the candidate batch.
func (v Verification) Baseline() Leg { return v.baseline }

// Mutant is the isolated leg against the re-materialised mutant.
func (v Verification) Mutant() Leg { return v.mutant }

// Candidate is a generated assertion nothing has verified. It is the only
// value that can be verified or refuted, and its fields are unexported: one
// exists only where the generator built it.
type Candidate struct {
	id         string
	mutantID   string
	alsoKills  []string
	targetFile string
	targetRun  string
	expression string
	patch      string
}

// NewCandidate carries a generated assertion and the patch that would add it.
//
// The identifier is the stable content hash over the mutant identifier, the
// target run and the expression — it survives a re-run and an unrelated edit,
// which is what makes `--apply` selectable by identifier. The patch is the
// diff rendered for this candidate by PatchFor, whose embedded message names
// this identifier: VerifiedMessage renders it from the same inputs.
func NewCandidate(mutantID, targetFile, targetRun, expression, patch string) Candidate {
	return Candidate{
		id:         identifier(mutantID, targetFile, targetRun, expression),
		mutantID:   mutantID,
		targetFile: targetFile,
		targetRun:  targetRun,
		expression: expression,
		patch:      patch,
	}
}

// ID is the stable content identifier.
func (c Candidate) ID() string { return c.id }

// MutantID is the survivor the assertion would kill.
func (c Candidate) MutantID() string { return c.mutantID }

// AlsoKills lists further survivors the same assertion kills.
func (c Candidate) AlsoKills() []string { return c.alsoKills }

// TargetFile is the test file the assertion belongs in, relative to the
// module directory.
func (c Candidate) TargetFile() string { return c.targetFile }

// TargetRun is the run block the assertion belongs in.
func (c Candidate) TargetRun() string { return c.targetRun }

// Expression is the generated assert condition.
func (c Candidate) Expression() string { return c.expression }

// Patch is the unified diff that would add the assertion.
func (c Candidate) Patch() string { return c.patch }

// Verify concludes the candidate's verification in its favour. The digest is
// the SHA-256 of the target file's bytes as verified — Digest computes it —
// and the evidence is both legs at the call: a verified suggestion with no
// evidence has no spelling.
func (c Candidate) Verify(digest string, evidence Verification) Suggestion {
	return Suggestion{
		id:         c.id,
		mutantID:   c.mutantID,
		alsoKills:  c.alsoKills,
		targetFile: c.targetFile,
		targetRun:  c.targetRun,
		outcome:    outcomeVerified,
		expression: c.expression,
		patch:      c.patch,
		digest:     digest,
		evidence:   &evidence,
	}
}

// Refute concludes that the assertion does not do what it claims. It is a
// tool finding: the reason states which leg failed and why, and the evidence
// is both legs, the failed one among them. The patch is kept, so the reader
// can see exactly what was wrong.
func (c Candidate) Refute(reason string, evidence Verification) Suggestion {
	return Suggestion{
		id:         c.id,
		mutantID:   c.mutantID,
		alsoKills:  c.alsoKills,
		targetFile: c.targetFile,
		targetRun:  c.targetRun,
		outcome:    outcomeRefuted,
		expression: c.expression,
		patch:      c.patch,
		evidence:   &evidence,
		reason:     reason,
	}
}

// outcome is the terminal conclusion a suggestion carries. The zero value is
// no conclusion at all, and only the constructors assign one.
type outcome uint8

const (
	outcomeVerified outcome = iota + 1
	outcomeRefuted
	outcomeSkipped
)

// Suggestion is a terminal outcome: a verification conclusion over a
// candidate, or a generation-time refusal. Constructible only from a
// Candidate transition or from Skipped; the fields are unexported, so every
// value carries exactly what its outcome requires and nothing a skipped one
// must not.
type Suggestion struct {
	id         string
	mutantID   string
	alsoKills  []string
	targetFile string
	targetRun  string
	outcome    outcome
	skip       SkipReason
	expression string
	patch      string
	digest     string
	evidence   *Verification
	reason     string
}

// Skipped is the suggestion for a survivor the generator refused before any
// candidate existed: the target is one nothing can write, the value is one
// the contract must not render, or the delta is one no legal assertion
// addresses. It takes no patch and no expression — no reason in the closed
// vocabulary permits one — so a generator limit is reported as a limit and
// can never be dressed as a refutation.
func Skipped(mutantID, targetFile, targetRun string, reason SkipReason, detail string) Suggestion {
	return Suggestion{
		id:         identifier(mutantID, targetFile, targetRun, string(reason)),
		mutantID:   mutantID,
		targetFile: targetFile,
		targetRun:  targetRun,
		outcome:    outcomeSkipped,
		skip:       reason,
		reason:     detail,
	}
}

// ID is the stable content identifier.
func (s Suggestion) ID() string { return s.id }

// MutantID is the survivor the outcome is about.
func (s Suggestion) MutantID() string { return s.mutantID }

// AlsoKills lists further survivors the same assertion kills, for an outcome
// that concluded over a collapsed candidate.
func (s Suggestion) AlsoKills() []string { return s.alsoKills }

// TargetFile is the test file the assertion belongs in, relative to the
// module directory.
func (s Suggestion) TargetFile() string { return s.targetFile }

// TargetRun is the run block the assertion belongs in.
func (s Suggestion) TargetRun() string { return s.targetRun }

// Expression is the assert condition, for an outcome that concluded over a
// candidate. A skipped one carries none.
func (s Suggestion) Expression() string { return s.expression }

// Patch is the unified diff that would add the assertion, for an outcome
// that concluded over a candidate. A skipped one carries none.
func (s Suggestion) Patch() string { return s.patch }

// VerifiedDigest is the SHA-256 of the target file's bytes as verified, for
// a verified outcome; it binds an apply to what was proven.
func (s Suggestion) VerifiedDigest() string { return s.digest }

// Verification is the two-leg evidence, for a verified or refuted outcome.
func (s Suggestion) Verification() (Verification, bool) {
	if s.evidence == nil {
		return Verification{}, false //nolint:exhaustruct // no evidence exists.
	}

	return *s.evidence, true
}

// Verified reports whether the outcome concluded in the candidate's favour.
// False alongside an empty skip reason is a refutation.
func (s Suggestion) Verified() bool { return s.outcome == outcomeVerified }

// SkipReason is the generation-time refusal, and whether this outcome is one.
func (s Suggestion) SkipReason() (SkipReason, bool) {
	return s.skip, s.outcome == outcomeSkipped
}

// StatusReason states why, for a refuted or a skipped outcome.
func (s Suggestion) StatusReason() string { return s.reason }
