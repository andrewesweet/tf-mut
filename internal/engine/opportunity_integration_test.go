//go:build integration

package engine_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// The M5-0.5a opportunity census and mined-rung count (#156): the static
// opportunity survey over both pinned corpora — the M4.5 synthesis corpus
// (research/corpus/m45-synthesis.json) and the M5 benchmark corpus
// (research/corpus/m5-benchmark.json) — in the `todos` posture.
//
// No Terraform executes anywhere in this measurement: not a version probe,
// not an init, not a plan. Each module gets one `todos` invocation, which
// lists the module inputs the synthesis pipeline could not resolve and why,
// plus — where the listing names a refused typed candidate — one probe
// invocation that offers the refused candidate back as an answer and reads
// which refusal comes back. The probe asks the same closed evaluator, never
// a plan: an undecidable constraint moves under the fail-open answer rule,
// a decidably false one does not, and a redacted attempted value cannot
// bind, which is the M8 redaction rule publishing itself.
//
// The measurement is network-gated behind TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1,
// which here licenses archive fetching and nothing else. An operational
// failure is retried once and published as a fact about this run; a module
// that refuses to parse is published as refused; the census never aborts on
// one. Completed rows go to a side-car so a measurement that outlives its
// process resumes instead of re-fetching.
//
// The claim the counts license is the static opportunity census over the
// pinned corpora — and nothing about what a plan would have decided.

const (
	opportunityM45Manifest = "../../research/corpus/m45-synthesis.json"
	opportunityM5Manifest  = "../../research/corpus/m5-benchmark.json"
	opportunityOutput      = "../../.artifacts/measurement/m5-opportunity-census.json"
	// opportunityRows is the crash-safe side-car: one completed module per
	// line, appended as the module finishes.
	opportunityRows = "../../.artifacts/measurement/m5-opportunity-census-rows.jsonl"
	// opportunityArchives keeps the M4.5 corpus's extracted, digest-verified
	// archives between runs. The M5 corpus shares the M5-0.4 census's cache:
	// same repositories, same digests, same extraction.
	opportunityArchives = "../../.artifacts/opportunity-archives"

	opportunityMarker = ".opportunity-extracted"

	opportunityM45CorpusName = "m45-synthesis"
	opportunityM5CorpusName  = "m5-benchmark"
)

// opportunityM45Module is one M4.5 manifest entry.
type opportunityM45Module struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	SHA256     string `json:"sha256"`
}

type opportunityM45Corpus struct {
	Description string                 `json:"description"`
	Modules     []opportunityM45Module `json:"modules"`
}

// opportunityCorpusSummary is one corpus's folded counts.
type opportunityCorpusSummary struct {
	Corpus   string `json:"corpus"`
	Manifest string `json:"manifest"`
	Modules  int    `json:"modules"`
	Measured int    `json:"measured"`
	Refused  int    `json:"refused_modules"`
	// JSONDeclared counts the modules whose manifest entry — or, for the
	// M4.5 corpus, whose own module directory — records root .tf.json
	// declarations.
	JSONDeclared int `json:"json_declared_modules"`
	// ModulesWithOpportunities is the stage-1 count: modules carrying at
	// least one deduplicated unresolved input.
	ModulesWithOpportunities int               `json:"modules_with_opportunities"`
	Opportunities            int               `json:"opportunities"`
	Strata                   opportunityStrata `json:"strata"`
}

// opportunityTotals is both corpora folded together, with the opportunity
// classes counted and the two decision rules' inputs the ticket names.
type opportunityTotals struct {
	Modules                  int                       `json:"modules"`
	Measured                 int                       `json:"measured"`
	Refused                  int                       `json:"refused_modules"`
	ModulesWithOpportunities int                       `json:"modules_with_opportunities"`
	Opportunities            int                       `json:"opportunities"`
	ByClass                  map[opportunityClass]int  `json:"by_class"`
	Strata                   opportunityStrata         `json:"strata"`
	DecisionInputs           opportunityDecisionInputs `json:"decision_inputs"`
}

