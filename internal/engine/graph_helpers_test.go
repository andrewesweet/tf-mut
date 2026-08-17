package engine_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

// The supplemental terraform graph comparison drives the real binary directly
// from the test suite. It deliberately does not go through internal/tfexec:
// the production runner must never learn a graph subcommand, and
// TestNoUserInvocationExecutesTerraformGraph holds it to that.

// graphEdge is one resource-level dependency terraform graph reports: from
// depends on to.
type graphEdge struct {
	from string
	to   string
}

// terraformGraphEdges initialises the module offline and returns the DOT edges
// between managed resources and data sources in the root module.
func terraformGraphEdges(t *testing.T, moduleDir string) []graphEdge {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	runTerraform(ctx, t, moduleDir, "init", "-backend=false", "-input=false")
	output := runTerraform(ctx, t, moduleDir, "graph")

	edgePattern := regexp.MustCompile(`"([^"]+)"\s*->\s*"([^"]+)"`)
	edges := []graphEdge{}

	for _, match := range edgePattern.FindAllStringSubmatch(output, -1) {
		from, fromOK := dotResource(match[1])
		to, toOK := dotResource(match[2])

		if !fromOK || !toOK || from == to {
			continue
		}

		edges = append(edges, graphEdge{from: from, to: to})
	}

	return edges
}

// dotResource extracts a root-module resource or data address from a DOT node
// label, rejecting the graph's bookkeeping nodes (provider, root, locals,
// outputs and variables are compared through the reference graph itself).
// Module-prefixed resources are compared separately by the module-wiring
// check.
func dotResource(label string) (string, bool) {
	label = dotLabel(label)

	if strings.HasPrefix(label, "provider[") || strings.Contains(label, "module.") {
		return "", false
	}

	parts := strings.Split(label, ".")

	if parts[0] == "data" && len(parts) >= 3 {
		return strings.Join(parts[:3], "."), true
	}

	if len(parts) >= 2 && !isReservedGraphRoot(parts[0]) {
		return strings.Join(parts[:2], "."), true
	}

	return "", false
}

func isReservedGraphRoot(name string) bool {
	switch name {
	case "var", "local", "output", "provider", "root", "meta", "data":
		return true
	default:
		return false
	}
}

// dotLabel normalises a DOT node label.
func dotLabel(label string) string {
	label = strings.TrimSpace(label)
	label = strings.TrimPrefix(label, "[root] ")

	// Strip terraform graph's annotations, e.g. "(expand)".
	if index := strings.Index(label, " "); index >= 0 {
		label = label[:index]
	}

	return label
}

// dotModuleResource extracts an addressable node from the plan-type DOT
// graph: resources, variables, locals and outputs, module-prefixed or not.
// Bookkeeping nodes — root, providers, and bare module close/expand markers —
// are rejected.
func dotModuleResource(label string) (string, bool) {
	label = dotLabel(label)

	if strings.HasPrefix(label, "provider[") || label == "root" || !strings.Contains(label, ".") {
		return "", false
	}

	// A bare module marker names no value the reference graph addresses.
	parts := strings.Split(label, ".")
	if parts[len(parts)-1] == "" || (parts[0] == "module" && len(parts) == 2) {
		return "", false
	}

	return label, true
}

// terraformGraphModuleEdges lists the DOT edges with at least one
// module-prefixed endpoint.
func terraformGraphModuleEdges(t *testing.T, moduleDir string) []graphEdge {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	runTerraform(ctx, t, moduleDir, "init", "-backend=false", "-input=false")

	// The plan-type graph carries the full evaluation wiring — module inputs
	// and outputs included — where the simplified default does not.
	output := runTerraform(ctx, t, moduleDir, "graph", "-type=plan")

	edgePattern := regexp.MustCompile(`"([^"]+)"\s*->\s*"([^"]+)"`)
	edges := []graphEdge{}

	for _, match := range edgePattern.FindAllStringSubmatch(output, -1) {
		from, fromOK := dotModuleResource(match[1])
		to, toOK := dotModuleResource(match[2])

		if !fromOK || !toOK || from == to {
			continue
		}

		if !strings.Contains(from, "module.") && !strings.Contains(to, "module.") {
			continue
		}

		edges = append(edges, graphEdge{from: from, to: to})
	}

	return edges
}

// resourceLevelReach reports whether the reference graph agrees that `from`
// depends on `to`: the forward cone of `to` must contain `from`.
func resourceLevelReach(graph *discovery.Graph, from, to string) bool {
	cone, ok := graph.SiteCone(".", to)
	if !ok {
		return false
	}

	return cone.ContainsResource(".", from)
}

// seedMissingEdge removes the discriminate fixture's resource-to-resource
// reference, so the intact module's terraform graph edges no longer hold in
// the doctored one.
func seedMissingEdge(t *testing.T, moduleDir string) {
	t.Helper()

	path := filepath.Join(moduleDir, "main.tf")

	source, err := os.ReadFile(path) //nolint:gosec // fixture copy under t.TempDir.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	doctored := strings.ReplaceAll(string(source),
		"input = terraform_data.app.input.name", `input = "constant"`)
	if doctored == string(source) {
		t.Fatal("the seeded edit found nothing to remove; the fixture moved under this test")
	}

	if err := os.WriteFile(path, []byte(doctored), 0o600); err != nil { //nolint:gosec // fixture copy under t.TempDir.
		t.Fatalf("writing %s: %v", path, err)
	}
}

// runTerraform executes one terraform command in a module directory with the
// suite's hermetic environment.
func runTerraform(ctx context.Context, t *testing.T, moduleDir string, args ...string) string {
	t.Helper()

	command := exec.CommandContext(ctx, "terraform", args...) //nolint:gosec // fixed binary name, test-owned arguments.
	command.Dir = moduleDir
	command.Env = append(os.Environ(), terraformEnv(t)...)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("terraform %s in %s: %v\n%s", strings.Join(args, " "), moduleDir, err, output)
	}

	return string(output)
}
