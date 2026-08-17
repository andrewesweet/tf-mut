package engine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3e.1 (#53): the generated function catalogue, by fault model rather than
// Cartesian product (review C7). Generation canonicalises core:: aliases and
// emits only explicitly justified semantic families; the catalogue ships
// behind an opt-in flag; curated identifiers win deduplication; and admission
// to `standard` is a separate, evidence-carrying change.

// generatedOperator is the generated catalogue's one operator identifier.
const generatedOperator = "FN-FAMILY-SWAP"

// familiesPreview previews the families fixture, with or without the opt-in.
func familiesPreview(t *testing.T, generated bool) report.Report {
	t.Helper()

	config := baseConfig(t, copyFixture(t, "families"))
	config.Preview = true
	config.GeneratedFunctions = generated

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	return result
}

// familyMutants filters the generated-operator mutants.
func familyMutants(result report.Report) []report.Mutant {
	mutants := []report.Mutant{}

	for _, mutant := range result.Mutants {
		if mutant.Operator == generatedOperator {
			mutants = append(mutants, mutant)
		}
	}

	return mutants
}

// TestTheGeneratedCatalogueIsOptIn: without the flag, the population carries
// no generated operator at all — byte-identical to pre-M3e; with it, the
// families fire.
func TestTheGeneratedCatalogueIsOptIn(t *testing.T) {
	t.Parallel()

	if defaulted := familyMutants(familiesPreview(t, false)); len(defaulted) != 0 {
		t.Fatalf("the default population carries %d generated mutants; the catalogue is opt-in",
			len(defaulted))
	}

	if opted := familyMutants(familiesPreview(t, true)); len(opted) == 0 {
		t.Fatal("the opt-in generated nothing; the catalogue is dead")
	}
}

// TestCrossFamilyPairsAreImpossible is the C7 fixture: file() shares the
// unary string-to-string signature with upper(), and no generated mutant may
// connect them — or any other pair of families.
func TestCrossFamilyPairsAreImpossible(t *testing.T) {
	t.Parallel()

	//nolint:goconst // the family table restates the fixture's function names.
	families := map[string][]string{
		"order-statistics": {"min", "max"},
		"rounding":         {"floor", "ceil"},
		"case":             {"upper", "lower", "title"},
		"string-search":    {"startswith", "endswith", "strcontains"},
		"set-algebra":      {"concat", "setunion", "setintersection", "setsubtract"},
	}

	familyOf := map[string]string{}
	for name, members := range families {
		for _, member := range members {
			familyOf[member] = name
		}
	}

	edited := regexp.MustCompile(`^-.*?([a-z_:]+)\(`)
	replaced := regexp.MustCompile(`^\+.*?([a-z_:]+)\(`)

	for _, mutant := range familyMutants(familiesPreview(t, true)) {
		if strings.Contains(mutant.Site, "bait") {
			t.Fatalf("a generated mutant fired on the file() bait: %s", mutant.Diff)
		}

		original, replacement := "", ""

		for line := range strings.Lines(mutant.Diff) {
			if match := edited.FindStringSubmatch(line); match != nil && original == "" {
				original = strings.TrimPrefix(match[1], "core::")
			}

			if match := replaced.FindStringSubmatch(line); match != nil && replacement == "" {
				replacement = strings.TrimPrefix(match[1], "core::")
			}
		}

		if original == "" || replacement == "" {
			continue
		}

		if familyOf[original] == "" || familyOf[original] != familyOf[replacement] {
			t.Fatalf("generated substitution %s -> %s crosses families:\n%s",
				original, replacement, mutant.Diff)
		}
	}
}

// TestAliasesAreCanonicalised: core::max is the same function as max — the
// curated swap fires on it, and every replacement preserves the caller's
// core:: spelling.
func TestAliasesAreCanonicalised(t *testing.T) {
	t.Parallel()

	result := familiesPreview(t, true)

	curatedOnAlias := false
	prefixPreserved := false

	for _, mutant := range result.Mutants {
		if !strings.Contains(mutant.Site, "aliased") {
			continue
		}

		if mutant.Operator == "FN-SWAP" {
			curatedOnAlias = true
		}

		if strings.Contains(mutant.Diff, "core::min") {
			prefixPreserved = true
		}
	}

	if !curatedOnAlias {
		t.Fatal("the curated FN-SWAP did not fire on the core:: alias")
	}

	if !prefixPreserved {
		t.Fatal("a replacement dropped the caller's core:: spelling")
	}
}

// TestCuratedIdentifiersWinDeduplication: floor -> ceil is both a curated
// pair and a family pair with identical mutated content; the population must
// carry it once, as FN-SWAP.
func TestCuratedIdentifiersWinDeduplication(t *testing.T) {
	t.Parallel()

	result := familiesPreview(t, true)

	curated := 0
	generatedDuplicate := 0

	for _, mutant := range result.Mutants {
		if !strings.Contains(mutant.Site, "rounded") {
			continue
		}

		if mutant.Operator == "FN-SWAP" && strings.Contains(mutant.Diff, "ceil") {
			curated++
		}

		if mutant.Operator == "FN-FAMILY-SWAP" && strings.Contains(mutant.Diff, "ceil") {
			generatedDuplicate++
		}
	}

	if curated != 1 || generatedDuplicate != 0 {
		t.Fatalf("floor -> ceil appears as %d curated and %d generated mutants; "+
			"the curated identifier must win the deduplication", curated, generatedDuplicate)
	}
}
