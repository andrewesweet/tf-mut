//go:build integration

package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
)

// The M5-0.4 benchmark-corpus census (#155): the module-admission measurement
// over the pinned 23-repository corpus of Oasis's published evaluation
// (research/corpus/m5-benchmark.json).
//
// Every module gets two invocations, always both, in this order, each decided
// by its own outcome and never inferred from the other: first the preview
// invocation, whose own outcome decides population availability; second the
// run invocation (--no-cache --tier standard), whose own outcome decides the
// row outcome in M5d's total vocabulary. An operational row is retried once
// and published as a fact about this run, not the module; the census never
// aborts on one.
//
// The measurement is network-gated behind TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1,
// which here licenses archive fetching and nothing else: no census request
// ever sets AllowRealInfrastructure or AllowUnsandboxedEffects, so no public
// suite is graded with a bypassed safety gate. A row whose module carries
// unsandboxed effects is the unsandboxed-effects outcome of the vocabulary,
// not a bypass. The environment points Terraform at no repository mirror —
// the mirror carries hashicorp/null only — so init downloads the providers
// each module names into the shared plugin cache.
//
// The claim the count of scored modules licenses is the module-admission rate
// over the pinned corpus, published with both denominators (all pinned
// modules and modules with known populations) and the count of unknown
// populations — and nothing about comparability with Oasis's numbers.
//
// The two invocations go through the engine seam, engine.Run, rather than
// the built binary: the seam is the repository's fixed testing decision, the
// M4.5-0 corpus measurement set the precedent, and the row vocabulary is
// classified from the typed stage sentinels only the seam returns. The CLI
// adds only .tf-mut.hcl loading and exit-code projection over the same
// engine, so no safety gate differs between the two.

const (
	censusManifest = "../../research/corpus/m5-benchmark.json"
	censusOutput   = "../../.artifacts/measurement/m5-benchmark-census.json"
	// censusRows is the crash-safe side-car: one completed module's pair per
	// line, appended as the module finishes, so a measurement that outlives a
	// process resumes instead of repeating hours of work.
	censusRows = "../../.artifacts/measurement/m5-benchmark-census-rows.jsonl"
	// censusArchives keeps the extracted, digest-verified archives between
	// runs; t.TempDir would delete them with the process.
	censusArchives = "../../.artifacts/census-archives"

	// censusRootTestRoot is the manifest's test-root value for a suite
	// colocated with the module subdirectory.
	censusRootTestRoot = "root"
)

// errFetchStatus and errDigestMismatch are the fetch stage's two static
// failures; each is wrapped with the module and the observed value so the
// operational row's reason carries both.
var (
	errFetchStatus    = errors.New("unexpected HTTP status")
	errDigestMismatch = errors.New("archive digest does not match the pin")
)

// benchmarkModule is one manifest entry.
type benchmarkModule struct {
	Name         string `json:"name"`
	Repository   string `json:"repository"`
	Commit       string `json:"commit"`
	SHA256       string `json:"sha256"`
	ModuleSubdir string `json:"module_subdirectory"`
	TestRoot     string `json:"test_root"`
	JSONDeclared bool   `json:"json_declared"`
}

// benchmarkCorpus is the manifest's shape.
type benchmarkCorpus struct {
	Description string            `json:"description"`
	Modules     []benchmarkModule `json:"modules"`
}

