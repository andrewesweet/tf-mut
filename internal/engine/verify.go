package engine

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/suggest"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// Verification, under the one contract the M4 spec review's M1 disposition
// chose: for each target test file the candidate batch is applied to a sandbox
// copy and the full suite runs — green required; each suggestion is then
// checked alone against its re-materialised mutant — failure required.
//
// The two legs answer different questions and neither alone is enough. The
// baseline leg rejects an assertion that breaks the suite; the isolated mutant
// leg rejects one that is vacuously true and would have passed anyway.
// Isolation is what makes the kill attributable: applying the whole batch would
// let one good assertion launder its neighbours.
//
// A baseline leg that comes back not green is attributed before any isolated
// leg is spent: the failing runs' diagnostics name the suggestions whose
// assertions failed, only those are refuted, and the batch re-runs without
// them — bisected when a failure names nothing — so one bad assertion no
// longer refutes its whole file.
//
// Nothing here reads or writes the verdict cache. A verification run is about a
// file that exists in no source tree, and a cached verdict about it would be a
// claim about a program nobody has.

// verifySuggestions runs both legs over every candidate and assigns the
// verified or refuted outcome with its evidence. The only route from a
// candidate to a terminal outcome is the transition that demands the digest
// and both legs, so what comes back carries what it claims.
func verifySuggestions(
	ctx context.Context,
	plan executionPlan,
	candidates []suggest.Candidate,
) ([]suggest.Suggestion, verificationCosts, error) {
	byFile := groupCandidates(candidates)
	if len(byFile) == 0 {
		return nil, verificationCosts{}, nil
	}

	mutated := mutantsByID(plan)
	outcomes := map[string]suggest.Suggestion{}
	costs := verificationCosts{}

	for _, file := range sortedTargets(byFile) {
		group := byFile[file]

		attributed, err := attributeBatch(ctx, plan, file, group, &costs)
		if err != nil {
			return nil, verificationCosts{}, err
		}

		maps.Copy(outcomes, attributed.refuted)

		for _, candidate := range attributed.survivors {
			outcomes[candidate.ID()] = decide(ctx, plan, mutated, candidate,
				attributed.baseline[candidate.ID()], attributed.digest[candidate.ID()], &costs)
		}
	}

	concluded := make([]suggest.Suggestion, 0, len(candidates))
	for _, candidate := range candidates {
		concluded = append(concluded, outcomes[candidate.ID()])
	}

	return concluded, costs, nil
}

// verificationCosts counts what verification actually executed, so the
// report's cost statement states runs that happened rather than a contract's
// upper bound.
type verificationCosts struct {
	fullSuite int
	isolated  int
}

// batchAttribution is the baseline leg's outcome over one target file's
// candidates: who it refuted, who survived to the isolated legs, and the
// green leg and digest each survivor was proven against. A bisected pool
// proves its halves on different legs, so the baseline evidence is
// per-candidate rather than one leg for the file.
type batchAttribution struct {
	refuted   map[string]suggest.Suggestion
	survivors []suggest.Candidate
	baseline  map[string]suggest.Leg
	digest    map[string]string
}

// attributionBudget bounds the baseline leg's full-suite runs for one file's
// batch of n candidates: the initial run plus, per round, either a named
// refutation (the pool shrinks by at least one) or a bisection split (toward
// singletons), and a binary decomposition of n candidates never exceeds 2n
// runs. Past the cap every unattributed candidate is refuted, as the whole
// batch was before attribution existed: the contract's requirement — the
// suite green with the batch applied — is unmet, so none of them can be
// verified.
func attributionBudget(batch []suggest.Candidate) int {
	return 2*len(batch) + 1
}

