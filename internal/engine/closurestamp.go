package engine

// The verdict cache promises that a key describes the inputs of its verdicts.
// The promise has a hole the key arithmetic cannot close by itself: the key
// is taken once, and the sandboxes read the live closure root afterwards, so
// an edit landing between key time and persist files the changed tree's
// verdicts under the unchanged tree's key — and a later run on the restored
// tree replays them. Two witnesses guard this window. The persist stamps
// cover the closure root and everything below it except skippedClosureNames,
// plus resolution-path components outside it only where they are symlinks or
// the resolved target. They are re-checked before replay and immediately
// before persist. The resolution chain records the device and inode of every
// component the root's path and symlinked entries resolve through. Each
// mutant's evaluation samples it before materialisation; persist re-checks it.
// An ancestor swap detected by any sample refuses persistence even if the
// original is restored before persist. Unrelated sibling churn in shared
// parents does not change their identities.
//
// Two accepted residuals leave both witnesses clean: a same-inode edit and
// restore preserving size and every recorded timestamp, including ctime; and
// an ancestor swap staged and fully restored between consecutive per-mutant
// samples without moving persist stamps. Either can persist verdicts under
// the original key after a sandbox consumed different bytes.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/andrewesweet/tf-mut/internal/sandbox"
)

// closureStamp is one entry's change stamp: metadata ordinary writes move.
// Size and mtime catch a content edit, inode and device a replacement, and
// ctime — which moves on ordinary writes, including the undo
// that would restore identical bytes and defeat a content re-check — the
// edit-and-restore the key-time-to-persist window is widest for.
type closureStamp struct {
	device uint64
	inode  uint64
	size   int64
	mtime  time.Time
	ctime  time.Time
}

func (s closureStamp) differs(current closureStamp) bool {
	return s.device != current.device || s.inode != current.inode ||
		s.size != current.size || !s.mtime.Equal(current.mtime) || !s.ctime.Equal(current.ctime)
}

// skippedClosureNames is the stamp walk's skip set: the names the mutant
// sandboxes never copy, whatever their type — Terraform's
// per-working-directory data directory, git metadata, and tf-mut's own
// verdict cache. It mirrors the sandbox walk that materialises each mutant's
// copy. Stamping one entry more than the sandboxes take is the safe
// direction: it can refuse a cache write that would have been sound, and it
// can never file verdicts under a key that does not describe them.
//
//nolint:gochecknoglobals // an immutable lookup table.
var skippedClosureNames = map[string]bool{
	sandbox.DataDirName:  true,
	".git":               true,
	sandbox.CacheDirName: true,
}

// chainStamp is one resolution-path component's identity: device and inode
// alone. Content and times are the persist stamps' business; the chain
// witness answers exactly one question — is the entry now at this path
// the entry that was there at key time?
type chainStamp struct {
	device uint64
	inode  uint64
}

// stampClosureTree records a stamp for every entry of the closure root the
// mutant sandboxes take: every non-directory entry the walk yields, and the
// directories themselves, whose stamps move when a sibling is added,
// renamed or removed without any recorded file's moving. Components outside
// the root carry persist stamps only if they are symlinks or the resolved
// target itself, so unrelated activity in shared
// parents cannot refuse a sound cache write. Alongside the stamps it
// returns the resolution chain: device and inode for every component the
// root's path resolves through, ancestors included, for the mid-run
// sampling that catches an ancestor swap when a sample observes it.
func stampClosureTree(root string) (map[string]closureStamp, map[string]chainStamp, error) {
	stamps := map[string]closureStamp{}
	chain := map[string]chainStamp{}
	if err := stampDereference(root, root, stamps, chain); err != nil {
		return nil, nil, fmt.Errorf("stamping the input closure: %w", err)
	}

	walk := func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", path, err)
		}

		if relative != "." && skippedClosureNames[entry.Name()] {
			if entry.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		stamp, recorded := stampOf(info)
		if !recorded {
			return fmt.Errorf("%w: %s", errUnstampableEntry, path)
		}

		key := filepath.ToSlash(relative)
		if _, recorded := stamps[key]; !recorded {
			stamps[key] = stamp
		}
		recordChain(chain, root, path, info)

		if info.Mode()&os.ModeSymlink != 0 {
			return stampDereference(root, path, stamps, chain)
		}

		return nil
	}

	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, nil, fmt.Errorf("stamping the input closure: %w", err)
	}

	return stamps, chain, nil
}

// recordChain notes one component's device and inode under its path
// relative to the closure root. The first record wins: the resolution walk
// revisits shared components across symlinks, and every visit stats the
// same live component.
func recordChain(chain map[string]chainStamp, root, path string, info os.FileInfo) {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return
	}

	key := filepath.ToSlash(relative)
	if _, recorded := chain[key]; recorded {
		return
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}

	chain[key] = chainStamp{device: stat.Dev, inode: stat.Ino}
}

