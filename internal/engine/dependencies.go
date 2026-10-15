package engine

import (
	"context"
	"io"

	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// Dependencies is the composition root: everything the engine constructs
// around the request, held as values rather than built inline. Each field
// replaces an existing construction on the Run path — a field that replaced
// nothing would be a seam control by another name, and the audit beside
// TestExportedEngineTypesDeclareNoSeamControls holds every exported field
// honest on that point.
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

// Engine is a wired engine: dependencies in, Run out. New is the only way to
// build one, and Run is the only thing one does.
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
