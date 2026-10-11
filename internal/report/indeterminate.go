package report

import (
	"slices"
	"strings"
)

// indeterminateGroup is the survivors that share one unknown-at-plan path
// set: the same unknowns make them all indeterminate, so they read as one
// finding with its membership, not as one finding per mutant.
type indeterminateGroup struct {
	paths   []string
	members []Mutant
}

// partitionIndeterminate splits the survivors carrying the
// indeterminate-unknown-values diagnosis away from the other findings,
// grouping the indeterminate ones by their shared unknown-path set.
func partitionIndeterminate(findings []Mutant) (groups []indeterminateGroup, rest []Mutant) {
	index := map[string]int{}

	for _, finding := range findings {
		if finding.State != Survived || finding.Verdict == nil ||
			finding.Verdict.Diagnosis != IndeterminateUnknownValues {
			rest = append(rest, finding)

			continue
		}

		paths := slices.Clone(finding.Verdict.Evidence.UnknownPaths)
		slices.Sort(paths)

		key := strings.Join(paths, "\x00")

		at, found := index[key]
		if !found {
			at = len(groups)
			index[key] = at
			groups = append(groups, indeterminateGroup{paths: paths})
		}

		groups[at].members = append(groups[at].members, finding)
	}

	return groups, rest
}