type opportunityDecisionInputs struct {
	// StageOneOpportunities is the deduplicated opportunity total the #82
	// stage-1 rule reads.
	StageOneOpportunities int `json:"stage_one_opportunities"`
	// MinedReached and MinedFired are the mined rung's counts the removal
	// rule reads, over both corpora and both strata.
	MinedReached int `json:"mined_reached"`
	MinedFired   int `json:"mined_fired"`
}

type opportunityMeasurement struct {
	Corpora []opportunityCorpusSummary `json:"corpora"`
	Modules []opportunityRow           `json:"modules"`
	Totals  opportunityTotals          `json:"totals"`
	Claim   string                     `json:"licensed_claim"`
}

func TestTheOpportunityCensusOverThePinnedCorpora(t *testing.T) {
	t.Parallel()
	requireRealInfrastructureOptIn(t)

	ctx := context.Background()
	completed := loadOpportunityRows(t)

	measurement := opportunityMeasurement{
		Corpora: []opportunityCorpusSummary{},
		Modules: []opportunityRow{},
	}

	// The M4.5 synthesis corpus: tag-pinned, measured at the archive root,
	// exactly where the synthesis-rate measurement read it.
	m45 := loadOpportunityM45Corpus(t)
	for _, module := range m45.Modules {
		if done, ok := completed[opportunityM45CorpusName+"/"+module.Name]; ok && done.Refused == "" {
			measurement.Modules = append(measurement.Modules, done)

			continue
		}

		row := censusM45Module(ctx, t, module)

		recordOpportunityRow(t, row)
		measurement.Modules = append(measurement.Modules, row)
	}

	// The M5 benchmark corpus: commit-pinned, measured at the manifest's
	// module subdirectory, sharing the M5-0.4 census's archive cache.
	m5 := loadBenchmarkCorpus(t)
	for _, module := range m5.Modules {
		if done, ok := completed[opportunityM5CorpusName+"/"+module.Name]; ok && done.Refused == "" {
			measurement.Modules = append(measurement.Modules, done)

			continue
		}

		row := censusM5Module(ctx, t, module)

		// The manifest records this corpus's JSON stratum; the pin is
		// checked against the module directory, and a pin that lies fails
		// the measurement.
		if row.Refused == "" && row.JSONDeclared != module.JSONDeclared {
			t.Errorf("%s: the manifest pins json_declared=%t, the module directory declares %d JSON variables",
				module.Name, module.JSONDeclared, row.JSONVariables)
		}

		row.JSONDeclared = module.JSONDeclared

		recordOpportunityRow(t, row)
		measurement.Modules = append(measurement.Modules, row)
	}

	measurement.Modules = orderOpportunityRows(t, m45, m5, measurement.Modules)
	summariseOpportunities(&measurement)
	publishOpportunities(t, measurement)

	t.Logf("stage 1: %d opportunities over %d measured modules, %d modules carrying at least one",
		measurement.Totals.Opportunities, measurement.Totals.Measured, measurement.Totals.ModulesWithOpportunities)
	t.Logf("mined rung: reached %d, fired %d (native reached %d fired %d)",
		measurement.Totals.DecisionInputs.MinedReached, measurement.Totals.DecisionInputs.MinedFired,
		measurement.Totals.Strata.Native.Reached, measurement.Totals.Strata.Native.Fired)
}

// censusM45Module fetches and reads one M4.5 module, or publishes the
// refusal.
func censusM45Module(ctx context.Context, t *testing.T, module opportunityM45Module) opportunityRow {
	t.Helper()

	root, retries, fetchErr := fetchPinnedTagArchive(ctx, t, module)
	if fetchErr != nil {
		return opportunityRow{
			Corpus: opportunityM45CorpusName, Module: module.Name, Ref: module.Tag,
			Refused: opportunityReason(fetchErr), Retries: retries,
		}
	}

	return measureOpportunityModule(ctx, t,
		opportunityM45CorpusName, module.Name, module.Tag, root, retries)
}

