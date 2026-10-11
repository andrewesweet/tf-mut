package engine_test

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// The telemetry tests hold the P1 contract: a run's JSON report records where
// its time went — the named phases of the pipeline and of each mutant's
// evaluation, the resource usage of every child Terraform process, and the
// instrumentation's own cost — without deciding anything about any verdict.

// testSubcommand is the Terraform subcommand whose child usage the telemetry
// aggregates; a constant because it names the aggregation key, not a word.
const testSubcommand = "test"

// phaseNames projects a phase list onto its names, in the recorded order.
func phaseNames(phases []report.PhaseTiming) []string {
	names := make([]string, 0, len(phases))
	for _, phase := range phases {
		names = append(names, phase.Phase)
	}

	return names
}

// childUsage finds the aggregate recorded for one Terraform subcommand.
func childUsage(children []report.ProcessUsage, command string) (report.ProcessUsage, bool) {
	for _, child := range children {
		if child.Command == command {
			return child, true
		}
	}

	return report.ProcessUsage{}, false
}

// TestRunTelemetryRecordsPhasesAndChildProcessUsage is the P1 seam contract
// over a real-Terraform fixture: the run-level block names the phases the
// pipeline executed in order, aggregates the child Terraform processes by
// subcommand, and carries the instrumentation's own cost; every fresh-executed
// mutant carries the same record for its own evaluation.
func TestRunTelemetryRecordsPhasesAndChildProcessUsage(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")
	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Telemetry == nil {
		t.Fatal("the report carries no run-level telemetry block")
	}

	assertRunTelemetryRecordsThePipeline(t, result.Telemetry)
	assertRunTelemetryRecordsChildProcesses(t, result.Telemetry)
	assertFreshMutantsCarryTelemetry(t, result)
}

// assertRunTelemetryRecordsThePipeline checks the run block's shape: wall
// time, overhead, scope statement, and the phases the pipeline actually has.
func assertRunTelemetryRecordsThePipeline(t *testing.T, telemetry *report.Telemetry) {
	t.Helper()

	if telemetry.WallMS <= 0 {
		t.Errorf("run wall time is %d ms, want more than zero", telemetry.WallMS)
	}

	if telemetry.OverheadMS < 0 {
		t.Errorf("instrumentation overhead is %d ms, want a non-negative cost", telemetry.OverheadMS)
	}

	if telemetry.ChildScope == "" {
		t.Error("the telemetry block does not state what the child numbers cover")
	}

	// The phases the pipeline actually has, named from the code: version,
	// discovery, the warm workspace's steps, the two baseline legs, generation
	// and the execution loop.
	phases := phaseNames(telemetry.Phases)
	for _, wanted := range []string{
		"version", "discovery", "warm-materialise", "warm-backend", "warm-init",
		"warm-schema", "baseline-test", "baseline-fingerprint", "generation", "execution",
	} {
		if !slices.Contains(phases, wanted) {
			t.Errorf("run telemetry is missing phase %q; recorded %v", wanted, phases)
		}
	}

	// Phases are appended when each completes, so a sequential pipeline
	// records them in execution order. The order of the warm steps is pinned,
	// because those cannot overlap.
	if materialised := slices.Index(phases, "warm-materialise"); materialised >= 0 {
		if initialised := slices.Index(phases, "warm-init"); initialised < materialised {
			t.Errorf("warm phases recorded out of execution order: %v", phases)
		}
	}

	for _, phase := range telemetry.Phases {
		if phase.DurationMS < 0 {
			t.Errorf("phase %q carries a negative duration %d", phase.Phase, phase.DurationMS)
		}
	}
}

