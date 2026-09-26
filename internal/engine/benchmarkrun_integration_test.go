//go:build integration

package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M5d public benchmark (#164): the pinned protocol of issue #152's M5d
// section, run over the same pinned 23-repository corpus as the M5-0.4 census
// (research/corpus/m5-benchmark.json), publishing both tables the ticket
// names — the module-admission table in modules and the mutant-level table in
// Oasis's units — side by side with Oasis's row and the stated limitations.
//
// The protocol. Cache is off on every scored invocation and no pack is
// selected: the standard tier only. Each module runs two legs. The cold leg
// deletes that module's plugin cache and makes a fresh one, so providers
// download cold; the warm leg reuses the cache the cold leg filled, so
// providers resolve warm. Within a leg the preview invocation and the run
// invocation each get a fresh sandbox (a fresh WorkDir) and are each decided
// by their own outcome, never inferred from the other. A leg's row outcome is
// its cold leg's, after the one permitted operational retry; an operational
// row is published as a fact about this run, never an abort, and a resumed
// measurement retries it.
//
// The portable assertions. Three facts about a module must hold across its
// two legs, or the pin is broken and the test fails after publishing
// everything the legs produced: population availability and, where known, the
// count; the refusal-class row outcome, decided before any Terraform runs;
// and, per mutant over scored modules, the verdict — state and canonical
// verdict both. The verdict-identity claim is the M3b invariance shape over
// real provider schemas. The wall clock is the one non-portable publication:
// it is published on named hardware and asserted about nothing.
//
// The measurement is network-gated behind TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1,
// which licenses archive fetching and nothing else: no benchmark request ever
// sets AllowRealInfrastructure or AllowUnsandboxedEffects. A module whose
// effects the gates refuse is the unsandboxed-effects row of the vocabulary.
//
// This is the benchmark, a separate measurement from the M5-0.4 census with
// its own side-car and published files; the census's are never written here.
// It is also the only place the four census gate fixtures' classification is
// exercised over real modules at scale — the offline fixtures pin the
// vocabulary, the gate names them, and this test earns their rows.

const (
	benchmarkOutput = "../../.artifacts/measurement/m5-benchmark.json"
	// benchmarkRows is the crash-safe side-car: one completed module's pair
	// of legs per line, appended as the module finishes, so a measurement
	// that outlives a process resumes instead of repeating hours of work.
	benchmarkRows = "../../.artifacts/measurement/m5-benchmark-rows.jsonl"
	// benchmarkPluginCaches keeps one plugin cache per module, so the cold
	// leg's download is cold and the warm leg's resolution is warm.
	benchmarkPluginCaches = "../../.artifacts/benchmark/plugin-caches"

	// benchmarkJobs pins the per-invocation mutant concurrency of the
	// protocol. benchmarkModuleConcurrency bounds how many modules measure
	// at once; the product stays memory-safe on a laptop-scale machine.
	benchmarkJobs              = 4
	benchmarkModuleConcurrency = 2

	// benchmarkTier is the protocol's only tier: the default selection, no
	// packs, which is the posture the whole-milestone invariance proof pins.
	benchmarkTier = "standard"
)

// benchmarkProtocol is the measurement's pinned protocol: what the document
// publishes so another machine can reproduce the shape of the run.
type benchmarkProtocol struct {
	PinnedAt          string            `json:"pinned_at"`
	TerraformVersion  string            `json:"terraform_version"`
	Jobs              int               `json:"jobs"`
	ModuleConcurrency int               `json:"module_concurrency"`
	Tier              string            `json:"tier"`
	Packs             string            `json:"packs"`
	VerdictCache      string            `json:"verdict_cache"`
	ColdLeg           string            `json:"cold_leg"`
	WarmLeg           string            `json:"warm_leg"`
	Environment       string            `json:"environment"`
	Hardware          benchmarkHardware `json:"hardware"`
}

// benchmarkHardware names the machine the wall clock was measured on.
type benchmarkHardware struct {
	CPUModel     string `json:"cpu_model"`
	LogicalCores int    `json:"logical_cores"`
	MemoryKiB    int64  `json:"memory_kib"`
	Kernel       string `json:"kernel"`
	Environment  string `json:"environment"`
}

// benchmarkMeasurement is the published benchmark: the protocol, the rows,
// and the two tables the ticket names.
type benchmarkMeasurement struct {
	Corpus          string                  `json:"corpus"`
	Protocol        benchmarkProtocol       `json:"protocol"`
	Rows            []benchmarkModuleResult `json:"rows"`
	ModuleAdmission moduleAdmissionTable    `json:"module_admission"`
	MutantLevel     mutantLevelTable        `json:"mutant_level"`
	LicensedClaim   string                  `json:"licensed_claim"`
}

// TestTheBenchmarkOverThePinnedCorpus runs the pinned protocol over the
// manifest's 23 pinned modules, cold leg then warm leg per module, and
// publishes both tables. A violated portable assertion fails the test after
// the publication: the measurement is the evidence, and the break is named.
func TestTheBenchmarkOverThePinnedCorpus(t *testing.T) {
	t.Parallel()
	requireRealInfrastructureOptIn(t)

	loaded := loadBenchmarkCorpus(t)
	completed := loadBenchmarkLegs(t)
	prefetchArchives(t, loaded, completed)

	results := make([]benchmarkModuleResult, len(loaded.Modules))
	var (
		mutex  sync.Mutex
		semaph = make(chan struct{}, benchmarkModuleConcurrency)
		wg     sync.WaitGroup
	)

	for index, module := range loaded.Modules {
		wg.Go(func() {
			semaph <- struct{}{}
			defer func() { <-semaph }()

			if done, ok := completed[module.Name]; ok &&
				done.Commit == module.Commit && done.Cold.Row != string(rowOperational) {
				mutex.Lock()
				results[index] = done
				mutex.Unlock()
				t.Logf("%s: resumed from the side-car (cold row %s)", module.Name, done.Cold.Row)

				return
			}

			pair := runBenchmarkPair(t, module)
			pair.Module = module.Name
			pair.Repository = module.Repository
			pair.Commit = module.Commit

			mutex.Lock()
			recordBenchmarkLeg(t, pair)
			results[index] = pair
			mutex.Unlock()

			t.Logf("%s: cold row=%s warm row=%s population known=%v size=%d (%ds cold, %ds warm)",
				module.Name, pair.Cold.Row, pair.Warm.Row, pair.Cold.PopulationKnown,
				pair.Cold.PopulationSize, pair.WallClock.ColdMS/1000, pair.WallClock.WarmMS/1000)
		})
	}

	wg.Wait()

	// The portable assertions hold across every published pair, resumed or
	// freshly measured: availability and count, refusal determinism, and
	// per-mutant verdict identity over the scored modules.
	violations := []string{}
	for _, pair := range results {
		assertBenchmarkLegsAgree(&violations, pair.Module, pair.Cold, pair.Warm)
	}

	measurement := benchmarkMeasurement{
		Corpus: censusManifest,
		Rows:   results,
		LicensedClaim: "the module-admission rate over both denominators and the pooled " +
			"mutant-level arithmetic over the scored modules — side by side with Oasis on " +
			"named axes, under the limitations the document states",
	}
	measurement.Protocol = benchmarkProtocol{
		PinnedAt:          time.Now().UTC().Format(time.RFC3339),
		TerraformVersion:  measuredTerraformVersion(results),
		Jobs:              benchmarkJobs,
		ModuleConcurrency: benchmarkModuleConcurrency,
		Tier:              benchmarkTier,
		Packs:             "none",
		VerdictCache:      "off",
		ColdLeg:           "fresh per-module plugin cache, fresh sandbox",
		WarmLeg:           "the cold leg's plugin cache, fresh sandbox",
		Environment:       "WSL2: providers from the registry through the per-module cache; no mirror",
		Hardware:          measureHardware(t),
	}

	summariseBenchmark(t, &measurement, len(loaded.Modules))
	publishBenchmark(t, measurement)

	t.Logf("module-admission: %s scored over known populations, %s over the pinned corpus; "+
		"%d unknown populations", measurement.ModuleAdmission.ScoredOverKnown,
		measurement.ModuleAdmission.ScoredOverPinned, measurement.ModuleAdmission.UnknownPopulations)
	t.Logf("mutant-level: %d generated (%s), %d scored, %.1f%% pooled mutation score, "+
		"%.1f%% unweighted median over %d modules",
		measurement.MutantLevel.Generated, measurement.MutantLevel.GeneratedBasis,
		measurement.MutantLevel.ScoredSet, measurement.MutantLevel.MutationScore*100,
		measurement.MutantLevel.MedianScore*100, measurement.MutantLevel.MedianModules)

	for _, violation := range violations {
		t.Errorf("portable assertion broken: %s", violation)
	}
}

// prefetchArchives fetches every unfinished module's archive before the
// concurrent legs start: extraction can Fatal on the test's own goroutine,
// the fetches are cheap against the legs, and the cache markers make the
// in-leg fetches instant. A persistent failure is logged here; the leg's own
// retry publishes it as an operational row and goes on.
func prefetchArchives(t *testing.T, loaded benchmarkCorpus, completed map[string]benchmarkModuleResult) {
	t.Helper()

	for _, module := range loaded.Modules {
		if done, ok := completed[module.Name]; ok &&
			done.Commit == module.Commit && done.Cold.Row != string(rowOperational) {
			continue
		}

		archive, err := fetchPinnedRepository(t.Context(), t, module, censusArchives)
		if err == nil {
			requirePinnedModuleDirectory(t, module,
				filepath.Join(archive, filepath.FromSlash(module.ModuleSubdir)))

			continue
		}

		if archive, err = fetchPinnedRepository(t.Context(), t, module, censusArchives); err != nil {
			t.Logf("%s: operational at the fetch stage: %s", module.Name, censusReason(err))

			continue
		}

		requirePinnedModuleDirectory(t, module,
			filepath.Join(archive, filepath.FromSlash(module.ModuleSubdir)))
	}
}

// requirePinnedModuleDirectory fails fast when a manifest entry's module
// subdirectory does not resolve to a Terraform module directory. Every leg
// measured against a wrong directory turns into refusals that look like
// module facts — the two legs agree on them, so the portable assertions hold
// — and the wrongness would publish silently. A module directory carries at
// least one .tf or .tf.json file by definition.
func requirePinnedModuleDirectory(t *testing.T, module benchmarkModule, moduleDir string) {
	t.Helper()

	entries, err := os.ReadDir(moduleDir)
	if err != nil {
		t.Fatalf("%s: reading the pinned module directory %s: %v", module.Name, moduleDir, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tf.json") {
			return
		}
	}

	t.Fatalf("%s: no .tf or .tf.json file under %s; the manifest subdirectory does not resolve",
		module.Name, moduleDir)
}

// runBenchmarkPair makes the module's two legs — cold, then warm — each with
// its own wall clock.
func runBenchmarkPair(t *testing.T, module benchmarkModule) benchmarkModuleResult {
	t.Helper()

	cacheDir := filepath.Join(repositoryRootValue(t), benchmarkPluginCaches, module.Name)

	cold, coldMS := runBenchmarkLeg(t, module, cacheDir, true)
	warm, warmMS := runBenchmarkLeg(t, module, cacheDir, false)

	return benchmarkModuleResult{
		Cold: cold, Warm: warm,
		WallClock: benchmarkMove{ColdMS: coldMS, WarmMS: warmMS},
	}
}

// runBenchmarkLeg is one leg: a fresh sandbox for the preview invocation and
// another for the run invocation, against the leg's plugin cache — freshly
// emptied when fresh is set, reused otherwise. An operational failure is
// retried once per invocation; the vocabulary's static refusals are not.
func runBenchmarkLeg(
	t *testing.T, module benchmarkModule, cacheDir string, fresh bool,
) (leg benchmarkLeg, wallClockMS int64) {
	t.Helper()

	if fresh {
		if err := os.RemoveAll(cacheDir); err != nil {
			t.Fatalf("clearing %s's plugin cache: %v", module.Name, err)
		}
	}

	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		t.Fatalf("creating %s's plugin cache: %v", module.Name, err)
	}

	started := time.Now()

	moduleDir, fetchErr := fetchForLeg(t, module)
	if fetchErr != nil {
		leg.Retries++
		leg.OperationalReason = censusReason(fetchErr)
		leg.Row = string(rowOperational)
		leg.UnknownReason = censusReason(fetchErr)

		return leg, time.Since(started).Milliseconds()
	}

	benchmarkLegPreview(t, module, moduleDir, cacheDir, &leg)
	benchmarkLegRun(t, module, moduleDir, cacheDir, &leg)

	return leg, time.Since(started).Milliseconds()
}

