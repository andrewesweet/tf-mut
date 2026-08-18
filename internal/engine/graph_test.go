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
	return []string{"closure", discriminateFixture, "foreach", "count-indexed", "count-tolerant"}
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

	intact := copyFixture(t, discriminateFixture)
	edges := terraformGraphEdges(t, intact)

	if len(edges) == 0 {
		t.Fatal("the discriminate fixture produced no terraform graph edges to compare against")
	}

	doctored := copyFixture(t, discriminateFixture)
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
	for _, dir := range []string{internalTree, commandTree} {
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

// TestProviderConfigurationMakesTheConeUnbounded is the #44 review's
// reproduction: var.region is consumed only by provider configuration, whose
// influence the graph does not model edge by edge. The variable's cone must
// be unbounded — observable, with every payload address in-cone — so neither
// the static shortcut nor the path-scoped unknown rule can turn the missing
// provider edges into a proof.
func TestProviderConfigurationMakesTheConeUnbounded(t *testing.T) {
	t.Parallel()

	configuration := discover(t, copyFixture(t, "provider-config"))
	graph := configuration.BuildGraph()

	cone, ok := graph.SiteCone(".", "var.region.default")
	if !ok {
		t.Fatal("var.region.default did not map into the graph")
	}

	if !cone.ContainsObservable() {
		t.Fatal("a cone reaching provider configuration reported nothing observable; " +
			"the static shortcut would claim a false Unobservable")
	}

	if !cone.ContainsPayloadAddress("aws_sqs_queue.work.arn") {
		t.Fatal("a cone reaching provider configuration excluded a payload unknown; " +
			"the path-scoped rule would claim a false equality")
	}
}

// TestModuleWiringAgreesWithTerraformGraph extends the supplemental check to
// the module-wiring edges the resource-level comparison used to reject: a
// DOT edge into or out of a child-module resource must be reachable in the
// reference graph through the call wiring.
func TestModuleWiringAgreesWithTerraformGraph(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, discriminateFixture)
	configuration := discover(t, module)
	graph := configuration.BuildGraph()

	edges := terraformGraphModuleEdges(t, module)
	if len(edges) == 0 {
		t.Skip("the fixture's terraform graph reports no module-wiring edges to compare")
	}

	for _, edge := range edges {
		cone, ok := graph.ConeOfPayloadAddress(edge.to)
		if !ok {
			t.Errorf("terraform graph names %s, which the reference graph cannot resolve", edge.to)

			continue
		}

		if !cone.ContainsPayloadAddress(edge.from) {
			t.Errorf("terraform graph sees %s -> %s across the module boundary and the "+
				"reference graph does not", edge.from, edge.to)
		}
	}
}

// TestARemoteModuleCallMakesTheConeUnbounded is the #44 re-review's first
// reproduction: a remote call's wiring is unmodellable, so the call and its
// input nodes make any cone unbounded — an EXT-MODULE-INPUT-DELETE site maps,
// and precisely because it maps, it must never license a graph-derived proof.
func TestARemoteModuleCallMakesTheConeUnbounded(t *testing.T) {
	t.Parallel()

	configuration := discover(t, copyFixture(t, "module-wiring"))
	graph := configuration.BuildGraph()

	for _, site := range []string{"module.remote.prefix", "var.remote_prefix.default"} {
		cone, ok := graph.SiteCone(".", site)
		if !ok {
			t.Fatalf("%s did not map into the graph", site)
		}

		if !cone.ContainsObservable() {
			t.Fatalf("the cone of %s reports nothing observable; a remote call's missing "+
				"wiring would license a false static Unobservable", site)
		}

		if !cone.ContainsPayloadAddress("module.remote.anything.at.all") {
			t.Fatalf("the cone of %s excluded a remote payload address; the unknown rule "+
				"would claim a false equality", site)
		}
	}
}

// TestAWholeObjectModuleReadDrawsAnEdge is the #44 re-review's second
// reproduction: `output "whole_child" { value = module.child }` reads every
// child output, and the reference must wire rather than being silently
// dropped — a mutation inside the child reaches the root output.
func TestAWholeObjectModuleReadDrawsAnEdge(t *testing.T) {
	t.Parallel()

	configuration := discover(t, copyFixture(t, "module-wiring"))
	graph := configuration.BuildGraph()

	cone, ok := graph.SiteCone("child", "local.shaped")
	if !ok {
		t.Fatal("the child's local.shaped did not map into the graph")
	}

	if !cone.ContainsPayloadAddress("output.whole_child") {
		t.Fatal("a mutation inside the child does not reach the whole-object reader; " +
			"the reference was silently dropped")
	}
}
