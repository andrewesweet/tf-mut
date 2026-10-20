package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/store"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// M3b.2 (#48): the incremental cache — coarse before clever, per the C4
// disposition: the key is a hash of the entire materialised source closure,
// all tests, the resolved configuration, the lock and module inventory with
// remote payloads included, the relevant environment, the Terraform identity,
// the cache format version and the masked baseline fingerprint. Any doubt is
// a miss. Source ranges and metadata are rehydrated from the current tree,
// never trusted from cache; only the verdict travels.
//
// Cached evidence may embed plan values and source text. Sharing a cache
// across repositories — or through a CI restore key — is a documented risk,
// and --no-cache is the mitigation.

// cacheFormatVersion invalidates every entry when verdict semantics, the
// stored shape, or key derivation changes. Sandbox changes that affect
// verdicts also require a bump. TestPreSandboxFixInvalidVerdictsAreNotReplayed
// protects this upgrade boundary. An older-format entry is a miss, not an
// error. Selected verdicts are recomputed under the current execution and
// oracle rules (TestAnEntryUnderThePreviousFormatVersionIsAMissNotAnError).
// The tf-mut-cache-7 bump moves the run's own observations out of the
// storage schema's typed fields and into the opaque observed document the
// engine alone decodes, so the store's document is the domain's
// stored-record shape and no publication type.
// The tf-mut-cache-8 bump follows the child environment gaining its
// performance defaults — GOGC, GOMEMLIMIT and TF_LOG_SDK, set by
// internal/tfexec for every Terraform invocation. A stored verdict was
// computed under the previous child environment, so it replays as a miss
// and is re-executed, not trusted across the change.
// The tf-mut-cache-9 bump follows the single-phase execution: every mutant
// is now judged from one verbose invocation, where the stored verdict's run
// rows and child invocations record the two-phase schedule the engine no
// longer follows. It replays as a miss and is re-executed, not trusted
// across the change.
const cacheFormatVersion = "tf-mut-cache-10"

const cacheManifestFormatVersion = cacheFormatVersion + "-env-2"

// CacheMaxEntries is the deterministic size cap. Beyond it, stale-key entries
// are evicted first, then current-key entries, each in name order. The
// filesystem store is built with this cap; the policy — that eviction is
// deterministic and stale keys leave first — is the store's contract.
const CacheMaxEntries = 8192

// VerdictStore is the verdict cache's persistence boundary. The engine owns
// the run key and every dimension it hashes, decides which entries may
// replay and which verdicts may be stored, and judges what a stored entry
// means; the store owns the disk. Every irregularity on read is the absence
// the caller recovers from — a miss, never an error.
type VerdictStore interface {
	// Get returns the stored entry for a mutant under a run key.
	Get(runKey, mutantID string) (store.CacheEntry, bool)
	// Put persists one entry under a run key.
	Put(runKey, mutantID string, entry store.CacheEntry)
	// Evict enforces the store's size cap: stale keys leave first, then the
	// key named, each in name order.
	Evict(runKey string)
	// WriteManifest persists a generation's miss-explanation sidecar.
	WriteManifest(runKey string, manifest store.CacheManifest)
	// StoredManifests returns the stored generation manifests, newest first.
	StoredManifests() []store.CacheManifest
	// Lock serialises a batched read or write across concurrent invocations.
	// The store's own methods take no lock; callers hold one across a batch.
	Lock() (func(), error)
}

// verdictCache is one run's connection to the project-local cache: the run
// key the engine computed, the manifest that explains it, and the store the
// wiring selected. Everything here judges stored entries; everything on
// disk happens behind the store.
type verdictCache struct {
	store VerdictStore
	key   string
	// manifest is this run's key beside its environment digest and
	// per-variable environment map. The store writes it next to
	// the entries; a later run whose key misses diffs it to explain an
	// environment change behind a miss.
	manifest store.CacheManifest
}

// openCache computes the run key and opens the project-local cache. It
// returns nil — no reads, no writes — under either unsafe opt-in, under
// --no-cache, or where the cache location cannot be used safely.
func openCache(
	configuration discovery.Configuration,
	settings settings, prepared warmWorkspace,
	terraform tfexec.Version,
) *verdictCache {
	if settings.NoCache || settings.AllowRealInfrastructure || settings.AllowUnsandboxedEffects {
		return nil
	}

	key, manifest, err := cacheKey(configuration, settings, prepared, terraform)
	if err != nil {
		return nil
	}

	dir := filepath.Join(configuration.ModuleDir, sandbox.CacheDirName)

	// No symlink traversal: the location must be a real directory or absent.
	// A pre-existing directory with looser permissions is corrected to the
	// mandatory 0700 — evidence may embed plan values and source text — and
	// where it cannot be secured the cache is not used at all.
	verdicts, err := settings.verdictStore(dir)
	if err != nil {
		return nil
	}

	return &verdictCache{store: verdicts, key: key, manifest: manifest}
}

