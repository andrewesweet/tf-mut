package engine_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
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
	"sync/atomic"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// The telemetry tests hold the P1 contract: a run's JSON report records where
// its time went — the named phases of the pipeline and of each mutant's
// evaluation, the resource usage of every child Terraform process, and the
// instrumentation's own cost — without deciding anything about any verdict.

// testSubcommand is the Terraform subcommand whose child usage the telemetry
// aggregates; a constant because it names the aggregation key, not a word.
const testSubcommand = "test"

const (
	warmInitPhase             = "warm-init"
	fileSymlinkKind           = "file-symlink"
	moduleSymlinkKind         = "module-symlink"
	parentSymlinkExistingKind = "parent-symlink-existing"
	parentSymlinkNewKind      = "parent-symlink-new"
	traceWriterCase           = "writer"
)

// runIDShape is the identifier shape every run publishes: the OpenTelemetry
// trace-ID shape — thirty-two lowercase hex characters — whether it came from
// the run span's trace ID or from crypto/rand.
var runIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

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

	module := copyFixture(t, discriminateFixture)
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
		"version", "discovery", "warm-materialise", "warm-backend", warmInitPhase,
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
		if initialised := slices.Index(phases, warmInitPhase); initialised < materialised {
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

	module := copyFixture(t, discriminateFixture)
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

	module := copyFixture(t, discriminateFixture)
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
// on the writer given and nowhere else, each line carrying the run
// identifier as a `[<run_id>] ` prefix — the same identifier the report's
// telemetry block publishes, minted at random here because the run exports no
// traces. A run with no writer — the default, and every other test in this
// suite — stays silent.
//
//nolint:paralleltest // Swaps the package-level progress interval, which is not safe in parallel.
func TestProgressLinesReportTheLongRun(t *testing.T) {
	module := copyFixture(t, discriminateFixture)
	config := baseConfig(t, module)

	var line lockedBuffer
	config.Progress = &line
	engine.SetProgressInterval(t, time.Millisecond)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	text := line.String()
	if text == "" {
		t.Fatal("a run with a progress writer and a one-millisecond interval printed nothing")
	}

	// The run identifier exists even without tracing, in the trace-ID shape:
	// thirty-two lowercase hex characters.
	if result.Telemetry == nil || !runIDShape.MatchString(result.Telemetry.RunID) {
		t.Fatalf("report run_id = %q, want the 32-hex trace-ID shape", result.Telemetry.RunID)
	}

	// The line names the executed population and the pace: n of N mutants,
	// an elapsed time and an ETA, all on one stderr-shaped line under the
	// run identifier this report publishes.
	pattern := regexp.MustCompile(
		`\[` + result.Telemetry.RunID + `\] tf-mut: \d+/` + strconv.Itoa(result.Population.Selected) +
			` mutants, \d+s elapsed, ETA `,
	)
	if !pattern.MatchString(text) {
		t.Fatalf("progress output does not match the [run_id] n/N, elapsed, ETA shape:\n%s", text)
	}

	// Every line carries the prefix, not only the periodic ones.
	prefix := "[" + result.Telemetry.RunID + "] "
	for line := range strings.SplitSeq(strings.TrimSuffix(text, "\n"), "\n") {
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("line without the run-identifier prefix %q: %q", prefix, line)
		}
	}

	// The final count reached the total: the tracker did not stop early.
	counted := regexp.MustCompile(`\] tf-mut: (\d+)/`)
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

	module := copyFixture(t, discriminateFixture)
	config := baseConfig(t, module)

	var line lockedBuffer

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("run: %v", err)
	}

	if text := line.String(); text != "" {
		t.Fatalf("a run with no progress writer wrote %q", text)
	}
}

//
//nolint:paralleltest // Swaps the package-level progress interval, which is not safe in parallel.
func TestShortRunPrintsNoProgress(t *testing.T) {
	config := baseConfig(t, copyFixture(t, discriminateFixture))
	var line lockedBuffer
	config.Progress = &line
	engine.SetProgressInterval(t, time.Hour)
	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatal(err)
	}

	if text := line.String(); text != "" {
		t.Fatalf("a run shorter than the first progress interval printed %q", text)
	}
}