// fetchForLeg resolves the module's pinned directory for this leg — the
// archive root joined with the manifest's module subdirectory — with the one
// permitted operational retry.
func fetchForLeg(t *testing.T, module benchmarkModule) (string, error) {
	t.Helper()

	archive, err := fetchPinnedRepository(t.Context(), t, module, censusArchives)
	if err == nil {
		return filepath.Join(archive, filepath.FromSlash(module.ModuleSubdir)), nil
	}

	if archive, err = fetchPinnedRepository(t.Context(), t, module, censusArchives); err != nil {
		return "", err
	}

	return filepath.Join(archive, filepath.FromSlash(module.ModuleSubdir)), nil
}

// benchmarkLegPreview is the leg's first invocation: the preview invocation
// decides availability.
func benchmarkLegPreview(
	t *testing.T, module benchmarkModule, moduleDir, cacheDir string, leg *benchmarkLeg,
) {
	t.Helper()

	preview := previewRequest(t, moduleDir)
	preview.TestDirectory = censusTestDirectory(module.TestRoot)
	preview.Env = legEnvironment(cacheDir)
	preview.Jobs = benchmarkJobs
	preview.WorkDir = t.TempDir()

	previewStarted := time.Now()

	previewResult, previewErr := engine.Run(t.Context(), preview)
	fact := classifyAvailability(previewErr, previewResult)
	if previewErr != nil && classifyPreviewFailure(previewErr) == rowOperational {
		leg.Retries++
		leg.OperationalReason = censusReason(previewErr)
		preview.WorkDir = t.TempDir()

		previewResult, previewErr = engine.Run(t.Context(), preview)
		fact = classifyAvailability(previewErr, previewResult)
	}

	leg.PreviewMS = time.Since(previewStarted).Milliseconds()
	leg.PopulationKnown = fact.Known
	leg.PopulationSize = fact.Size
	leg.UnknownReason = fact.Reason
	leg.TerraformVersion = previewResult.TerraformVersion
}

