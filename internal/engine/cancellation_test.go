package engine_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

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

func TestCancellationDuringTraceFlushPreservesBaselineReport(t *testing.T) {
	t.Parallel()

	ctx, endpoint := cancelDuringTraceFlush(t)
	request := baseConfig(t, copyFixture(t, "skeleton"))
	request.NoCache = true
	request.WriteBaseline = true
	request.OTLPEndpoint = endpoint

	result, err := engine.Run(ctx, request)
	assertTraceCancellationPreservesResult(t, ctx, err)
	if result.Gates == nil || result.Gates.Baseline == nil ||
		result.Gates.Baseline.Write != report.BaselineWritten {
		t.Fatalf("the report lost the completed baseline write: %+v", result.Gates)
	}
	if _, statErr := os.Stat(filepath.Join(request.ModuleDir, engine.DefaultBaselineName)); statErr != nil {
		t.Fatalf("the reported baseline was not written: %v", statErr)
	}
}

func TestCancellationDuringTraceFlushPreservesApplyReport(t *testing.T) {
	t.Parallel()

	ctx, endpoint := cancelDuringTraceFlush(t)
	request := applyAllRequest(t, copyFixture(t, suggestBasicFixture))
	request.NoCache = true
	request.OTLPEndpoint = endpoint

	result, err := engine.Run(ctx, request)
	assertTraceCancellationPreservesResult(t, ctx, err)
	if result.Apply == nil || len(result.Apply.Written) == 0 || result.Apply.Aborted != "" {
		t.Fatalf("the report lost the completed suggestion writes: %+v", result.Apply)
	}
	for _, path := range result.Apply.Written {
		if _, statErr := os.Stat(filepath.Join(request.ModuleDir, path)); statErr != nil {
			t.Fatalf("%s is reported written but is absent: %v", path, statErr)
		}
	}
}

func TestCancellationDuringTraceFlushPreservesCharacterisationReport(t *testing.T) {
	t.Parallel()

	ctx, endpoint := cancelDuringTraceFlush(t)
	request := characteriseRequest(t, copyFixture(t, untestedBranchesFixture))
	request.Write = true
	request.OTLPEndpoint = endpoint

	result, err := engine.Run(ctx, request)
	assertTraceCancellationPreservesResult(t, ctx, err)
	if result.Characterisation == nil || result.Characterisation.Write == nil ||
		len(result.Characterisation.Write.Written) == 0 || result.Characterisation.Write.Refused != "" {
		t.Fatalf("the report lost the completed suite writes: %+v", result.Characterisation)
	}
	for _, path := range result.Characterisation.Write.Written {
		if _, statErr := os.Stat(filepath.Join(request.ModuleDir, path)); statErr != nil {
			t.Fatalf("%s is reported written but is absent: %v", path, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(request.ModuleDir, engine.RegistryName)); statErr != nil {
		t.Fatalf("the generated suite has no registry: %v", statErr)
	}
}

func cancelDuringTraceFlush(t *testing.T) (context.Context, string) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		request := &collectortrace.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		for _, resource := range request.GetResourceSpans() {
			for _, scope := range resource.GetScopeSpans() {
				for _, span := range scope.GetSpans() {
					if span.GetName() == "tf-mut.run" {
						cancel()
					}
				}
			}
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(server.Close)

	return ctx, server.URL
}

func assertTraceCancellationPreservesResult(t *testing.T, ctx context.Context, err error) {
	t.Helper()

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("trace flush never canceled the run: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("late cancellation discarded the write report: %v", err)
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