func TestExpiredMutantBudgetCountsNoTestChild(t *testing.T) {
	t.Parallel()

	config := baseConfig(t, copyFixture(t, discriminateFixture))
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

	sink := newSpanCollector(t, 0)

	module := copyFixture(t, discriminateFixture)
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

func TestTraceExportWarningsAndProgressShareTheWriter(t *testing.T) {
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "1")
	t.Setenv("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "1")

	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(sink.Close)

	var output bytes.Buffer
	request := baseConfig(t, copyFixture(t, discriminateFixture))
	request.NoCache = true
	request.OTLPEndpoint = sink.URL
	request.Progress = &output
	engine.SetProgressInterval(t, time.Millisecond)

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	// Warnings and progress share the writer and the run-identifier prefix.
	prefix := "[" + result.Telemetry.RunID + "] "
	warning := prefix + "tf-mut: trace export failed"
	progress := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `tf-mut: \d+/\d+ mutants, .* elapsed, ETA .*`)
	warnings, lines := 0, 0
	for line := range strings.SplitSeq(strings.TrimSuffix(output.String(), "\n"), "\n") {
		switch {
		case line == warning:
			warnings++
		case progress.MatchString(line):
			lines++
		default:
			t.Fatalf("corrupt warning or progress line: %q", line)
		}
	}

	if warnings == 0 || lines == 0 {
		t.Fatalf("warnings = %d, progress lines = %d; want both", warnings, lines)
	}
}

func TestTraceExportWarningsStopWhenRunReturns(t *testing.T) {
	t.Setenv("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "1")
	t.Setenv("OTEL_BSP_EXPORT_TIMEOUT", "30000")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_TIMEOUT", "30000")

	release, drained := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	var released sync.Once
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		case 2:
			close(drained)
		default:
		}

		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(sink.Close)
	t.Cleanup(func() { released.Do(func() { close(release) }) })

	var output bytes.Buffer
	request := baseConfig(t, copyFixture(t, "skeleton"))
	request.NoCache = true
	request.OTLPEndpoint = sink.URL
	request.Progress = &output
	engine.SetProgressInterval(t, time.Hour)

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("export requests before return = %d, want one still pending", got)
	}
	warning := output.String()
	expectedWarning := "[" + result.Telemetry.RunID + "] tf-mut: trace export failed\n"

	output.Reset()
	_, _ = output.WriteString("caller owns this buffer\n")
	released.Do(func() { close(release) })
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("the timed-out exporter did not finish its pending batch")
	}

	if got := output.String(); got != "caller owns this buffer\n" {
		t.Fatalf("an export wrote to the caller's buffer after return: %q", got)
	}
	if warning != expectedWarning {
		t.Fatalf("shutdown warning = %q, want %q", warning, expectedWarning)
	}
}

//nolint:paralleltest // Subtests share the process-wide OTel environment set by their parent.
func TestTraceExportWarningsStayWithTheirRun(t *testing.T) {
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "60000")

	for _, secondWriter := range []string{traceWriterCase, "nil"} {
		t.Run(secondWriter, func(t *testing.T) {
			assertTraceExportWarningsStayWithTheirRun(t, secondWriter)
		})
	}
}

func assertTraceExportWarningsStayWithTheirRun(t *testing.T, secondWriter string) {
	t.Helper()

	type runOutcome struct {
		report report.Report
		err    error
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	exporting, release := make(chan struct{}), make(chan struct{})
	var received, released sync.Once
	firstSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Do(func() { close(exporting) })
		select {
		case <-release:
			w.WriteHeader(http.StatusUnauthorized)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(firstSink.Close)
	t.Cleanup(func() { released.Do(func() { close(release) }) })

	secondSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(secondSink.Close)

	var firstOutput, secondOutput bytes.Buffer
	engine.SetProgressInterval(t, time.Hour)
	first := baseConfig(t, copyFixture(t, "skeleton"))
	first.NoCache = true
	first.OTLPEndpoint = firstSink.URL
	first.Progress = &firstOutput
	second := baseConfig(t, copyFixture(t, "skeleton"))
	second.NoCache = true
	second.OTLPEndpoint = secondSink.URL
	if secondWriter == traceWriterCase {
		second.Progress = &secondOutput
	}

	finished := make(chan runOutcome, 1)
	go func() {
		firstReport, err := engine.Run(ctx, first)
		finished <- runOutcome{report: firstReport, err: err}
	}()

	select {
	case <-exporting:
	case <-ctx.Done():
		t.Fatal("the first run never reached its trace export")
	}

	secondReport, err := engine.Run(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	released.Do(func() { close(release) })
	firstOutcome := <-finished
	if firstOutcome.err != nil {
		t.Fatal(firstOutcome.err)
	}

	// Each warning carries the run identifier that run's own report publishes.
	warning := "[" + firstOutcome.report.Telemetry.RunID + "] tf-mut: trace export failed\n"
	if firstOutput.String() != warning {
		t.Fatalf("first run warning = %q, want %q", firstOutput.String(), warning)
	}
	wanted := ""
	if secondWriter == traceWriterCase {
		wanted = "[" + secondReport.Telemetry.RunID + "] tf-mut: trace export failed\n"
	}
	if secondOutput.String() != wanted {
		t.Fatalf("second run warning = %q, want %q", secondOutput.String(), wanted)
	}
}

// spanCollector is the OTLP/HTTP sink the seam test points the engine at.
type spanCollector struct {
	server    *httptest.Server
	mutex     sync.Mutex
	spans     []*tracepb.Span
	resources []*tracepb.ResourceSpans
}

func newSpanCollector(t *testing.T, delay time.Duration) *spanCollector {
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
		sink.resources = append(sink.resources, request.GetResourceSpans()...)
		for _, resource := range request.GetResourceSpans() {
			for _, scope := range resource.GetScopeSpans() {
				sink.spans = append(sink.spans, scope.GetSpans()...)
			}
		}

		// The delay is the staged slow exporter whose latency the wall-time
		// assertions measure; it must stay real elapsed time.
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})

	sink.server = httptest.NewServer(mux)
	t.Cleanup(sink.server.Close)

	return sink
}

func (s *spanCollector) spanNames(t *testing.T) []string {
	t.Helper()

	spans := s.exportedSpans()
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.GetName())
	}

	return names
}