// envComponent is the key-component kind the whole effective environment
// hashes under. The miss explanation diffs it per variable, even when other
// components also differ.
const envComponent = "env"

// keyRecorder hashes the key's (kind, name, value) sequence into the
// overall digest and, beside it, into the environment digest. The overall
// byte sequence is unchanged — what hits still hits — and the environment
// digest lets a later run tell whether the environment moved. Beside both,
// it hashes each kind's own sequence into that kind's component digest, so
// a miss can name the component that changed (D17). Only digests ever reach
// the debug log — values, environment values above all, never do.
type keyRecorder struct {
	overall     hash.Hash
	environment hash.Hash
	components  map[string]hash.Hash
}

func (r *keyRecorder) write(kind, name, value string) {
	_, _ = fmt.Fprintf(r.overall, "%s\x00%s\x00%s\x00", kind, name, value)

	if kind == envComponent {
		_, _ = fmt.Fprintf(r.environment, "%s\x00%s\x00%s\x00", kind, name, value)
	}

	component, found := r.components[kind]
	if !found {
		component = sha256.New()
		r.components[kind] = component
	}

	_, _ = fmt.Fprintf(component, "%s\x00%s\x00%s\x00", kind, name, value)
}

// cacheKey hashes every input that can reach a verdict, and returns the key
// beside the manifest that explains it: the environment digest and the
// per-variable environment map.
func cacheKey(
	configuration discovery.Configuration,
	settings settings, prepared warmWorkspace,
	terraform tfexec.Version,
) (string, store.CacheManifest, error) {
	recorder := &keyRecorder{overall: sha256.New(), environment: sha256.New(), components: map[string]hash.Hash{}}
	write := recorder.write

	write("format", "", cacheFormatVersion)
	write("terraform", settings.TerraformBinary, terraform.Terraform)
	write("platform", "", terraform.Platform)
	write("baseline", "", baselineFingerprint(prepared))
	write("config", "", resolvedConfiguration(settings))

	if err := writeClosure(write, configuration, settings, prepared, prepared.sources, nil); err != nil {
		return "", store.CacheManifest{}, err
	}

	key := hex.EncodeToString(recorder.overall.Sum(nil))
	if settings.Debug && settings.Progress != nil {
		logger := slog.New(slog.NewTextHandler(settings.Progress, &slog.HandlerOptions{Level: slog.LevelDebug}))
		logCacheKey(logger, key, recorder.components)
	}

	return key, store.CacheManifest{
		FormatVersion: cacheManifestFormatVersion,
		RunKey:        key,
		Components:    map[string]string{envComponent: hex.EncodeToString(recorder.environment.Sum(nil))},
		Env:           envDigestMap(settings),
	}, nil
}

// logCacheKey records the run key beside each of its component digests at
// debug level, so a miss names the component that changed. Only digests
// travel: values — environment values above all — never reach the log.
func logCacheKey(logger *slog.Logger, key string, components map[string]hash.Hash) {
	for _, kind := range sortedComponentKinds(components) {
		logger.Debug("tf-mut: cache key component",
			"component", kind, "digest", hex.EncodeToString(components[kind].Sum(nil)))
	}

	logger.Debug("tf-mut: cache key", "key", key)
}

func sortedComponentKinds(components map[string]hash.Hash) []string {
	kinds := make([]string, 0, len(components))
	for kind := range components {
		kinds = append(kinds, kind)
	}

	slices.Sort(kinds)

	return kinds
}

// envDigestMap reduces the whole effective environment to a map of variable
// name to the hash of its sorted value set, including the process's values
// and the run's additions. A single value keeps its original digest.
func envDigestMap(settings settings) map[string]string {
	values := map[string][]string{}
	digests := map[string]string{}

	for _, entry := range relevantEnvironment(settings) {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = append(values[name], value)
	}

	for name, entries := range values {
		sum := sha256.Sum256([]byte(strings.Join(entries, "\x00")))
		digests[name] = hex.EncodeToString(sum[:])
	}

	return digests
}

