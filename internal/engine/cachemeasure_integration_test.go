//go:build integration

package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The cache over-invalidation measurement (M4d.2, issue #64), under the M5
// disposition's pinned protocol. Everything the protocol pins is in this file:
// the fixture (`testdata/cache-measure`, digests below), the edit sequence,
// the populations (full, standard tier, no exclusions), the environment (the
// suite's own hermetic Terraform environment), and the simulated-key
// algorithm.
//
// The simulated key under measurement is the obvious finer candidate: a
// per-mutant key over the mutant's identifier and the content of the mutant's
// own file, so an edit elsewhere in the module reuses the verdict. The
// measurement's job is not to tune it but to catch it lying: for every edit,
// every verdict the simulated key would reuse is asserted equal to the fresh
// verdict, the false-reuse count is published, and any non-zero count rejects
// the key regardless of its hit rate. No finer key is built under any result.
//
// The result of running this measurement is published in
// docs/research/11-m4-exit-gate.md.

// The pinned edit sequence. Each edit is applied to a fresh copy of the
// fixture at its previous state, in order.
var cacheMeasurementEdits = []struct {
	name string
	file string
	edit func(content string) string
}{
	{
		// E1: a comment-only edit. No verdict can change; the coarse key
		// invalidates everything anyway, which is the over-invalidation the
		// measurement quantifies.
		name: "E1-comment-only",
		file: "a.tf",
		edit: func(content string) string {
			return content + "\n# E1: a comment the configuration does not evaluate\n"
		},
	},
	{
		// E2: a value edit in a.tf, to a value no assertion pins so the
		// baseline stays green. Mutant identifiers are content-derived, so
		// a.tf's population is new; b.tf's mutants are unchanged and the
		// simulated key reuses them — correctly, and the assertion proves it.
		name: "E2-value-edit",
		file: "a.tf",
		edit: func(content string) string {
			return strings.Replace(content, `"observed-or-not"`, `"observed-differently"`, 1)
		},
	},
	{
		// E3: the seeded verdict-changing dependency. Removing b.tf's reader
		// of local.orphan flips the a.tf orphan mutants from Survived to
		// statically Unobservable without touching a.tf, which is exactly the
		// reuse the per-file key would claim and must not be allowed to.
		name: "E3-cross-file-dependency",
		file: "b.tf",
		edit: func(string) string {
			return "# E3: the reader is gone, so nothing observes local.orphan.\n" +
				"output \"reader\" {\n  value = \"static\"\n}\n"
		},
	},
}

func TestCacheOverInvalidationMeasurement(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "cache-measure")

	t.Logf("fixture digests: %s", fixtureDigests(t, module))

	previous := measureRun(t, module)

	for _, step := range cacheMeasurementEdits {
		path := filepath.Join(module, step.file)
		writeFile(t, path, step.edit(readFile(t, path)))

		fileDigestsBefore := perFileDigests(previous)
		fresh := measureRun(t, module)

		coarse := len(fresh.verdicts)
		reused, falseReuse := simulateReuse(t, previous, fresh, fileDigestsBefore)
		simulated := coarse - reused

		t.Logf("%s: population=%d coarse-invalidated=%d simulated-invalidated=%d "+
			"simulated-reused=%d false-reuse=%d",
			step.name, coarse, coarse, simulated, reused, falseReuse)

		if step.name == "E3-cross-file-dependency" && falseReuse == 0 {
			t.Fatal("the seeded verdict-changing dependency was not caught: " +
				"the measurement cannot reject a lying key it cannot see lie")
		}

		if step.name != "E3-cross-file-dependency" && falseReuse != 0 {
			t.Fatalf("%s: %d false reuses on an edit that changes no cross-file verdict",
				step.name, falseReuse)
		}

		previous = fresh
	}
}

// measured is one fresh run's verdicts with the per-file content the simulated
// key would hash.
type measured struct {
	// verdicts maps mutant ID to "state diagnosis file".
	verdicts map[string]measuredVerdict
	// files maps module-relative file name to its content digest.
	files map[string]string
}

type measuredVerdict struct {
	state     report.State
	diagnosis report.Diagnosis
	file      string
}

func measureRun(t *testing.T, module string) measured {
	t.Helper()

	config := baseConfig(t, module)
	config.NoCache = true // the measurement is about keys, not about the shipped cache's contents.

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("measurement run: %v", err)
	}

	runs := measured{verdicts: map[string]measuredVerdict{}, files: map[string]string{}}

	for _, mutant := range result.Mutants {
		diagnosis := report.Diagnosis("")
		if mutant.Verdict != nil {
			diagnosis = mutant.Verdict.Diagnosis
		}

		runs.verdicts[mutant.ID] = measuredVerdict{
			state: mutant.State, diagnosis: diagnosis, file: mutant.Range.File,
		}
	}

	entries, err := os.ReadDir(module)
	if err != nil {
		t.Fatalf("listing %s: %v", module, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tf") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(module, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}

		sum := sha256.Sum256(content)
		runs.files[entry.Name()] = hex.EncodeToString(sum[:])
	}

	return runs
}

func perFileDigests(previous measured) map[string]string {
	digests := map[string]string{}
	for name, digest := range previous.files {
		digests[name] = digest
	}

	return digests
}

// simulateReuse applies the simulated per-file key: a fresh mutant's verdict
// would be reused when a mutant with the same identifier existed before and
// its own file's content digest is unchanged. Every claimed reuse is compared
// against the fresh verdict; a mismatch is a false reuse.
func simulateReuse(
	t *testing.T,
	previous, fresh measured,
	before map[string]string,
) (reused, falseReuse int) {
	t.Helper()

	for id, now := range fresh.verdicts {
		then, existed := previous.verdicts[id]
		if !existed {
			continue
		}

		fileName := filepath.Base(now.file)
		if before[fileName] == "" || before[fileName] != fresh.files[fileName] {
			continue
		}

		reused++

		if then.state != now.state || then.diagnosis != now.diagnosis {
			falseReuse++

			t.Logf("false reuse: mutant %s in %s was %s/%s, is %s/%s",
				id, fileName, then.state, then.diagnosis, now.state, now.diagnosis)
		}
	}

	return reused, falseReuse
}

func fixtureDigests(t *testing.T, module string) string {
	t.Helper()

	parts := []string{}

	for path, digest := range treeDigest(t, module) {
		parts = append(parts, path+"="+digest[:12])
	}

	return strings.Join(parts, " ")
}