// benchmarkLegRun is the leg's second invocation: the run invocation decides
// the row outcome and, on a scored row, carries the report-derived
// publications.
func benchmarkLegRun(
	t *testing.T, module benchmarkModule, moduleDir, cacheDir string, leg *benchmarkLeg,
) {
	t.Helper()

	run := censusRun(t, moduleDir)
	run.TestDirectory = censusTestDirectory(module.TestRoot)
	run.Env = legEnvironment(cacheDir)
	run.Jobs = benchmarkJobs
	run.WorkDir = t.TempDir()

	runStarted := time.Now()

	runResult, runErr := engine.Run(t.Context(), run)
	row := classifyRow(runErr, runResult)
	if row == rowOperational {
		leg.Retries++
		leg.OperationalReason = censusReason(runErr)
		run.WorkDir = t.TempDir()

		runResult, runErr = engine.Run(t.Context(), run)
		row = classifyRow(runErr, runResult)
	}

	leg.Row = string(row)
	leg.RunMS = time.Since(runStarted).Milliseconds()

	if runErr == nil {
		leg.TerraformVersion = runResult.TerraformVersion
		leg.Metrics = &runResult.Metrics
		leg.PseudoTested = countPseudoTested(runResult)
		leg.States, leg.Verdicts = legVerdicts(t, runResult)
	}
}

