package engine

import (
	"context"
	"io"

	"github.com/andrewesweet/tf-mut/internal/tracing"

	"github.com/andrewesweet/tf-mut/internal/store"
)

// Dependencies supplies the checkout resolver, tracer factory and store
// factories used by New. The runner stays the concrete tfexec.Runner — it is
// never wired here — and the store factories are the only persistence
// seam: the engine owns every key, path and document shape.
type Dependencies struct {
	// CheckoutRoot resolves the git work tree containing a directory. It
	// replaces the checkoutRoot call in discovery; nil selects checkoutRoot.
	CheckoutRoot func(ctx context.Context, dir string) (string, error)
	// NewTracer builds the run's tracer. It replaces the tracing.New call in
	// instrumentation; nil selects tracing.New. The return type stays the
	// concrete tracer, so no tracing port is introduced alongside.
	NewTracer func(ctx context.Context, endpoint string, warnings io.Writer) *tracing.Tracer
	// NewVerdictStore opens the run's verdict cache at dir. It replaces the
	// filesystem cache; nil selects newFileVerdictStore — the project-local
	// cache under the engine's format versions and size cap.
	NewVerdictStore func(dir string) (VerdictStore, error)
	// NewBaselineStore supplies the acceptance-list store; nil selects the
	// default filesystem baseline.
	NewBaselineStore func() BaselineStore
}

// stores are the run's resolved persistence ports, carried on the settings
// value the request produced. The engine owns what is stored and where;
// these are only the mechanisms, resolved once per run.
type stores struct {
	verdicts func(dir string) (VerdictStore, error)
	baseline func() BaselineStore
}

// DefaultDependencies returns the production wiring: the real checkout
// lookup, the real tracer factory and the filesystem stores. engine.Run is
// defined as New(DefaultDependencies()).Run, so the default wiring and the
// direct entry point are one behaviour by construction.
func DefaultDependencies() Dependencies {
	return Dependencies{
		CheckoutRoot:     checkoutRoot,
		NewTracer:        tracing.New,
		NewVerdictStore:  newFileVerdictStore,
		NewBaselineStore: newFileBaselineStore,
	}
}

// Engine runs requests with the dependencies supplied to New.
type Engine struct {
	dependencies Dependencies
}

// New wires the dependencies into an engine. A nil checkout resolver,
// tracer factory or store factory selects the production default, so the
// zero Dependencies behaves like DefaultDependencies.
func New(dependencies Dependencies) Engine {
	if dependencies.CheckoutRoot == nil {
		dependencies.CheckoutRoot = checkoutRoot
	}

	if dependencies.NewTracer == nil {
		dependencies.NewTracer = tracing.New
	}

	if dependencies.NewVerdictStore == nil {
		dependencies.NewVerdictStore = newFileVerdictStore
	}

	if dependencies.NewBaselineStore == nil {
		dependencies.NewBaselineStore = newFileBaselineStore
	}

	return Engine{dependencies: dependencies}
}

// newFileVerdictStore is the default verdict-store wiring: the filesystem
// cache under the engine's format versions and its deterministic size cap.
func newFileVerdictStore(dir string) (VerdictStore, error) {
	return store.NewVerdictCache(dir, cacheFormatVersion, cacheManifestFormatVersion, CacheMaxEntries)
}

// newFileBaselineStore is the default acceptance-list wiring.
func newFileBaselineStore() BaselineStore {
	return store.NewBaselineFile()
}

// baselineLoad reads the acceptance list at path.
func (s settings) baselineLoad(path string) ([]store.AcceptanceEntry, error) {
	if s.stores.baseline == nil {
		return store.NewBaselineFile().Load(path)
	}

	return s.stores.baseline().Load(path)
}

// baselineSave writes the accepted list at path.
func (s settings) baselineSave(path string, entries []store.AcceptanceEntry) error {
	if s.stores.baseline == nil {
		return store.NewBaselineFile().Save(path, entries)
	}

	return s.stores.baseline().Save(path, entries)
}

// verdictStore opens the run's verdict store at dir, which the caller has
// already decided to use.
func (s settings) verdictStore(dir string) (VerdictStore, error) {
	if s.stores.verdicts == nil {
		return newFileVerdictStore(dir)
	}

	return s.stores.verdicts(dir)
}
