package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3b.3 (#49): the baseline file under the normative gate truth table (C8).
// "Staleness and baseline rewrite require a full unsampled population;
// sampled runs are non-authoritative and cannot satisfy --min-score or
// --fail-on-new without a separately named unsafe opt-in; a baseline entry
// absent from a scoped population is unobserved; any current finding (state
// or actionability class) not accepted in the baseline is new."
//
// Baseline entries never suppress safety gates, Invalid-class operator
// defects, or the incomplete-score marker: the gates run before the baseline
// is even read, Invalid mutants are not findings, and the metrics are
// computed without reference to acceptance.

// DefaultBaselineName is the project-local baseline file.
const DefaultBaselineName = ".tf-mut-baseline.json"

// baselineFormatVersion is the file format contract.
const baselineFormatVersion = "tf-mut-baseline-1"

// baselineFileMode keeps the acceptance list owner-writable and world-read
// only through the repository's own permissions.
const baselineFileMode = 0o600

// Baseline failure modes.
var (
	// ErrBaselineWrite reports a write requested off the full unsampled
	// population: a scoped run must never silently shrink the accepted list.
	ErrBaselineWrite = errors.New(
		"a baseline write requires a full unsampled run: neither --since nor --sample may shape it",
	)
	// ErrBaselineFile reports a baseline file that could not be read: a
	// corrupt acceptance list must never fake an empty one.
	ErrBaselineFile = errors.New("the baseline file could not be read")
)

// acceptanceFile is the persisted acceptance list.
type acceptanceFile struct {
	FormatVersion string            `json:"format_version"`
	Entries       []acceptanceEntry `json:"entries"`
}

// acceptanceEntry accepts one finding by stable ID and actionability class.
type acceptanceEntry struct {
	ID string `json:"id"`
	// Actionable is the accepted actionability class: an accepted
	// indeterminate whose diagnosis becomes actionable is new again.
	Actionable bool `json:"actionable"`
	// State, Site and Operator are context for the human reading the file;
	// acceptance is decided by ID and class alone.
	State    string `json:"state,omitempty"`
	Site     string `json:"site,omitempty"`
	Operator string `json:"operator,omitempty"`
}

// baselinePath resolves the baseline file location.
func baselinePath(settings Config, moduleDir string) string {
	if settings.BaselinePath != "" {
		if filepath.IsAbs(settings.BaselinePath) {
			return settings.BaselinePath
		}

		return filepath.Join(moduleDir, settings.BaselinePath)
	}

	return filepath.Join(moduleDir, DefaultBaselineName)
}

// applyBaselineGate reads the acceptance list, classifies every current
// finding as new or accepted, reports stale or unobserved entries per the
// truth table, writes the baseline where requested, and records the
// fail-on-new outcome.
func applyBaselineGate(settings Config, moduleDir string, result *report.Report) error {
	if !settings.FailOnNew && !settings.WriteBaseline {
		return nil
	}

	path := baselinePath(settings, moduleDir)

	accepted, err := readBaseline(path)
	if err != nil {
		return err
	}

	// The truth table's run shapes: a population that is scoped, sampled or
	// served even partly from the cache is not the Full row. Staleness and a
	// baseline write both require a full, unsampled, freshly executed
	// population.
	full := result.Population.Omitted == 0 && result.Sampling == nil &&
		result.Population.Cached == 0
	gate := judgeFindings(accepted, result, full)
	gate.Path = filepath.Base(path)

	if settings.WriteBaseline {
		if !full {
			return fmt.Errorf("%w: this run replayed %d cached verdict(s); use --no-cache",
				ErrBaselineWrite, result.Population.Cached)
		}

		if writeErr := writeBaseline(path, result); writeErr != nil {
			return writeErr
		}

		gate.Write = report.BaselineWritten
	}

	if result.Gates == nil {
		result.Gates = &report.Gates{} //nolint:exhaustruct // filled below.
	}

	result.Gates.Baseline = &gate
	result.Gates.FailOnNew = report.GateOutcome{
		Evaluated: settings.FailOnNew,
		Scope:     scopeLabel(full),
		Partial:   !full,
		Passed:    len(gate.New) == 0,
		Refused:   "",
	}

	return nil
}

// judgeFindings applies the acceptance list to the current findings.
func judgeFindings(accepted []acceptanceEntry, result *report.Report, full bool) report.BaselineGate {
	acceptedByID := map[string]acceptanceEntry{}
	for _, entry := range accepted {
		acceptedByID[entry.ID] = entry
	}

	selected := map[string]bool{}
	matched := map[string]bool{}

	gate := report.BaselineGate{
		Path: "", Accepted: len(accepted), Matched: 0,
		New: []string{}, Stale: nil, Unobserved: nil,
		StalenessReported: full, Write: "",
	}

	for index := range result.Mutants {
		mutant := &result.Mutants[index]
		selected[mutant.ID] = true

		if !isFinding(*mutant) {
			continue
		}

		entry, found := acceptedByID[mutant.ID]
		if found && entry.Actionable == findingActionable(*mutant) {
			matched[mutant.ID] = true
			gate.Matched++

			labelFinding(mutant, "accepted")

			continue
		}

		gate.New = append(gate.New, mutant.ID)
		labelFinding(mutant, "new")
	}

	slices.Sort(gate.New)

	for _, entry := range accepted {
		if matched[entry.ID] {
			continue
		}

		switch {
		case full:
			// The whole population ran and nothing matched: the entry is
			// stale, and only a full population may say so.
			gate.Stale = append(gate.Stale, entry.ID)
		case !selected[entry.ID]:
			// A scoped population says nothing about an unselected entry.
			gate.Unobserved = append(gate.Unobserved, entry.ID)
		default:
			// Selected but resolved: observed, not stale — the truth table
			// reserves staleness for the full population.
		}
	}

	slices.Sort(gate.Stale)
	slices.Sort(gate.Unobserved)

	return gate
}

// isFinding reports the finding classes the baseline governs: survivors with
// any diagnosis, and the structurally unassertable.
func isFinding(mutant report.Mutant) bool {
	return mutant.State == report.Survived || mutant.State == report.StructurallyUnassertable
}

// findingActionable is the finding's actionability class.
func findingActionable(mutant report.Mutant) bool {
	if mutant.State == report.StructurallyUnassertable {
		return true
	}

	if mutant.Verdict == nil {
		return true
	}

	return mutant.Verdict.Diagnosis.Actionable()
}

func labelFinding(mutant *report.Mutant, status string) {
	if mutant.Provenance == nil {
		mutant.Provenance = &report.Provenance{
			Selection: report.SelectionFull, Reason: "", Execution: report.ExecutionFresh,
			CacheKey: "", BaselineStatus: "",
		}
	}

	mutant.Provenance.BaselineStatus = status
}

// readBaseline loads the acceptance list. A missing file is an empty list —
// that is what adoption looks like — and a corrupt one is an error.
func readBaseline(path string) ([]acceptanceEntry, error) {
	content, err := os.ReadFile(path) //nolint:gosec // module-relative baseline path.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("%w: %s: %w", ErrBaselineFile, path, err)
	}

	decoded := acceptanceFile{} //nolint:exhaustruct // decoded from disk.
	if err := json.Unmarshal(content, &decoded); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBaselineFile, path, err)
	}

	if decoded.FormatVersion != baselineFormatVersion {
		return nil, fmt.Errorf("%w: %s declares format %q, this build reads %q",
			ErrBaselineFile, path, decoded.FormatVersion, baselineFormatVersion)
	}

	return decoded.Entries, nil
}

