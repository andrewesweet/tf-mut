package engine

import (
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// project maps an oracle outcome onto the published report: the outcome's
// state becomes the mutant's State, and the outcome's classification becomes
// its Verdict. The report DTO keeps its exported fields — this is the one
// place the Oracle context's vocabulary is spelled onto the wire.
func project(verdict report.Mutant, outcome oracle.Outcome) report.Mutant {
	verdict.State = projectState(outcome.State())
	verdict.Verdict = &report.Verdict{
		Diagnosis: projectDiagnosis(outcome.Diagnosis()),
		Message:   outcome.Message(),
		Fix:       outcome.Fix(),
		Evidence:  projectEvidence(outcome.Evidence()),
	}

	return verdict
}

// projectState translates the Oracle context's state vocabulary into the
// published wire spelling. The switch is exhaustive over the context's closed
// set, so a state the oracle gains is a compile-time demand on this table.
func projectState(state oracle.State) report.State {
	switch state {
	case oracle.StateSurvived:
		return report.Survived
	case oracle.StateStructurallyUnassertable:
		return report.StructurallyUnassertable
	case oracle.StateUnobservable:
		return report.Unobservable
	}

	return ""
}

// projectDiagnosis translates the Oracle context's diagnosis vocabulary into
// the published wire spelling. It is exhaustive over the emitted diagnoses;
// an outcome that carries none projects to none.
func projectDiagnosis(diagnosis oracle.Diagnosis) report.Diagnosis {
	switch diagnosis {
	case oracle.IndeterminateUnknownValues:
		return report.IndeterminateUnknownValues
	case oracle.IndeterminateVolatility:
		return report.IndeterminateVolatility
	case oracle.WeakAssertion:
		return report.WeakAssertion
	case oracle.NoAssertion:
		return report.NoAssertion
	case oracle.Unasserted:
		return report.Unasserted
	}

	return ""
}

// projectEvidence maps the outcome's evidence onto the published DTO, field
// for field. The delta is projected only where a constructor recorded one, so
// a diagnosis that carries no delta keeps carrying none.
func projectEvidence(evidence oracle.Evidence) report.Evidence {
	projected := report.Evidence{
		UnknownPaths:       evidence.UnknownPaths(),
		VolatileComponents: evidence.VolatileComponents(),
		UnstableAttributes: evidence.UnstableAttributes(),
		Assertion:          evidence.Assertion(),
		ClosureVerdict:     evidence.ClosureVerdict(),
		DefeatedBy:         evidence.DefeatedBy(),
	}

	if delta := evidence.Delta(); delta != nil {
		projected.Delta = projectDelta(delta)
	}

	return projected
}

// maxReportedChanges bounds the delta a report carries. A mutant that empties a
// resource changes every attribute of it, and a hundred lines of evidence
// serves nobody. Confirmed by measurement (M3c): only 4.3% of real survivors
// saturate this cap, all of them whole-resource mutants.
const maxReportedChanges = 20

func projectDelta(changes []fingerprint.Change) []report.Change {
	converted := make([]report.Change, 0, min(len(changes), maxReportedChanges))

	for index, change := range changes {
		if index >= maxReportedChanges {
			break
		}

		converted = append(converted, report.Change{
			Run:     change.Run,
			Path:    change.Path,
			Address: change.Address,
			// The values are withheld where Terraform marks them sensitive
			// (2.2.0). The reader still learns that the path changed, which is
			// the whole evidential content of a delta; the value itself is the
			// one thing a report must not carry, because every reporter — the
			// JSON document, the SARIF artefact, the job step summary — would
			// carry it too.
			Baseline: withheld(change.Sensitive, change.Baseline),
			Mutant:   withheld(change.Sensitive, change.Mutant),
			// Carried through deliberately: the suggestion engine's
			// sensitivity predicate is decided from this flag, and dropping it
			// here would let a secret reach a generated expression.
			Sensitive: change.Sensitive,
		})
	}

	return converted
}

// SensitiveWithheld is what a report carries in place of a sensitive value. It
// is deliberately not a canonical rendering: every rendering of a string starts
// with a quote, so nothing a module could hold can collide with it.
const SensitiveWithheld = report.SensitiveWithheld

// withheld replaces a sensitive rendering, and leaves an absent one absent so
// that "the path was not there" stays distinguishable from "it was withheld".
func withheld(sensitive bool, rendering string) string {
	if !sensitive || rendering == "" {
		return rendering
	}

	return SensitiveWithheld
}

// projectGeneration maps generation's and verification's outcomes onto the
// published DTOs in survivor order — the order generation produced them in —
// with collapsed duplicates carried by their first assertion's AlsoKills. A
// survivor the generator said nothing about projects to nothing.
func projectGeneration(
	selected []report.Mutant,
	candidates []suggest.Candidate,
	concluded []suggest.Suggestion,
	skipped []suggest.Suggestion,
) []report.Suggestion {
	carriers := map[string]suggest.Candidate{}
	for _, candidate := range candidates {
		carriers[candidate.MutantID()] = candidate
	}

	terminals := map[string]suggest.Suggestion{}
	for _, suggestion := range concluded {
		terminals[suggestion.MutantID()] = suggestion
	}

	for _, suggestion := range skipped {
		terminals[suggestion.MutantID()] = suggestion
	}

	projected := make([]report.Suggestion, 0, len(candidates)+len(skipped))

	for _, mutant := range selected {
		if candidate, found := carriers[mutant.ID]; found {
			projected = append(projected, projectCandidate(candidate))

			continue
		}

		if suggestion, found := terminals[mutant.ID]; found {
			projected = append(projected, projectSuggestion(suggestion))
		}
	}

	return projected
}

// projectCandidate maps a candidate onto the published DTO. The candidate
// status needs no mapping: the value's type is the status.
func projectCandidate(candidate suggest.Candidate) report.Suggestion {
	return report.Suggestion{
		ID:         candidate.ID(),
		MutantID:   candidate.MutantID(),
		AlsoKills:  candidate.AlsoKills(),
		TargetFile: candidate.TargetFile(),
		TargetRun:  candidate.TargetRun(),
		Status:     report.SuggestionCandidate,
		Expression: candidate.Expression(),
		Patch:      candidate.Patch(),
	}
}

// projectSuggestion maps a terminal outcome onto the published DTO, carrying
// exactly the artefacts its outcome requires: a verified one its digest and
// both legs, a refuted one its legs and reason, a skipped one its reason and
// no patch at all.
func projectSuggestion(suggestion suggest.Suggestion) report.Suggestion {
	projected := report.Suggestion{
		ID:           suggestion.ID(),
		MutantID:     suggestion.MutantID(),
		AlsoKills:    suggestion.AlsoKills(),
		TargetFile:   suggestion.TargetFile(),
		TargetRun:    suggestion.TargetRun(),
		Expression:   suggestion.Expression(),
		Patch:        suggestion.Patch(),
		StatusReason: suggestion.StatusReason(),
	}

	reason, skipped := suggestion.SkipReason()

	switch {
	case skipped:
		projected.Status = projectSkipReason(reason)
	case suggestion.Verified():
		projected.Status = report.SuggestionVerified
		projected.VerifiedDigest = suggestion.VerifiedDigest()
		projected.Verification = projectVerification(suggestion)
	default:
		projected.Status = report.SuggestionRefuted
		projected.Verification = projectVerification(suggestion)
	}

	return projected
}

// projectVerification maps a terminal outcome's evidence onto the published
// DTO. A verified or refuted outcome carries both legs by construction, so
// the absence the guard names is unreachable.
func projectVerification(suggestion suggest.Suggestion) *report.Verification {
	evidence, carried := suggestion.Verification()
	if !carried {
		return nil
	}

	return &report.Verification{
		Baseline: projectLeg(evidence.Baseline()),
		Mutant:   projectLeg(evidence.Mutant()),
	}
}

// projectLeg maps one verification leg onto the published DTO.
func projectLeg(leg suggest.Leg) report.VerificationLeg {
	return report.VerificationLeg{
		Passed: leg.Passed(),
		Runs:   projectRunRecords(leg.Runs()),
		Detail: leg.Detail(),
	}
}

// projectRunRecords maps a leg's run references onto the published DTO. The
// slice is never nil: an empty leg marshals as an empty list, not a null.
func projectRunRecords(records []suggest.RunRecord) []report.RunOutcome {
	projected := make([]report.RunOutcome, len(records))

	for index, record := range records {
		projected[index] = report.RunOutcome{
			File:   record.File(),
			Run:    record.Run(),
			Phase:  record.Phase(),
			Status: record.Status(),
		}
	}

	return projected
}

// projectSkipReason translates the Suggestion context's skip vocabulary into
// the published wire spelling. The switch is exhaustive over the context's
// closed set, so a reason the context gains is a compile-time demand on this
// table.
func projectSkipReason(reason suggest.SkipReason) report.SuggestionStatus {
	switch reason {
	case suggest.SkipSensitive:
		return report.SuggestionSkippedSensitive
	case suggest.SkipUnaddressable:
		return report.SuggestionSkippedUnaddressable
	case suggest.SkipUnrenderable:
		return report.SuggestionSkippedUnrenderable
	case suggest.SkipUnsupportedTarget:
		return report.SuggestionSkippedUnsupportedTarget
	}

	return ""
}
