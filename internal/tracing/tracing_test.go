package tracing_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/andrewesweet/tf-mut/internal/tracing"
)

const prefixedTracePath = "/otlp/v1/traces"

// collector is a minimal OTLP/HTTP trace sink: it decodes the protobuf
// ExportTraceServiceRequest bodies the exporter posts and keeps the span
// names for assertions.
type collector struct {
	mutex  sync.Mutex
	server *httptest.Server
	names  []string
	paths  []string
	bodies int
	spans  []*tracepb.Span
}

func newCollector(t *testing.T, status int) *collector {
	t.Helper()

	sink := &collector{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

		sink.mutex.Lock()
		defer sink.mutex.Unlock()
		sink.bodies++
		sink.paths = append(sink.paths, r.URL.Path)
		for _, resource := range request.GetResourceSpans() {
			for _, scope := range resource.GetScopeSpans() {
				sink.spans = append(sink.spans, scope.GetSpans()...)
				for _, span := range scope.GetSpans() {
					sink.names = append(sink.names, span.GetName())
				}
			}
		}

		w.WriteHeader(status)
		_, _ = w.Write([]byte("{}"))
	})

	sink.server = httptest.NewServer(mux)
	t.Cleanup(sink.server.Close)

	return sink
}

func (c *collector) spanNames(t *testing.T) []string {
	t.Helper()

	c.mutex.Lock()
	defer c.mutex.Unlock()

	return append([]string{}, c.names...)
}

func (c *collector) exportedSpans(t *testing.T) []*tracepb.Span {
	t.Helper()

	c.mutex.Lock()
	defer c.mutex.Unlock()

	return slices.Clone(c.spans)
}

// TestAttributesProjectOntoTheExportedSpans pins the port's own attribute
// value: string and integer values the caller hands Start and End reach the
// exported spans unchanged, and an attribute that is neither — only the zero
// value can be one — is dropped rather than guessed at. The engine seam's
// OTLP tests pin the same projection for the spans the run actually records.
func TestAttributesProjectOntoTheExportedSpans(t *testing.T) {
	sink := newCollector(t, http.StatusOK)
	t.Setenv(tracing.EndpointEnv, "")

	tracer := tracing.New(t.Context(), sink.server.URL+"/v1/traces", nil)

	ctx, run := tracer.Start(t.Context(), "tf-mut.run",
		tracing.String("tf-mut.command", "run"), tracing.Int("tf-mut.jobs", 4))
	run.End(tracing.String("tf-mut.state", "clean"),
		tracing.Int("tf-mut.overhead_ms", 12),
		tracing.Attribute{})

	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := tracer.Shutdown(shutdown); err != nil {
		t.Fatalf("flushing: %v", err)
	}

	started := false
	for _, span := range sink.exportedSpans(t) {
		if span.GetName() != "tf-mut.run" {
			continue
		}
		started = true

		values := map[string]any{}
		for _, attribute := range span.GetAttributes() {
			if attribute.GetKey() == "" {
				t.Fatal("the zero attribute reached the wire")
			}

			if intValue, ok := attribute.GetValue().GetValue().(*commonv1.AnyValue_IntValue); ok {
				values[attribute.GetKey()] = intValue.IntValue

				continue
			}

			values[attribute.GetKey()] = attribute.GetValue().GetStringValue()
		}

		if values["tf-mut.command"] != "run" || values["tf-mut.state"] != "clean" {
			t.Fatalf("string attributes did not survive the projection: %v", values)
		}

		if values["tf-mut.jobs"] != int64(4) || values["tf-mut.overhead_ms"] != int64(12) {
			t.Fatalf("integer attributes did not survive the projection: %v", values)
		}
	}

	if !started {
		t.Fatal("no run span was exported")
	}
}

func TestDisabledWithoutEndpoint(t *testing.T) {
	t.Setenv(tracing.EndpointEnv, "")

	ctx := context.Background()
	tracer := tracing.New(ctx, "", nil)

	if tracer.Enabled() {
		t.Fatal("a tracer with no flag and no environment variable must be disabled")
	}

	ctx, span := tracer.Start(ctx, "tf-mut.run")
	span.End()

	if err := tracer.Shutdown(ctx); err != nil {
		t.Fatalf("shutting a disabled tracer down: %v", err)
	}
}

