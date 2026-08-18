package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
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

// cacheFormatVersion invalidates every entry when the verdict shape or the
// key derivation changes.
const cacheFormatVersion = "tf-mut-cache-1"

// The cache is private by construction: evidence may embed plan values and
// source text.
const (
	cacheDirMode   = 0o700
	cacheEntryMode = 0o600
)

// CacheMaxEntries is the deterministic size cap. Beyond it, stale-key entries
// are evicted first, then current-key entries, each in name order.
const CacheMaxEntries = 8192

// keyPrefixLength is the number of key characters an entry name carries, so
// stale keys are recognisable without opening the entry.
const keyPrefixLength = 16

// verdictCache is one run's connection to the project-local cache.
type verdictCache struct {
	dir string
	key string
}

// cacheEntry is the persisted verdict of one mutant under one key.
type cacheEntry struct {
	FormatVersion string              `json:"format_version"`
	RunKey        string              `json:"run_key"`
	MutantID      string              `json:"mutant_id"`
	State         report.State        `json:"state"`
	Verdict       *report.Verdict     `json:"verdict,omitempty"`
	Runs          []report.RunOutcome `json:"runs"`
	Diagnostics   []report.Diagnostic `json:"diagnostics,omitempty"`
	ExecutedRuns  int                 `json:"executed_runs"`
	Validated     bool                `json:"validated"`
}

// openCache computes the run key and opens the project-local cache. It
// returns nil — no reads, no writes — under either unsafe opt-in, under
// --no-cache, or where the cache location cannot be used safely.
func openCache(
	configuration discovery.Configuration,
	settings Config,
	prepared warm,
	terraform tfexec.Version,
) *verdictCache {
	if settings.NoCache || settings.AllowRealInfrastructure || settings.AllowUnsandboxedEffects {
		return nil
	}

	key, err := cacheKey(configuration, settings, prepared, terraform)
	if err != nil {
		return nil
	}

	dir := filepath.Join(configuration.ModuleDir, sandbox.CacheDirName)

	// No symlink traversal: the location must be a real directory or absent.
	// A pre-existing directory with looser permissions is corrected to the
	// mandatory 0700 — evidence may embed plan values and source text — and
	// where it cannot be corrected the cache is not used at all.
	if !ensureCacheDir(dir) {
		return nil
	}

	return &verdictCache{dir: dir, key: key}
}

// ensureCacheDir makes the cache location a private real directory.
func ensureCacheDir(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil {
		return os.MkdirAll(dir, cacheDirMode) == nil
	}

	if !info.Mode().IsDir() {
		return false
	}

	if info.Mode().Perm() == cacheDirMode {
		return true
	}

	return os.Chmod(dir, cacheDirMode) == nil
}

// cacheKey hashes every input that can reach a verdict.
func cacheKey(
	configuration discovery.Configuration,
	settings Config,
	prepared warm,
	terraform tfexec.Version,
) (string, error) {
	digest := sha256.New()
	write := func(kind, name, value string) {
		_, _ = fmt.Fprintf(digest, "%s\x00%s\x00%s\x00", kind, name, value)
	}

	write("format", "", cacheFormatVersion)
	write("terraform", settings.TerraformBinary, terraform.Terraform)
	write("platform", "", terraform.Platform)
	write("baseline", "", baselineFingerprint(prepared))
	write("config", "", resolvedConfiguration(settings))

	if err := writeClosure(write, configuration, settings, prepared, prepared.sources, nil); err != nil {
		return "", err
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
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
	settings Config,
	prepared warm,
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
		discovery.Options{SkipJSON: settings.DisableJSONReading})
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

	return hex.EncodeToString(digest.Sum(nil)), nil
}

// writeClosure hashes the whole input closure into a digest.
func writeClosure(
	write func(kind, name, value string),
	configuration discovery.Configuration,
	settings Config,
	prepared warm,
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

	// The relevant environment: everything Terraform-shaped, from the
	// caller's environment and the run's own additions.
	for _, entry := range relevantEnvironment(settings) {
		write("env", "", entry)
	}

	return nil
}

// resolvedConfiguration serialises the settings that can change a verdict.
// Jobs is deliberately absent: verdicts are proven independent of
// parallelism, and a cache keyed on it would miss for no reason.
func resolvedConfiguration(settings Config) string {
	return fmt.Sprintf("%s|%v|%v|%v|%s|%v|%v|%v|%v|%v",
		settings.TestDirectory, settings.TimeoutFactor, settings.TimeoutFloor,
		settings.AllowIncompleteScore, settings.Tier,
		settings.IncludeOperators, settings.ExcludeOperators,
		settings.ExcludePaths, settings.ExcludeResources, settings.TestSelection)
}