// assertRunTelemetryRecordsChildProcesses checks the run's child aggregate:
// the run forked Terraform for the baseline legs and every mutant, so the
// test aggregate exists and is populated.
func assertRunTelemetryRecordsChildProcesses(t *testing.T, telemetry *report.Telemetry) {
	t.Helper()

	testUsage, found := childUsage(telemetry.Children, testSubcommand)
	if !found {
		t.Fatalf("no child usage recorded for the test subcommand; recorded %v", telemetry.Children)
	}

	if testUsage.Invocations < 2 {
		t.Errorf("test subcommand records %d invocations, want at least the two baseline legs",
			testUsage.Invocations)
	}

	if testUsage.PeakRSSKB <= 0 {
		t.Errorf("test subcommand peak RSS is %d KiB, want a real process's non-zero size",
			testUsage.PeakRSSKB)
	}

	if testUsage.UserCPUMS < 0 || testUsage.SystemCPUMS < 0 {
		t.Errorf("test subcommand CPU times are negative: %d/%d",
			testUsage.UserCPUMS, testUsage.SystemCPUMS)
	}

	if _, found := childUsage(telemetry.Children, "init"); !found {
		t.Errorf("no child usage recorded for the init subcommand; recorded %v", telemetry.Children)
	}
}

// assertFreshMutantsCarryTelemetry checks the per-mutant records every fresh
// execution must leave behind.
func assertFreshMutantsCarryTelemetry(t *testing.T, result report.Report) {
	t.Helper()

	fresh := 0
	for _, mutant := range result.Mutants {
		if mutant.Provenance == nil || mutant.Provenance.Execution != report.ExecutionFresh {
			continue
		}

		fresh++

		if mutant.Telemetry == nil {
			t.Errorf("fresh-executed mutant %s carries no telemetry", mutant.ID)

			continue
		}

		mutantPhases := phaseNames(mutant.Telemetry.Phases)
		for _, wanted := range []string{"sandbox", testSubcommand} {
			if !slices.Contains(mutantPhases, wanted) {
				t.Errorf("mutant %s telemetry is missing phase %q; recorded %v",
					mutant.ID, wanted, mutantPhases)
			}
		}

		if mutant.Telemetry.WallMS <= 0 {
			t.Errorf("mutant %s wall time is %d ms, want more than zero",
				mutant.ID, mutant.Telemetry.WallMS)
		}

		// Plausibility: one mutant's evaluation is contained in the run.
		if mutant.Telemetry.WallMS > result.Telemetry.WallMS {
			t.Errorf("mutant %s wall %d ms exceeds the run's %d ms",
				mutant.ID, mutant.Telemetry.WallMS, result.Telemetry.WallMS)
		}

		if len(mutant.Telemetry.Children) == 0 {
			t.Errorf("mutant %s telemetry records no child process usage", mutant.ID)

			continue
		}

		testChild, found := childUsage(mutant.Telemetry.Children, testSubcommand)
		if !found {
			t.Errorf("mutant %s recorded children %v, none for the test subcommand",
				mutant.ID, mutant.Telemetry.Children)

			continue
		}

		if testChild.PeakRSSKB <= 0 {
			t.Errorf("mutant %s test child peak RSS is %d KiB, want non-zero",
				mutant.ID, testChild.PeakRSSKB)
		}
	}

	if fresh == 0 {
		t.Fatal("the fixture produced no fresh execution, so the per-mutant assertion is vacuous")
	}
}

// TestPreviewCarriesRunTelemetryWithoutMutantTelemetry pins the boundary: a
// preview executes nothing, so its telemetry stops at the warm workspace and
// no mutant carries an execution record.
func TestPreviewCarriesRunTelemetryWithoutMutantTelemetry(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")
	result, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if result.Telemetry == nil {
		t.Fatal("the preview report carries no run-level telemetry block")
	}

	phases := phaseNames(result.Telemetry.Phases)
	for _, executed := range []string{"baseline-test", "execution"} {
		if slices.Contains(phases, executed) {
			t.Errorf("preview telemetry records the execution-only phase %q", executed)
		}
	}

	for _, wanted := range []string{"warm-init", "warm-schema", "generation"} {
		if !slices.Contains(phases, wanted) {
			t.Errorf("preview telemetry is missing phase %q; recorded %v", wanted, phases)
		}
	}

	for _, mutant := range result.Mutants {
		if mutant.Telemetry != nil {
			t.Errorf("preview mutant %s carries telemetry for an evaluation that never ran",
				mutant.ID)
		}
	}
}

