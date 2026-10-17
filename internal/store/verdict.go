package store

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
	"time"

	"github.com/andrewesweet/tf-mut/internal/oracle"
)

// The cache is private by construction: evidence may embed plan values and
// source text.
const (
	cacheDirMode   = 0o700
	cacheEntryMode = 0o600
)

// KeyPrefixLength is the number of key characters an entry name carries, so
// stale keys are recognisable without opening the entry.
const KeyPrefixLength = 16

// manifestSuffix names the per-generation sidecar: the key prefix so a
// stale generation is recognisable, and a fixed suffix no entry name can
// carry — entry names pair the prefix with hex.
const manifestSuffix = "-manifest.json"

// CacheEntry is the persisted verdict of one mutant under one key: the
// Oracle context's own record — the document oracle.ParseRecord rebuilds
// the outcome from — and, beside it, what the run itself observed as an
// opaque document. The record is the whole verdict; the observation document
// is the publication's own business, encoded and decoded by the caller and
// never opened here, so the storage schema stays the domain's stored-record
// shape rather than the publication DTO.
type CacheEntry struct {
	FormatVersion string        `json:"format_version"`
	RunKey        string        `json:"run_key"`
	MutantID      string        `json:"mutant_id"`
	Record        oracle.Record `json:"record"`

	// Observed is the caller's own document for what the run observed —
	// the runs, their diagnostics, the execution counts — carried as stored
	// and never verdict content, so replay projects the record once, forward,
	// through the same projection a fresh verdict went through. It is
	// marshalled as stored, never opened here; a document that does not
	// parse is the caller's miss.
	Observed json.RawMessage `json:"observed"`
}

// CacheManifest is one generation's key beside its environment digest and
// the environment as a map of variable name to the hash of its value set.
// Values never reach this document — neither
// the whole-environment lines the key hashes, nor any single value — so a
// reader learns which names differ and nothing else.
type CacheManifest struct {
	FormatVersion string `json:"format_version"`
	RunKey        string `json:"run_key"`
	// Components carries only the environment component's hex digest.
	Components map[string]string `json:"components"`
	// Env maps each environment variable name to the hex digest of its value set.
	Env map[string]string `json:"env"`
}

// VerdictCache is the filesystem verdict cache. The engine computes the run
// key, decides which entries may replay and which verdicts may be stored,
// and names the format versions and the size cap at construction; the store
// says what is on disk and treats every irregularity on read as a miss.
type VerdictCache struct {
	dir                   string
	entryFormatVersion    string
	manifestFormatVersion string
	maxEntries            int
}

// NewVerdictCache opens the cache directory and returns the store for it.
// The location must be a real directory or absent — no symlink traversal —
// and a pre-existing directory with looser permissions is corrected to the
// mandatory 0700 — evidence may embed plan values and source text. An error
// reports a location that could not be secured, and the caller proceeds
// without the cache.
func NewVerdictCache(
	dir, entryFormatVersion, manifestFormatVersion string,
	maxEntries int,
) (*VerdictCache, error) {
	if !ensureCacheDir(dir) {
		return nil, errCacheUnsecuredError{dir}
	}

	return &VerdictCache{
		dir:                   dir,
		entryFormatVersion:    entryFormatVersion,
		manifestFormatVersion: manifestFormatVersion,
		maxEntries:            maxEntries,
	}, nil
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

// Get returns the stored entry for a mutant under a run key, treating every
// irregularity — a symlink, a parse failure, a format or key mismatch — as
// a miss.
func (c *VerdictCache) Get(runKey, mutantID string) (CacheEntry, bool) {
	path := filepath.Join(c.dir, entryName(runKey, mutantID))

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return CacheEntry{}, false
	}

	content, err := os.ReadFile(path) //nolint:gosec // cache-owned digest-named path.
	if err != nil {
		return CacheEntry{}, false
	}

	entry := CacheEntry{} // decoded from disk.
	if json.Unmarshal(content, &entry) != nil {
		return CacheEntry{}, false
	}

	if entry.FormatVersion != c.entryFormatVersion || entry.RunKey != runKey ||
		entry.MutantID != mutantID {
		return CacheEntry{}, false
	}

	return entry, true
}

// Put persists one entry atomically: a temporary file in the same
// directory, then a rename.
func (c *VerdictCache) Put(runKey, mutantID string, entry CacheEntry) {
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

	if err := os.Rename(name, filepath.Join(c.dir, entryName(runKey, mutantID))); err != nil {
		_ = os.Remove(name)
	}
}

