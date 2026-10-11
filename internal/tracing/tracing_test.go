package tracing_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// collector is a minimal OTLP/HTTP trace sink: it decodes the protobuf
// ExportTraceServiceRequest bodies the exporter posts and keeps the span
// names for assertions.
type collector struct {
	mutex  sync.Mutex
	server *httptest.Server
	names  []string
	paths  []string
	bodies int
}

func newCollector(t *testing.T) *collector {
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
				for _, span := range scope.GetSpans() {
					sink.names = append(sink.names, span.GetName())
				}
			}
		}

		w.WriteHeader(http.StatusOK)
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

func TestDisabledWithoutEndpoint(t *testing.T) {
	t.Setenv(tracing.EndpointEnv, "")

	ctx := context.Background()
	tracer := tracing.New(ctx, "")

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
	ambient := newCollector(t)
	t.Setenv(tracing.EndpointEnv, ambient.server.URL+"/v1/traces")

	target := newCollector(t)

	ctx := context.Background()
	tracer := tracing.New(ctx, target.server.URL+"/v1/traces")
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
	sink := newCollector(t)
	t.Setenv(tracing.EndpointEnv, sink.server.URL+"/v1/traces")

	ctx := context.Background()
	tracer := tracing.New(ctx, "")
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
	tracer := tracing.New(ctx, "://not-a-url")
	if tracer.Enabled() {
		t.Fatal("a tracer over an unusable endpoint must disable itself")
	}
}

func TestEndpointPathsUseTheTraceSuffix(t *testing.T) {
	for _, endpointPath := range []string{"", "/", "/otlp", "/otlp/", "/v1/traces", "/otlp/v1/traces"} {
		for _, ambient := range []bool{false, true} {
			t.Run(fmt.Sprintf("path=%q/environment=%t", endpointPath, ambient), func(t *testing.T) {
				sink := newCollector(t)
				t.Setenv(tracing.EndpointEnv, "")
				endpoint := sink.server.URL + endpointPath
				if ambient {
					t.Setenv(tracing.EndpointEnv, endpoint)
					endpoint = ""
				}

				tracer := tracing.New(t.Context(), endpoint)
				_, span := tracer.Start(t.Context(), "tf-mut.run")
				span.End()
				shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := tracer.Shutdown(shutdown); err != nil {
					t.Fatal(err)
				}

				want := "/v1/traces"
				if endpointPath == "/otlp" || endpointPath == "/otlp/" || endpointPath == "/otlp/v1/traces" {
					want = "/otlp/v1/traces"
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
		{
			name:     "unsupported-scheme",
			endpoint: "ftp://user:password@collector.example/private?api_key=secret#token",
			safeURL:  " ftp://collector.example",
		},
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

				tracer := tracing.New(t.Context(), endpoint)
				if tracer.Enabled() {
					t.Fatal("an unusable endpoint enabled tracing")
				}

				if _, err := log.Seek(0, io.SeekStart); err != nil {
					t.Fatal(err)
				}

				warning, err := io.ReadAll(log)
				if err != nil {
					t.Fatal(err)
				}

				want := "tf-mut: tracing disabled: an unusable OTLP endpoint" + test.safeURL + "\n"
				if string(warning) != want {
					t.Fatalf("warning = %q, want %q", warning, want)
				}
			})
		}
	}
}