// attributeBatch runs the baseline leg over one target file and attributes a
// failing batch before any isolated leg is spent.
//
// The contract is the batch green; when the run is not, the failing runs'
// diagnostics attribute the failure: every applied assertion carries its
// suggestion's identifier in its error_message, and Terraform copies that
// message verbatim into the failing run's diagnostic. Only the named
// candidates are refuted, and the batch re-runs without them. A failure that
// names no candidate — an assertion whose presence errors the run, whose
// diagnostic is Terraform's own — bisects the pool until a failing half is a
// single candidate.
func attributeBatch(
	ctx context.Context,
	plan executionPlan,
	file string,
	group []suggest.Candidate,
	costs *verificationCosts,
) (batchAttribution, error) {
	attributed := batchAttribution{
		refuted:   map[string]suggest.Suggestion{},
		survivors: []suggest.Candidate{},
		baseline:  map[string]suggest.Leg{},
		digest:    map[string]string{},
	}

	a := &attributor{
		plan:        plan,
		file:        file,
		budget:      attributionBudget(group),
		costs:       costs,
		attribution: attributed,
	}

	if err := a.attribute(ctx, group); err != nil {
		return batchAttribution{}, err //nolint:exhaustruct // nothing was concluded.
	}

	return a.attribution, nil
}

// attributor carries one file's attribution in progress: the remaining
// budget of full-suite runs, and the verdicts assigned so far.
type attributor struct {
	plan        executionPlan
	file        string
	budget      int
	costs       *verificationCosts
	attribution batchAttribution
}

// bisectionDivisor is where a failing unnamed pool splits: a failing half
// retains at least one culprit until the halves are singletons, which no
// other split ratio guarantees for every pool size.
const bisectionDivisor = 2

// attribute runs the batch leg over pool and assigns its verdicts, recursing
// on the failure shapes the attribution contract defines.
func (a *attributor) attribute(ctx context.Context, pool []suggest.Candidate) error {
	if len(pool) == 0 {
		return nil
	}

	if a.budget <= 0 {
		a.exhausted(pool)

		return nil
	}

	a.budget--

	run, err := runBatch(ctx, a.plan, a.file, pool, &a.costs.fullSuite)
	if err != nil {
		return err
	}

	if run.leg.Passed() {
		for _, candidate := range pool {
			a.attribution.survivors = append(a.attribution.survivors, candidate)
			a.attribution.baseline[candidate.ID()] = run.leg
			a.attribution.digest[candidate.ID()] = run.digest
		}

		return nil
	}

	named := namedCandidates(run.result, pool)

	switch {
	case len(named) > 0:
		for _, candidate := range named {
			a.attribution.refuted[candidate.ID()] = candidate.Refute(
				"the full suite is not green with the suggested assertions applied, and the failing run names this suggestion",
				suggest.NewVerification(run.leg, suggest.NewLeg(false, nil,
					"not run: the failing batch run names this suggestion")),
			)
		}

		return a.attribute(ctx, withoutCandidates(pool, named))

	case len(pool) == 1:
		a.attribution.refuted[pool[0].ID()] = pool[0].Refute(
			"the full suite is not green with this suggestion applied on its own, "+
				"so it breaks the suite the contract requires green",
			suggest.NewVerification(run.leg, suggest.NewLeg(false, nil,
				"not run: the batch of one failed without naming any suggestion")),
		)

		return nil

	default:
		half := len(pool) / bisectionDivisor

		if err := a.attribute(ctx, pool[:half]); err != nil {
			return err
		}

		return a.attribute(ctx, pool[half:])
	}
}

// exhausted refutes every candidate the budget gave out on. It is the bound's
// failure mode, not a verdict about any one assertion: no candidate still in
// an unattributed pool has proven the batch green, so none can be verified.
func (a *attributor) exhausted(pool []suggest.Candidate) {
	for _, candidate := range pool {
		a.attribution.refuted[candidate.ID()] = candidate.Refute(
			"the verification budget ran out while the suite was still not green with the suggested assertions applied",
			suggest.NewVerification(
				suggest.NewLeg(false, nil, "not run: the attribution budget was exhausted"),
				suggest.NewLeg(false, nil, "not run: the attribution budget was exhausted"),
			),
		)
	}
}

