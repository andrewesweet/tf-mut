package engine

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3b.1 (#45): `--since` selection, per the C5 disposition — "the union of the
// committed range, the working tree, and untracked Terraform/test/configuration
// files". Merge conflicts, shallow clones lacking the ref, and unknown refs
// are errors, never silent full or empty runs.

// The --since failure modes. Each aborts the run: a wrong ref must never fake
// a green gate.
var (
	// ErrSinceRef reports a ref git cannot resolve, in this clone.
	ErrSinceRef = errors.New("--since ref cannot be resolved")
	// ErrSinceRepository reports a module outside any git work tree.
	ErrSinceRepository = errors.New("--since requires a git repository")
	// ErrSinceConflict reports an in-progress merge conflict.
	ErrSinceConflict = errors.New("--since cannot select over an unresolved merge conflict")
	// ErrSampledGate reports a sampled run asked to satisfy a gate without the
	// separately named unsafe opt-in.
	ErrSampledGate = errors.New(
		"a sampled run is non-authoritative and cannot satisfy a gate without --allow-sampled-gate",
	)
)

// selection is what the count levers decided about the generated population.
type selection struct {
	// keep maps mutant index to whether it is selected.
	keep []bool
	// provenance carries the per-mutant selection provenance.
	provenance []report.Provenance
	// metadata is the report's selection record.
	metadata report.Selection
	// sampling is the report's sampling record, where sampling ran.
	sampling *report.Sampling
}

// selectPopulation applies --since and --sample to the described population.
func selectPopulation(
	ctx context.Context,
	configuration discovery.Configuration,
	settings Config,
	mutants []report.Mutant,
) (selection, error) {
	chosen := selection{
		keep:       make([]bool, len(mutants)),
		provenance: make([]report.Provenance, len(mutants)),
		metadata:   report.Selection{Mode: report.SelectionFull, Ref: "", ForcedFull: ""},
		sampling:   nil,
	}

	for index := range mutants {
		chosen.keep[index] = true
		chosen.provenance[index] = report.Provenance{
			Selection: report.SelectionFull,
			Reason:    "the whole population runs",
			Execution: report.ExecutionFresh,
			CacheKey:  "", BaselineStatus: "",
		}
	}

	if settings.Since != "" {
		if err := applySince(ctx, configuration, settings, mutants, &chosen); err != nil {
			return selection{}, err
		}
	}

	if settings.HasSample {
		applySample(settings, mutants, &chosen)
	}

	return chosen, nil
}

// applySince computes the changed-file union and scopes the population to it.
func applySince(
	ctx context.Context,
	configuration discovery.Configuration,
	settings Config,
	mutants []report.Mutant,
	chosen *selection,
) error {
	changes, err := changedPaths(ctx, configuration.ClosureRoot, settings.Since)
	if err != nil {
		return err
	}

	chosen.metadata = report.Selection{
		Mode: report.SelectionSince, Ref: settings.Since, ForcedFull: "",
	}

	if forced := fullPopulationTrigger(changes); forced != "" {
		// The full population runs, and the report says why.
		chosen.metadata.ForcedFull = forced

		for index := range mutants {
			chosen.provenance[index].Selection = report.SelectionSince
			chosen.provenance[index].Reason = "full population forced: " + forced
		}

		return nil
	}

	selected, modules := changedTerraform(configuration, changes)

	for index, mutant := range mutants {
		file := mutant.Range.File
		inFile := selected[file]
		inModule := modules[mutant.Module]

		chosen.keep[index] = inFile || inModule
		chosen.provenance[index].Selection = report.SelectionSince

		switch {
		case inFile:
			chosen.provenance[index].Reason = file + " changed since " + settings.Since
		case inModule:
			chosen.provenance[index].Reason = "a file deleted from the module referenced content " +
				"this mutant may depend on"
		default:
			chosen.provenance[index].Reason = "unchanged since " + settings.Since
		}
	}

	return nil
}

// change is one changed path with its git status letter.
type change struct {
	status string
	path   string
}

// changedPaths lists the closure-relative changed paths across the committed
// range, the index, the working tree, and the untracked files.
func changedPaths(ctx context.Context, closureRoot, ref string) ([]change, error) {
	if _, err := gitRun(ctx, closureRoot, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("%w: %s is not inside a git work tree", ErrSinceRepository, closureRoot)
	}

	if _, err := gitRun(ctx, closureRoot, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return nil, fmt.Errorf("%w: %q (unknown ref, or a shallow clone that lacks it)", ErrSinceRef, ref)
	}

	if conflicted, err := gitRun(ctx, closureRoot, "ls-files", "-u"); err != nil {
		return nil, fmt.Errorf("listing conflicts: %w", err)
	} else if strings.TrimSpace(conflicted) != "" {
		return nil, fmt.Errorf("%w", ErrSinceConflict)
	}

	changes := []change{}

	// The committed range needs a merge base; its absence is an error too.
	committed, err := gitRun(ctx, closureRoot,
		"diff", "--name-status", "--find-renames", "--relative", "-z", ref+"...HEAD")
	if err != nil {
		return nil, fmt.Errorf("%w: no merge base between %q and HEAD", ErrSinceRef, ref)
	}

	changes = append(changes, parseNameStatus(committed)...)

	for _, args := range [][]string{
		{"diff", "--name-status", "--find-renames", "--relative", "-z", "--cached"},
		{"diff", "--name-status", "--find-renames", "--relative", "-z"},
	} {
		output, diffErr := gitRun(ctx, closureRoot, args...)
		if diffErr != nil {
			return nil, fmt.Errorf("diffing the working tree: %w", diffErr)
		}

		changes = append(changes, parseNameStatus(output)...)
	}

	// Untracked files, ignored ones included: Terraform does not read git's
	// ignore rules, so an ignored *.auto.tfvars or .tf file still changes
	// execution (review of #45). Tool-owned and Terraform-owned directories
	// are excluded by name; everything is NUL-separated so no filename shape
	// can hide.
	untracked, err := gitRun(ctx, closureRoot,
		"ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("listing untracked files: %w", err)
	}

	ignored, err := gitRun(ctx, closureRoot,
		"ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("listing ignored files: %w", err)
	}

	for _, path := range splitNul(untracked + ignored) {
		if toolOwnedPath(path) {
			continue
		}

		changes = append(changes, change{status: "A", path: path})
	}

	return changes, nil
}

// splitNul splits NUL-separated git output.
func splitNul(output string) []string {
	paths := []string{}

	for path := range strings.SplitSeq(output, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}

	return paths
}

// toolOwnedPath reports the directories whose contents never select: the
// tool's own cache, Terraform's data directory, and version control itself.
func toolOwnedPath(path string) bool {
	slashed := "/" + filepath.ToSlash(path)

	for _, owned := range []string{"/.tf-mut-cache/", "/.terraform/", "/.git/"} {
		if strings.Contains(slashed, owned) {
			return true
		}
	}

	return false
}

// parseNameStatus parses `git diff --name-status -z` output: NUL-separated
// records of a status followed by one path, or two for a rename — which
// contributes both names, per the C5 disposition. NUL separation keeps every
// legal filename shape intact.
func parseNameStatus(output string) []change {
	changes := []change{}
	fields := splitNul(output)

	for index := 0; index < len(fields); index++ {
		status := fields[index][:1]

		if index+1 >= len(fields) {
			break
		}

		index++
		changes = append(changes, change{status: status, path: fields[index]})

		if status == "R" && index+1 < len(fields) {
			index++
			changes = append(changes, change{status: status, path: fields[index]})
		}
	}

	return changes
}

// fullPopulationTrigger returns the changed file class that forces the full
// population, or empty. "Changed configuration" is exactly #33's list: any
// non-.tf class change cannot be scoped.
func fullPopulationTrigger(changes []change) string {
	for _, changed := range changes {
		name := filepath.Base(changed.path)

		switch {
		case strings.HasSuffix(name, ".tftest.hcl") || strings.HasSuffix(name, ".tftest.json"):
			return changed.path + " (test file)"
		case strings.HasSuffix(name, ".tfvars") || strings.HasSuffix(name, ".tfvars.json"):
			return changed.path + " (variables file)"
		case name == ".tf-mut.hcl":
			return changed.path + " (tf-mut configuration)"
		case name == ".terraform.lock.hcl":
			return changed.path + " (dependency lock)"
		case strings.HasSuffix(name, ".tf.json"):
			// Discovery reads only .tf, so a JSON configuration change has no
			// mutant sites to scope to; the honest outcome is the full
			// population, never a silent empty selection (review of #45).
			return changed.path + " (JSON configuration, outside mutation scope)"
		default:
		}
	}

	return ""
}

// changedTerraform maps the .tf-class changes onto the module closure: the
// changed files, and the modules a deletion selects conservatively.
func changedTerraform(
	configuration discovery.Configuration,
	changes []change,
) (files, modules map[string]bool) {
	files = map[string]bool{}
	modules = map[string]bool{}

	for _, changed := range changes {
		if !strings.HasSuffix(changed.path, ".tf") {
			continue
		}

		moduleRel, inClosure := owningModule(configuration, changed.path)
		if !inClosure {
			continue
		}

		if changed.status == "D" {
			// The deleted content cannot be resolved, so every file that could
			// have referenced it — the whole module — is selected.
			modules[moduleRel] = true

			continue
		}

		files[changed.path] = true
	}

	return files, modules
}

// owningModule finds the closure module a closure-relative path belongs to.
func owningModule(configuration discovery.Configuration, path string) (string, bool) {
	dir := filepath.ToSlash(filepath.Dir(path))

	for _, module := range configuration.Modules {
		if module.Rel == dir {
			return module.Rel, true
		}
	}

	return "", false
}

// applySample keeps a deterministic fraction of the currently selected
// population: mutants ordered by a seeded hash of their identifier, the first
// N% kept. The report labels the run non-authoritative.
func applySample(settings Config, mutants []report.Mutant, chosen *selection) {
	selectedIndexes := []int{}

	for index := range mutants {
		if chosen.keep[index] {
			selectedIndexes = append(selectedIndexes, index)
		}
	}

	slices.SortFunc(selectedIndexes, func(left, right int) int {
		return cmp.Compare(sampleRank(settings.SampleSeed, mutants[left].ID),
			sampleRank(settings.SampleSeed, mutants[right].ID))
	})

	// Ceiling on the real percentage, so a non-empty population never
	// samples to nothing — 0.5% of 224 keeps 2, not 0.
	kept := min(int(math.Ceil(float64(len(selectedIndexes))*settings.SamplePercent/wholePercent)),
		len(selectedIndexes))

	for position, index := range selectedIndexes {
		if position < kept {
			chosen.provenance[index].Selection = report.SelectionSample
			chosen.provenance[index].Reason = fmt.Sprintf("in the %g%% sample (seed %d)",
				settings.SamplePercent, settings.SampleSeed)

			continue
		}

		chosen.keep[index] = false
		chosen.provenance[index].Selection = report.SelectionSample
		chosen.provenance[index].Reason = "outside the sample"
	}

	chosen.sampling = &report.Sampling{
		RatePercent:   settings.SamplePercent,
		Seed:          settings.SampleSeed,
		Authoritative: false,
		GateOptIn:     settings.AllowSampledGate && (settings.HasMinScore || settings.FailOnNew),
	}
}

// wholePercent is the denominator of a percentage sample.
const wholePercent = 100

// sampleRank orders mutants deterministically under a seed.
func sampleRank(seed int64, id string) uint64 {
	digest := sha256.Sum256(fmt.Appendf(nil, "%d|%s", seed, id))

	return binary.BigEndian.Uint64(digest[:8])
}

// gitRun executes one git command against the closure root with a stripped
// environment: anything that redirects git at another repository is removed,
// and the caller's personal git configuration is neutralised so that a global
// ignore pattern — `*.tfvars` is a popular one — cannot silently hide a
// changed file from selection (standing process rule 5). The repository's own
// .gitignore is project intent and stays in force.
func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	//nolint:gosec // fixed binary; tool-controlled arguments.
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)

	environment := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}

	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			environment = append(environment, entry)
		}
	}

	command.Env = environment

	output, err := command.Output()
	if err != nil {
		detail := ""

		exitError := &exec.ExitError{}
		if errors.As(err, &exitError) {
			detail = strings.TrimSpace(string(exitError.Stderr))
		}

		return "", fmt.Errorf("git %s: %w %s", strings.Join(args, " "), err, detail)
	}

	return string(output), nil
}

