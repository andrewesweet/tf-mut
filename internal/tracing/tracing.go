// Package tracing exports the run's structure as OpenTelemetry spans — one
// per run, per mutant and per phase — over OTLP/HTTP.
//
// The exporter exists only when an endpoint is configured, by the
// --otlp-endpoint flag or the OTEL_EXPORTER_OTLP_ENDPOINT variable the OTel
// conventions define. There is no default endpoint: a run with neither set
// constructs a disabled tracer, and every call on it is a no-op, so the
// unobserved run pays nothing and sends nothing anywhere.
package tracing

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// EndpointEnv is the variable the OpenTelemetry specification defines for the
// collector endpoint. An explicit flag value wins over it.
const EndpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"

// Tracer hands out spans, or no-ops when disabled.
type Tracer struct {
	disabled bool
	tracer   oteltrace.Tracer
	provider *sdktrace.TracerProvider
}

// Span is one span in progress, or an inert placeholder when the tracer is
// disabled.
type Span struct {
	span oteltrace.Span
}

// errEndpointURL is the refusal for a configured endpoint that is not an
// absolute http or https URL with a host.
var errEndpointURL = errors.New("the URL needs an http or https scheme and a host")

// New resolves the endpoint — the argument first, then the environment — and
// returns a tracer that exports over OTLP/HTTP. Any failure to build the
// exporter disables tracing rather than failing the run: the telemetry is a
// passenger, and a misconfigured endpoint must not cost the verdicts. A
// disabled or misconfigured endpoint is reported on stderr, which is not a
// report sink.
func New(ctx context.Context, endpoint string) *Tracer {
	if endpoint == "" {
		endpoint = os.Getenv(EndpointEnv)
	}

	if strings.TrimSpace(endpoint) == "" {
		return &Tracer{disabled: true}
	}

	// The exporter defers URL problems to export time, where they would die
	// silently in a batch goroutine; refuse them here instead.
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		if err == nil {
			err = errEndpointURL
		}

		warn(endpoint, err)

		return &Tracer{disabled: true}
	}

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		warn(endpoint, err)

		return &Tracer{disabled: true}
	}

	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))

	return &Tracer{
		tracer:   provider.Tracer("tf-mut"),
		provider: provider,
	}
}

func warn(endpoint string, err error) {
	fmt.Fprintf(os.Stderr, "tf-mut: tracing disabled: OTLP endpoint %q unusable: %v\n", endpoint, err)
}

// Enabled reports whether spans are being recorded.
func (t *Tracer) Enabled() bool {
	return t != nil && !t.disabled
}

// Start begins a span parented to the context's current span. When disabled
// it returns an inert placeholder and the context unchanged.
func (t *Tracer) Start(ctx context.Context, name string, attributes ...attribute.KeyValue) (context.Context, *Span) {
	if !t.Enabled() {
		return ctx, &Span{}
	}

	//nolint:spancheck // the span is the caller's to End; every collector bracket does
	ctx, span := t.tracer.Start(ctx, name, oteltrace.WithAttributes(attributes...))

	return ctx, &Span{span: span} //nolint:spancheck // the span is the caller's to End; every collector bracket does
}

// End closes the span. A disabled span's End is a no-op.
func (s *Span) End() {
	if s != nil && s.span != nil {
		s.span.End()
	}
}

// Attach returns a context that carries the span, so spans started later from
// the returned context nest under it.
func (s *Span) Attach(ctx context.Context) context.Context {
	if s == nil || s.span == nil {
		return ctx
	}

	return oteltrace.ContextWithSpan(ctx, s.span)
}

// Shutdown flushes whatever the batcher still holds. It is the caller's job
// to give the call a context that outlives any request cancellation — a
// Ctrl-C killed run should still ship the spans it managed to record.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if !t.Enabled() {
		return nil
	}

	return t.provider.Shutdown(ctx)
}