// namedCandidates extracts the pool candidates a failing batch run named, in
// pool order. A candidate the run names carried an assertion the baseline
// refutes — that is what a failed assertion is — and no candidate the run
// says nothing about is refuted on this run's evidence alone.
func namedCandidates(result tfexec.TestResult, pool []suggest.Candidate) []suggest.Candidate {
	inPool := make(map[string]suggest.Candidate, len(pool))
	for _, candidate := range pool {
		inPool[candidate.ID()] = candidate
	}

	named := []suggest.Candidate{}
	seen := map[string]bool{}

	for _, diagnostic := range result.Diagnostics {
		id, ok := suggest.SuggestionIDFromMessage(diagnostic.Detail)
		if !ok || seen[id] {
			continue
		}

		candidate, found := inPool[id]
		if !found {
			continue
		}

		seen[id] = true
		named = append(named, candidate)
	}

	return named
}

// withoutCandidates removes the named candidates from the pool, keeping order.
func withoutCandidates(pool, named []suggest.Candidate) []suggest.Candidate {
	remove := make(map[string]bool, len(named))
	for _, candidate := range named {
		remove[candidate.ID()] = true
	}

	kept := make([]suggest.Candidate, 0, len(pool))
	for _, candidate := range pool {
		if !remove[candidate.ID()] {
			kept = append(kept, candidate)
		}
	}

	return kept
}

// decide runs the isolated mutant leg where the baseline leg allows it, and
// assigns the outcome.
func decide(
	ctx context.Context,
	plan executionPlan,
	mutated map[string]mutation.Mutant,
	candidate suggest.Candidate,
	baseline suggest.Leg,
	digest string,
	costs *verificationCosts,
) suggest.Suggestion {
	if !baseline.Passed() {
		return candidate.Refute(
			"the full suite is not green with the suggested assertions applied",
			suggest.NewVerification(baseline, suggest.NewLeg(false, nil,
				"not run: the baseline leg refuted the batch this suggestion belongs to")),
		)
	}

	// One isolated check per mutant the suggestion claims: a collapsed
	// suggestion lists its extra kills in AlsoKills, and attribution stays
	// per-mutant — every listed mutant must die against this assertion alone.
	runs := []suggest.RunRecord{}
	details := []string{}

	for _, mutantID := range append([]string{candidate.MutantID()}, candidate.AlsoKills()...) {
		leg, err := verifyAgainstMutant(ctx, plan, mutated, candidate, mutantID, &costs.isolated)
		if err != nil {
			return candidate.Refute(
				"the isolated mutant leg could not be executed: "+err.Error(),
				suggest.NewVerification(baseline, suggest.NewLeg(false, nil, err.Error())),
			)
		}

		runs = append(runs, leg.Runs()...)
		details = append(details, leg.Detail())

		if !leg.Passed() {
			return candidate.Refute(
				mutantLegRefutation(mutantID, leg),
				suggest.NewVerification(baseline,
					suggest.NewLeg(false, runs, strings.Join(details, "; "))),
			)
		}
	}

	return candidate.Verify(digest, suggest.NewVerification(baseline,
		suggest.NewLeg(true, runs, strings.Join(details, "; "))))
}

// mutantLegRefutation words the refutation from the statuses the leg's runs
// actually produced. A run that errored did not survive — the mutant's target
// crashed under the assertion rather than passing it — and a reason claiming
// survival would be false about what the leg executed.
func mutantLegRefutation(mutantID string, leg suggest.Leg) string {
	if legErrored(leg) {
		return fmt.Sprintf("mutant %s errored the suite with the suggested assertion applied on "+
			"its own; the run errored rather than failed, so the assertion does not kill it", mutantID)
	}

	return fmt.Sprintf("mutant %s survived the suggested assertion applied on its own, so the "+
		"assertion does not kill it", mutantID)
}

// legErrored reports whether any run the leg executed errored.
func legErrored(leg suggest.Leg) bool {
	for _, run := range leg.Runs() {
		if run.Status() == tfexec.StatusError {
			return true
		}
	}

	return false
}

// batchRun is one baseline-leg execution: the verdict leg, the raw result —
// attribution reads the failing runs' diagnostics, which the leg alone does
// not carry — and the content digest the run was proven on.
type batchRun struct {
	leg    suggest.Leg
	result tfexec.TestResult
	digest string
}