func TestExplicitEndpointWinsOverEnvironment(t *testing.T) {
	ambient := newCollector(t, http.StatusOK)
	t.Setenv(tracing.EndpointEnv, ambient.server.URL+"/v1/traces")

	target := newCollector(t, http.StatusOK)

	ctx := context.Background()
	tracer := tracing.New(ctx, target.server.URL+"/v1/traces", nil)
	if !tracer.Enabled() {
		t.Fatal("an explicit endpoint must enable the tracer")
	}

	ctx, span := tracer.Start(ctx, "tf-mut.run")
	span.End()

	shutdown := context.WithoutCancel(ctx)
	shutdown, cancel := context.WithTimeout(shutdown, 5*time.Second)
	defer cancel()
	if err := tracer.Shutdown(shutdown); err != nil {
		t.Fatalf("flushing: %v", err)
	}

	if names := ambient.spanNames(t); len(names) != 0 {
		t.Fatalf("the ambient OTEL_EXPORTER_OTLP_ENDPOINT must not receive spans, got %v", names)
	}

	if names := target.spanNames(t); len(names) != 1 || names[0] != "tf-mut.run" {
		t.Fatalf("the explicit endpoint must receive exactly the run span, got %v", names)
	}
}

func TestEnvironmentConfiguresExport(t *testing.T) {
	sink := newCollector(t, http.StatusOK)
	t.Setenv(tracing.EndpointEnv, sink.server.URL+"/v1/traces")

	ctx := context.Background()
	tracer := tracing.New(ctx, "", nil)
	if !tracer.Enabled() {
		t.Fatal("the environment variable alone must enable the tracer")
	}

	ctx, run := tracer.Start(ctx, "tf-mut.run")

	_, mutant := tracer.Start(ctx, "tf-mut.mutant")
	mutant.End()
	run.End()

	shutdown := context.WithoutCancel(ctx)
	shutdown, cancel := context.WithTimeout(shutdown, 5*time.Second)
	defer cancel()
	if err := tracer.Shutdown(shutdown); err != nil {
		t.Fatalf("flushing: %v", err)
	}

	names := sink.spanNames(t)
	if len(names) != 2 {
		t.Fatalf("expected run and mutant spans, got %v", names)
	}
}

func TestUnusableEndpointDisablesInsteadOfFailing(t *testing.T) {
	t.Setenv(tracing.EndpointEnv, "")

	ctx := context.Background()
	// A nil warning writer must disable the tracer without anywhere to write.
	tracer := tracing.New(ctx, "://not-a-url", nil)
	if tracer.Enabled() {
		t.Fatal("a tracer over an unusable endpoint must disable itself")
	}
}

func TestEndpointPathsUseTheTraceSuffix(t *testing.T) {
	for _, endpointPath := range []string{"", "/", "/otlp", "/otlp/", "/v1/traces", prefixedTracePath} {
		for _, ambient := range []bool{false, true} {
			t.Run(fmt.Sprintf("path=%q/environment=%t", endpointPath, ambient), func(t *testing.T) {
				sink := newCollector(t, http.StatusOK)
				t.Setenv(tracing.EndpointEnv, "")
				endpoint := sink.server.URL + endpointPath
				if ambient {
					t.Setenv(tracing.EndpointEnv, endpoint)
					endpoint = ""
				}

				tracer := tracing.New(t.Context(), endpoint, nil)
				_, span := tracer.Start(t.Context(), "tf-mut.run")
				span.End()
				shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := tracer.Shutdown(shutdown); err != nil {
					t.Fatal(err)
				}

				want := "/v1/traces"
				if endpointPath == "/otlp" || endpointPath == "/otlp/" || endpointPath == prefixedTracePath {
					want = prefixedTracePath
				}

				sink.mutex.Lock()
				defer sink.mutex.Unlock()
				if len(sink.paths) != 1 || sink.paths[0] != want || len(sink.names) != 1 {
					t.Fatalf("export paths = %v, spans = %v; want one span at %s", sink.paths, sink.names, want)
				}
			})
		}
	}
}

