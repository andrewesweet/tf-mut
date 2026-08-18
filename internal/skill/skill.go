// Package skill installs the agent skills that ship with the binary.
//
// The install is the never-write contract's fourth recorded exception: a write
// into the caller's tree, asked for by name, with per-agent target paths,
// atomic writes, user edits preserved unless forced, and same-version against
// cross-version behaviour defined rather than implied.
package skill

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed content/mutation-loop.md
var mutationLoop string

// The supported agent adapters. `generic` serves Cursor and every other
// framework; a dedicated cursor adapter ships only if the generic form proves
// insufficient (M4 spec review, M4 — agent-integration.md carries the same
// set).
const (
	AgentClaude  = "claude"
	AgentGeneric = "generic"
)

// ErrUnknownAgent reports an --agent value outside the documented set.
var ErrUnknownAgent = errors.New("unknown agent")

// TargetPath is the documented per-agent install location, relative to the
// --path root.
//
//   - claude:  .claude/skills/tf-mut-mutation/SKILL.md — the Claude Code
//     project-skill convention.
//   - generic: .agents/skills/tf-mut-mutation.md — a plain markdown document
//     any framework can be pointed at.
func TargetPath(agent string) (string, error) {
	switch agent {
	case AgentClaude:
		return filepath.Join(".claude", "skills", "tf-mut-mutation", "SKILL.md"), nil
	case AgentGeneric:
		return filepath.Join(".agents", "skills", "tf-mut-mutation.md"), nil
	default:
		return "", fmt.Errorf("%w: %q (want %s or %s)", ErrUnknownAgent,
			agent, AgentClaude, AgentGeneric)
	}
}

// Outcome names what the install did to the target.
type Outcome string

// The outcomes an install reports, one per file.
const (
	// OutcomeInstalled means the file did not exist and was written.
	OutcomeInstalled Outcome = "installed"
	// OutcomeUnchanged means the file already carries this binary's content:
	// a same-version reinstall is a no-op.
	OutcomeUnchanged Outcome = "unchanged"
	// OutcomeUpgraded means an unmodified install of another version was
	// replaced by this binary's content.
	OutcomeUpgraded Outcome = "upgraded"
	// OutcomePreservedEdit means the file was edited by its user and was left
	// alone; --force replaces it.
	OutcomePreservedEdit Outcome = "preserved-user-edit"
	// OutcomeForced means --force replaced a user-edited file.
	OutcomeForced Outcome = "forced"
)

// Result is one installed file's report line.
type Result struct {
	// Path is the written file, relative to the install root.
	Path string
	// Outcome is what happened to it.
	Outcome Outcome
}

// Install places the mutation-loop skill for the agent under root.
//
// The content is versioned with the binary and stamped with its own digest,
// which is how the three cases are told apart without a registry: bytes equal
// to what this binary ships is a no-op; a digest stamp that matches the file's
// own content is an unmodified install of some other version and is replaced;
// anything else is a user edit and is preserved unless forced.
func Install(root, agent, version string, force bool) (Result, error) {
	relative, err := TargetPath(agent)
	if err != nil {
		return Result{}, err //nolint:exhaustruct // nothing was installed.
	}

	target := filepath.Join(root, relative)
	shipped := stamped(mutationLoop, version)

	existing, err := os.ReadFile(target) //nolint:gosec // the caller-chosen install root.
	outcome := OutcomeInstalled

	switch {
	case err == nil && string(existing) == shipped:
		return Result{Path: relative, Outcome: OutcomeUnchanged}, nil
	case err == nil && !unmodified(string(existing)):
		if !force {
			return Result{Path: relative, Outcome: OutcomePreservedEdit}, nil
		}

		outcome = OutcomeForced
	case err == nil:
		outcome = OutcomeUpgraded
	case !errors.Is(err, os.ErrNotExist):
		return Result{}, fmt.Errorf("reading %s: %w", relative, err) //nolint:exhaustruct // nothing was installed.
	default:
		// The target does not exist: a fresh install.
	}

	if err := atomicInstall(target, shipped); err != nil {
		return Result{}, err //nolint:exhaustruct // nothing was installed.
	}

	return Result{Path: relative, Outcome: outcome}, nil
}

// The stamp the installed file carries, from which an unmodified install is
// recognised across versions.
const (
	stampPrefix = "<!-- tf-mut skill; version "
	stampMiddle = "; content sha256:"
	stampSuffix = " -->\n"
)

// stamped renders the shipped content with its version-and-digest stamp.
func stamped(content, version string) string {
	body := content

	digest := sha256.Sum256([]byte(body))

	return stampPrefix + version + stampMiddle +
		hex.EncodeToString(digest[:]) + stampSuffix + body
}

// unmodified reports whether a file's stamp still matches its own content,
// which is what "an unmodified install of some version" means.
func unmodified(content string) bool {
	rest, found := strings.CutPrefix(content, stampPrefix)
	if !found {
		return false
	}

	_, rest, found = strings.Cut(rest, stampMiddle)
	if !found {
		return false
	}

	recorded, body, found := strings.Cut(rest, stampSuffix)
	if !found {
		return false
	}

	digest := sha256.Sum256([]byte(body))

	return recorded == hex.EncodeToString(digest[:])
}

// The modes an install writes with: directories group-traversable, the skill
// itself world-readable documentation.
const (
	installDirectoryMode = 0o750
	installedFileMode    = 0o644
)

// atomicInstall writes through a temporary file in the target's directory and
// renames it into place.
func atomicInstall(target, content string) error {
	directory := filepath.Dir(target)

	if err := os.MkdirAll(directory, installDirectoryMode); err != nil {
		return fmt.Errorf("creating %s: %w", directory, err)
	}

	temporary, err := os.CreateTemp(directory, ".tf-mut-skill-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", directory, err)
	}

	name := temporary.Name()

	defer func() { _ = os.Remove(name) }()

	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("writing %s: %w", target, err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing the temporary file for %s: %w", target, err)
	}

	if err := os.Chmod(name, installedFileMode); err != nil {
		return fmt.Errorf("setting the mode of %s: %w", target, err)
	}

	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("installing %s: %w", target, err)
	}

	return nil
}

// Content returns the shipped skill body, for the suite test that asserts it
// references only commands and flags the binary has.
func Content() string {
	return mutationLoop
}