func (s *spanCollector) exportedSpans() []*tracepb.Span {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return slices.Clone(s.spans)
}

func (s *spanCollector) resourceAttributeString(key string) string {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, resource := range s.resources {
		for _, attr := range resource.GetResource().GetAttributes() {
			if attr.GetKey() == key {
				return attr.GetValue().GetStringValue()
			}
		}
	}

	return ""
}

func TestTraceMeasurementsMatchTheReport(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{discriminateFixture, "mutant-volatile", suggestBasicFixture} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			sink := newSpanCollector(t, 0)
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

	// The report publishes the run span's trace identifier: the one
	// identifier the trace, the report and every stderr line share.
	assertRunIdentifierIsTheRunSpanTraceID(t, spans, result)

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

// assertRunIdentifierIsTheRunSpanTraceID holds the identifier contract at the
// seam: the report's telemetry publishes exactly one run span, and its trace
// ID is the report's run_id — the same value every span of the run carries.
func assertRunIdentifierIsTheRunSpanTraceID(t *testing.T, spans []*tracepb.Span, result report.Report) {
	t.Helper()

	if result.Telemetry == nil || !runIDShape.MatchString(result.Telemetry.RunID) {
		t.Fatalf("report run_id = %q, want the 32-hex trace-ID shape", result.Telemetry.RunID)
	}

	found := 0
	for _, span := range spans {
		if span.GetName() != "tf-mut.run" {
			continue
		}

		found++
		if got, want := hex.EncodeToString(span.GetTraceId()), result.Telemetry.RunID; got != want {
			t.Fatalf("run span trace id = %s, report run_id = %s", got, want)
		}
	}

	if found != 1 {
		t.Fatalf("exported %d run spans, want exactly one", found)
	}
}

// TestExecutionErrorAppearsOnTheMutantSpan holds the trace half of the
// execution-error contract: an operational failure the report carries as an
// ExecutionError is also an event on that mutant's span, carrying the same
// text, so a trace shows the failure the report shows.
func TestExecutionErrorAppearsOnTheMutantSpan(t *testing.T) {
	t.Parallel()

	sink := newSpanCollector(t, 0)
	config := baseConfig(t, copyFixture(t, "skeleton"))
	config.TestSelection = []string{"tests/does-not-exist.tftest.hcl"}
	config.OTLPEndpoint = sink.server.URL

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Errors) == 0 {
		t.Fatal("the selection matching nothing produced no execution error to trace")
	}

	spans := sink.exportedSpans()
	for _, failure := range result.Errors {
		assertErrorIsASpanEvent(t, spans, failure)
	}
}

// assertErrorIsASpanEvent finds the mutant's exported span and requires an
// execution-error event carrying the report's failure text verbatim.
func assertErrorIsASpanEvent(t *testing.T, spans []*tracepb.Span, failure report.ExecutionError) {
	t.Helper()

	for _, span := range spans {
		if span.GetName() != "tf-mut.mutant" || spanAttributeString(span, "tf-mut.mutant_id") != failure.MutantID {
			continue
		}

		for _, event := range span.GetEvents() {
			if event.GetName() != "tf-mut.execution_error" {
				continue
			}

			for _, attribute := range event.GetAttributes() {
				if attribute.GetKey() == "tf-mut.error" && attribute.GetValue().GetStringValue() == failure.Message {
					return
				}
			}
		}

		t.Fatalf("mutant %s carries the execution error %q in the report but no matching span event",
			failure.MutantID, failure.Message)
	}

	t.Fatalf("execution error for mutant %s has no exported mutant span", failure.MutantID)
}