// TestCachedVerdictsCarryNoMutantTelemetry: a cached verdict replayed from the
// verdict cache was not executed by this run, so it must not present this
// run's telemetry — and the run-level block still records what this run did.
func TestCachedVerdictsCarryNoMutantTelemetry(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")
	// One configuration for both runs: the cache key includes the environment,
	// and a second fixture copy would be a different key and a cold cache.
	config := baseConfig(t, module)

	first, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached == 0 {
		t.Fatal("the replay run served nothing from the cache; the assertion is vacuous")
	}

	if first.Population.Cached != 0 {
		t.Errorf("the first run served %d verdicts from a cache it could not have filled",
			first.Population.Cached)
	}

	cached := 0
	for _, mutant := range second.Mutants {
		if mutant.Provenance != nil && mutant.Provenance.Execution == report.ExecutionCached {
			cached++

			if mutant.Telemetry != nil {
				t.Errorf("cached mutant %s presents telemetry for an evaluation this run never ran",
					mutant.ID)
			}
		}
	}

	if cached != second.Population.Cached {
		t.Fatalf("counted %d cached mutants for a population reporting %d", cached,
			second.Population.Cached)
	}

	if second.Telemetry == nil {
		t.Fatal("the replay run carries no run-level telemetry block")
	}

	phases := phaseNames(second.Telemetry.Phases)
	if !slices.Contains(phases, "cache-load") {
		t.Errorf("replay telemetry is missing the cache-load phase; recorded %v", phases)
	}
}

// lockedBuffer is a concurrency-safe writer: the progress line comes from a
// ticker goroutine while the test may read at any moment.
type lockedBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.buffer.String()
}

// TestProgressLinesReportTheLongRun pins F5: a run with a progress writer and
// a short interval emits periodic n/N lines carrying elapsed time and an ETA,
// on the writer given and nowhere else. A run with no writer — the default,
// and every other test in this suite — stays silent.
func TestProgressLinesReportTheLongRun(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")
	config := baseConfig(t, module)

	var line lockedBuffer
	config.Progress = &line
	config.ProgressIntervalForTesting = time.Millisecond

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	text := line.String()
	if text == "" {
		t.Fatal("a run with a progress writer and a one-millisecond interval printed nothing")
	}

	// The line names the executed population and the pace: n of N mutants,
	// an elapsed time and an ETA, all on one stderr-shaped line.
	pattern := regexp.MustCompile(
		`tf-mut: \d+/` + strconv.Itoa(result.Population.Selected) + ` mutants, \d+s elapsed, ETA `,
	)
	if !pattern.MatchString(text) {
		t.Fatalf("progress output does not match the n/N, elapsed, ETA shape:\n%s", text)
	}

	// The final count reached the total: the tracker did not stop early.
	counted := regexp.MustCompile(`tf-mut: (\d+)/`)
	last := 0
	for _, match := range counted.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(match[1]); err == nil {
			if n < last {
				t.Fatalf("progress moved backwards from %d to %d:\n%s", last, n, text)
			}

			last = n
		}
	}

	if last != result.Population.Selected {
		t.Errorf("final progress reached %d of %d mutants; the line must reach the total",
			last, result.Population.Selected)
	}
}

// TestProgressStaysSilentWithoutAWriter is the quiet half of F5: the default
// run writes no progress line anywhere, so piping stderr never shows one.
func TestProgressStaysSilentWithoutAWriter(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")
	config := baseConfig(t, module)

	var line lockedBuffer
	config.Progress = &line
	config.ProgressIntervalForTesting = 0 // the default interval, still no writer below

	config.Progress = nil

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("run: %v", err)
	}

	if text := line.String(); text != "" {
		t.Fatalf("a run with no progress writer wrote %q", text)
	}
}

func TestShortRunPrintsNoProgress(t *testing.T) {
	t.Parallel()

	config := baseConfig(t, copyFixture(t, "discriminate"))
	var line lockedBuffer
	config.Progress = &line
	config.ProgressIntervalForTesting = time.Hour
	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatal(err)
	}

	if text := line.String(); text != "" {
		t.Fatalf("a run shorter than the first progress interval printed %q", text)
	}
}

