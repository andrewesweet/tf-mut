package engine

import (
	"context"
	"io"

	"github.com/andrewesweet/tf-mut/internal/tfexec"
	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// Dependencies is the composition root: everything the engine constructs
// around the request, held as values rather than built inline. Each field
// replaces an existing construction on the Run path — a field that replaced
// nothing would be a seam control by another name, and the audit beside
// TestExportedEngineTypesDeclareNoSeamControls holds every exported field
// honest on that point.
//
// The Terraform runner stays the concrete tfexec.Runner, never an interface,
// so no fake runner can be wired in: the no-fake-runner rule the testing
// seam states holds at the type level. probe overlays the request settings'
// binary and environment onto the template, so the zero template behaves
// exactly like today's literal construction.
type Dependencies struct {
	// Runner is the Terraform runner template.
	Runner tfexec.Runner
	// CheckoutRoot resolves the git work tree containing a directory. It
	// replaces the checkoutRoot call in discovery; nil selects checkoutRoot.
	CheckoutRoot func(ctx context.Context, dir string) (string, error)
	// NewTracer builds the run's tracer. It replaces the tracing.New call in
	// instrumentation; nil selects tracing.New. The return type stays the
	// concrete tracer, so no tracing port is introduced alongside.
	NewTracer func(ctx context.Context, endpoint string, warnings io.Writer) *tracing.Tracer
	// WarningWriter is the run's warning channel when the request names
	// none: the fallback for the progress, tracing and CPU-profile lines
	// instrumentation wires up. Nil — the default — drops them, exactly as
	// a request without a progress writer does today.
	WarningWriter io.Writer
}

// DefaultDependencies returns the production wiring: the real runner, the
// real checkout lookup and the real tracer factory. engine.Run is defined as
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