// runBatch is one baseline-leg run: the given candidates applied at once to
// the target file's current content, and the full suite run once over the
// result.
func runBatch(
	ctx context.Context,
	plan executionPlan,
	file string,
	candidates []suggest.Candidate,
	executions *int,
) (batchRun, error) {
	content, digest, err := batched(plan, file, candidates)
	if err != nil {
		return batchRun{}, err //nolint:exhaustruct // nothing ran.
	}

	result, err := runVerification(ctx, plan, "batch-"+shortName(file), map[string][]byte{
		closureRelative(plan, file): content,
	}, executions)
	if err != nil {
		return batchRun{}, err //nolint:exhaustruct // nothing ran.
	}

	green := !result.HasStatus(tfexec.StatusFail) &&
		!result.HasStatus(tfexec.StatusError) && result.ExecutedRuns() > 0

	detail := fmt.Sprintf("the full suite ran %d run block(s) with %d suggested assertion(s) applied to %s",
		result.ExecutedRuns(), len(candidates), file)
	if !green {
		detail = fmt.Sprintf("the full suite did not stay green with %d suggested assertion(s) applied to %s",
			len(candidates), file)
	}

	return batchRun{
		leg:    suggest.NewLeg(green, runRecords(result), detail),
		result: result,
		digest: digest,
	}, nil
}

// verifyAgainstMutant is the isolated leg: this suggestion alone, against the
// re-materialised mutant it claims to kill. A run that fails is the required
// outcome; a run that errors is not, because an assertion that crashes is not
// an assertion that catches anything.
func verifyAgainstMutant(
	ctx context.Context,
	plan executionPlan,
	mutated map[string]mutation.Mutant,
	candidate suggest.Candidate,
	mutantID string,
	executions *int,
) (suggest.Leg, error) {
	mutant, found := mutated[mutantID]
	if !found {
		return suggest.Leg{}, //nolint:exhaustruct // nothing ran.
			fmt.Errorf("%w: mutant %s is not in this population", ErrSurvivorSelection, mutantID)
	}

	original, err := suggest.ReadTarget(plan.configuration.ModuleDir, candidate.TargetFile())
	if err != nil {
		return suggest.Leg{}, err //nolint:exhaustruct // nothing ran.
	}

	alone, err := suggest.Apply(original, candidate.TargetFile(), candidate.TargetRun(),
		candidate.Expression(), verificationMessage(candidate))
	if err != nil {
		return suggest.Leg{}, err //nolint:exhaustruct // nothing ran.
	}

	result, err := runVerification(ctx, plan, "kill-"+candidate.ID()+"-"+mutantID, map[string][]byte{
		closureRelative(plan, candidate.TargetFile()): alone,
		mutant.File: mutant.Mutated,
	}, executions)
	if err != nil {
		return suggest.Leg{}, err //nolint:exhaustruct // nothing ran.
	}

	killed := result.HasStatus(tfexec.StatusFail)
	errored := result.HasStatus(tfexec.StatusError)

	detail := fmt.Sprintf("mutant %s survived %d run block(s) with this suggestion applied alone",
		mutantID, result.ExecutedRuns())
	if errored && !killed {
		detail = fmt.Sprintf("mutant %s errored %d run block(s) with this suggestion applied alone; "+
			"the run errored rather than failed", mutantID, result.ExecutedRuns())
	}
	if killed {
		detail = fmt.Sprintf("mutant %s failed the suggested assertion applied on its own, "+
			"so the kill is attributable to this suggestion", mutantID)
	}

	return suggest.NewLeg(killed, runRecords(result), detail), nil
}

