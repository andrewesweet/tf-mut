package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

func TestCancellationAfterAFailedRunWritesNoBaseline(t *testing.T) {
	t.Parallel()

	request, marker := interruptedRunRequest(t)
	request.WriteBaseline = true

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	observed := make(chan bool, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				observed <- false

				return
			case <-ticker.C:
				if _, err := os.Stat(marker); err == nil {
					observed <- true
					cancel()

					return
				}
			}
		}
	}()

	_, err := engine.Run(ctx, request)
	cancel()

	if !<-observed {
		t.Fatalf("the mutant never reached the run after its failed assertion: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want cancellation", err)
	}

	baseline := filepath.Join(request.ModuleDir, engine.DefaultBaselineName)
	if _, statErr := os.Stat(baseline); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the canceled run wrote a baseline: %v", statErr)
	}
}

func TestADeadlineAfterAFailedRunKeepsItsKilledVerdict(t *testing.T) {
	t.Parallel()

	request, _ := interruptedRunRequest(t)
	request.TimeoutFactor = 0.0001
	request.TimeoutFloor = 500 * time.Millisecond

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Mutants) != 1 {
		t.Fatalf("mutant count = %d, want 1", len(result.Mutants))
	}

	mutant := result.Mutants[0]
	if mutant.State != report.Killed || !mutant.DeadlineReached {
		t.Fatalf("mutant state = %s, deadline reached = %t", mutant.State, mutant.DeadlineReached)
	}
}

func interruptedRunRequest(t *testing.T) (engine.RunRequest, string) {
	t.Helper()

	module := t.TempDir()
	marker := filepath.Join(t.TempDir(), "applying")
	writeFile(t, filepath.Join(module, "main.tf"), `resource "terraform_data" "app" {
  input = false

  provisioner "local-exec" {
    command = <<-EOT
      if [ "${self.input}" = true ]; then
        printf ready > "$TF_MUT_CANCELLATION_MARKER"
        exec sleep 30
      fi
    EOT
  }
}

output "app" {
  value = terraform_data.app.input
}
`)

	tests := filepath.Join(module, "tests")
	if err := os.Mkdir(tests, 0o750); err != nil {
		t.Fatalf("creating test directory: %v", err)
	}
	writeFile(t, filepath.Join(tests, "unit.tftest.hcl"), `run "planned" {
  command = plan

  assert {
    condition     = output.app == false
    error_message = "the input must stay false"
  }
}

run "applied" {
  command = apply
}
`)

	request := baseConfig(t, module)
	request.AllowUnsandboxedEffects = true
	request.NoCache = true
	request.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
	request.Env = append(request.Env, "TF_MUT_CANCELLATION_MARKER="+marker)

	return request, marker
}