// Evict enforces the deterministic size cap the store was built with: stale
// keys leave first, then the key named, each in name order.
func (c *VerdictCache) Evict(runKey string) {
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

		if strings.HasPrefix(name, runKey[:KeyPrefixLength]+"-") {
			current = append(current, name)
		} else {
			stale = append(stale, name)
		}
	}

	slices.Sort(stale)
	slices.Sort(current)

	excess := len(stale) + len(current) - c.maxEntries

	for _, name := range append(stale, current...) {
		if excess <= 0 {
			break
		}

		_ = os.Remove(filepath.Join(c.dir, name))
		excess--
	}
}

// WriteManifest persists a generation's key beside its meaning — the
// environment digest and the per-variable environment map, values never
// included — so a later run can name environment changes behind a miss.
// Best effort like every cache write: a lost manifest costs only the
// explanation, never a verdict.
func (c *VerdictCache) WriteManifest(runKey string, manifest CacheManifest) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return
	}

	temporary, err := os.CreateTemp(c.dir, "manifest-*")
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

	if err := os.Rename(name, filepath.Join(c.dir, manifestName(runKey))); err != nil {
		_ = os.Remove(name)
	}
}

// StoredManifests returns the stored generation manifests, newest first, as
// the miss explanation reads them. A directory that cannot be read, an
// unreadable sidecar, or one that is not a manifest of the store's format
// claims nothing.
func (c *VerdictCache) StoredManifests() []CacheManifest {
	type generation struct {
		modified time.Time
		name     string
		manifest CacheManifest
	}

	generations := []generation{}

	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), manifestSuffix) {
			continue
		}

		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}

		content, readErr := os.ReadFile(filepath.Join(c.dir, entry.Name()))
		if readErr != nil {
			continue
		}

		stored := CacheManifest{}
		if json.Unmarshal(content, &stored) != nil ||
			!c.validCacheManifest(stored, entry.Name()) {
			continue
		}

		generations = append(generations, generation{
			modified: info.ModTime(),
			name:     entry.Name(),
			manifest: stored,
		})
	}

	slices.SortFunc(generations, func(a, b generation) int {
		if !a.modified.Equal(b.modified) {
			return b.modified.Compare(a.modified)
		}

		return strings.Compare(a.name, b.name)
	})

	manifests := make([]CacheManifest, len(generations))
	for index, generation := range generations {
		manifests[index] = generation.manifest
	}

	return manifests
}

// Lock takes the cache's advisory lock, serialising concurrent invocations.
// Callers hold it across a batched read or a batched write; the store's own
// methods take no lock.
func (c *VerdictCache) Lock() (func(), error) {
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

// manifestName is a generation's sidecar: the key prefix so stale
// generations are recognisable, beside the entries it explains.
func manifestName(runKey string) string {
	return runKey[:KeyPrefixLength] + manifestSuffix
}

// entryName is the entry file for a mutant under a run key: a key prefix so
// stale keys are recognisable, and a digest of key and mutant so no input
// ever becomes a path segment — the grammar permits no traversal.
func entryName(runKey, mutantID string) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s|%s", runKey, mutantID))

	return runKey[:KeyPrefixLength] + "-" + hex.EncodeToString(digest[:])[:32] + ".json"
}

func (c *VerdictCache) validCacheManifest(manifest CacheManifest, name string) bool {
	if manifest.FormatVersion != c.manifestFormatVersion || !validCacheDigest(manifest.RunKey) {
		return false
	}

	if name != manifest.RunKey[:KeyPrefixLength]+manifestSuffix || manifest.Env == nil ||
		len(manifest.Components) != 1 || !validCacheDigest(manifest.Components[envComponent]) {
		return false
	}

	if (len(manifest.Env) == 0) != (manifest.Components[envComponent] == hashHex(nil)) {
		return false
	}

	for variable, digest := range manifest.Env {
		if variable == "" || strings.ContainsAny(variable, "=\x00") || !validCacheDigest(digest) {
			return false
		}
	}

	return true
}

func validCacheDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}

	decoded, err := hex.DecodeString(digest)

	return err == nil && hex.EncodeToString(decoded) == digest
}

// hashHex digests bytes to the hex form the manifest validation compares.
// It mirrors the engine key policy's own digest helper, which stays beside
// the key it serves.
func hashHex(content []byte) string {
	digest := sha256.Sum256(content)

	return hex.EncodeToString(digest[:])
}

// envComponent is the key-component kind the whole effective environment
// hashes under. The manifest names it so a miss explanation can diff the
// environment per variable.
const envComponent = "env"

// errCacheUnsecuredError reports a cache location that could not be secured
// as a private real directory. The caller proceeds without the cache.
type errCacheUnsecuredError struct {
	dir string
}

func (e errCacheUnsecuredError) Error() string {
	return fmt.Sprintf("the cache directory %s could not be secured", e.dir)
}
