package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/store"
)

// The verdict cache stores the Oracle context's own record beside the run's
// observations, names entries by key prefix and digest, and treats every
// irregularity on read — a wrong key, a wrong mutant, a foreign format, a
// corrupt document, a missing file — as the miss the caller recovers from.
const (
	testEntryFormat    = "test-cache-1"
	testManifestFormat = "test-cache-1-env-2"

	mutA = "MUT-A"
	mutB = "MUT-B"
	mutC = "MUT-C"

	// observedDoc is an opaque observation document, as the engine would
	// encode it: the runs as executed beside their execution counts.
	observedDoc = `{"runs":[{"run":"root"}],"executed_runs":1}`
)

// errNoEntry is the truncation helper's report that no stored entry carries
// the mutant ID named.
var errNoEntry = errors.New("no stored entry for the mutant ID")

// TestTheVerdictStoreRoundTripsEntriesAndTreatsEveryIrregularityAsAMiss is
// the store's own contract: a put entry comes back through get under its key
// and mutant, and every wrong question is a miss rather than an error.
func TestTheVerdictStoreRoundTripsEntriesAndTreatsEveryIrregularityAsAMiss(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cache, err := store.NewVerdictCache(dir, testEntryFormat, testManifestFormat, 16)
	if err != nil {
		t.Fatalf("NewVerdictCache: %v", err)
	}

	key := digest64("the-run-key")
	other := digest64("another-run-key")
	stored := store.CacheEntry{
		FormatVersion: testEntryFormat,
		RunKey:        key,
		MutantID:      mutA,
		Record:        oracle.Record{State: oracle.StateKilled},
		Observed:      json.RawMessage(observedDoc),
	}

	cache.Put(key, mutA, stored)

	entry, found := cache.Get(key, mutA)
	if !found {
		t.Fatal("the entry just stored was a miss")
	}

	if entry.MutantID != mutA || entry.RunKey != key || entry.FormatVersion != testEntryFormat {
		t.Fatalf("the round trip moved the entry: %+v", entry)
	}

	if string(entry.Observed) != observedDoc {
		t.Fatalf("the round trip moved the stored observations: %s", entry.Observed)
	}

	if _, err := oracle.ParseRecord(entry.Record); err != nil {
		t.Fatalf("the stored record does not parse: %v", err)
	}

	if _, found := cache.Get(key, "MUT-0002"); found {
		t.Fatal("another mutant under the same key hit")
	}

	if _, found := cache.Get(other, mutA); found {
		t.Fatal("the same mutant under another key hit")
	}

	if _, found := cache.Get(digest64("no-such-key"), mutA); found {
		t.Fatal("an absent key hit")
	}

	// A foreign format version is a miss: an older-format entry is never an
	// error, it is recomputed.
	stale := stored
	stale.FormatVersion, stale.MutantID = "test-cache-0", "MUT-STALE"
	cache.Put(key, "MUT-STALE", stale)
	if _, found := cache.Get(key, "MUT-STALE"); found {
		t.Fatal("an entry under a foreign format version hit")
	}

	// Corruption is a miss, not an error: the entry file on disk is
	// truncated behind the store's back.
	corrupt := stored
	corrupt.MutantID = "MUT-CORRUPT"
	cache.Put(key, "MUT-CORRUPT", corrupt)
	if err := truncateEntry(t, dir, "MUT-CORRUPT"); err != nil {
		t.Fatalf("truncating the entry: %v", err)
	}

	if _, found := cache.Get(key, "MUT-CORRUPT"); found {
		t.Fatal("a corrupt entry hit")
	}
}

// TestTheVerdictStoreEvictsStaleKeysBeforeTheCurrentKeyInNameOrder pins the
// deterministic size cap: beyond it, entries under stale keys leave first,
// then entries under the key named, each in name order.
func TestTheVerdictStoreEvictsStaleKeysBeforeTheCurrentKeyInNameOrder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	const maxEntries = 4
	cache, err := store.NewVerdictCache(dir, testEntryFormat, testManifestFormat, maxEntries)
	if err != nil {
		t.Fatalf("NewVerdictCache: %v", err)
	}

	staleKey := digest64("the-stale-generation")
	currentKey := digest64("the-current-generation")
	entry := store.CacheEntry{
		FormatVersion: testEntryFormat,
		Observed:      json.RawMessage(observedDoc),
	}

	for _, key := range []string{staleKey, currentKey} {
		for _, mutant := range []string{mutA, mutB, mutC} {
			one := entry
			one.RunKey, one.MutantID = key, mutant
			cache.Put(key, mutant, one)
		}
	}

	// The names on disk decide the order, so the pin does not recompute the
	// naming grammar: stale names leave smallest first, and only then the
	// current key's own.
	stalePrefix, currentPrefix := staleKey[:16], currentKey[:16]
	before := namesByPrefix(t, dir, stalePrefix, currentPrefix)
	if len(before.stale) != 3 || len(before.current) != 3 {
		t.Fatalf("the fixture did not stage six entries: %d stale, %d current",
			len(before.stale), len(before.current))
	}

	cache.Evict(currentKey)

	for _, mutant := range []string{mutA, mutB, mutC} {
		if _, found := cache.Get(currentKey, mutant); !found {
			t.Fatalf("eviction removed a current-key entry: %s", mutant)
		}
	}

	remaining := namesByPrefix(t, dir, stalePrefix, currentPrefix)
	if len(remaining.stale) != 1 {
		t.Fatalf("eviction left %d stale entries, want exactly one", len(remaining.stale))
	}

	// The two stale names smallest in name order are the ones that left.
	sorted := append([]string{}, before.stale...)
	slices.Sort(sorted)
	want := sorted[len(sorted)-1]
	if remaining.stale[0] != want {
		t.Fatalf("the surviving stale entry is %s, want the name-order largest %s",
			remaining.stale[0], want)
	}

	if len(remaining.current) != len(before.current) {
		t.Fatal("eviction removed current-key entries while stale ones remained")
	}

	if files := countEntryFiles(t, dir); files != maxEntries {
		t.Fatalf("the directory holds %d entries after eviction, the cap is %d", files, maxEntries)
	}
}

