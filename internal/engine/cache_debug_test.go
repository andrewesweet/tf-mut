package engine_test

import (
	"bytes"
	"regexp"
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
func TestCacheKeyComponentsAreLoggedAtDebug(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	const (
		probeName          = "TFMUT_CACHE_PROBE"
		probeValue         = "probe-value-debug-9d3f41"
		terraformComponent = "terraform"
		sourceComponent    = "source"
	)
	config.Env = append(config.Env, probeName+"="+probeValue)

	var captured bytes.Buffer
	config.Progress = &captured

	first := cachedRun(t, config)
	if first.Population.Cached != 0 {
		t.Fatalf("the first run reported %d cached verdicts from an empty cache",
			first.Population.Cached)
	}
	if strings.Contains(captured.String(), "level=DEBUG") {
		t.Fatalf("debug logging enabled by default: %s", captured.String())
	}

	captured.Reset()
	config.Debug = true
	second := cachedRun(t, config)
	if second.Population.Cached == 0 || second.Population.Fresh != 0 {
		t.Fatalf("enabling debug logging prevented cache reuse: %+v", second.Population)
	}
	if second.Telemetry == nil || second.Telemetry.RunID == "" {
		t.Fatal("the report has no run identifier")
	}

	logs := captured.String()
	if strings.Contains(logs, probeValue) {
		t.Fatal("an environment value reaches the debug log")
	}

	prefix := "[" + second.Telemetry.RunID + "] "
	components := map[string]string{}
	componentLine := regexp.MustCompile(`level=DEBUG msg="tf-mut: cache key component"` +
		` component=([a-z]+) digest=([0-9a-f]{64})$`)
	for line := range strings.SplitSeq(strings.TrimSuffix(logs, "\n"), "\n") {
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("debug line lacks the report's run identifier %q: %q", prefix, line)
		}
		if match := componentLine.FindStringSubmatch(line); len(match) == 3 {
			components[match[1]] = match[2]
		}
	}

	for _, component := range []string{
		"format", terraformComponent, "platform", "baseline", "config", sourceComponent, testSubcommand, "env",
	} {
		if _, found := components[component]; !found {
			t.Fatalf("no valid debug digest for key component %q: %s", component, logs)
		}
	}

	keyLine := regexp.MustCompile(`(?m)level=DEBUG msg="tf-mut: cache key" key=([0-9a-f]{64})$`)
	match := keyLine.FindStringSubmatch(logs)
	if len(match) != 2 {
		t.Fatalf("no valid full cache key logged at debug level: %s", logs)
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
		if !strings.HasPrefix(match[1], basis) {
			t.Fatalf("the report's cache basis %q does not match logged key %q", basis, match[1])
		}
	}
}