// censusM5Module fetches and reads one M5 module, or publishes the refusal.
func censusM5Module(ctx context.Context, t *testing.T, module benchmarkModule) opportunityRow {
	t.Helper()

	root, retries, fetchErr := fetchPinnedCommitArchive(ctx, t, module)
	if fetchErr != nil {
		return opportunityRow{
			Corpus: opportunityM5CorpusName, Module: module.Name, Ref: module.Commit,
			Refused: opportunityReason(fetchErr), Retries: retries,
		}
	}

	moduleDir := filepath.Join(root, filepath.FromSlash(module.ModuleSubdir))

	return measureOpportunityModule(ctx, t,
		opportunityM5CorpusName, module.Name, module.Commit, moduleDir, retries)
}

// measureOpportunityModule runs one module's census reading. The retries
// count carries the operational retries the fetch already spent; an
// invocation that fails operationally is retried once more and published
// either way. A module whose sources do not parse is published as refused
// by the reader itself — a module fact, not an operational failure.
func measureOpportunityModule(
	ctx context.Context, t *testing.T,
	corpus, module, ref, moduleDir string, retries int,
) opportunityRow {
	t.Helper()

	common := engine.Common{
		ModuleDir:               moduleDir,
		TestDirectory:           engine.DefaultTestDirectory,
		Jobs:                    testJobs,
		TimeoutFactor:           engine.DefaultTimeoutFactor,
		AllowRealInfrastructure: false,
		AllowUnsandboxedEffects: false,
		// The todos posture runs no Terraform; the environment exists so the
		// request is honest about the one it would have had.
		Env:     []string{checkpointDisabled, inAutomation},
		WorkDir: t.TempDir(),
	}

	read, err := readOpportunityReading(ctx, corpus, module, ref, moduleDir, common)
	if err != nil {
		read, err = readOpportunityReading(ctx, corpus, module, ref, moduleDir, common)
		retries++
	}

	row := read.row
	row.Retries = retries

	if err != nil {
		row.Refused = opportunityReason(err)
	}

	return row
}

// loadOpportunityM45Corpus reads the M4.5 manifest.
func loadOpportunityM45Corpus(t *testing.T) opportunityM45Corpus {
	t.Helper()

	content, err := os.ReadFile(opportunityM45Manifest)
	if err != nil {
		t.Fatalf("reading the M4.5 corpus manifest: %v", err)
	}

	loaded := opportunityM45Corpus{Description: "", Modules: nil}
	if err := json.Unmarshal(content, &loaded); err != nil {
		t.Fatalf("decoding the M4.5 corpus manifest: %v", err)
	}

	if len(loaded.Modules) != 10 {
		t.Fatalf("the manifest pins %d modules, want the M4.5 corpus's 10", len(loaded.Modules))
	}

	return loaded
}

// fetchPinnedTagArchive downloads a tag-pinned archive, checks its digest
// and extracts it, returning the archive root. An already-extracted archive
// is reused through the marker file, so one measurement run fetches each
// repository once. The second return counts the retries spent.
func fetchPinnedTagArchive(
	ctx context.Context, t *testing.T, module opportunityM45Module,
) (root string, retries int, err error) {
	t.Helper()

	root, err = fetchTagArchive(ctx, t, module)
	if err != nil {
		retries++
		root, err = fetchTagArchive(ctx, t, module)
	}

	return root, retries, err
}

