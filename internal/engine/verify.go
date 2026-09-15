package engine

import (
	"context"
	"fmt"
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
// copy and the full suite runs once — green required; each suggestion is then
// checked alone against its re-materialised mutant — failure required.
//
// The two legs answer different questions and neither alone is enough. The
// baseline leg rejects an assertion that breaks the suite; the isolated mutant
// leg rejects one that is vacuously true and would have passed anyway.
// Isolation is what makes the kill attributable: applying the whole batch would
// let one good assertion launder its neighbours.
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
) ([]suggest.Suggestion, error) {
	byFile := groupCandidates(candidates)
	if len(byFile) == 0 {
		return nil, nil
	}

	mutated := mutantsByID(plan)
	outcomes := map[string]suggest.Suggestion{}

	for _, file := range sortedTargets(byFile) {
		group := byFile[file]

		baseline, digest, err := verifyBatch(ctx, plan, file, group)
		if err != nil {
			return nil, err
		}

		for _, candidate := range group {
			outcomes[candidate.ID()] = decide(ctx, plan, mutated, candidate, baseline, digest)
		}
	}

	concluded := make([]suggest.Suggestion, 0, len(candidates))
	for _, candidate := range candidates {
		concluded = append(concluded, outcomes[candidate.ID()])
	}

	return concluded, nil
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
		leg, err := verifyAgainstMutant(ctx, plan, mutated, candidate, mutantID)
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
				fmt.Sprintf("mutant %s survived the suggested assertion applied on its own, so the "+
					"assertion does not kill it", mutantID),
				suggest.NewVerification(baseline,
					suggest.NewLeg(false, runs, strings.Join(details, "; "))),
			)
		}
	}

	return candidate.Verify(digest, suggest.NewVerification(baseline,
		suggest.NewLeg(true, runs, strings.Join(details, "; "))))
}

// verifyBatch is the baseline leg: every candidate of one target file applied
// at once, and the full suite run once over the result.
func verifyBatch(
	ctx context.Context,
	plan executionPlan,
	file string,
	candidates []suggest.Candidate,
) (suggest.Leg, string, error) {
	content, digest, err := batched(plan, file, candidates)
	if err != nil {
		return suggest.Leg{}, "", err //nolint:exhaustruct // nothing ran.
	}

	result, err := runVerification(ctx, plan, "batch-"+shortName(file), map[string][]byte{
		closureRelative(plan, file): content,
	})
	if err != nil {
		return suggest.Leg{}, "", err //nolint:exhaustruct // nothing ran.
	}

	green := !result.HasStatus(tfexec.StatusFail) &&
		!result.HasStatus(tfexec.StatusError) && result.ExecutedRuns() > 0

	detail := fmt.Sprintf("the full suite ran %d run block(s) with %d suggested assertion(s) applied to %s",
		result.ExecutedRuns(), len(candidates), file)
	if !green {
		detail = fmt.Sprintf("the full suite did not stay green with %d suggested assertion(s) applied to %s",
			len(candidates), file)
	}

	return suggest.NewLeg(green, runRecords(result), detail), digest, nil
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
	})
	if err != nil {
		return suggest.Leg{}, err //nolint:exhaustruct // nothing ran.
	}

	killed := result.HasStatus(tfexec.StatusFail)

	detail := fmt.Sprintf("mutant %s survived %d run block(s) with this suggestion applied alone",
		mutantID, result.ExecutedRuns())
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

	result, err := plan.runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: plan.configuration.TestDirRelative(),
		Filters:       nil,
		Verbose:       false,
		Timeout:       0,
	})
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