func TestUnusableEndpointWarningsRedactSecrets(t *testing.T) {
	for _, test := range []struct {
		name     string
		endpoint string
		safeURL  string
	}{
		//nolint:gosec // planted credentials prove that endpoint warnings redact secrets.
		{
			name:     "unsupported-scheme",
			endpoint: "ftp://user:password@collector.example/private?api_key=secret#token",
			safeURL:  " ftp://collector.example",
		},
		//nolint:gosec // planted credentials prove that endpoint warnings redact secrets.
		{
			name:     "malformed-url",
			endpoint: "https://user:password@collector.example/%zz?api_key=secret#token",
		},
	} {
		for _, ambient := range []bool{false, true} {
			t.Run(test.name+fmt.Sprintf("/environment=%t", ambient), func(t *testing.T) {
				t.Setenv(tracing.EndpointEnv, "")
				endpoint := test.endpoint
				if ambient {
					t.Setenv(tracing.EndpointEnv, endpoint)
					endpoint = ""
				}

				assertUnusableEndpointWarning(t, endpoint, test.safeURL)
			})
		}
	}
}

func assertUnusableEndpointWarning(t *testing.T, endpoint, safeURL string) {
	t.Helper()

	// The warning must land on the writer the caller supplies — the engine
	// passes the request's progress writer, never a report sink — and leave
	// the process's own stderr untouched.
	log, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}

	original := os.Stderr
	os.Stderr = log
	t.Cleanup(func() {
		os.Stderr = original
		_ = log.Close()
	})

	warnings := &bytes.Buffer{}
	tracer := tracing.New(t.Context(), endpoint, warnings)
	if tracer.Enabled() {
		t.Fatal("an unusable endpoint enabled tracing")
	}

	if _, seekErr := log.Seek(0, io.SeekStart); seekErr != nil {
		t.Fatal(seekErr)
	}

	wanted := "tf-mut: tracing disabled: an unusable OTLP endpoint" + safeURL + "\n"
	if warnings.String() != wanted {
		t.Fatalf("warning = %q, want %q", warnings.String(), wanted)
	}

	stray, err := io.ReadAll(log)
	if err != nil {
		t.Fatal(err)
	}

	if len(stray) != 0 {
		t.Fatalf("the tracer wrote to stderr instead of the warning writer: %q", stray)
	}
}

func TestExportFailureWarningsOmitConfigurationSecrets(t *testing.T) {
	sink := newCollector(t, http.StatusUnauthorized)
	t.Setenv(tracing.EndpointEnv, "https://private-user:private-password@ambient.example/%zz")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "private-header-token")
	log, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}

	original := os.Stderr
	os.Stderr = log
	t.Cleanup(func() {
		os.Stderr = original
		_ = log.Close()
	})

	warnings := &bytes.Buffer{}
	tracer := tracing.New(t.Context(), sink.server.URL+"/private-path-token", warnings)
	_, span := tracer.Start(t.Context(), "tf-mut.run")
	span.End()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if shutdownErr := tracer.Shutdown(shutdown); shutdownErr != nil {
		t.Fatal(shutdownErr)
	}

	if warnings.String() != "tf-mut: trace export failed\n" {
		t.Fatalf("warning exposes configuration or omits the fixed message: %q", warnings.String())
	}

	if _, seekErr := log.Seek(0, io.SeekStart); seekErr != nil {
		t.Fatal(seekErr)
	}

	stray, err := io.ReadAll(log)
	if err != nil {
		t.Fatal(err)
	}

	if len(stray) != 0 {
		t.Fatalf("the tracer wrote to stderr instead of the warning writer: %q", stray)
	}

	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	if len(sink.paths) != 1 || sink.paths[0] != "/private-path-token/v1/traces" {
		t.Fatalf("the failing endpoint was not exercised: %v", sink.paths)
	}
}