func TestExpiredMutantBudgetCountsNoTestChild(t *testing.T) {
	t.Parallel()

	config := baseConfig(t, copyFixture(t, "discriminate"))
	config.NoCache = true
	config.TimeoutFloor = time.Nanosecond
	config.TimeoutFactor = 1e-12
	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}

	if result.Count(report.Timeout) == 0 {
		t.Fatal("the expired budget produced no timeout")
	}

	for _, mutant := range result.Mutants {
		if mutant.State != report.Timeout {
			continue
		}

		if mutant.Telemetry == nil {
			t.Fatalf("timed-out mutant %s carries no telemetry", mutant.ID)
		}

		if _, found := childUsage(mutant.Telemetry.Children, testSubcommand); found {
			t.Fatalf("mutant %s counted a test child that could not start: %+v", mutant.ID, mutant.Telemetry)
		}
	}
}

// TestOTLPExportCarriesRunAndPhaseSpans is the tracing seam contract: a run
// pointed at an OTLP/HTTP endpoint by the flag exports its structure — the
// run span and the phase spans the pipeline executed — to that endpoint and
// no other.
func TestOTLPExportCarriesRunAndPhaseSpans(t *testing.T) {
	t.Parallel()

	sink := newSpanCollector(t)

	module := copyFixture(t, "discriminate")
	config := baseConfig(t, module)
	config.OTLPEndpoint = sink.server.URL + "/v1/traces"

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("run: %v", err)
	}

	names := sink.spanNames(t)

	if !slices.Contains(names, "tf-mut.run") {
		t.Errorf("no run span exported; got %v", names)
	}

	if !slices.Contains(names, "tf-mut.phase") {
		t.Errorf("no phase spans exported; got %v", names)
	}
}

// spanCollector is the OTLP/HTTP sink the seam test points the engine at.
type spanCollector struct {
	server *httptest.Server
	mutex  sync.Mutex
	spans  []*tracepb.Span
}

func newSpanCollector(t *testing.T) *spanCollector {
	t.Helper()

	sink := &spanCollector{}
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
		for _, resource := range request.GetResourceSpans() {
			for _, scope := range resource.GetScopeSpans() {
				for _, span := range scope.GetSpans() {
					sink.spans = append(sink.spans, span)
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

func (s *spanCollector) spanNames(t *testing.T) []string {
	t.Helper()

	names := []string{}
	for _, span := range s.exportedSpans() {
		names = append(names, span.GetName())
	}

	return names
}

func (s *spanCollector) exportedSpans() []*tracepb.Span {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return slices.Clone(s.spans)
}

func TestTraceMeasurementsMatchTheReport(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"discriminate", "mutant-volatile", suggestBasicFixture} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			sink := newSpanCollector(t)
			config := suggestRequest(t, copyFixture(t, fixture))
			config.NoCache = true
			config.OTLPEndpoint = sink.server.URL
			result, err := engine.Run(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}

			assertTraceUsageMatches(t, sink.exportedSpans(), result)
		})
	}
}

func assertTraceUsageMatches(t *testing.T, spans []*tracepb.Span, result report.Report) {
	t.Helper()

	children := []report.ProcessUsage{}
	for _, span := range spans {
		if usage, found := tracedChildUsage(t, span); found {
			children = append(children, usage)
		}
	}

	if len(children) == 0 || result.Telemetry == nil {
		t.Fatal("no child resource measurements to compare")
	}

	if got, want := aggregateChildUsage(children), aggregateChildUsage(result.Telemetry.Children); !maps.Equal(got, want) {
		t.Fatalf("trace child measurements = %v, report = %v", got, want)
	}

	for _, mutant := range result.Mutants {
		if mutant.Telemetry != nil {
			assertMutantPhaseChildCounts(t, mutant)
			assertMutantTraceUsage(t, spans, mutant)
		}
	}
}