func assertMutantPhaseChildCounts(t *testing.T, mutant report.Mutant) {
	t.Helper()

	expected := map[string]int{}
	for _, phase := range mutant.Telemetry.Phases {
		switch phase.Phase {
		// The mutant's own test invocation and the volatility re-run start a
		// Terraform child; the fingerprint phase is the comparison over the
		// stream the test invocation already produced, so it starts none.
		case testSubcommand, "volatility-rerun":
			expected[testSubcommand]++
		case "validate":
			expected["validate"]++
		default:
			continue
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

		got, want := aggregateChildUsage(children), aggregateChildUsage(mutant.Telemetry.Children)
		if !maps.Equal(got, want) {
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
	for _, command := range []string{curateCommand, characteriseCommand, "until-dry"} {
		t.Run(command, func(t *testing.T) {
			ambient := newSpanCollector(t, 0)
			t.Setenv(tracing.EndpointEnv, ambient.server.URL)
			target := newSpanCollector(t, 0)
			request := nestedTraceRequest(t, command, target.server.URL)

			if _, err := engine.Run(t.Context(), request); err != nil {
				t.Fatal(err)
			}

			if names := ambient.spanNames(t); len(names) != 0 {
				t.Fatalf("ambient endpoint received spans: %v", names)
			}

			names := target.spanNames(t)
			if !slices.Contains(names, "tf-mut.phase") ||
				(command != characteriseCommand && !slices.Contains(names, "tf-mut.mutant")) {
				t.Fatalf("nested command returned before exporting its spans: %v", names)
			}

			warmInit := false
			for _, span := range target.exportedSpans() {
				if spanAttributeString(span, "tf-mut.phase") == warmInitPhase {
					warmInit = true
				}
			}

			if !warmInit {
				t.Fatal("the explicit endpoint received no nested warm-up span")
			}
		})
	}
}

func nestedTraceRequest(t *testing.T, command, endpoint string) engine.Request {
	t.Helper()

	if command == curateCommand {
		config := curateRequest(t, copyFixture(t, curateFixture))
		config.OTLPEndpoint = endpoint
		config.NoCache = true

		return config
	}

	config := characteriseRequest(t, copyFixture(t, untestedBranchesFixture))
	config.OTLPEndpoint = endpoint
	config.UntilDry = command == "until-dry"

	return config
}

// TestCPUProfileWritesTheNamedFile pins the profile switch: a run with
// --cpu-profile writes a non-empty gzip stream — pprof's container format —
// to the named path, and a run without one writes nothing.
func TestCPUProfileWritesTheNamedFile(t *testing.T) {
	t.Parallel()

	profilePath := filepath.Join(t.TempDir(), "tf-mut.pprof")

	module := copyFixture(t, discriminateFixture)
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

//nolint:paralleltest // a wall-time measurement with the process-global CPU profiler must stay serial.
func TestCommandWallTimeIncludesTraceAndProfileCleanup(t *testing.T) {
	for _, command := range []report.Command{
		report.CommandRun, report.CommandPreview, report.CommandSuggest, report.CommandCurate,
	} {
		t.Run(string(command), func(t *testing.T) {
			sink := newSpanCollector(t, 300*time.Millisecond)
			fixture := discriminateFixture
			if command == report.CommandCurate {
				fixture = curateFixture
			}

			common := commonRequest(t, copyFixture(t, fixture))
			common.OTLPEndpoint = sink.server.URL
			common.CPUProfile = filepath.Join(t.TempDir(), "cpu.pprof")
			requests := map[report.Command]engine.Request{
				report.CommandRun:     engine.RunRequest{Common: common, NoCache: true},
				report.CommandPreview: engine.PreviewRequest{Common: common},
				report.CommandSuggest: engine.SuggestRequest{Common: common, NoCache: true},
				report.CommandCurate:  engine.CurateRequest{Common: common, NoCache: true},
			}

			started := time.Now()
			result, err := engine.Run(t.Context(), requests[command])
			elapsed := time.Since(started).Milliseconds()
			if err != nil {
				t.Fatal(err)
			}

			if result.Telemetry == nil || len(sink.exportedSpans()) == 0 {
				t.Fatal("the command returned no telemetry or exported spans")
			}

			if delta := elapsed - result.Telemetry.WallMS; delta < 0 || delta > 100 {
				t.Fatalf("wall_ms = %d, complete Run took %d ms", result.Telemetry.WallMS, elapsed)
			}
		})
	}
}

func TestCPUProfileRefusesModuleDestinations(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{
		"existing", "new", "module-directory", "relative", fileSymlinkKind,
		parentSymlinkExistingKind, parentSymlinkNewKind, moduleSymlinkKind, "missing-parent", "hardlink", "dangling-symlink",
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, discriminateFixture)
			before := treeDigest(t, module)
			common, wantText, wantIs := profileRefusalRequest(t, module, kind)

			for _, request := range []engine.Request{
				engine.RunRequest{Common: common},
				engine.PreviewRequest{Common: common},
				engine.SuggestRequest{Common: common},
				engine.CharacteriseRequest{Common: common},
				engine.TodosRequest{Common: common},
				engine.CurateRequest{Common: common},
			} {
				_, err := engine.Run(t.Context(), request)
				switch {
				case wantIs != nil && !errors.Is(err, wantIs):
					t.Fatalf("%T returned %v, want the CPU-profile refusal", request, err)
				case wantIs == nil && (err == nil || !strings.Contains(err.Error(), wantText)):
					t.Fatalf("%T returned %v, want a profile refusal containing %q", request, err, wantText)
				default:
				}

				assertTreeUnchanged(t, module, before)
			}
		})
	}
}

func profileRefusalRequest(t *testing.T, module, kind string) (engine.Common, string, error) {
	t.Helper()

	common := commonRequest(t, module)
	common.CPUProfile = filepath.Join(module, mainFile)
	// Two of the three refusal kinds have sentinels: which failure happened is
	// identity, asserted with errors.Is. The parent-directory refusal has no
	// sentinel today, so its wording stays the contract there.
	wantIs, wantText := engine.ErrCPUProfileInModule, ""

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
	case fileSymlinkKind, parentSymlinkExistingKind, parentSymlinkNewKind, moduleSymlinkKind:
		alias := filepath.Join(t.TempDir(), "alias")
		target := module
		if kind == fileSymlinkKind {
			target = common.CPUProfile
		}

		if err := os.Symlink(target, alias); err != nil {
			t.Fatal(err)
		}

		switch kind {
		case fileSymlinkKind:
			common.CPUProfile = alias
			wantIs, wantText = os.ErrExist, ""
		case parentSymlinkExistingKind:
			common.CPUProfile = filepath.Join(alias, mainFile)
		case parentSymlinkNewKind:
			common.CPUProfile = filepath.Join(alias, "tf-mut.pprof")
		case moduleSymlinkKind:
			common.ModuleDir = alias
		default:
			// Other kinds retain the original module and destination.
		}
	case "missing-parent":
		common.CPUProfile = filepath.Join(t.TempDir(), "missing", "tf-mut.pprof")
		wantIs, wantText = nil, "CPU profile parent directory"
	case "hardlink":
		alias := filepath.Join(t.TempDir(), "profile")
		if err := os.Link(common.CPUProfile, alias); err != nil {
			t.Fatal(err)
		}

		common.CPUProfile = alias
		wantIs, wantText = os.ErrExist, ""
	case "dangling-symlink":
		alias := filepath.Join(t.TempDir(), "profile")
		if err := os.Symlink(filepath.Join(filepath.Dir(alias), "missing"), alias); err != nil {
			t.Fatal(err)
		}

		common.CPUProfile = alias
		wantIs, wantText = os.ErrExist, ""
	default:
		// The existing-file case retains the original module and destination.
	}

	return common, wantText, wantIs
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
				testsupport.WriteFile(t, target, "output \"protected\" { value = true }\n")
			}

			if kind == "outward-source-symlink" {
				if err := os.Symlink(target, filepath.Join(module, "versions.tf")); err != nil {
					t.Fatal(err)
				}
			}

			before := treeDigest(t, root)
			//nolint:gosec // the target is this test's own temporary fixture or file.
			content, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}

			config := baseConfig(t, module)
			config.CPUProfile = target
			_, runErr := engine.Run(t.Context(), config)
			// An existing destination is refused by O_EXCL; identity is the
			// os.ErrExist behind the *os.PathError, not its message.
			if !errors.Is(runErr, os.ErrExist) {
				t.Fatalf("run = %v, want an existing-destination refusal", runErr)
			}

			//nolint:gosec // the target is this test's own temporary fixture or file.
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