// runVerification materialises one throwaway sandbox and runs the whole suite
// in it. No filter is applied: the contract is the full suite, once.
func runVerification(
	ctx context.Context,
	plan executionPlan,
	name string,
	mutations map[string][]byte,
	executions *int,
) (tfexec.TestResult, error) {
	built, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: plan.configuration.ClosureRoot,
		ModuleRel:  plan.configuration.RootRelative(),
		Target:     filepath.Join(plan.workRoot, "verify-"+name),
		Mutations:  mutations,
		Share: &sandbox.Share{
			DataDir:  plan.prepared.dataDir,
			LockFile: plan.prepared.lockFile,
		},
		Hardlink: true,
	})
	if err != nil {
		return tfexec.TestResult{}, //nolint:exhaustruct // nothing ran.
			fmt.Errorf("materialising the verification sandbox: %w", err)
	}

	defer func() { _ = os.RemoveAll(built.Root) }()

	verificationPhase := plan.collector.phase(phaseSuggestVerification)
	result, err := plan.runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: plan.configuration.TestDirRelative(),
		Filters:       nil,
		Verbose:       false,
		Timeout:       0,
	})
	verificationPhase()
	if result.Started {
		plan.collector.child("test", result.Usage)
		*executions++
	}

	if err != nil {
		return tfexec.TestResult{}, //nolint:exhaustruct // the run did not complete.
			fmt.Errorf("running the verification suite: %w", err)
	}

	return result, nil
}

// batched applies every candidate of one target file to its current content,
// and returns the digest of the content they were verified against.
func batched(
	plan executionPlan,
	file string,
	candidates []suggest.Candidate,
) ([]byte, string, error) {
	original, err := suggest.ReadTarget(plan.configuration.ModuleDir, file)
	if err != nil {
		return nil, "", err
	}

	content := original

	for _, candidate := range candidates {
		content, err = suggest.Apply(content, file, candidate.TargetRun(),
			candidate.Expression(), verificationMessage(candidate))
		if err != nil {
			return nil, "", err
		}
	}

	return content, suggest.Digest(original), nil
}

// verificationMessage names the suggestion and the mutant and never the
// compared value. It is the same renderer apply uses, deliberately: the bytes
// verified and the bytes written must be identical.
func verificationMessage(candidate suggest.Candidate) string {
	return suggest.VerifiedMessage(candidate.ID(), candidate.MutantID())
}

// runRecords translates a verification run's executed run blocks into the
// evidence shape the Suggestion context carries.
func runRecords(result tfexec.TestResult) []suggest.RunRecord {
	records := make([]suggest.RunRecord, 0, len(result.Runs))

	for _, run := range result.Runs {
		records = append(records, suggest.NewRunRecord(run.File, run.Run, phaseOne, run.Status))
	}

	return records
}

// mutantsByID indexes the generated population so a suggestion can
// re-materialise the mutant it claims to kill.
func mutantsByID(plan executionPlan) map[string]mutation.Mutant {
	indexed := make(map[string]mutation.Mutant, len(plan.generated))
	for _, mutant := range plan.generated {
		indexed[mutant.ID] = mutant
	}

	return indexed
}

// closureRelative converts a module-relative test file path into the
// closure-relative key a sandbox mutation is addressed by.
func closureRelative(plan executionPlan, moduleRelative string) string {
	absolute := suggest.TargetPath(plan.configuration.ModuleDir, moduleRelative)

	relative, err := filepath.Rel(plan.configuration.ClosureRoot, absolute)
	if err != nil {
		return moduleRelative
	}

	return filepath.ToSlash(relative)
}

// shortName makes a filesystem-safe sandbox name out of a path.
func shortName(path string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(path)
}

func groupCandidates(candidates []suggest.Candidate) map[string][]suggest.Candidate {
	byFile := map[string][]suggest.Candidate{}

	for _, candidate := range candidates {
		byFile[candidate.TargetFile()] = append(byFile[candidate.TargetFile()], candidate)
	}

	return byFile
}

// sortedTargets orders a by-file grouping deterministically. The grouping's
// value type is whichever suggestion representation the caller holds: apply
// reads the published DTO, verification reads the context's candidates.
func sortedTargets[V any](byFile map[string][]V) []string {
	files := make([]string, 0, len(byFile))
	for file := range byFile {
		files = append(files, file)
	}

	slices.Sort(files)

	return files
}
