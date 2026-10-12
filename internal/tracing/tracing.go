// Package tracing exports the run's structure as OpenTelemetry spans — one
// per run, per mutant and per phase — over OTLP/HTTP.
//
// The exporter exists only when an endpoint is configured, by the
// --otlp-endpoint flag or the OTEL_EXPORTER_OTLP_ENDPOINT variable the OTel
// conventions define. There is no default endpoint: a run with neither set
// constructs a disabled tracer that exports nothing. JSON telemetry remains
// enabled independently of tracing.
package tracing

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
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

// Attribute is one span attribute: a string key carrying a string or an
// integer value. String and Int are the only constructors; the zero value
// carries nothing and is dropped rather than guessed at. It is the port's
// own vocabulary, so callers never import OpenTelemetry's attribute types.
type Attribute struct {
	key   string
	value any // string or int64, by the constructors alone
}

// String returns an attribute carrying a string value.
func String(key, value string) Attribute {
	return Attribute{key: key, value: value}
}

// Int returns an attribute carrying an integer value.
func Int(key string, value int64) Attribute {
	return Attribute{key: key, value: value}
}

// keyValues projects the port's attributes onto the exporter's vocabulary.
// An attribute carrying neither a string nor an integer — only the zero
// value can be one — is dropped.
func keyValues(attributes []Attribute) []attribute.KeyValue {
	values := make([]attribute.KeyValue, 0, len(attributes))
	for _, a := range attributes {
		switch value := a.value.(type) {
		case string:
			values = append(values, attribute.String(a.key, value))
		case int64:
			values = append(values, attribute.Int64(a.key, value))
		default:
			// Only the constructors write the field; the zero value carries
			// nothing and is dropped rather than guessed at.
		}
	}

	return values
}

// New resolves the endpoint — the argument first, then the environment — and
// returns a tracer that exports over OTLP/HTTP. Any failure to build the
// exporter disables tracing rather than failing the run: the telemetry is a
// passenger, and a misconfigured endpoint must not cost the verdicts. A
// misconfigured endpoint, and a failed export, is reported on warnings, the
// caller-supplied writer the engine points at the request's progress writer —
// never a report sink; a nil writer drops the warning. With no endpoint
// configured, tracing is disabled silently.
func New(ctx context.Context, endpoint string, warnings io.Writer) *Tracer {
	if warnings == nil {
		warnings = io.Discard
	}

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
		warn(warnings, parsed)

		return &Tracer{disabled: true}
	}

	if !strings.HasSuffix(parsed.Path, "/v1/traces") {
		parsed.Path = path.Join(parsed.Path, "v1/traces")
		parsed.RawPath = ""
	}

	otel.SetLogger(logr.Discard())

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(parsed.String()))
	if err != nil {
		warn(warnings, parsed)

		return &Tracer{disabled: true}
	}

	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(&warningExporter{
		SpanExporter: exporter,
		warnings:     warnings,
	}))

	return &Tracer{
		tracer:   provider.Tracer("tf-mut"),
		provider: provider,
	}
}

type warningExporter struct {
	sdktrace.SpanExporter
	warnings io.Writer
}

func (e *warningExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if err := e.SpanExporter.ExportSpans(ctx, spans); err != nil {
		_, _ = fmt.Fprintln(e.warnings, "tf-mut: trace export failed")
	}

	return nil
}

func warn(warnings io.Writer, endpoint *url.URL) {
	target := ""
	if endpoint != nil && endpoint.Scheme != "" && endpoint.Host != "" {
		target = " " + (&url.URL{Scheme: endpoint.Scheme, Host: endpoint.Host}).Redacted()
	}

	_, _ = fmt.Fprintf(warnings, "tf-mut: tracing disabled: an unusable OTLP endpoint%s\n", target)
}

// Enabled reports whether spans are being recorded.
func (t *Tracer) Enabled() bool {
	return t != nil && !t.disabled
}

// Start begins a span parented to the context's current span. When disabled
// it returns an inert placeholder and the context unchanged.
func (t *Tracer) Start(ctx context.Context, name string, attributes ...Attribute) (context.Context, *Span) {
	if !t.Enabled() {
		return ctx, &Span{}
	}

	//nolint:spancheck // the span is the caller's to End; every collector bracket does
	ctx, span := t.tracer.Start(ctx, name, oteltrace.WithAttributes(keyValues(attributes)...))

	return ctx, &Span{span: span} //nolint:spancheck // the span is the caller's to End; every collector bracket does
}

// End closes the span. A disabled span's End is a no-op.
func (s *Span) End(attributes ...Attribute) {
	if s != nil && s.span != nil {
		s.span.SetAttributes(keyValues(attributes)...)
		s.span.End()
	}
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