func assertMutantPhaseChildCounts(t *testing.T, mutant report.Mutant) {
	t.Helper()

	expected := map[string]int{}
	for _, phase := range mutant.Telemetry.Phases {
		switch phase.Phase {
		case "test", "fingerprint", "volatility-rerun":
			expected[testSubcommand]++
		case "validate":
			expected["validate"]++
		}
	}

	actual := map[string]int{}
	for command, usage := range aggregateChildUsage(mutant.Telemetry.Children) {
		actual[command] = usage.Invocations
	}

	if !maps.Equal(actual, expected) {
		t.Fatalf("mutant %s child invocations = %v, executed phases require %v", mutant.ID, actual, expected)
	}
}

func assertMutantTraceUsage(t *testing.T, spans []*tracepb.Span, mutant report.Mutant) {
	t.Helper()

	for _, parent := range spans {
		if parent.GetName() != "tf-mut.mutant" || spanAttributeString(parent, "tf-mut.mutant_id") != mutant.ID {
			continue
		}

		children := []report.ProcessUsage{}
		for _, phase := range spans {
			if bytes.Equal(phase.GetParentSpanId(), parent.GetSpanId()) {
				if usage, found := tracedChildUsage(t, phase); found {
					children = append(children, usage)
				}
			}
		}

		if got, want := aggregateChildUsage(children), aggregateChildUsage(mutant.Telemetry.Children); !maps.Equal(got, want) {
			t.Fatalf("mutant %s trace child measurements = %v, report = %v", mutant.ID, got, want)
		}

		return
	}

	t.Fatalf("mutant %s has telemetry but no exported parent span", mutant.ID)
}

func spanAttributeString(span *tracepb.Span, key string) string {
	for _, attr := range span.GetAttributes() {
		if attr.GetKey() == key {
			return attr.GetValue().GetStringValue()
		}
	}

	return ""
}

func tracedChildUsage(t *testing.T, span *tracepb.Span) (report.ProcessUsage, bool) {
	t.Helper()

	usage := report.ProcessUsage{Invocations: 1}
	measurements := map[string]int64{}
	for _, attr := range span.GetAttributes() {
		if attr.GetKey() == "tf-mut.child.command" {
			usage.Command = attr.GetValue().GetStringValue()
		} else {
			measurements[attr.GetKey()] = attr.GetValue().GetIntValue()
		}
	}

	if usage.Command == "" {
		return report.ProcessUsage{}, false
	}

	for _, name := range []string{"user_cpu_ms", "system_cpu_ms", "peak_rss_kb"} {
		if _, found := measurements["tf-mut.child."+name]; !found {
			t.Fatalf("child %s trace omits %s", usage.Command, name)
		}
	}

	usage.UserCPUMS = measurements["tf-mut.child.user_cpu_ms"]
	usage.SystemCPUMS = measurements["tf-mut.child.system_cpu_ms"]
	usage.PeakRSSKB = measurements["tf-mut.child.peak_rss_kb"]

	return usage, true
}

func aggregateChildUsage(children []report.ProcessUsage) map[string]report.ProcessUsage {
	aggregates := map[string]report.ProcessUsage{}
	for _, child := range children {
		usage := aggregates[child.Command]
		usage.Command = child.Command
		usage.Invocations += child.Invocations
		usage.UserCPUMS += child.UserCPUMS
		usage.SystemCPUMS += child.SystemCPUMS
		usage.PeakRSSKB = max(usage.PeakRSSKB, child.PeakRSSKB)
		aggregates[child.Command] = usage
	}

	return aggregates
}