// InputClosureDigest is the digest of everything a run reads: the sources, the
// tests, the JSON syntax, the lock, the automatically loaded variable files,
// the mock data, the materialised module inventory and the relevant
// environment.
//
// It is the cache key's composition minus the parts that describe *this* run,
// and it is what the characterisation write protocol commits against: a
// scaffold is only green for the closure that produced it, so the commit step
// re-checks this digest immediately before each rename.
// The digest deliberately excludes the paths the commit itself is placing.
// Without that, writing the first generated file would change the closure and
// the probe would refuse the second — the commit tripping over its own
// footprints. What the probe is for is a change somebody *else* made.
func InputClosureDigest(
	configuration discovery.Configuration,
	settings settings, prepared warmWorkspace,
	excluded map[string]bool,
) (string, error) {
	digest := sha256.New()
	write := func(kind, name, value string) {
		_, _ = fmt.Fprintf(digest, "%s\x00%s\x00%s\x00", kind, name, value)
	}

	// The closure is re-*discovered*, not just re-read. The finding this
	// digest answers is "sources can change after harvest while the output
	// stays identical", and a leg fed from the path list discovery captured
	// would miss the whole class of change that matters most: a `.tf`,
	// `.tftest.hcl`, `.tf.json` or `.tftest.json` file added since. Membership
	// has to be recomputed, not replayed.
	live, err := discovery.DiscoverWith(configuration.ModuleDir, settings.TestDirectory,
		discovery.Options{SkipJSON: disableJSONReading(settings)})
	if err != nil {
		return "", err
	}

	sources, err := moduleSources(live)
	if err != nil {
		return "", err
	}

	for path := range excluded {
		delete(sources, path)
	}

	if err := writeClosure(write, live, settings, prepared, sources, excluded); err != nil {
		return "", err
	}

	// The lock the *module* carries, not the warmed copy the run initialised
	// from. `prepared.lockFile` lives in the sandbox and cannot change while a
	// scaffold is being verified, so hashing it proves nothing about the tree
	// this commit is about to write into. Editing the module's own
	// `.terraform.lock.hcl` selects different provider versions, which is
	// exactly the kind of change the probe exists to refuse a stale write over.
	if path, found := live.LockFilePath(); found {
		content, err := os.ReadFile(path) //nolint:gosec // discovery-owned path.
		if err != nil {
			return "", err
		}

		write("live-lock", "", hashBytes(content))
	}

	// The resolved configuration is part of the snapshot contract: the same
	// sources under a different test selection or exclusion set are a
	// different program, and the suite was proven green under this one.
	write("config", "", resolvedConfiguration(settings))

	return hex.EncodeToString(digest.Sum(nil)), nil
}

// writeClosure hashes the whole input closure into a digest.
func writeClosure(
	write func(kind, name, value string),
	configuration discovery.Configuration,
	settings settings, prepared warmWorkspace,
	sources map[string][]byte,
	excluded map[string]bool,
) error {
	// The entire materialised source closure, in path order.
	for _, path := range sortedKeys(sources) {
		rel, err := filepath.Rel(configuration.ClosureRoot, path)
		if err != nil {
			return err
		}

		write("source", filepath.ToSlash(rel), hashBytes(sources[path]))
	}

	// Every test file.
	for _, path := range configuration.Tests.Files {
		if excluded[path] {
			continue
		}

		content, err := os.ReadFile(path) //nolint:gosec // discovery-owned path.
		if err != nil {
			return err
		}

		write("test", filepath.Base(path), hashBytes(content))
	}

	// Every JSON-syntax file in the closure, whether or not this run read it.
	// Terraform reads them regardless, so they reach verdicts; the key hashes
	// all JSON classes and no finer key is built (M4c).
	for _, file := range configuration.JSONFiles() {
		if excluded[file.Path] {
			continue
		}

		content, err := os.ReadFile(file.Path)
		if err != nil {
			return err
		}

		write("json", file.Rel, hashBytes(content))
	}

	// The selected user packs' bytes: a pack is data that decides the
	// population, so an edit to one is a miss and a stale write is refused,
	// exactly as an edit to `.tf-mut.hcl` is.
	for _, pack := range settings.loadedPacks {
		write("pack", pack.Name, pack.Digest)
	}

	// The dependency lock, where one exists.
	if prepared.lockFile != "" {
		content, err := os.ReadFile(prepared.lockFile)
		if err != nil {
			return err
		}

		write("lock", "", hashBytes(content))
	}

	// The automatically loaded variable files: Terraform reads them without
	// being asked, so they reach verdicts without appearing in the source
	// closure (review of #48).
	if err := hashAutoVarFiles(write, configuration.ModuleDir); err != nil {
		return err
	}

	// Mock-data files (round-3 review, PR #69): a mock_provider's source
	// directory of .tfmock.hcl/.tfmock.json files decides the values a mocked
	// run sees, so an edit there changes verdicts — and nothing else hashes
	// them, because they are in no inventory and no test-file list.
	if err := hashMockDataFiles(write, configuration.ClosureRoot); err != nil {
		return err
	}

	// The module inventory with remote payloads: everything Terraform
	// materialised under the warm workspace's modules directory.
	if err := hashTree(write, filepath.Join(prepared.dataDir, "modules")); err != nil {
		return err
	}

	// See relevantEnvironment for the environment included in the key.
	for _, entry := range relevantEnvironment(settings) {
		write(envComponent, "", entry)
	}

	return nil
}

