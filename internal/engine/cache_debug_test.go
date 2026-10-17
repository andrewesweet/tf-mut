package engine_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// TestCacheKeyComponentsAreLoggedAtDebug is the D17 step-one proof: every
// cache-key component's digest is logged at debug level, so a miss names the
// component that changed. Values never reach the log — environment values
// above all — only their digests.
//
// The reported key is tied back to the report independently: a logged key
// must carry the prefix the replayed mutants cite as their cache basis.
//
// The test is sequential for the global default logger it installs: parallel
// tests drive the same engine and would interleave their records with this
// capture.
//
//nolint:paralleltest // installs the process-global default slog handler for the capture window.
func TestCacheKeyComponentsAreLoggedAtDebug(t *testing.T) {
	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	const probeName = "TFMUT_CACHE_PROBE"
	const probeValue = "probe-value-debug-9d3f41"
	config.Env = append(config.Env, probeName+"="+probeValue)

	captured := []debugRecord{}
	previous := slog.Default()
	slog.SetDefault(slog.New(debugHandler{records: &captured}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	first := cachedRun(t, config)
	if first.Population.Cached != 0 {
		t.Fatalf("the first run reported %d cached verdicts from an empty cache",
			first.Population.Cached)
	}

	second := cachedRun(t, config)
	if second.Population.Cached == 0 || second.Population.Fresh != 0 {
		t.Fatalf("the second unchanged run was not all cache hits: %+v", second.Population)
	}

	components := map[string]string{}
	keys := []string{}

	for _, record := range captured {
		if strings.Contains(record.message, probeValue) {
			t.Fatalf("an environment value reaches the debug log message: %q", record.message)
		}

		for key, value := range record.attributes {
			if strings.Contains(value, probeValue) {
				t.Fatalf("an environment value reaches the debug log attribute %q", key)
			}
		}

		switch record.message {
		case "tf-mut: cache key component":
			components[record.attributes["component"]] = record.attributes["digest"]
		case "tf-mut: cache key":
			keys = append(keys, record.attributes["key"])
		}
	}

	// The components the key always hashes, whatever the fixture carries.
	for _, component := range []string{
		"format", "terraform", "platform", "baseline", "config", "source", "test", "env",
	} {
		digest, found := components[component]
		if !found {
			t.Fatalf("no debug digest logged for key component %q (logged: %v)",
				component, componentNames(components))
		}

		if !isHexDigest(digest) {
			t.Fatalf("component %q logged digest %q, want 64 lowercase hex characters", component, digest)
		}
	}

	if len(keys) == 0 {
		t.Fatal("no cache key logged at debug level")
	}

	for _, key := range keys {
		if !isHexDigest(key) {
			t.Fatalf("logged key %q, want 64 lowercase hex characters", key)
		}
	}

	bases := map[string]bool{}
	for _, mutant := range second.Mutants {
		if mutant.Provenance != nil && mutant.Provenance.Execution == report.ExecutionCached {
			bases[mutant.Provenance.CacheKey] = true
		}
	}

	if len(bases) == 0 {
		t.Fatal("the replayed run cites no cache basis; the log correlation is vacuous")
	}

	for basis := range bases {
		matched := false

		for _, key := range keys {
			if strings.HasPrefix(key, basis) {
				matched = true
				break
			}
		}

		if !matched {
			t.Fatalf("the report's cache basis %q matches no logged key", basis)
		}
	}
}

// debugRecord is one captured slog record: its message and its stringified
// attributes.
type debugRecord struct {
	message    string
	attributes map[string]string
}

// debugHandler captures every record the engine logs at debug level and
// above into the slice it points at.
type debugHandler struct {
	records *[]debugRecord
}

func (h debugHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h debugHandler) Handle(_ context.Context, record slog.Record) error {
	captured := debugRecord{message: record.Message, attributes: map[string]string{}}
	record.Attrs(func(attr slog.Attr) bool {
		captured.attributes[attr.Key] = attr.Value.String()
		return true
	})
	*h.records = append(*h.records, captured)

	return nil
}

func (h debugHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h debugHandler) WithGroup(string) slog.Handler { return h }

func componentNames(components map[string]string) []string {
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}

	return names
}

func isHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}

	for _, digit := range value {
		if !strings.ContainsRune("0123456789abcdef", digit) {
			return false
		}
	}

	return true
}
