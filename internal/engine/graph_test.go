package engine_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

// The attribute-level reference graph (M3a.1, issue #44) is proven here in the
// two ways the spec permits: the fail-closed adapters are exercised over every
// operator generation site in the matrix fixtures, and the graph is compared
// with `terraform graph` at resource level over the fixture corpus — a
// supplemental, test-suite-only check, never a user-invocation dependency and
// never evidence of attribute-level soundness (review M1, C3).

// discover parses a fixture the way the engine does, without executing it.
func discover(t *testing.T, moduleDir string) discovery.Configuration {
	t.Helper()

	configuration, err := discovery.Discover(moduleDir, "tests")
	if err != nil {
		t.Fatalf("discovering %s: %v", moduleDir, err)
	}

	return configuration
}

// TestEveryGenerationSiteMapsIntoTheGraph exercises the site adapter over every
// operator generation site in the matrix fixtures. A site the adapter cannot
// map falls back to the whole-payload unknown rule for that mutant (C3), and a
// fallback here means an operator writes addresses the graph does not — which
// is exactly the drift this sweep exists to catch, so the expected fallback set
// is empty.
func TestEveryGenerationSiteMapsIntoTheGraph(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"operators", "dynamic"} {
		module := copyFixture(t, fixture)
		configuration := discover(t, module)
		graph := configuration.BuildGraph()

		for _, mutant := range preview(t, module, nil).Mutants {
			if _, ok := graph.SiteCone(mutant.Module, mutant.Site); !ok {
				t.Errorf("fixture %s: operator %s site %s (module %q) does not map into the graph; "+
					"the mutant falls back to the whole-payload unknown rule, which loses the "+
					"path-scoped oracle for it", fixture, mutant.Operator, mutant.Site, mutant.Module)
			}
		}
	}
}

// TestAnUnmappableSiteFallsBackClosed pins the site adapter's failure
// direction: a site the graph has never heard of reports unmapped, which the
// engine treats as the whole-payload unknown rule — conservative, never a
// silent empty cone.
func TestAnUnmappableSiteFallsBackClosed(t *testing.T) {
	t.Parallel()

	configuration := discover(t, copyFixture(t, "closure"))
	graph := configuration.BuildGraph()

	if _, ok := graph.SiteCone(".", "aws_instance.never_declared.some_attr"); ok {
		t.Fatal("a site outside the configuration mapped into the graph; the fallback would never fire")
	}

	if _, ok := graph.SiteCone("no-such-module", "local.x"); ok {
		t.Fatal("a site in an unknown module mapped into the graph")
	}
}

// TestAnUnmappablePayloadUnknownIsInCone pins the payload adapter's failure
// direction: an unknown whose path the graph cannot place is treated as
// in-cone, so it blocks the equality claim rather than licensing one (C3).
func TestAnUnmappablePayloadUnknownIsInCone(t *testing.T) {
	t.Parallel()

	configuration := discover(t, copyFixture(t, "closure"))
	graph := configuration.BuildGraph()

	cone, ok := graph.SiteCone(".", "local.tier")
	if !ok {
		t.Fatal("the closure fixture's local.tier did not map into the graph")
	}

	for _, address := range []string{
		"utterly.unknown.address",
		"module.never_declared.terraform_data.x.output",
		"data.external.not_here.result",
	} {
		if !cone.ContainsPayloadAddress(address) {
			t.Errorf("unmappable payload address %q was reported out-of-cone; "+
				"the adapter must fail closed to in-cone", address)
		}
	}
}

// TestTheForwardConeUnionsSameResourceAttributes pins the cone definition:
// closure from the mutated node plus every attribute of any resource the cone
// touches, so a computed attribute of a cone-touched resource counts as
// in-cone even though no expression references it.
func TestTheForwardConeUnionsSameResourceAttributes(t *testing.T) {
	t.Parallel()

	configuration := discover(t, copyFixture(t, "closure"))
	graph := configuration.BuildGraph()

	cone, ok := graph.SiteCone(".", "terraform_data.graded.input")
	if !ok {
		t.Fatal("terraform_data.graded.input did not map into the graph")
	}

	// `output` is computed: no expression in the fixture assigns it, so only
	// the same-resource union can place it in the cone.
	if !cone.ContainsPayloadAddress("terraform_data.graded.output") {
		t.Fatal("a computed attribute of the mutated resource itself is outside the cone")
	}
}

// graphCorpus is the fixture corpus for the supplemental `terraform graph`
// comparison. Its diversity is the point (review M1): locals chains, module
// wiring, splats, conditional references, count indexing.
func graphCorpus() []string {
	return []string{"closure", "discriminate", "foreach", "count-indexed", "count-tolerant"}
}

// TestGraphAgreesWithTerraformGraphOverTheCorpus is the supplemental check:
// resource-level agreement between the reference graph and `terraform graph`
// over the fixture corpus. It runs only in this suite.
func TestGraphAgreesWithTerraformGraphOverTheCorpus(t *testing.T) {
	t.Parallel()

	for _, fixture := range graphCorpus() {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, fixture)
			configuration := discover(t, module)
			graph := configuration.BuildGraph()

			for _, edge := range terraformGraphEdges(t, module) {
				if !resourceLevelReach(graph, edge.from, edge.to) {
					t.Errorf("terraform graph sees %s -> %s and the reference graph does not",
						edge.from, edge.to)
				}
			}
		})
	}
}

// TestASeededMissingEdgeFailsTheSupplementalCheck proves the comparison can
// fail: with the fixture's reference removed from the source, the terraform
// graph edges recorded from the intact module no longer hold, and the
// comparator reports the disagreement.
func TestASeededMissingEdgeFailsTheSupplementalCheck(t *testing.T) {
	t.Parallel()

	intact := copyFixture(t, "discriminate")
	edges := terraformGraphEdges(t, intact)

	if len(edges) == 0 {
		t.Fatal("the discriminate fixture produced no terraform graph edges to compare against")
	}

	doctored := copyFixture(t, "discriminate")
	seedMissingEdge(t, doctored)

	graph := discover(t, doctored).BuildGraph()

	disagreements := 0

	for _, edge := range edges {
		if !resourceLevelReach(graph, edge.from, edge.to) {
			disagreements++
		}
	}

	if disagreements == 0 {
		t.Fatal("removing the reference changed nothing; the supplemental check cannot fail")
	}
}

// TestNoUserInvocationExecutesTerraformGraph asserts the M1 disposition
// structurally: no production source invokes `terraform graph`. The
// supplemental check drives the binary from this test file only.
func TestNoUserInvocationExecutesTerraformGraph(t *testing.T) {
	t.Parallel()

	invocation := regexp.MustCompile(`"graph"`)

	root := filepath.Join("..", "..")
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			source, readErr := os.ReadFile(path) //nolint:gosec // repository-relative audit walk.
			if readErr != nil {
				return readErr
			}

			if invocation.Match(source) {
				t.Errorf("%s passes \"graph\" to something; terraform graph is test-suite-only", path)
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
}
