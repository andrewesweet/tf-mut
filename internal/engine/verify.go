package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
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
// verified or refuted outcome with its evidence.
func verifySuggestions(
	ctx context.Context,
	plan executionPlan,
	suggestions []report.Suggestion,
) ([]report.Suggestion, error) {
	byFile := groupCandidates(suggestions)
	if len(byFile) == 0 {
		return suggestions, nil
	}

	mutated := mutantsByID(plan)
	outcomes := map[string]report.Suggestion{}

	for _, file := range sortedTargets(byFile) {
		group := byFile[file]

		baseline, digest, err := verifyBatch(ctx, plan, file, group)
		if err != nil {
			return nil, err
		}

		for _, candidate := range group {
			outcomes[candidate.ID] = decide(ctx, plan, mutated, candidate, baseline, digest)
		}
	}

	verified := slices.Clone(suggestions)
	for index, suggestion := range verified {
		if outcome, found := outcomes[suggestion.ID]; found {
			verified[index] = outcome
		}
	}

	return verified, nil
}

// decide runs the isolated mutant leg where the baseline leg allows it, and
// assigns the outcome.
func decide(
	ctx context.Context,
	plan executionPlan,
	mutated map[string]mutation.Mutant,
	candidate report.Suggestion,
	baseline report.VerificationLeg,
	digest string,
) report.Suggestion {
	if !baseline.Passed {
		return refute(candidate, baseline, report.VerificationLeg{
			Passed: false, Runs: []report.RunOutcome{},
			Detail: "not run: the baseline leg refuted the batch this suggestion belongs to",
		}, "the full suite is not green with the suggested assertions applied")
	}

	mutantLeg, err := verifyAgainstMutant(ctx, plan, mutated, candidate)
	if err != nil {
		return refute(candidate, baseline, report.VerificationLeg{
			Passed: false, Runs: []report.RunOutcome{}, Detail: err.Error(),
		}, "the isolated mutant leg could not be executed: "+err.Error())
	}

	if !mutantLeg.Passed {
		return refute(candidate, baseline, mutantLeg,
			"the mutant survived the suggested assertion applied on its own, so the "+
				"assertion does not kill it")
	}

	candidate.Status = report.SuggestionVerified
	candidate.VerifiedDigest = digest
	candidate.Verification = &report.Verification{Baseline: baseline, Mutant: mutantLeg}

	return candidate
}

func refute(
	candidate report.Suggestion,
	baseline, mutantLeg report.VerificationLeg,
	reason string,
) report.Suggestion {
	candidate.Status = report.SuggestionRefuted
	candidate.VerifiedDigest = ""
	candidate.StatusReason = reason
	candidate.Verification = &report.Verification{Baseline: baseline, Mutant: mutantLeg}

	return candidate
}

// verifyBatch is the baseline leg: every candidate of one target file applied
// at once, and the full suite run once over the result.
func verifyBatch(
	ctx context.Context,
	plan executionPlan,
	file string,
	candidates []report.Suggestion,
) (report.VerificationLeg, string, error) {
	content, digest, err := batched(plan, file, candidates)
	if err != nil {
		return report.VerificationLeg{}, "", err //nolint:exhaustruct // nothing ran.
	}

	result, err := runVerification(ctx, plan, "batch-"+shortName(file), map[string][]byte{
		closureRelative(plan, file): content,
	})
	if err != nil {
		return report.VerificationLeg{}, "", err //nolint:exhaustruct // nothing ran.
	}

	green := !result.HasStatus(tfexec.StatusFail) &&
		!result.HasStatus(tfexec.StatusError) && result.ExecutedRuns() > 0

	detail := fmt.Sprintf("the full suite ran %d run block(s) with %d suggested assertion(s) applied to %s",
		result.ExecutedRuns(), len(candidates), file)
	if !green {
		detail = fmt.Sprintf("the full suite did not stay green with %d suggested assertion(s) applied to %s",
			len(candidates), file)
	}

	return report.VerificationLeg{
		Passed: green, Runs: runOutcomes(result), Detail: detail,
	}, digest, nil
}

// verifyAgainstMutant is the isolated leg: this suggestion alone, against the
// re-materialised mutant it claims to kill. A run that fails is the required
// outcome; a run that errors is not, because an assertion that crashes is not
// an assertion that catches anything.
func verifyAgainstMutant(
	ctx context.Context,
	plan executionPlan,
	mutated map[string]mutation.Mutant,
	candidate report.Suggestion,
) (report.VerificationLeg, error) {
	mutant, found := mutated[candidate.MutantID]
	if !found {
		return report.VerificationLeg{}, //nolint:exhaustruct // nothing ran.
			fmt.Errorf("%w: mutant %s is not in this population", ErrSurvivorSelection, candidate.MutantID)
	}

	original, err := suggest.ReadTarget(plan.configuration.ModuleDir, candidate.TargetFile)
	if err != nil {
		return report.VerificationLeg{}, err //nolint:exhaustruct // nothing ran.
	}

	alone, err := suggest.Apply(original, candidate.TargetFile, candidate.TargetRun,
		candidate.Expression, verificationMessage(candidate))
	if err != nil {
		return report.VerificationLeg{}, err //nolint:exhaustruct // nothing ran.
	}

	result, err := runVerification(ctx, plan, "kill-"+candidate.ID, map[string][]byte{
		closureRelative(plan, candidate.TargetFile): alone,
		mutant.File: mutant.Mutated,
	})
	if err != nil {
		return report.VerificationLeg{}, err //nolint:exhaustruct // nothing ran.
	}

	killed := result.HasStatus(tfexec.StatusFail)

	detail := fmt.Sprintf("the mutant survived %d run block(s) with this suggestion applied alone",
		result.ExecutedRuns())
	if killed {
		detail = "the mutant failed the suggested assertion applied on its own, " +
			"so the kill is attributable to this suggestion"
	}

	return report.VerificationLeg{Passed: killed, Runs: runOutcomes(result), Detail: detail}, nil
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
	candidates []report.Suggestion,
) ([]byte, string, error) {
	original, err := suggest.ReadTarget(plan.configuration.ModuleDir, file)
	if err != nil {
		return nil, "", err
	}

	content := original

	for _, candidate := range candidates {
		content, err = suggest.Apply(content, file, candidate.TargetRun,
			candidate.Expression, verificationMessage(candidate))
		if err != nil {
			return nil, "", err
		}
	}

	return content, suggest.Digest(original), nil
}

// verificationMessage names the suggestion and the mutant and never the
// compared value. It is the same renderer apply uses, deliberately: the bytes
// verified and the bytes written must be identical.
func verificationMessage(candidate report.Suggestion) string {
	return suggest.VerifiedMessage(candidate.ID, candidate.MutantID)
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

func groupCandidates(suggestions []report.Suggestion) map[string][]report.Suggestion {
	byFile := map[string][]report.Suggestion{}

	for _, suggestion := range suggestions {
		if suggestion.Status != report.SuggestionCandidate {
			continue
		}

		byFile[suggestion.TargetFile] = append(byFile[suggestion.TargetFile], suggestion)
	}

	return byFile
}

func sortedTargets(byFile map[string][]report.Suggestion) []string {
	files := make([]string, 0, len(byFile))
	for file := range byFile {
		files = append(files, file)
	}

	slices.Sort(files)

	return files
}