func fetchTagArchive(ctx context.Context, t *testing.T, module opportunityM45Module) (string, error) {
	t.Helper()

	target := filepath.Join(opportunityArchives, module.Name)
	marker := filepath.Join(target, opportunityMarker)

	if extracted, err := os.ReadFile(marker); err == nil { //nolint:gosec // a census-owned temporary path.
		return filepath.Join(target, string(extracted)), nil
	}

	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/refs/tags/%s",
		module.Repository, module.Tag)

	archive, err := fetchArchive(ctx, t, module.Name, url)
	if err != nil {
		return "", err
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

// fetchPinnedCommitArchive downloads a commit-pinned archive, checks its
// digest and extracts it, sharing the M5-0.4 census's cache directory,
// target layout and marker file — the two censuses read the same pinned
// bytes, so one fetch serves both. The second return counts the retries.
func fetchPinnedCommitArchive(
	ctx context.Context, t *testing.T, module benchmarkModule,
) (root string, retries int, err error) {
	t.Helper()

	root, err = fetchCommitArchive(ctx, t, module)
	if err != nil {
		retries++
		root, err = fetchCommitArchive(ctx, t, module)
	}

	return root, retries, err
}

func fetchCommitArchive(ctx context.Context, t *testing.T, module benchmarkModule) (string, error) {
	t.Helper()

	target := filepath.Join(censusArchives, module.Name)
	marker := filepath.Join(target, ".census-extracted")

	if extracted, err := os.ReadFile(marker); err == nil { //nolint:gosec // a census-owned temporary path.
		return filepath.Join(target, string(extracted)), nil
	}

	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/%s",
		module.Repository, module.Commit)

	archive, err := fetchArchive(ctx, t, module.Name, url)
	if err != nil {
		return "", err
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

// fetchArchive downloads one archive over the corpus user agent.
func fetchArchive(ctx context.Context, t *testing.T, name, url string) ([]byte, error) {
	t.Helper()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building the request for %s: %w", name, err)
	}

	request.Header.Set("User-Agent", corpusUserAgent)

	client := http.Client{Timeout: 10 * time.Minute}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", name, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %w: %s", name, errFetchStatus, response.Status)
	}

	archive, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	return archive, nil
}

// loadOpportunityRows reads the side-car's completed rows, keyed by corpus
// and module.
func loadOpportunityRows(t *testing.T) map[string]opportunityRow {
	t.Helper()

	completed := map[string]opportunityRow{}

	content, err := os.ReadFile(opportunityRows)
	if err != nil {
		return completed // no side-car yet: nothing has completed.
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(content)), "\n") {
		if line == "" {
			continue
		}

		var row opportunityRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decoding a completed opportunity row: %v", err)
		}

		completed[row.Corpus+"/"+row.Module] = row
	}

	return completed
}

// recordOpportunityRow appends one module's finished reading to the side-car.
func recordOpportunityRow(t *testing.T, row opportunityRow) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(opportunityRows), 0o750); err != nil {
		t.Fatalf("creating the measurement directory: %v", err)
	}

	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("encoding the opportunity row: %v", err)
	}

	file, err := os.OpenFile(opportunityRows, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening the opportunity row side-car: %v", err)
	}

	defer func() { _ = file.Close() }()

	if _, err := file.Write(append(encoded, '\n')); err != nil {
		t.Fatalf("appending the opportunity row: %v", err)
	}
}

// orderOpportunityRows orders the rows in manifest order, M4.5 corpus first,
// and fails where a module never completed.
func orderOpportunityRows(
	t *testing.T, m45 opportunityM45Corpus, m5 benchmarkCorpus, rows []opportunityRow,
) []opportunityRow {
	t.Helper()

	byKey := map[string]opportunityRow{}
	for _, row := range rows {
		byKey[row.Corpus+"/"+row.Module] = row
	}

	ordered := make([]opportunityRow, 0, len(m45.Modules)+len(m5.Modules))

	for _, module := range m45.Modules {
		row, done := byKey[opportunityM45CorpusName+"/"+module.Name]
		if !done {
			t.Fatalf("%s has no completed opportunity reading", module.Name)
		}

		ordered = append(ordered, row)
	}

	for _, module := range m5.Modules {
		row, done := byKey[opportunityM5CorpusName+"/"+module.Name]
		if !done {
			t.Fatalf("%s has no completed opportunity reading", module.Name)
		}

		ordered = append(ordered, row)
	}

	return ordered
}