// legEnvironment points Terraform at this leg's plugin cache only; the safety
// environment variables the product gates on are deliberately absent — the
// benchmark never bypasses a gate.
func legEnvironment(cacheDir string) []string {
	return []string{
		checkpointDisabled,
		inAutomation,
		"TF_PLUGIN_CACHE_DIR=" + cacheDir,
	}
}

// classifyPreviewFailure maps a failed preview invocation onto the row
// vocabulary: a preview error carries the same stage sentinels a run error
// does, so classifyRow's fail-closed order decides refusal against
// operational, and only the operational class earns a retry.
func classifyPreviewFailure(previewErr error) censusRow {
	return classifyRow(previewErr, report.Report{})
}

// canonicalLegVerdict renders a verdict for the leg's identity map, byte for
// byte — the M3b invariance comparison's own rendering.
func canonicalLegVerdict(t *testing.T, verdict *report.Verdict) string {
	t.Helper()

	if verdict == nil {
		return ""
	}

	encoded, err := json.Marshal(verdict)
	if err != nil {
		t.Fatalf("encoding a verdict for the identity map: %v", err)
	}

	return string(encoded)
}

// legVerdicts builds the scored leg's per-mutant identity maps: state by
// identifier, canonical verdict by identifier.
func legVerdicts(
	t *testing.T, runResult report.Report,
) (states, verdicts map[string]string) {
	t.Helper()

	states = make(map[string]string, len(runResult.Mutants))
	verdicts = make(map[string]string, len(runResult.Mutants))

	for _, mutant := range runResult.Mutants {
		states[mutant.ID] = string(mutant.State)
		verdicts[mutant.ID] = canonicalLegVerdict(t, mutant.Verdict)
	}

	return states, verdicts
}

// countPseudoTested counts the report's pseudo-tested findings — the
// headline finding kind, one per resource address.
func countPseudoTested(runResult report.Report) int {
	count := 0

	for _, finding := range runResult.Findings {
		if finding.Kind == report.PseudoTested {
			count++
		}
	}

	return count
}