func TestNestedCommandsFlushToTheExplicitEndpoint(t *testing.T) {
	for _, command := range []string{"curate", "characterise", "until-dry"} {
		t.Run(command, func(t *testing.T) {
			ambient := newSpanCollector(t)
			t.Setenv(tracing.EndpointEnv, ambient.server.URL)
			target := newSpanCollector(t)
			var request engine.Request
			if command == "curate" {
				config := curateRequest(t, copyFixture(t, curateFixture))
				config.OTLPEndpoint = target.server.URL
				config.NoCache = true
				request = config
			} else {
				config := characteriseRequest(t, copyFixture(t, untestedBranchesFixture))
				config.OTLPEndpoint = target.server.URL
				config.UntilDry = command == "until-dry"
				request = config
			}

			if _, err := engine.Run(t.Context(), request); err != nil {
				t.Fatal(err)
			}

			if names := ambient.spanNames(t); len(names) != 0 {
				t.Fatalf("ambient endpoint received spans: %v", names)
			}

			names := target.spanNames(t)
			if !slices.Contains(names, "tf-mut.phase") || (command != "characterise" && !slices.Contains(names, "tf-mut.mutant")) {
				t.Fatalf("nested command returned before exporting its spans: %v", names)
			}

			warmInit := false
			for _, span := range target.exportedSpans() {
				if spanAttributeString(span, "tf-mut.phase") == "warm-init" {
					warmInit = true
				}
			}

			if !warmInit {
				t.Fatal("the explicit endpoint received no nested warm-up span")
			}
		})
	}
}

// TestCPUProfileWritesTheNamedFile pins the profile switch: a run with
// --cpu-profile writes a non-empty gzip stream — pprof's container format —
// to the named path, and a run without one writes nothing.
func TestCPUProfileWritesTheNamedFile(t *testing.T) {
	t.Parallel()

	profilePath := filepath.Join(t.TempDir(), "tf-mut.pprof")

	module := copyFixture(t, "discriminate")
	config := baseConfig(t, module)
	config.CPUProfile = profilePath

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("run: %v", err)
	}

	//nolint:gosec // the path is this test's own temporary file.
	content, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("reading the profile the run should have written: %v", err)
	}

	if len(content) == 0 {
		t.Fatal("the CPU profile is empty")
	}

	// gzip.NewWriter wrote it; the first two bytes are the gzip magic.
	if content[0] != 0x1f || content[1] != 0x8b {
		t.Fatalf("the profile does not begin with the gzip magic: % x", content[:2])
	}
}

func TestCPUProfileRefusesModuleDestinations(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{
		"existing", "new", "module-directory", "relative", "file-symlink",
		"parent-symlink-existing", "parent-symlink-new", "module-symlink", "missing-parent", "hardlink", "dangling-symlink",
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "discriminate")
			before := treeDigest(t, module)
			common, wantError := profileRefusalRequest(t, module, kind)

			for _, request := range []engine.Request{
				engine.RunRequest{Common: common},
				engine.PreviewRequest{Common: common},
				engine.SuggestRequest{Common: common},
				engine.CharacteriseRequest{Common: common},
				engine.TodosRequest{Common: common},
				engine.CurateRequest{Common: common},
			} {
				if _, err := engine.Run(t.Context(), request); err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("%T returned %v, want a profile refusal containing %q", request, err, wantError)
				}

				assertTreeUnchanged(t, module, before)
			}
		})
	}
}

func profileRefusalRequest(t *testing.T, module, kind string) (engine.Common, string) {
	t.Helper()

	common := commonRequest(t, module)
	common.CPUProfile = filepath.Join(module, mainFile)
	wantError := "inside the module directory"

	switch kind {
	case "new":
		common.CPUProfile = filepath.Join(module, "tf-mut.pprof")
	case "module-directory":
		common.CPUProfile = module
	case "relative":
		working, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}

		common.ModuleDir, err = filepath.Rel(working, module)
		if err != nil {
			t.Fatal(err)
		}

		common.CPUProfile = filepath.Join(common.ModuleDir, mainFile)
	case "file-symlink", "parent-symlink-existing", "parent-symlink-new", "module-symlink":
		alias := filepath.Join(t.TempDir(), "alias")
		target := module
		if kind == "file-symlink" {
			target = common.CPUProfile
		}

		if err := os.Symlink(target, alias); err != nil {
			t.Fatal(err)
		}

		switch kind {
		case "file-symlink":
			common.CPUProfile = alias
			wantError = "file exists"
		case "parent-symlink-existing":
			common.CPUProfile = filepath.Join(alias, mainFile)
		case "parent-symlink-new":
			common.CPUProfile = filepath.Join(alias, "tf-mut.pprof")
		case "module-symlink":
			common.ModuleDir = alias
		}
	case "missing-parent":
		common.CPUProfile = filepath.Join(t.TempDir(), "missing", "tf-mut.pprof")
		wantError = "CPU profile parent directory"
	case "hardlink":
		alias := filepath.Join(t.TempDir(), "profile")
		if err := os.Link(common.CPUProfile, alias); err != nil {
			t.Fatal(err)
		}

		common.CPUProfile = alias
		wantError = "file exists"
	case "dangling-symlink":
		alias := filepath.Join(t.TempDir(), "profile")
		if err := os.Symlink(filepath.Join(filepath.Dir(alias), "missing"), alias); err != nil {
			t.Fatal(err)
		}

		common.CPUProfile = alias
		wantError = "file exists"
	}

	return common, wantError
}