// relevantEnvironment is the whole effective environment — the process's
// plus the run's additions, sorted. Any provider can read any variable, so
// no allowlist can be sound (re-review of #48): "any doubt is a miss" makes
// the whole environment the key, and over-inclusion costs only misses.
func relevantEnvironment(settings Config) []string {
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

// entryName is the entry file for a mutant under this key: a key prefix so
// stale keys are recognisable, and a digest of key and mutant so no input
// ever becomes a path segment — the grammar permits no traversal.
func (c *verdictCache) entryName(mutantID string) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s|%s", c.key, mutantID))

	return c.key[:keyPrefixLength] + "-" + hex.EncodeToString(digest[:])[:32] + ".json"
}

// load replays cached verdicts onto the selected population, marking each
// replayed mutant and returning the number of hits. Everything about the
// mutant except the verdict — site, range, diff — stays as the current tree
// describes it.
func (c *verdictCache) load(mutants []report.Mutant) int {
	unlock, err := c.lock()
	if err != nil {
		return 0
	}

	defer unlock()

	hits := 0

	for index := range mutants {
		if mutants[index].State != report.Pending {
			continue
		}

		entry, found := c.read(mutants[index].ID)
		if !found {
			continue
		}

		mutants[index].State = entry.State
		mutants[index].Verdict = entry.Verdict
		mutants[index].Runs = entry.Runs
		mutants[index].Diagnostics = entry.Diagnostics
		mutants[index].ExecutedRuns = entry.ExecutedRuns
		mutants[index].Validated = entry.Validated

		if mutants[index].Provenance != nil {
			mutants[index].Provenance.Execution = report.ExecutionCached
			mutants[index].Provenance.CacheKey = c.key[:keyPrefixLength]
		}

		hits++
	}

	return hits
}

// read opens one entry, treating every irregularity — a symlink, a parse
// failure, a format or key mismatch — as a miss.
func (c *verdictCache) read(mutantID string) (cacheEntry, bool) {
	path := filepath.Join(c.dir, c.entryName(mutantID))

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return cacheEntry{}, false
	}

	content, err := os.ReadFile(path) //nolint:gosec // cache-owned digest-named path.
	if err != nil {
		return cacheEntry{}, false
	}

	entry := cacheEntry{} //nolint:exhaustruct // decoded from disk.
	if json.Unmarshal(content, &entry) != nil {
		return cacheEntry{}, false
	}

	if entry.FormatVersion != cacheFormatVersion || entry.RunKey != c.key || entry.MutantID != mutantID {
		return cacheEntry{}, false
	}

	return entry, true
}

// store persists the fresh executed verdicts, then evicts deterministically.
// A Timeout is never stored: a budget overrun is not a fact about the module.
func (c *verdictCache) store(mutants []report.Mutant) {
	unlock, err := c.lock()
	if err != nil {
		return
	}

	defer unlock()

	for _, mutant := range mutants {
		if !cacheable(mutant) {
			continue
		}

		entry := cacheEntry{
			FormatVersion: cacheFormatVersion,
			RunKey:        c.key,
			MutantID:      mutant.ID,
			State:         mutant.State,
			Verdict:       mutant.Verdict,
			Runs:          mutant.Runs,
			Diagnostics:   mutant.Diagnostics,
			ExecutedRuns:  mutant.ExecutedRuns,
			Validated:     mutant.Validated,
		}

		c.write(mutant.ID, entry)
	}

	c.evict()
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

// write persists one entry atomically: a temporary file in the same
// directory, then a rename.
func (c *verdictCache) write(mutantID string, entry cacheEntry) {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return
	}

	temporary, err := os.CreateTemp(c.dir, "write-*")
	if err != nil {
		return
	}

	name := temporary.Name()

	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		_ = os.Remove(name)

		return
	}

	if err := temporary.Chmod(cacheEntryMode); err != nil {
		_ = temporary.Close()
		_ = os.Remove(name)

		return
	}

	if err := temporary.Close(); err != nil {
		_ = os.Remove(name)

		return
	}

	if err := os.Rename(name, filepath.Join(c.dir, c.entryName(mutantID))); err != nil {
		_ = os.Remove(name)
	}
}

// evict enforces the deterministic size cap: stale-key entries leave first,
// then current-key entries, each in name order.
func (c *verdictCache) evict() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	stale := []string{}
	current := []string{}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || entry.IsDir() {
			continue
		}

		if strings.HasPrefix(name, c.key[:keyPrefixLength]+"-") {
			current = append(current, name)
		} else {
			stale = append(stale, name)
		}
	}

	slices.Sort(stale)
	slices.Sort(current)

	excess := len(stale) + len(current) - CacheMaxEntries

	for _, name := range append(stale, current...) {
		if excess <= 0 {
			break
		}

		_ = os.Remove(filepath.Join(c.dir, name))
		excess--
	}
}

// lock takes the cache's advisory lock, serialising concurrent invocations.
func (c *verdictCache) lock() (func(), error) {
	file, err := os.OpenFile(filepath.Join(c.dir, "lock"), os.O_CREATE|os.O_RDWR, cacheEntryMode)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()

		return nil, err
	}

	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func sortedKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