// measuredTerraformVersion takes the protocol's Terraform version from the
// first leg that observed one.
func measuredTerraformVersion(rows []benchmarkModuleResult) string {
	for _, row := range rows {
		if row.Cold.TerraformVersion != "" {
			return row.Cold.TerraformVersion
		}

		if row.Warm.TerraformVersion != "" {
			return row.Warm.TerraformVersion
		}
	}

	return ""
}

// loadBenchmarkLegs reads the side-car's completed module pairs, keyed by
// module.
func loadBenchmarkLegs(t *testing.T) map[string]benchmarkModuleResult {
	t.Helper()

	completed := map[string]benchmarkModuleResult{}

	content, err := os.ReadFile(benchmarkRows)
	if err != nil {
		return completed // no side-car yet: nothing has completed.
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(content)), "\n") {
		if line == "" {
			continue
		}

		var pair benchmarkModuleResult
		if err := json.Unmarshal([]byte(line), &pair); err != nil {
			t.Fatalf("decoding a completed benchmark pair: %v", err)
		}

		completed[pair.Module] = pair
	}

	return completed
}

// recordBenchmarkLeg appends one module's finished pair to the side-car.
func recordBenchmarkLeg(t *testing.T, pair benchmarkModuleResult) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(benchmarkRows), 0o750); err != nil {
		t.Fatalf("creating the measurement directory: %v", err)
	}

	encoded, err := json.Marshal(pair)
	if err != nil {
		t.Fatalf("encoding the benchmark pair: %v", err)
	}

	file, err := os.OpenFile(benchmarkRows, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening the benchmark side-car: %v", err)
	}

	defer func() { _ = file.Close() }()

	if _, err := file.Write(append(encoded, '\n')); err != nil {
		t.Fatalf("appending the benchmark pair: %v", err)
	}
}

// summariseBenchmark keeps the module table's counts consistent with the
// rows; the pinned count is the manifest's, not the rows' — a row the run
// could not decide is still a pinned module.
func summariseBenchmark(t *testing.T, measurement *benchmarkMeasurement, pinned int) {
	t.Helper()

	table := aggregateModuleAdmission(pinned, measurement.Rows)
	measurement.ModuleAdmission = table
	measurement.MutantLevel = aggregateMutantLevel(table.Pinned, measurement.Rows)
}

// publishBenchmark writes the published measurement.
func publishBenchmark(t *testing.T, measurement benchmarkMeasurement) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(benchmarkOutput), 0o750); err != nil {
		t.Fatalf("creating the measurement directory: %v", err)
	}

	encoded, err := json.MarshalIndent(measurement, "", "  ")
	if err != nil {
		t.Fatalf("encoding the measurement: %v", err)
	}

	if err := os.WriteFile(benchmarkOutput, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing the measurement: %v", err)
	}
}

// repositoryRootValue is repositoryRoot's value form for goroutines that
// cannot Fatal on the caller's t.
func repositoryRootValue(t *testing.T) string {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	return root
}

// measureHardware reads the machine the wall clock was measured on, so the
// document can name it and another machine can judge the transferability of
// the timings. Every figure comes from /proc on Linux; elsewhere the fields
// stay empty rather than guessed.
func measureHardware(t *testing.T) benchmarkHardware {
	t.Helper()

	hardware := benchmarkHardware{LogicalCores: runtime.NumCPU()}
	hardware.CPUModel = procField("/proc/cpuinfo", "model name")

	if memory := procField("/proc/meminfo", "MemTotal"); memory != "" {
		if kib, err := strconv.ParseInt(memory, 10, 64); err == nil {
			hardware.MemoryKiB = kib
		}
	}

	hardware.Kernel = procField("/proc/sys/kernel/osrelease", "")
	if strings.Contains(strings.ToLower(hardware.Kernel), "microsoft") {
		hardware.Environment = "WSL2"
	}

	return hardware
}

// procField reads a /proc file and returns the value after the first line's
// prefix, colon stripped — "model name : Intel..." yields "Intel...".
func procField(path, prefix string) string {
	content, err := os.ReadFile(path) //nolint:gosec // a fixed kernel path.
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(content), "\n") {
		if value, ok := strings.CutPrefix(line, prefix); ok {
			value = strings.TrimSpace(value)
			value = strings.TrimPrefix(value, ":")
			value = strings.TrimSpace(value)
			if fields := strings.Fields(value); len(fields) >= 1 {
				return fields[0]
			}
		}
	}

	return ""
}
