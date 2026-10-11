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
// grouping the indeterminate ones by their shared unknown-path set. The
// largest group sorts first: one resource's unknown computed metadata can
// leave a large share of a plan-only population indeterminate, and that
// group is the headline a reader needs first.
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

	slices.SortFunc(groups, func(left, right indeterminateGroup) int {
		if order := len(right.members) - len(left.members); order != 0 {
			return order
		}

		return strings.Compare(strings.Join(left.paths, "\x00"), strings.Join(right.paths, "\x00"))
	})

	return groups, rest
}