// censusModuleRow is one module's published pair: one availability fact and
// one row outcome, each from its own invocation.
type censusModuleRow struct {
	Module     string `json:"module"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	// Availability, decided by the preview invocation.
	PopulationKnown bool   `json:"population_known"`
	PopulationSize  int    `json:"population_size"`
	UnknownReason   string `json:"unknown_population_reason,omitempty"`
	// Row outcome, decided by the run invocation.
	Row string `json:"row_outcome"`
	// OperationalReason is the first attempt's error text, truncated, when the
	// row needed a retry at the fetch or run stage; empty when it did not.
	OperationalReason string `json:"operational_reason,omitempty"`
	// Retries counts operational re-invocations across the fetch and run
	// stages.
	Retries int `json:"operational_retries"`
}

// censusMeasurement is the published census: the rows, the two denominators
// and the unknown-population count the table states.
type censusMeasurement struct {
	Corpus string            `json:"corpus"`
	Rows   []censusModuleRow `json:"rows"`
	// Pinned is the first denominator: every module in the manifest.
	Pinned int `json:"modules_pinned"`
	// Known is the second denominator: modules whose own preview invocation
	// exited zero with a report.
	Known            int    `json:"populations_known"`
	Unknown          int    `json:"populations_unknown"`
	Scored           int    `json:"scored_modules"`
	ScoredOverKnown  string `json:"admission_rate_over_known_populations"`
	ScoredOverPinned string `json:"admission_rate_over_pinned_corpus"`
	Claim            string `json:"licensed_claim"`
}

func TestTheBenchmarkCorpusCensus(t *testing.T) {
	t.Parallel()
	requireRealInfrastructureOptIn(t)

	loaded := loadBenchmarkCorpus(t)
	completed := loadCompletedRows(t)

	for _, module := range loaded.Modules {
		if done, ok := completed[module.Name]; ok && done.Commit == module.Commit && done.Row != string(rowOperational) {
			continue
		}

		row := runCensusPair(t, module)
		recordCompletedRow(t, row)

		t.Logf("%s: population known=%v size=%d row=%s", row.Module, row.PopulationKnown, row.PopulationSize, row.Row)
	}

	measurement := assembleMeasurement(t, loaded, loadCompletedRows(t))
	summariseCensus(t, &measurement)
	publishCensus(t, measurement)

	t.Logf("module-admission: %d scored over %d known populations (%s), %d over %d pinned; %d unknown populations",
		measurement.Scored, measurement.Known, measurement.ScoredOverKnown,
		measurement.Scored, measurement.Pinned, measurement.Unknown)
}

// loadCompletedRows reads the side-car's completed pairs, keyed by module.
func loadCompletedRows(t *testing.T) map[string]censusModuleRow {
	t.Helper()

	completed := map[string]censusModuleRow{}

	content, err := os.ReadFile(censusRows)
	if err != nil {
		return completed // no side-car yet: nothing has completed.
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(content)), "\n") {
		if line == "" {
			continue
		}

		var row censusModuleRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decoding a completed census row: %v", err)
		}

		completed[row.Module] = row
	}

	return completed
}

// recordCompletedRow appends one module's finished pair to the side-car.
func recordCompletedRow(t *testing.T, row censusModuleRow) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(censusRows), 0o750); err != nil {
		t.Fatalf("creating the measurement directory: %v", err)
	}

	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("encoding the census row: %v", err)
	}

	file, err := os.OpenFile(censusRows, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening the census row side-car: %v", err)
	}

	defer func() { _ = file.Close() }()

	if _, err := file.Write(append(encoded, '\n')); err != nil {
		t.Fatalf("appending the census row: %v", err)
	}
}

// assembleMeasurement orders the completed pairs in manifest order.
func assembleMeasurement(t *testing.T, loaded benchmarkCorpus, completed map[string]censusModuleRow) censusMeasurement {
	t.Helper()

	measurement := censusMeasurement{Corpus: censusManifest, Rows: []censusModuleRow{}}
	measurement.Pinned = len(loaded.Modules)

	for _, module := range loaded.Modules {
		row, done := completed[module.Name]
		if !done {
			t.Fatalf("%s has no completed census pair", module.Name)
		}

		measurement.Rows = append(measurement.Rows, row)
	}

	return measurement
}

// runCensusPair makes the module's two invocations and publishes their pair.
// A fetch or digest failure is the operational outcome of the vocabulary's
// first stage: retried once, published as a fact about this run, never an
// abort.
func runCensusPair(t *testing.T, module benchmarkModule) censusModuleRow {
	t.Helper()

	row := censusModuleRow{
		Module:     module.Name,
		Repository: module.Repository,
		Commit:     module.Commit,
	}

	archive, fetchErr := fetchPinnedRepository(t, module, censusArchives)
	if fetchErr != nil {
		row.Retries++
		row.OperationalReason = censusReason(fetchErr)
		archive, fetchErr = fetchPinnedRepository(t, module, censusArchives)
	}

	if fetchErr != nil {
		row.Row = string(rowOperational)
		row.UnknownReason = censusReason(fetchErr)

		return row
	}

	moduleDir := filepath.Join(archive, filepath.FromSlash(module.ModuleSubdir))

	// Invocation one: the preview invocation decides availability. Preview
	// is cache-free by construction, so there is no cache switch to set.
	preview := previewRequest(t, moduleDir)
	preview.TestDirectory = censusTestDirectory(module.TestRoot)
	preview.Env = censusEnv(t)
	preview.Jobs = testJobs
	preview.WorkDir = t.TempDir()

	previewResult, previewErr := engine.Run(t.Context(), preview)
	fact := classifyAvailability(previewErr, previewResult)
	row.PopulationKnown = fact.Known
	row.PopulationSize = fact.Size
	row.UnknownReason = fact.Reason

	// Invocation two: the run invocation decides the row outcome. Operational
	// rows are retried once; the retry's own outcome decides the row.
	run := censusRun(t, moduleDir)
	run.TestDirectory = censusTestDirectory(module.TestRoot)
	run.Env = censusEnv(t)
	run.WorkDir = t.TempDir()

	runResult, runErr := engine.Run(t.Context(), run)
	if classifyRow(runErr, runResult) == rowOperational {
		row.Retries++
		row.OperationalReason = censusReason(runErr)

		runResult, runErr = engine.Run(t.Context(), run)
	}

	row.Row = string(classifyRow(runErr, runResult))

	return row
}

// censusReason truncates an invocation's error text to what a published row
// can carry.
func censusReason(err error) string {
	if err == nil {
		return ""
	}

	const limit = 500

	text := err.Error()
	if len(text) > limit {
		text = text[:limit] + "…"
	}

	return text
}

// censusTestDirectory maps the manifest's test root onto the engine's
// TestDirectory: root means the suite is colocated with the module, which the
// engine reads regardless of the test directory, so the default stands.
func censusTestDirectory(testRoot string) string {
	if testRoot == censusRootTestRoot {
		return engine.DefaultTestDirectory
	}

	return testRoot
}

// censusEnv points Terraform at no repository mirror: the corpus modules name
// real providers, which init fetches into the shared plugin cache. The safety
// environment variables the product gates on are deliberately absent — the
// census never bypasses a gate.
func censusEnv(t *testing.T) []string {
	t.Helper()

	return []string{
		checkpointDisabled,
		inAutomation,
		"TF_PLUGIN_CACHE_DIR=" + pluginCache(t),
	}
}

func summariseCensus(t *testing.T, measurement *censusMeasurement) {
	t.Helper()

	for _, row := range measurement.Rows {
		if row.PopulationKnown {
			measurement.Known++
		} else {
			measurement.Unknown++
		}

		if row.Row == string(rowScored) {
			measurement.Scored++
		}
	}

	measurement.ScoredOverKnown = fmt.Sprintf("%d/%d", measurement.Scored, measurement.Known)
	measurement.ScoredOverPinned = fmt.Sprintf("%d/%d", measurement.Scored, measurement.Pinned)
	measurement.Claim = "the module-admission rate over the pinned corpus — " +
		"and nothing about comparability"
}

func publishCensus(t *testing.T, measurement censusMeasurement) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(censusOutput), 0o750); err != nil {
		t.Fatalf("creating the measurement directory: %v", err)
	}

	encoded, err := json.MarshalIndent(measurement, "", "  ")
	if err != nil {
		t.Fatalf("encoding the measurement: %v", err)
	}

	if err := os.WriteFile(censusOutput, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing the measurement: %v", err)
	}
}

func loadBenchmarkCorpus(t *testing.T) benchmarkCorpus {
	t.Helper()

	content, err := os.ReadFile(censusManifest)
	if err != nil {
		t.Fatalf("reading the corpus manifest: %v", err)
	}

	loaded := benchmarkCorpus{Description: "", Modules: nil}
	if err := json.Unmarshal(content, &loaded); err != nil {
		t.Fatalf("decoding the corpus manifest: %v", err)
	}

	if len(loaded.Modules) != 23 {
		t.Fatalf("the manifest pins %d repositories, want Oasis's 23", len(loaded.Modules))
	}

	return loaded
}

// fetchPinnedRepository downloads a pinned archive at its commit, checks its
// digest and extracts it, returning the archive root's path. A digest that
// does not match is a failure and never a warning: the whole point of pinning
// is that the census cannot drift. An already-extracted archive is reused,
// with the extracted root recorded in a marker file, so one measurement run
// fetches each repository once.
func fetchPinnedRepository(t *testing.T, module benchmarkModule, cache string) (string, error) {
	t.Helper()

	target := filepath.Join(cache, module.Name)
	marker := filepath.Join(target, ".census-extracted")

	if root, err := os.ReadFile(marker); err == nil { //nolint:gosec // a census-owned temporary path.
		return filepath.Join(target, string(root)), nil
	}

	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/%s",
		module.Repository, module.Commit)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building the request for %s: %w", module.Name, err)
	}

	request.Header.Set("User-Agent", corpusUserAgent)

	client := http.Client{Timeout: 10 * time.Minute}

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", module.Name, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching %s: %w: %s", module.Name, errFetchStatus, response.Status)
	}

	archive, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", module.Name, err)
	}

	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != module.SHA256 {
		return "", fmt.Errorf("%s: %w: got %s, pinned %s",
			module.Name, errDigestMismatch, hex.EncodeToString(sum[:]), module.SHA256)
	}

	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatalf("creating %s: %v", target, err)
	}

	rootPath := extract(t, archive, target)

	if err := os.WriteFile(marker, []byte(filepath.Base(rootPath)), 0o600); err != nil {
		t.Fatalf("marking %s extracted: %v", module.Name, err)
	}

	return rootPath, nil
}