// summariseOpportunities folds the rows into the per-corpus summaries, the
// totals and the two decision rules' inputs. Refused rows count towards the
// denominators and never towards the counts; the JSON stratum is measured
// only where a corpus carries a JSON-declared module, and is published as
// unmeasured otherwise — never as zero.
func summariseOpportunities(measurement *opportunityMeasurement) {
	order := []struct{ key, manifest string }{
		{opportunityM45CorpusName, opportunityM45Manifest},
		{opportunityM5CorpusName, opportunityM5Manifest},
	}

	grouped := map[string][]opportunityRow{}
	for _, row := range measurement.Modules {
		grouped[row.Corpus] = append(grouped[row.Corpus], row)
	}

	totals := opportunityTotals{
		ByClass: map[opportunityClass]int{},
		Strata:  opportunityStrata{Native: opportunityStratum{}},
	}

	measurement.Corpora = measurement.Corpora[:0]

	for _, entry := range order {
		summary := opportunityCorpusSummary{
			Corpus: entry.key, Manifest: entry.manifest,
			Strata: opportunityStrata{Native: opportunityStratum{}},
		}

		measured := make([]opportunityRow, 0, len(grouped[entry.key]))

		for _, row := range grouped[entry.key] {
			summary.Modules++

			if row.Refused != "" {
				summary.Refused++

				continue
			}

			summary.Measured++

			if row.JSONDeclared {
				summary.JSONDeclared++
			}

			summary.Opportunities += row.OpenOpportunities()

			if row.CarriesOpportunities() {
				summary.ModulesWithOpportunities++
			}

			measured = append(measured, row)
		}

		summary.Strata = summariseStrata(measured)
		measurement.Corpora = append(measurement.Corpora, summary)

		totals.Modules += summary.Modules
		totals.Measured += summary.Measured
		totals.Refused += summary.Refused
		totals.ModulesWithOpportunities += summary.ModulesWithOpportunities
		totals.Opportunities += summary.Opportunities
	}

	measuredAll := make([]opportunityRow, 0, len(measurement.Modules))
	for _, row := range measurement.Modules {
		for _, opportunity := range row.Opportunities {
			totals.ByClass[opportunity.Class]++
		}

		if row.Refused == "" {
			measuredAll = append(measuredAll, row)
		}
	}

	totals.Strata = summariseStrata(measuredAll)
	totals.DecisionInputs = opportunityDecisionInputs{
		StageOneOpportunities: totals.Opportunities,
		MinedReached:          totals.Strata.Native.Reached + jsonStratumReached(totals.Strata),
		MinedFired:            totals.Strata.Native.Fired + jsonStratumFired(totals.Strata),
	}

	measurement.Totals = totals
	measurement.Claim = "the static opportunity census over the pinned corpora — " +
		"and nothing about what a plan would have decided"
}

func jsonStratumReached(strata opportunityStrata) int {
	if strata.JSON == nil {
		return 0
	}

	return strata.JSON.Reached
}

func jsonStratumFired(strata opportunityStrata) int {
	if strata.JSON == nil {
		return 0
	}

	return strata.JSON.Fired
}

// publishOpportunities writes the measurement.
func publishOpportunities(t *testing.T, measurement opportunityMeasurement) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(opportunityOutput), 0o750); err != nil {
		t.Fatalf("creating the measurement directory: %v", err)
	}

	encoded, err := json.MarshalIndent(measurement, "", "  ")
	if err != nil {
		t.Fatalf("encoding the measurement: %v", err)
	}

	if err := os.WriteFile(opportunityOutput, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing the measurement: %v", err)
	}
}

// opportunityReason truncates an error to what a published row can carry.
func opportunityReason(err error) string {
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