func TestCPUProfileNeverOverwritesExistingFiles(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"outside", "upward-input", "outward-source-symlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			root := copyFixture(t, "upward")
			module := filepath.Join(root, "root")
			target := filepath.Join(root, "shared", mainFile)
			if kind != "upward-input" {
				target = filepath.Join(t.TempDir(), mainFile)
				writeFile(t, target, "output \"protected\" { value = true }\n")
			}

			if kind == "outward-source-symlink" {
				if err := os.Symlink(target, filepath.Join(module, "versions.tf")); err != nil {
					t.Fatal(err)
				}
			}

			before := treeDigest(t, root)
			content, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}

			config := baseConfig(t, module)
			config.CPUProfile = target
			if _, err := engine.Run(t.Context(), config); err == nil || !strings.Contains(err.Error(), "file exists") {
				t.Fatalf("run = %v, want an existing-destination refusal", err)
			}

			after, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(content, after) {
				t.Fatalf("existing profile target changed: %q, %v", after, err)
			}

			assertTreeUnchanged(t, root, before)
		})
	}
}

func TestSuggestTelemetryIncludesVerification(t *testing.T) {
	t.Parallel()

	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry-run=%t", dryRun), func(t *testing.T) {
			t.Parallel()

			request := suggestRequest(t, copyFixture(t, suggestBasicFixture))
			request.DryRun = dryRun
			request.NoCache = true
			result := runSuggest(t, request)

			verificationRuns := 0
			for _, warning := range result.Warnings {
				var fullSuite, isolated int
				if matched, err := fmt.Sscanf(warning,
					"verification executed %d full-suite run(s) plus %d isolated mutant run(s)",
					&fullSuite, &isolated); err == nil && matched == 2 {
					verificationRuns += fullSuite + isolated
				}
			}

			if !dryRun && verificationRuns == 0 {
				t.Fatal("the fixture verified no suggestions")
			}

			if dryRun && verificationRuns != 0 {
				t.Fatalf("dry run executed %d verification runs", verificationRuns)
			}

			assertVerificationTelemetry(t, result, verificationRuns)
		})
	}
}

func assertVerificationTelemetry(t *testing.T, result report.Report, verificationRuns int) {
	t.Helper()

	if result.Telemetry == nil {
		t.Fatal("the suggest report carries no telemetry")
	}

	phases := 0
	for _, phase := range result.Telemetry.Phases {
		if phase.Phase == "suggest-verification" {
			phases++
		}
	}

	if phases != verificationRuns {
		t.Errorf("recorded %d verification phases, want %d", phases, verificationRuns)
	}

	wantTests := 2 + verificationRuns
	for _, mutant := range result.Mutants {
		if mutant.Telemetry == nil {
			continue
		}

		for _, child := range mutant.Telemetry.Children {
			if child.Command == testSubcommand {
				wantTests += child.Invocations
			}
		}
	}

	usage, found := childUsage(result.Telemetry.Children, testSubcommand)
	if !found || usage.Invocations != wantTests {
		t.Errorf("recorded %d test children, want %d including both verification legs", usage.Invocations, wantTests)
	}
}
