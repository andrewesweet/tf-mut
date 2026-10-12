package engine

import (
	"fmt"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// project maps an oracle outcome onto the published report: the outcome's
// state becomes the mutant's State, and everything the outcome carries — the
// classification, the diagnostics, the suppression — is written field for
// field. A terminal outcome carries no diagnosis, so it carries no verdict
// either, which is what keeps the wire honest about which states are findings.
// The report DTO keeps its exported fields — this is the one place the Oracle
// context's vocabulary is spelled onto the wire.
func project(verdict report.Mutant, outcome oracle.Outcome) report.Mutant {
	verdict.State = projectState(outcome.State())

	if diagnostics := outcome.Diagnostics(); len(diagnostics) > 0 {
		verdict.Diagnostics = projectDiagnostics(diagnostics)
	}

	if suppression := outcome.Suppression(); suppression != nil {
		verdict.Suppression = projectSuppression(suppression)
	}

	verdict.Verdict = nil

	if outcome.Message() != "" {
		verdict.Verdict = &report.Verdict{
			Diagnosis: projectDiagnosis(outcome.Diagnosis()),
			Message:   outcome.Message(),
			Fix:       outcome.Fix(),
			Evidence:  projectEvidence(outcome.Evidence()),
		}
	}

	return verdict
}

// projectState translates the Oracle context's state vocabulary into the
// published wire spelling. The switch is exhaustive over the context's closed
// set, so a state the oracle gains is a compile-time demand on this table.
func projectState(state oracle.State) report.State {
	switch state {
	case oracle.StateInvalid:
		return report.Invalid
	case oracle.StateKilled:
		return report.Killed
	case oracle.StateKilledByError:
		return report.KilledByError
	case oracle.StateTimeout:
		return report.Timeout
	case oracle.StateSurvived:
		return report.Survived
	case oracle.StateStructurallyUnassertable:
		return report.StructurallyUnassertable
	case oracle.StateUnobservable:
		return report.Unobservable
	case oracle.StateNoCoverage:
		return report.NoCoverage
	case oracle.StateIgnored:
		return report.Ignored
	case oracle.StatePending:
		return report.Pending
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

// projectDiagnostics maps the outcome's diagnostics onto the published DTO,
// field for field, preserving the presence and absence of each location.
func projectDiagnostics(diagnostics []oracle.Diagnostic) []report.Diagnostic {
	converted := make([]report.Diagnostic, 0, len(diagnostics))

	for _, diagnostic := range diagnostics {
		entry := report.Diagnostic{
			Severity: diagnostic.Severity,
			Summary:  diagnostic.Summary,
			Detail:   diagnostic.Detail,
			Range:    nil,
			TestFile: diagnostic.TestFile,
			TestRun:  diagnostic.TestRun,
		}

		if diagnostic.Range != nil {
			entry.Range = &report.Range{
				File:  diagnostic.Range.File,
				Start: report.Position{Line: diagnostic.Range.Start.Line, Column: diagnostic.Range.Start.Column},
				End:   report.Position{Line: diagnostic.Range.End.Line, Column: diagnostic.Range.End.Column},
			}
		}

		converted = append(converted, entry)
	}

	return converted
}

// projectSuppression maps the outcome's suppression onto the published DTO,
// field for field.
func projectSuppression(suppression *oracle.Suppression) *report.Suppression {
	projected := &report.Suppression{
		Kind:      suppression.Kind,
		Operators: suppression.Operators,
		Reason:    suppression.Reason,
		Accepted:  suppression.Accepted,
		Range:     nil,
		Mutants:   suppression.Mutants,
		Rejection: suppression.Rejection,
	}

	if suppression.Range != nil {
		projected.Range = &report.Range{
			File:  suppression.Range.File,
			Start: report.Position{Line: suppression.Range.Start.Line, Column: suppression.Range.Start.Column},
			End:   report.Position{Line: suppression.Range.End.Line, Column: suppression.Range.End.Column},
		}
	}

	return projected
}

// oracleDiagnostics lifts the published diagnostics into the Oracle context's
// shape, so the terminal constructors can carry the evidence their states
// were decided from.
func oracleDiagnostics(diagnostics []report.Diagnostic) []oracle.Diagnostic {
	converted := make([]oracle.Diagnostic, 0, len(diagnostics))

	for _, diagnostic := range diagnostics {
		entry := oracle.Diagnostic{
			Severity: diagnostic.Severity,
			Summary:  diagnostic.Summary,
			Detail:   diagnostic.Detail,
			Range:    nil,
			TestFile: diagnostic.TestFile,
			TestRun:  diagnostic.TestRun,
		}

		if diagnostic.Range != nil {
			entry.Range = &oracle.Range{
				File:  diagnostic.Range.File,
				Start: oracle.Position{Line: diagnostic.Range.Start.Line, Column: diagnostic.Range.Start.Column},
				End:   oracle.Position{Line: diagnostic.Range.End.Line, Column: diagnostic.Range.End.Column},
			}
		}

		converted = append(converted, entry)
	}

	return converted
}

// oracleSuppression lifts a published suppression record into the Oracle
// context's shape, so the Ignored constructor can carry the decision the
// outcome records.
func oracleSuppression(suppression *report.Suppression) oracle.Suppression {
	lifted := oracle.Suppression{
		Kind:      suppression.Kind,
		Operators: suppression.Operators,
		Reason:    suppression.Reason,
		Accepted:  suppression.Accepted,
		Range:     nil,
		Mutants:   suppression.Mutants,
		Rejection: suppression.Rejection,
	}

	if suppression.Range != nil {
		lifted.Range = &oracle.Range{
			File:  suppression.Range.File,
			Start: oracle.Position{Line: suppression.Range.Start.Line, Column: suppression.Range.Start.Column},
			End:   oracle.Position{Line: suppression.Range.End.Line, Column: suppression.Range.End.Column},
		}
	}

	return lifted
}

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

// oracleState is the inverse of projectState, for rehydration: the closed
// published vocabulary, which since #102 is exactly the context's own state
// set. The exhaustive switches make a state gained on either side a
// compile-time demand on the pairing.
func oracleState(state report.State) oracle.State {
	switch state {
	case report.Invalid:
		return oracle.StateInvalid
	case report.Killed:
		return oracle.StateKilled
	case report.KilledByError:
		return oracle.StateKilledByError
	case report.Timeout:
		return oracle.StateTimeout
	case report.Survived:
		return oracle.StateSurvived
	case report.StructurallyUnassertable:
		return oracle.StateStructurallyUnassertable
	case report.Unobservable:
		return oracle.StateUnobservable
	case report.NoCoverage:
		return oracle.StateNoCoverage
	case report.Ignored:
		return oracle.StateIgnored
	case report.Pending:
		return oracle.StatePending
	}

	// A spelling outside the closed published set belongs to no state; the
	// parsing constructor refuses it.
	return ""
}

// oracleDiagnosis is the inverse of projectDiagnosis, for rehydration. An
// absent diagnosis maps as absent, where the terminal states record none.
// Anything outside the table, the withdrawn mock-masked spelling included, is
// not a diagnosis this context emits, so a record naming it is not one it
// stored. It is a table rather than a switch because the withdrawn spelling
// (since M3, issue #50) must be refused without being named: naming it is a
// deprecated reference, and an exhaustive switch cannot omit it.
func oracleDiagnosis(diagnosis report.Diagnosis) (oracle.Diagnosis, bool) {
	mapped, ok := map[report.Diagnosis]oracle.Diagnosis{
		"":                                "",
		report.IndeterminateUnknownValues: oracle.IndeterminateUnknownValues,
		report.IndeterminateVolatility:    oracle.IndeterminateVolatility,
		report.WeakAssertion:              oracle.WeakAssertion,
		report.NoAssertion:                oracle.NoAssertion,
		report.Unasserted:                 oracle.Unasserted,
	}[diagnosis]

	return mapped, ok
}

// storedRecord projects a stored verdict onto the Oracle context's record
// spelling, so a stored document can be rebuilt through ParseRecord exactly
// as a fresh verdict is built through the constructors. A verdict-less record
// is legal: the states whose constructors write no finding — and the
// provisional phase-one survivor — are stored and replayed without one, and
// the parsing constructor decides what the state may carry.
func storedRecord(state oracle.State, verdict *report.Verdict) (oracle.Record, error) {
	record := oracle.Record{State: state}

	if verdict == nil {
		return record, nil
	}

	diagnosis, mapped := oracleDiagnosis(verdict.Diagnosis)
	if !mapped {
		return oracle.Record{}, fmt.Errorf(
			"%w: stored diagnosis %q is not one this context emits", oracle.ErrIllegalRecord, verdict.Diagnosis,
		)
	}

	record.Diagnosis = diagnosis
	record.Message = verdict.Message
	record.Fix = verdict.Fix
	record.Delta = storedDelta(verdict.Evidence.Delta)
	record.UnknownPaths = verdict.Evidence.UnknownPaths
	record.VolatileComponents = verdict.Evidence.VolatileComponents
	record.UnstableAttributes = verdict.Evidence.UnstableAttributes
	record.Assertion = verdict.Evidence.Assertion
	record.ClosureVerdict = verdict.Evidence.ClosureVerdict
	record.DefeatedBy = verdict.Evidence.DefeatedBy

	return record, nil
}

// storedDelta projects the stored delta onto the context's change value,
// preserving absent and empty: a diagnosis that recorded no delta keeps
// recording none, and the projection of the rebuilt outcome must be the
// document that was stored.
func storedDelta(changes []report.Change) []fingerprint.Change {
	if changes == nil {
		return nil
	}

	converted := make([]fingerprint.Change, len(changes))

	for index, change := range changes {
		converted[index] = fingerprint.Change{
			Run:       change.Run,
			Path:      change.Path,
			Address:   change.Address,
			Baseline:  change.Baseline,
			Mutant:    change.Mutant,
			Sensitive: change.Sensitive,
		}
	}

	return converted
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

// gradingProjection is the report projection's subscriber state: what each
// published conclusion contributes to the report, held until the report is
// projected once, last.
type gradingProjection struct {
	plan        executionPlan
	moduleDir   string
	shell       report.Report
	evaluated   evaluatedPopulation
	completed   completedRun
	suggestions []report.Suggestion
	cost        string
	labels      map[int]string
}

// completeReport computes the aggregates over the classified population.
func (p *gradingProjection) completeReport(event PopulationClassified) error {
	finalisePhase := p.plan.collector.phase(phaseFinalise)
	p.completed = completeReport(p.plan.configuration, p.plan.settings, p.plan.populationDescribed, p.shell,
		p.evaluated.rows, event.population)
	finalisePhase()

	return nil
}

// accept applies the baseline gate to the classified population.
func (p *gradingProjection) accept(event PopulationClassified) error {
	labels, err := applyBaselineGate(p.plan.settings, p.moduleDir, p.shell, p.evaluated.rows,
		event.population, p.completed.gates)
	p.labels = labels

	return err
}

// subscribeSuggestions projects each suggestion outcome in publication order,
// which is survivor order, and the verification cost statement.
func (p *gradingProjection) subscribeSuggestions(events *dispatcher) {
	p.suggestions = []report.Suggestion{}

	events.suggestionProposed.subscribe(func(event SuggestionProposed) error {
		p.suggestions = append(p.suggestions, projectCandidate(event.candidate))

		return nil
	})
	events.suggestionVerified.subscribe(func(event SuggestionVerified) error {
		p.suggestions = append(p.suggestions, projectSuggestion(event.suggestion))

		return nil
	})
	events.suggestionRefuted.subscribe(func(event SuggestionRefuted) error {
		p.suggestions = append(p.suggestions, projectSuggestion(event.suggestion))

		return nil
	})
	events.suggestionSkipped.subscribe(func(event SuggestionSkipped) error {
		p.suggestions = append(p.suggestions, projectSuggestion(event.suggestion))

		return nil
	})
	events.verificationCosted.subscribe(func(event VerificationCosted) error {
		p.cost = event.costs.Statement()

		return nil
	})
}

// project attaches the aggregate results computed over the population fact.
// Per-mutant projections already accompany their classified rows; the shell's
// selection, sampling, population split and suppressions pass through without
// becoming inputs to the outcome arithmetic.
func (p *gradingProjection) project() report.Report {
	result := p.shell
	result.Mutants = p.evaluated.rows
	result.Errors = p.evaluated.failures
	result.Metrics = p.completed.metrics
	result.OperatorErrors = p.completed.operatorErrors
	result.Findings = p.completed.findings
	result.Warnings = append(result.Warnings, p.completed.warnings...)
	if p.cost != "" {
		result.Warnings = append(result.Warnings, p.cost)
	}

	result.Gates = p.completed.gates
	result.Suggestions = p.suggestions

	for index, label := range p.labels {
		if label != "" {
			labelFinding(&result.Mutants[index], label)
		}
	}

	return result
}

// conditionallyNoCoverageClaim is the finding a pre-classified mutant
// carries: the reason the oracle's NoCoverage constructor states for it. The
// fact itself — that the block is never instantiated — is published by
// discovery's ConditionalMultiplicity.
const conditionallyNoCoverageClaim = "the mutated block's multiplicity expression is statically zero under every " +
	"relevant run, so no run instantiates the block and nothing can execute the mutation"

// describeMutants converts generated mutants into report values, assigning the
// statically decidable states before anything executes: module-level
// NoCoverage first — the structural precedence and the M2 behaviour — then
// the guarded static Unobservable shortcut (M3a.2).
//
// Every described row leaves with the outcome classified for it, so the
// population fact the run reports is built at the classification sites
// themselves and never re-parsed from the published values.
func describeMutants(
	configuration discovery.Configuration,
	graph *discovery.Graph,
	settings settings,
	generated []mutation.Mutant,
) ([]report.Mutant, []oracle.Classified) {
	exercised := configuration.ExercisedModules()
	described := make([]report.Mutant, 0, len(generated))
	classified := make([]oracle.Classified, 0, len(generated))
	shortcutsDisabled := settings.staticShortcutsDisabled || disableStaticShortcuts(settings)

	for _, mutant := range generated {
		entry := report.Mutant{
			ID:       mutant.ID,
			Operator: string(mutant.Operator),
			Tier:     string(mutation.TierOf(mutant.Operator)),
			Module:   mutant.ModuleRel,
			Site:     mutant.Site,
			Resource: mutant.Resource,
			Range: report.Range{
				File:  mutant.File,
				Start: report.Position{Line: mutant.Range.Start.Line, Column: mutant.Range.Start.Column},
				End:   report.Position{Line: mutant.Range.End.Line, Column: mutant.Range.End.Column},
			},
			Diff:    mutant.Diff,
			Runs:    []report.RunOutcome{},
			Origins: projectOrigins(mutant.Origins),
		}

		var outcome oracle.Outcome

		switch {
		case !exercised[mutant.ModuleRel]:
			// Module-level NoCoverage: the report states the absence as a
			// count and no finding, so the outcome carries the state and
			// nothing else.
			outcome = oracle.NoCoverage("")
		case settings.mode != previewMode && !shortcutsDisabled &&
			configuration.ConditionalMultiplicity(graph, settings.Env, discovery.Site{
				ModuleRel: mutant.ModuleRel,
				File:      mutant.File,
				Address:   mutant.Site,
				Resource:  mutant.Resource,
				Mutated:   mutant.Mutated,
			}):
			// The finer conditional-instantiation claim (M3a.3): the mutated
			// multiplicity expression is statically zero under every relevant
			// run. Module-level NoCoverage above remains the strict subset.
			outcome = oracle.NoCoverage(conditionallyNoCoverageClaim)
		case settings.mode != previewMode && !shortcutsDisabled &&
			staticallyUnobservable(graph, mutant):
			// A preview keeps Pending — the documented preview contract — so
			// the shortcut fires only where execution would otherwise run.
			outcome = oracle.StaticallyUnobservable()
		default:
			// Pending stands: the state every executable mutant carries until
			// something decides otherwise. A preview retains it after the
			// module-level NoCoverage check above.
			outcome = oracle.Pending()
		}

		described = append(described, project(entry, outcome))
		classified = append(classified, oracle.Classified{Operator: mutant.Operator, Outcome: outcome})
	}

	return described, classified
}

// projectOrigins projects the generator's origins onto the published DTO:
// present exactly when at least one pack entry contributed, never empty.
func projectOrigins(origins []mutation.Origin) []report.Origin {
	if len(origins) == 0 {
		return nil
	}

	projected := make([]report.Origin, 0, len(origins))
	for _, origin := range origins {
		projected = append(projected, report.Origin{
			Operator: string(origin.Operator), Pack: origin.Pack, Entry: origin.Entry,
		})
	}

	return projected
}

// describeUnmatched renders preview's pack summary, one line per entry that
// produced nothing.
func describeUnmatched(unmatched []mutation.Origin) []string {
	described := make([]string, 0, len(unmatched))
	for _, origin := range unmatched {
		described = append(described, fmt.Sprintf("pack %q: entry %q (%s) matched no site: no top-level "+
			"resource argument of its type and attribute holds its literal, or the schema does not "+
			"describe the attribute at the entry's type", origin.Pack, origin.Entry, origin.Operator))
	}

	return described
}

// staticallyUnobservable applies the guarded static shortcut (M3a.2, review
// C2). Non-projecting operators bypass this shortcut; describeMutants checks
// NoCoverage first. The shortcut fires only where the site
// maps into the graph (unmapped fails closed to execution) and its forward
// cone reaches nothing observable: no resource, data source, output, check or
// contract construct.
func staticallyUnobservable(graph *discovery.Graph, mutant mutation.Mutant) bool {
	if !mutation.Projects(mutant.Operator) {
		return false
	}

	cone, ok := graph.SiteCone(mutant.ModuleRel, mutant.Site)
	if !ok {
		return false
	}

	return !cone.ContainsObservable()
}
