package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// The digest-mismatch row of the apply protocol, at the preflight itself.
//
// Within one invocation verification always precedes apply, so the only way
// bytes change in between is a concurrent editor — a race no engine-seam test
// can stage deterministically. The preflight is therefore exercised directly
// over a suggestion whose recorded digest no longer matches the file, which is
// exactly the state the race produces.
func TestAStaleVerifiedDigestAbortsThePreflightNamingBothDigests(t *testing.T) {
	t.Parallel()

	module := t.TempDir()
	testsDir := filepath.Join(module, "tests")

	if err := os.MkdirAll(testsDir, 0o750); err != nil {
		t.Fatalf("creating the test directory: %v", err)
	}

	target := filepath.Join(testsDir, "unit.tftest.hcl")
	content := "run \"applied\" {\n  command = apply\n}\n"

	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the target: %v", err)
	}

	stale := report.Suggestion{
		ID: "aaaabbbbcccc", MutantID: "0123456789ab",
		TargetFile: "tests/unit.tftest.hcl", TargetRun: "applied",
		Status: report.SuggestionVerified, Expression: "output.x == 1",
		Patch: "", VerifiedDigest: strings.Repeat("0", 64),
		Verification: nil, StatusReason: "",
	}

	_, err := preflight(applyContext{
		moduleDir: module, closureRoot: module, testDirs: []string{testsDir, module},
	}, []report.Suggestion{stale})
	if err == nil {
		t.Fatal("a stale digest passed the preflight")
	}

	for _, expected := range []string{"changed since", stale.ID, "000000000000"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("the refusal does not carry %q: %v", expected, err)
		}
	}

	//nolint:gosec // a test-owned temporary path.
	if readBack, readErr := os.ReadFile(target); readErr != nil || string(readBack) != content {
		t.Fatal("the preflight touched the file it refused")
	}
}
