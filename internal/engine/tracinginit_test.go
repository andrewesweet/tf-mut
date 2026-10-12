package engine_test

import (
	"bytes"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
)

const traceInitializationChild = "TF_MUT_TRACE_INITIALIZATION_CHILD"

func TestTraceInitializationDiagnosticsUseTheRunWriter(t *testing.T) {
	if os.Getenv(traceInitializationChild) == "1" {
		assertTraceInitialization(t)

		return
	}

	t.Parallel()

	for _, scenario := range []struct {
		name       string
		attributes string
		sampler    string
		argument   string
		warning    bool
		spans      bool
	}{
		{"default", "tf-mut.test=kept", "", "", false, true},
		{"missing-resource-value", "tf-mut.test=kept,private-token", "", "", true, true},
		{"invalid-resource-escape", "tf-mut.test=%private-token", "", "", true, true},
		{"unsupported-sampler", "tf-mut.test=kept", "private-sampler-token", "", true, true},
		{"invalid-ratio", "tf-mut.test=kept", "traceidratio", "private-argument-token", true, true},
		{"negative-ratio", "tf-mut.test=kept", "traceidratio", "-1", true, true},
		{"large-ratio", "tf-mut.test=kept", "traceidratio", "2", true, true},
		{"invalid-parent-ratio", "tf-mut.test=kept", "parentbased_traceidratio", "private-argument-token", true, true},
		{"negative-parent-ratio", "tf-mut.test=kept", "parentbased_traceidratio", "-1", true, true},
		{"large-parent-ratio", "tf-mut.test=kept", "parentbased_traceidratio", "2", true, true},
		{"always-on", "tf-mut.test=kept", "always_on", "", false, true},
		{"always-off", "tf-mut.test=kept", "always_off", "", false, false},
		{"parent-always-on", "tf-mut.test=kept", "parentbased_always_on", "", false, true},
		{"parent-always-off", "tf-mut.test=kept", "parentbased_always_off", "", false, false},
		{"ratio-zero", "tf-mut.test=kept", "traceidratio", "0", false, false},
		{"ratio-one", "tf-mut.test=kept", "traceidratio", "1", false, true},
		{"parent-ratio-zero", "tf-mut.test=kept", "parentbased_traceidratio", "0", false, false},
		{"parent-ratio-one", "tf-mut.test=kept", "parentbased_traceidratio", "1", false, true},
	} {
		for _, writer := range []string{"writer", "nil"} {
			t.Run(scenario.name+"/"+writer, func(t *testing.T) {
				t.Parallel()

				//nolint:gosec // Re-execute this test binary through the standard helper-process pattern.
				child := exec.CommandContext(t.Context(), os.Args[0],
					"-test.run=^TestTraceInitializationDiagnosticsUseTheRunWriter$", "-test.timeout=60s")
				child.Env = append(traceInitializationEnvironment(),
					traceInitializationChild+"=1",
					"TF_MUT_TRACE_INITIALIZATION_WRITER="+writer,
					"TF_MUT_TRACE_INITIALIZATION_WARNING="+strconv.FormatBool(scenario.warning),
					"TF_MUT_TRACE_INITIALIZATION_SPANS="+strconv.FormatBool(scenario.spans),
					"OTEL_RESOURCE_ATTRIBUTES="+scenario.attributes,
					"OTEL_TRACES_SAMPLER="+scenario.sampler,
					"OTEL_TRACES_SAMPLER_ARG="+scenario.argument,
					"OTEL_SERVICE_NAME=tf-mut-initialization-test",
				)
				var output, stderr bytes.Buffer
				child.Stdout = &output
				child.Stderr = &stderr
				if err := child.Run(); err != nil {
					t.Fatalf("fresh-process run: %v\n%s\n%s", err, output.String(), stderr.String())
				}
				if stderr.Len() != 0 {
					t.Fatalf("initialization bypassed the caller's writer: %q", stderr.String())
				}
			})
		}
	}
}

func traceInitializationEnvironment() []string {
	return slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "OTEL_") || strings.HasPrefix(entry, "TF_MUT_TRACE_INITIALIZATION_")
	})
}

func TestConcurrentTraceInitializationDiagnosticsKeepTheirWriters(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER", "private-sampler-token")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	for _, secondWriter := range []bool{true, false} {
		t.Run(strconv.FormatBool(secondWriter), func(t *testing.T) {
			var firstOutput, secondOutput bytes.Buffer
			first := baseConfig(t, copyFixture(t, "skeleton"))
			first.OTLPEndpoint = newSpanCollector(t, 0).server.URL
			first.Progress = &firstOutput
			second := baseConfig(t, copyFixture(t, "skeleton"))
			second.OTLPEndpoint = newSpanCollector(t, 0).server.URL
			if secondWriter {
				second.Progress = &secondOutput
			}

			finished := make(chan error, 2)
			for _, request := range []engine.RunRequest{first, second} {
				go func() {
					_, err := engine.Run(t.Context(), request)
					finished <- err
				}()
			}
			for range 2 {
				if err := <-finished; err != nil {
					t.Fatal(err)
				}
			}

			warning := "tf-mut: tracing initialization warning\n"
			if got := firstOutput.String(); got != warning {
				t.Fatalf("first run warning = %q, want %q", got, warning)
			}
			wanted := ""
			if secondWriter {
				wanted = warning
			}
			if got := secondOutput.String(); got != wanted {
				t.Fatalf("second run warning = %q, want %q", got, wanted)
			}
		})
	}
}

func assertTraceInitialization(t *testing.T) {
	t.Helper()

	sink := newSpanCollector(t, 0)
	request := baseConfig(t, copyFixture(t, "skeleton"))
	request.NoCache = true
	request.OTLPEndpoint = sink.server.URL
	request.ProgressIntervalForTesting = time.Hour
	var warnings bytes.Buffer
	if os.Getenv("TF_MUT_TRACE_INITIALIZATION_WRITER") == "writer" {
		request.Progress = &warnings
	}

	if _, err := engine.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	wanted := ""
	if request.Progress != nil && os.Getenv("TF_MUT_TRACE_INITIALIZATION_WARNING") == "true" {
		wanted = "tf-mut: tracing initialization warning\n"
	}
	if got := warnings.String(); got != wanted {
		t.Fatalf("initialization warning = %q, want %q", got, wanted)
	}

	names := sink.spanNames(t)
	if os.Getenv("TF_MUT_TRACE_INITIALIZATION_SPANS") == "false" {
		if len(names) != 0 {
			t.Fatalf("a valid never-sample configuration exported spans: %v", names)
		}

		return
	}
	if !slices.Contains(names, "tf-mut.run") || !slices.Contains(names, "tf-mut.phase") {
		t.Fatalf("initialization did not preserve SDK fallback sampling: %v", names)
	}
	wantResource := "kept"
	if strings.Contains(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), "%private-token") {
		wantResource = "%private-token"
	}
	if got := sink.resourceAttributeString("tf-mut.test"); got != wantResource {
		t.Fatalf("resource attribute = %q, want the SDK's retained value %q", got, wantResource)
	}
	if got := sink.resourceAttributeString("service.name"); got != "tf-mut-initialization-test" {
		t.Fatalf("service.name = %q; resource detection lost the valid service name", got)
	}
}
