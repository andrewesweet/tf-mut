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
	terraformVersion string,
) *verdictCache {
	if settings.NoCache || settings.AllowRealInfrastructure || settings.AllowUnsandboxedEffects {
		return nil
	}

	key, err := cacheKey(configuration, settings, prepared, terraformVersion)
	if err != nil {
		return nil
	}

	dir := filepath.Join(configuration.ModuleDir, sandbox.CacheDirName)

	// No symlink traversal: the location must be a real directory or absent.
	if info, statErr := os.Lstat(dir); statErr == nil {
		if !info.Mode().IsDir() {
			return nil
		}
	} else if mkErr := os.MkdirAll(dir, cacheDirMode); mkErr != nil {
		return nil
	}

	return &verdictCache{dir: dir, key: key}
}

// cacheKey hashes every input that can reach a verdict.
func cacheKey(
	configuration discovery.Configuration,
	settings Config,
	prepared warm,
	terraformVersion string,
) (string, error) {
	digest := sha256.New()
	write := func(kind, name, value string) {
		_, _ = fmt.Fprintf(digest, "%s\x00%s\x00%s\x00", kind, name, value)
	}

	write("format", "", cacheFormatVersion)
	write("terraform", settings.TerraformBinary, terraformVersion)
	write("baseline", "", baselineFingerprint(prepared))
	write("config", "", resolvedConfiguration(settings))

	// The entire materialised source closure, in path order.
	for _, path := range sortedKeys(prepared.sources) {
		rel, err := filepath.Rel(configuration.ClosureRoot, path)
		if err != nil {
			return "", err
		}

		write("source", filepath.ToSlash(rel), hashBytes(prepared.sources[path]))
	}

	// Every test file.
	for _, path := range configuration.Tests.Files {
		content, err := os.ReadFile(path) //nolint:gosec // discovery-owned path.
		if err != nil {
			return "", err
		}

		write("test", filepath.Base(path), hashBytes(content))
	}

	// The dependency lock, where one exists.
	if prepared.lockFile != "" {
		content, err := os.ReadFile(prepared.lockFile)
		if err != nil {
			return "", err
		}

		write("lock", "", hashBytes(content))
	}

	// The module inventory with remote payloads: everything Terraform
	// materialised under the warm workspace's modules directory.
	if err := hashTree(write, filepath.Join(prepared.dataDir, "modules")); err != nil {
		return "", err
	}

	// The relevant environment: everything Terraform-shaped, from the
	// caller's environment and the run's own additions.
	for _, entry := range relevantEnvironment(settings) {
		write("env", "", entry)
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
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

// relevantEnvironment lists the sorted TF_-prefixed entries from the process
// environment and the run's additions — TF_VAR_, TF_CLI_ and the rest of the
// Terraform surface. Over-inclusion costs a miss; exclusion could cost a lie.
func relevantEnvironment(settings Config) []string {
	entries := []string{}

	for _, entry := range append(os.Environ(), settings.Env...) {
		if strings.HasPrefix(entry, "TF_") {
			entries = append(entries, entry)
		}
	}

	slices.Sort(entries)

	return slices.Compact(entries)
}

func hashBytes(content []byte) string {
	digest := sha256.Sum256(content)

	return hex.EncodeToString(digest[:])
}

// hashTree hashes every regular file under root, in path order, without
// following symlinks. An absent root hashes nothing.
func hashTree(write func(kind, name, value string), root string) error {
	paths := []string{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
