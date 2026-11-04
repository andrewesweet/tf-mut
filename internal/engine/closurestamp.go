package engine

// The verdict cache promises that a key describes the inputs of its verdicts.
// The promise has a hole the key arithmetic cannot close by itself: the key
// is taken once, and the sandboxes read the live closure root afterwards, so
// an edit landing between key time and persist files the changed tree's
// verdicts under the unchanged tree's key — and a later run on the restored
// tree replays them. These stamps close the hole at the persist end: what
// the sandboxes take is recorded at key time and re-checked immediately
// before anything is stored.

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

// closureStamp is one file's change stamp: the filesystem identity a write
// cannot avoid moving. Size and mtime catch a content edit, inode and device
// a replacement, and ctime — which moves on every write, including the undo
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

// stampClosureTree records a stamp for every entry of the closure root the
// mutant sandboxes take: every non-directory entry the walk yields, and the
// directories themselves, whose stamps move when a sibling is added,
// renamed or removed without any recorded file's moving.
func stampClosureTree(root string) (map[string]closureStamp, error) {
	stamps := map[string]closureStamp{}
	if err := stampDereference(root, root, stamps); err != nil {
		return nil, fmt.Errorf("stamping the input closure: %w", err)
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

		if info.Mode()&os.ModeSymlink != 0 {
			return stampDereference(root, path, stamps)
		}

		return nil
	}

	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, fmt.Errorf("stamping the input closure: %w", err)
	}

	return stamps, nil
}

func stampDereference(root, path string, stamps map[string]closureStamp) error {
	const maxSymlinkHops = 255

	current := string(filepath.Separator)
	pending := strings.Split(path, string(filepath.Separator))
	links := 0

	for {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		stamp, recorded := stampOf(info)
		if !recorded {
			return fmt.Errorf("%w: %s", errUnstampableEntry, current)
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if _, recorded := stamps[key]; !recorded {
			stamps[key] = stamp
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