// writeBaseline persists the current findings as the accepted list.
func writeBaseline(path string, result *report.Report) error {
	entries := []acceptanceEntry{}

	for _, mutant := range result.Mutants {
		if !isFinding(mutant) {
			continue
		}

		entries = append(entries, acceptanceEntry{
			ID:         mutant.ID,
			Actionable: findingActionable(mutant),
			State:      string(mutant.State),
			Site:       mutant.Site,
			Operator:   mutant.Operator,
		})
	}

	slices.SortFunc(entries, func(left, right acceptanceEntry) int {
		if left.ID < right.ID {
			return -1
		}

		if left.ID > right.ID {
			return 1
		}

		return 0
	})

	encoded, err := json.MarshalIndent(acceptanceFile{
		FormatVersion: baselineFormatVersion,
		Entries:       entries,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding baseline: %w", err)
	}

	if err := os.WriteFile(path, append(encoded, '\n'), baselineFileMode); err != nil {
		return fmt.Errorf("writing baseline: %w", err)
	}

	return nil
}

// checkBaselineWrite refuses a write off the full unsampled population,
// before any work is done.
func checkBaselineWrite(settings Config) error {
	if !settings.WriteBaseline {
		return nil
	}

	if settings.Since != "" || settings.HasSample {
		return fmt.Errorf("%w", ErrBaselineWrite)
	}

	return nil
}
