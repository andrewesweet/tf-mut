package tracing_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	bodies int
}

func newCollector(t *testing.T) *collector {
	t.Helper()

	sink := &collector{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
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
	nested := run.Attach(ctx)

	_, mutant := tracer.Start(nested, "tf-mut.mutant")
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