// TestTheVerdictStoreSecuresItsDirectory pins the location contract: a
// symlinked location is refused rather than traversed, and a pre-existing
// directory with looser permissions is corrected to owner-only.
func TestTheVerdictStoreSecuresItsDirectory(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "cache")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlinking: %v", err)
	}

	if _, err := store.NewVerdictCache(link, testEntryFormat, testManifestFormat, 16); err == nil {
		t.Fatal("a symlinked cache location was accepted")
	}

	loose := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(loose, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := store.NewVerdictCache(loose, testEntryFormat, testManifestFormat, 16); err != nil {
		t.Fatalf("a loose directory was refused rather than corrected: %v", err)
	}

	info, err := os.Stat(loose)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if info.Mode().Perm() != 0o700 {
		t.Fatalf("the loose directory was left at %o", info.Mode().Perm())
	}
}

// TestTheVerdictStoreExplainsMissesFromStoredManifests pins the generation
// sidecar: the manifest written under a key is read back newest first, and a
// document that is not a manifest of the store's format claims nothing.
func TestTheVerdictStoreExplainsMissesFromStoredManifests(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cache, err := store.NewVerdictCache(dir, testEntryFormat, testManifestFormat, 16)
	if err != nil {
		t.Fatalf("NewVerdictCache: %v", err)
	}

	key := digest64("the-run-key")
	cache.WriteManifest(key, store.CacheManifest{
		FormatVersion: testManifestFormat,
		RunKey:        key,
		Components:    map[string]string{"env": digest64("environment-values")},
		Env:           map[string]string{"TF_VAR_size": digest64("small")},
	})

	manifests := cache.StoredManifests()
	if len(manifests) != 1 {
		t.Fatalf("StoredManifests returned %d manifests, want 1", len(manifests))
	}

	if manifests[0].RunKey != key || manifests[0].Env["TF_VAR_size"] != digest64("small") {
		t.Fatalf("the manifest round trip moved the document: %+v", manifests[0])
	}

	// A sidecar that does not parse claims nothing.
	junk := filepath.Join(dir, "0000000000000000-manifest.json")
	if err := os.WriteFile(junk, []byte("not json"), 0o600); err != nil {
		t.Fatalf("writing junk: %v", err)
	}

	if got := len(cache.StoredManifests()); got != 1 {
		t.Fatalf("StoredManifests counted junk as a generation: %d", got)
	}
}

// TestTheVerdictStoreSerialisesConcurrentBatchesThroughItsLock asserts the
// advisory lock is takeable and releasable, twice in a row, so concurrent
// invocations serialise their batched reads and writes.
func TestTheVerdictStoreSerialisesConcurrentBatchesThroughItsLock(t *testing.T) {
	t.Parallel()

	cache, err := store.NewVerdictCache(t.TempDir(), testEntryFormat, testManifestFormat, 16)
	if err != nil {
		t.Fatalf("NewVerdictCache: %v", err)
	}

	for range 2 {
		unlock, err := cache.Lock()
		if err != nil {
			t.Fatalf("Lock: %v", err)
		}

		unlock()
	}
}

// digest64 is a realistic run key: full hex, longer than the entry prefix.
func digest64(seed string) string {
	digest := sha256.Sum256([]byte(seed))

	return hex.EncodeToString(digest[:])
}

// truncateEntry corrupts the one entry file whose stored mutant ID is named,
// behind the store's back.
func truncateEntry(t *testing.T, dir, mutantID string) error {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, "*-*.json"))
	if err != nil {
		return err
	}

	for _, path := range matches {
		content, err := os.ReadFile(path) //nolint:gosec // a test-owned, digest-named path.
		if err != nil {
			return err
		}

		entry := store.CacheEntry{}
		if json.Unmarshal(content, &entry) == nil && entry.MutantID == mutantID {
			return os.WriteFile(path, []byte("{not json"), 0o600)
		}
	}

	return errNoEntry
}

// entryNames are the stored entry documents under two key prefixes.
type entryNames struct {
	stale   []string
	current []string
}

// namesByPrefix classifies the entry files on disk by their key prefix.
func namesByPrefix(t *testing.T, dir, stalePrefix, currentPrefix string) entryNames {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the cache directory: %v", err)
	}

	names := entryNames{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}

		switch {
		case strings.HasPrefix(name, stalePrefix):
			names.stale = append(names.stale, name)
		case strings.HasPrefix(name, currentPrefix):
			names.current = append(names.current, name)
		default:
			// Neither prefix: not staged by this test.
		}
	}

	return names
}

// countEntryFiles counts the stored entry documents, manifests aside.
func countEntryFiles(t *testing.T, dir string) int {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the cache directory: %v", err)
	}

	files := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			files++
		}
	}

	return files
}
