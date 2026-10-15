package testsupport_test

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// Each executable has its own path, so ETXTBSY can only come from a writable
// descriptor inherited by a concurrent fork, never from rewriting a script
// that is already running.
func TestExecutableWritesDoNotRaceConcurrentForks(t *testing.T) {
	t.Parallel()

	const (
		workers    = 16
		iterations = 32
		want       = "wrapper-ready"
	)

	for worker := range workers {
		t.Run(strconv.Itoa(worker), func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			for iteration := range iterations {
				path := filepath.Join(directory, strconv.Itoa(iteration))
				testsupport.WriteExecutable(t, path, "#!/bin/sh\nprintf '%s' \"$1\"\n")

				//nolint:gosec // test-owned executable and argument.
				output, err := exec.CommandContext(t.Context(), path, want).Output()
				if err != nil {
					t.Fatalf("executing freshly written script: %v", err)
				}
				if string(output) != want {
					t.Fatalf("output = %q, want %q", output, want)
				}
			}
		})
	}
}