func stampDereference(root, path string, stamps map[string]closureStamp, chain map[string]chainStamp) error {
	const maxSymlinkHops = 255

	current := string(filepath.Separator)
	pending := strings.Split(path, string(filepath.Separator))
	links := 0

	for {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		recordChain(chain, root, current, info)
		if err := recordPersistStamp(stamps, root, current, info, len(pending) == 0); err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > maxSymlinkHops {
				return syscall.ELOOP
			}
			target, err := os.Readlink(current)
			if err != nil {
				return err
			}
			pending = append(strings.Split(target, string(filepath.Separator)), pending...)
			current = filepath.Dir(current)
			if filepath.IsAbs(target) {
				current = string(filepath.Separator)
			}
		}

		if len(pending) == 0 {
			return nil
		}
		component := pending[0]
		pending = pending[1:]
		current = filepath.Join(current, component)
	}
}

// recordPersistStamp records one resolution component's change stamp where
// the persist stamps cover it. A component outside the closure root is
// recorded only if it is a symlink, whose retargeting mid-run changes what
// the closure entries resolve to, or the
// resolved target itself, the content a mutant sandbox reads. Intermediate
// plain ancestors are pass-through — shared temporary parents, the user's
// home — and churn there from unrelated activity must not refuse a sound
// cache write. The resolution chain catches a wholesale plain-ancestor swap
// if a sample observes it. The closure root's stamp catches a replacement
// still present at re-check; restoring the original root can leave it clean.
func recordPersistStamp(
	stamps map[string]closureStamp,
	root, current string,
	info os.FileInfo,
	last bool,
) error {
	relative, err := filepath.Rel(root, current)
	if err != nil {
		return err
	}

	key := filepath.ToSlash(relative)
	above := strings.HasPrefix(key, "..")
	if above && info.Mode()&os.ModeSymlink == 0 && !last {
		return nil
	}

	stamp, recorded := stampOf(info)
	if !recorded {
		return fmt.Errorf("%w: %s", errUnstampableEntry, current)
	}

	if _, already := stamps[key]; !already {
		stamps[key] = stamp
	}

	return nil
}

// closureMoved re-checks every recorded stamp and reports the first relative
// path whose stamp no longer holds — an edited file, a replaced one, a
// vanished one, or a directory whose entry set changed. Sorted order makes
// the answer deterministic. An unreadable path is a moved stamp: any doubt
// is a miss.
func closureMoved(root string, stamps map[string]closureStamp) (string, bool) {
	paths := make([]string, 0, len(stamps))
	for path := range stamps {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))

		info, err := os.Lstat(path)
		if err != nil {
			return relative, true
		}

		stamp, recorded := stampOf(info)
		if !recorded || stamps[relative].differs(stamp) {
			return relative, true
		}
	}

	return "", false
}

// chainMoved re-checks the resolution chain and reports the first component
// whose device or inode no longer holds — the signature of a wholesale
// swap: another entry now sits at the path the closure resolves through.
// The caller retains a sampled change even if the original returns later;
// this check alone cannot detect a swap already restored. Sorted order
// makes the answer deterministic, and an unreadable path is a moved one:
// any doubt is a miss. Times and sizes are deliberately not compared —
// sibling churn in a shared parent moves a directory's metadata without
// moving its identity, and that churn is not the tree's.
func chainMoved(root string, chain map[string]chainStamp) (string, bool) {
	paths := make([]string, 0, len(chain))
	for path := range chain {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))

		info, err := os.Lstat(path)
		if err != nil {
			return relative, true
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || chain[relative].device != stat.Dev || chain[relative].inode != stat.Ino {
			return relative, true
		}
	}

	return "", false
}

// stampOf reads one entry's change stamp. Linux reports it; where the
// platform does not, the entry is unrecordable and the whole stamping fails —
// a run whose stamps cannot be taken stores nothing, which is the direction
// every cache failure takes.
func stampOf(info os.FileInfo) (closureStamp, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return closureStamp{}, false
	}

	return closureStamp{
		device: stat.Dev,
		inode:  stat.Ino,
		size:   info.Size(),
		mtime:  info.ModTime(),
		ctime:  time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec),
	}, true
}

// errUnstampableEntry reports a closure entry whose filesystem identity the
// platform does not expose. The stamping refuses the whole tree over one:
// a run whose stamps cannot be taken stores nothing, which is the direction
// every cache failure takes.
var errUnstampableEntry = errors.New("the filesystem does not report the identity of a closure entry")

func closureChangedWarning(at string) string {
	return fmt.Sprintf(
		"verdicts were not cached: the input closure changed while the run was in flight "+
			"(first change at %s), so the cache key does not describe the tree these "+
			"verdicts were earned on; the next run on this tree executes the population fresh",
		at,
	)
}