// apply reduces the described population to the selected one, keeps the
// generated list aligned index for index, and computes the population split.
// Omitted mutants leave the report entirely; their count is the record that
// they existed.
func (s selection) apply(
	described []report.Mutant,
	generated []mutation.Mutant,
) ([]report.Mutant, []mutation.Mutant, report.Population) {
	selected := make([]report.Mutant, 0, len(described))
	kept := make([]mutation.Mutant, 0, len(generated))
	omitted := 0

	for index, mutant := range described {
		if !s.keep[index] {
			omitted++

			continue
		}

		provenance := s.provenance[index]
		mutant.Provenance = &provenance
		selected = append(selected, mutant)
		kept = append(kept, generated[index])
	}

	return selected, kept, report.Population{
		Selected: len(selected),
		Omitted:  omitted,
		Cached:   0,
		Fresh:    len(selected),
	}
}

// ErrSampleRange reports a sample percentage outside (0, 100].
var ErrSampleRange = errors.New("--sample must be a percentage greater than 0 and at most 100")

// checkSampledGate refuses gates over a sampled population without the named
// unsafe opt-in (the gate truth table's sampled row), and rejects a
// percentage no sample can honour.
func checkSampledGate(settings Config) error {
	if settings.HasSample &&
		(settings.SamplePercent <= 0 || settings.SamplePercent > wholePercent) {
		return fmt.Errorf("%w: %g", ErrSampleRange, settings.SamplePercent)
	}

	if !settings.HasSample || settings.AllowSampledGate {
		return nil
	}

	if settings.HasMinScore || settings.FailOnNew {
		return fmt.Errorf("%w", ErrSampledGate)
	}

	return nil
}
