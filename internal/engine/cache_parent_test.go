package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

func TestWarmParentChangesInvalidateCachedVerdicts(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"plan", "apply"} {
		for _, parent := range []string{"direct", "symlink"} {
			for _, publishedPath := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/published-path=%t", command, parent, publishedPath), func(t *testing.T) {
					t.Parallel()

					module := parentSensitiveCacheFixture(t, command, publishedPath)
					config := baseConfig(t, module)
					config.IncludeOperators = []string{string(mutation.StrCase)}
					special := filepath.Join(t.TempDir(), "special")
					ordinary := filepath.Join(t.TempDir(), "ordinary")
					for _, directory := range []string{special, ordinary} {
						if err := os.Mkdir(directory, 0o700); err != nil {
							t.Fatal(err)
						}
					}

					linked := filepath.Join(t.TempDir(), "work")
					if parent == "symlink" {
						if err := os.Symlink(special, linked); err != nil {
							t.Fatal(err)
						}
					}

					for _, leg := range []struct {
						directory string
						state     report.State
						cached    int
					}{
						{special, report.Survived, 0},
						{ordinary, report.Killed, 0},
						{special, report.Survived, 1},
					} {
						config.WorkDir = leg.directory
						if parent == "symlink" {
							if err := os.Remove(linked); err != nil {
								t.Fatal(err)
							}
							if err := os.Symlink(leg.directory, linked); err != nil {
								t.Fatal(err)
							}
							config.WorkDir = linked
						}

						result := cachedRun(t, config)
						if got := stateOf(t, result, "output.asserted.value"); got != leg.state {
							t.Fatalf("parent %s: got %s, want %s", leg.directory, got, leg.state)
						}
						if result.Population.Cached != leg.cached || result.Population.Fresh != 1-leg.cached {
							t.Fatalf("parent %s: population %+v, want cached=%d fresh=%d",
								leg.directory, result.Population, leg.cached, 1-leg.cached)
						}
					}
				})
			}
		}
	}
}

func parentSensitiveCacheFixture(t *testing.T, command string, publishedPath bool) string {
	t.Helper()

	module := copyFixture(t, "all-killed")
	source := "output \"asserted\" { value = \"kept\" }\n"
	path := "abspath(path.root)"
	if publishedPath {
		source += "output \"path\" { value = abspath(path.root) }\n"
		path = "output.path"
	}

	testsupport.WriteFile(t, filepath.Join(module, mainFile), source)
	testsupport.WriteFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"), fmt.Sprintf(`run "defaults" {
  command = %s
  assert {
    condition     = output.asserted == "kept" || strcontains(%s, "/special/")
    error_message = "keep the value outside special"
  }
}
`, command, path))

	return module
}