// resolvedConfiguration serialises the settings that can change a verdict.
// Jobs is deliberately absent: verdicts are proven independent of
// parallelism, and a cache keyed on it would miss for no reason.
func resolvedConfiguration(settings settings) string {
	return fmt.Sprintf("%s|%v|%v|%v|%s|%v|%v|%v|%v|%v|%v",
		settings.TestDirectory, settings.TimeoutFactor, settings.TimeoutFloor,
		settings.AllowIncompleteScore, settings.Tier,
		settings.IncludeOperators, settings.ExcludeOperators,
		settings.ExcludePaths, settings.ExcludeResources, settings.TestSelection,
		settings.Packs)
}

// relevantEnvironment is the whole effective environment — the process's
// plus the run's additions, sorted. Any provider can read any variable, so
// no allowlist can be sound (re-review of #48): "any doubt is a miss" makes
// the whole environment the key, and over-inclusion costs only misses.
func relevantEnvironment(settings settings) []string {
	entries := append(append([]string{}, os.Environ()...), settings.Env...)

	slices.Sort(entries)

	return slices.Compact(entries)
}

// hashAutoVarFiles hashes terraform.tfvars and every *.auto.tfvars — HCL and
// JSON variants both — in name order.
func hashAutoVarFiles(write func(kind, name, value string), moduleDir string) error {
	paths := []string{}

	for _, pattern := range []string{
		"terraform.tfvars", "terraform.tfvars.json", "*.auto.tfvars", "*.auto.tfvars.json",
	} {
		matches, err := filepath.Glob(filepath.Join(moduleDir, pattern))
		if err != nil {
			return err
		}

		paths = append(paths, matches...)
	}

	slices.Sort(paths)

	for _, path := range paths {
		content, err := os.ReadFile(path) //nolint:gosec // module-owned variable file.
		if err != nil {
			return err
		}

		write("auto-var", filepath.Base(path), hashBytes(content))
	}

	return nil
}

// hashMockDataFiles hashes every mock-data file under the closure, in path
// order, whatever directory its mock_provider points at.
func hashMockDataFiles(write func(kind, name, value string), closureRoot string) error {
	paths := []string{}

	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		if strings.HasSuffix(path, ".tfmock.hcl") || strings.HasSuffix(path, ".tfmock.json") {
			paths = append(paths, path)
		}

		return nil
	}

	if err := filepath.WalkDir(closureRoot, walk); err != nil {
		return fmt.Errorf("walking %s for mock data: %w", closureRoot, err)
	}

	slices.Sort(paths)

	for _, path := range paths {
		content, err := os.ReadFile(path) //nolint:gosec // a closure-owned path.
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(closureRoot, path)
		if err != nil {
			return err
		}

		write("mock-data", filepath.ToSlash(rel), hashBytes(content))
	}

	return nil
}

func hashBytes(content []byte) string {
	digest := sha256.Sum256(content)

	return hex.EncodeToString(digest[:])
}

