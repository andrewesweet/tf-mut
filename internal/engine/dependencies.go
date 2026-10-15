package engine

import (
	"context"
	"io"

	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// Dependencies supplies the checkout resolver and tracer factory used by New.
type Dependencies struct {
	// CheckoutRoot resolves the git work tree containing a directory. It
	// replaces the checkoutRoot call in discovery; nil selects checkoutRoot.
	CheckoutRoot func(ctx context.Context, dir string) (string, error)
	// NewTracer builds the run's tracer. It replaces the tracing.New call in
	// instrumentation; nil selects tracing.New. The return type stays the
	// concrete tracer, so no tracing port is introduced alongside.
	NewTracer func(ctx context.Context, endpoint string, warnings io.Writer) *tracing.Tracer
}

// DefaultDependencies returns the production wiring: the real checkout
// lookup and the real tracer factory. engine.Run is defined as
// New(DefaultDependencies()).Run, so the default wiring and the direct entry
// point are one behaviour by construction.
func DefaultDependencies() Dependencies {
	return Dependencies{
		CheckoutRoot: checkoutRoot,
		NewTracer:    tracing.New,
	}
}

// Engine runs requests with the dependencies supplied to New.
type Engine struct {
	dependencies Dependencies
}

// New wires the dependencies into an engine. A nil checkout resolver or
// tracer factory selects the production default, so the zero Dependencies
// behaves like DefaultDependencies.
func New(dependencies Dependencies) Engine {
	if dependencies.CheckoutRoot == nil {
		dependencies.CheckoutRoot = checkoutRoot
	}

	if dependencies.NewTracer == nil {
		dependencies.NewTracer = tracing.New
	}

	return Engine{dependencies: dependencies}
}