// hashTree hashes every regular file under root, in path order, without
// following symlinks. An absent root hashes nothing. Git metadata inside a
// downloaded module is skipped: the payload Terraform evaluates is the
// checked-out working tree, and .git carries clone-time clocks that would
// churn the key on every run.
func hashTree(write func(kind, name, value string), root string) error {
	paths := []string{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}

		if info.Mode().IsRegular() {
			paths = append(paths, path)
		}

		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	slices.Sort(paths)

	for _, path := range paths {
		content, readErr := os.ReadFile(path) //nolint:gosec // warm-workspace path.
		if readErr != nil {
			return readErr
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		write("module-payload", filepath.ToSlash(rel), hashBytes(content))
	}

	return nil
}

// explainMiss compares the newest eligible stored generation against this run,
// and reports the environment names that changed, even when other key
// components also differ. No stored generation, an unchanged environment,
// or a generation this binary predates is a miss without an explanation.
// Values reach neither the document nor the report: only their names travel.
func (c *verdictCache) explainMiss(mutants []report.Mutant) *report.CacheMiss {
	if !slices.ContainsFunc(mutants, func(mutant report.Mutant) bool {
		return mutant.State == report.Pending
	}) {
		return nil
	}

	unlock, err := c.store.Lock()
	if err != nil {
		return nil
	}

	defer unlock()

	for _, previous := range c.store.StoredManifests() {
		if previous.RunKey == c.key {
			return nil
		}

		if !c.hasUsableEntry(previous.RunKey, mutants) {
			continue
		}

		if c.manifest.Components[envComponent] == previous.Components[envComponent] {
			return nil
		}

		return diffEnvNames(previous.Env, c.manifest.Env)
	}

	return nil
}

func (c *verdictCache) hasUsableEntry(runKey string, mutants []report.Mutant) bool {
	for _, mutant := range mutants {
		if mutant.State != report.Pending {
			continue
		}

		entry, found := c.store.Get(runKey, mutant.ID)
		if !found {
			continue
		}

		candidate := mutant
		classified := oracle.Classified{}
		if rehydrated(&candidate, &classified, entry) && cacheable(candidate) {
			return true
		}
	}

	return false
}

// diffEnvNames reduces two per-variable digest maps to the sorted, bounded
// name lists the report carries: added, removed and changed. An empty diff —
// a comparison with no changed variable — is no explanation.
// Values are compared only as digests and never leave this
// function.
func diffEnvNames(stored, current map[string]string) *report.CacheMiss {
	miss := &report.CacheMiss{}

	for name := range current {
		previous, found := stored[name]
		if !found {
			miss.Added = append(miss.Added, name)
		} else if previous != current[name] {
			miss.Changed = append(miss.Changed, name)
		}
	}

	for name := range stored {
		if _, found := current[name]; !found {
			miss.Removed = append(miss.Removed, name)
		}
	}

	if len(miss.Added)+len(miss.Removed)+len(miss.Changed) == 0 {
		return nil
	}

	slices.Sort(miss.Added)
	slices.Sort(miss.Removed)
	slices.Sort(miss.Changed)

	miss.Truncated = truncateEnvNames(miss)

	return miss
}

// truncateEnvNames caps the three lists at the report bound, filling added
// before removed before changed, and reports whether any name was cut.
func truncateEnvNames(miss *report.CacheMiss) bool {
	remaining := report.MaxCacheMissNames
	truncated := false

	for _, list := range []*[]string{&miss.Added, &miss.Removed, &miss.Changed} {
		if len(*list) <= remaining {
			remaining -= len(*list)

			continue
		}

		*list = (*list)[:remaining]
		remaining = 0
		truncated = true
	}

	return truncated
}

// load replays cached verdicts onto the selected population, marking each
// replayed mutant and returning the number of hits. Everything about the
// mutant except the verdict — site, range, diff — stays as the current tree
// describes it; the verdict itself is rebuilt through the Oracle context, so
// an entry whose document no constructor could have produced is a miss like
// any other corruption. Each replayed outcome is classified onto the run's
// population fact, row for row with the published mutants.
func (c *verdictCache) load(mutants []report.Mutant, classified []oracle.Classified) int {
	unlock, err := c.store.Lock()
	if err != nil {
		return 0
	}

	defer unlock()

	hits := 0

	for index := range mutants {
		if mutants[index].State != report.Pending {
			continue
		}

		entry, found := c.store.Get(c.key, mutants[index].ID)
		if !found {
			continue
		}

		if !rehydrated(&mutants[index], &classified[index], entry) {
			continue
		}

		if mutants[index].Provenance != nil {
			mutants[index].Provenance.Execution = report.ExecutionCached
			mutants[index].Provenance.CacheKey = c.key[:store.KeyPrefixLength]
		}

		hits++
	}

	return hits
}

// rehydrated replays one stored entry onto its mutant and reports whether
// the entry was replayed; false is a miss, and the mutant executes again.
//
// The verdict travels through the Oracle context in one direction only: the
// stored record — what this context wrote — is rebuilt by oracle.ParseRecord,
// the parsing constructor, which refuses any document the constructors could
// not have produced, a Killed entry carrying a diagnosis being the canonical
// case. The rebuilt outcome is then projected once, forward, through the same
// projection a fresh verdict went through, and only then does the replayed
// verdict reach a reader. Nothing is lifted back out of published DTOs. The
// runs, diagnostics and execution counts are the run's own observations,
// rehydrated as stored; where the record itself carries the diagnostics as
// evidence, the projection below is their only writer and the stored
// observation stands down. The outcome is classified onto the population
// fact beside the published projection.
func rehydrated(mutant *report.Mutant, classified *oracle.Classified, entry store.CacheEntry) bool {
	outcome, err := oracle.ParseRecord(entry.Record)
	if err != nil {
		return false
	}

	observed := observedRun{}
	if err := json.Unmarshal(entry.Observed, &observed); err != nil {
		return false
	}

	if len(observed.Diagnostics) > 0 {
		mutant.Diagnostics = slices.Clone(observed.Diagnostics)
	}

	mutant.Runs = observed.Runs
	mutant.ExecutedRuns = observed.ExecutedRuns
	mutant.Validated = observed.Validated
	mutant.DeadlineReached = observed.DeadlineReached

	*mutant = project(*mutant, outcome)
	*classified = oracle.Classified{Operator: classified.Operator, Outcome: outcome}

	return true
}

// observedRun is the run's own observation document, stored beside the
// verdict's record and never part of it: the runs as executed, their
// diagnostics where the record does not carry them as evidence, and the
// execution counts. It is the publication-facing half of a cache entry, and
// the store carries it as stored without opening it.
type observedRun struct {
	Runs []report.RunOutcome `json:"runs"`
	// Diagnostics are the runs' own observations. Where the outcome carries
	// its diagnostics as evidence — a KilledByError or Invalid claim — these
	// stand down, because the record is their only carrier and the replay's
	// projection must have one writer per field.
	Diagnostics     []report.Diagnostic `json:"diagnostics,omitempty"`
	ExecutedRuns    int                 `json:"executed_runs"`
	Validated       bool                `json:"validated"`
	DeadlineReached bool                `json:"deadline_reached"`
}

// persist stores the fresh executed verdicts, then evicts deterministically.
// A Timeout is never stored: a budget overrun is not a fact about the module.
// Each verdict is stored as the Oracle context's own record, taken from the
// classified outcome — never from the published projection — so what the
// cache holds is what the context stored, and ParseRecord can rebuild it
// exactly. Where the outcome carries its diagnostics as evidence, the run's
// own observations stand down: the record is their only carrier, and replay
// must have one writer per field.
func (c *verdictCache) persist(mutants []report.Mutant, classified []oracle.Classified) {
	unlock, err := c.store.Lock()
	if err != nil {
		return
	}

	defer unlock()

	for index, mutant := range mutants {
		if !cacheable(mutant) {
			continue
		}

		outcome := classified[index].Outcome

		diagnostics := mutant.Diagnostics
		if len(outcome.Diagnostics()) > 0 {
			diagnostics = nil
		}

		observed, err := json.Marshal(observedRun{
			Runs:            mutant.Runs,
			Diagnostics:     diagnostics,
			ExecutedRuns:    mutant.ExecutedRuns,
			Validated:       mutant.Validated,
			DeadlineReached: mutant.DeadlineReached,
		})
		if err != nil {
			continue
		}

		entry := store.CacheEntry{
			FormatVersion: cacheFormatVersion,
			RunKey:        c.key,
			MutantID:      mutant.ID,
			Record:        outcome.Record(),
			Observed:      observed,
		}

		c.store.Put(c.key, mutant.ID, entry)
	}

	c.store.WriteManifest(c.key, c.manifest)
	c.store.Evict(c.key)
}

// cacheable admits verdicts that executed and cannot drift: a Timeout may be
// luck, and the statically classified states are cheaper to recompute than
// to read.
func cacheable(mutant report.Mutant) bool {
	if len(mutant.Runs) == 0 && !mutant.Validated {
		return false
	}

	if mutant.Provenance != nil && mutant.Provenance.Execution == report.ExecutionCached {
		return false
	}

	//nolint:exhaustive // everything else is uncacheable by default.
	switch mutant.State {
	case report.Killed, report.KilledByError, report.Survived,
		report.StructurallyUnassertable, report.Unobservable, report.Invalid:
		return true
	default:
		return false
	}
}

func sortedKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
