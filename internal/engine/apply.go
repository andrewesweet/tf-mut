package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// The apply protocol: the never-write contract's third recorded exception, with
// the runtime contract the M4 spec review's C6 demanded.
//
// Verification proves something about a specific sequence of bytes. Writing
// without binding the write to those bytes would let an editor change between
// the two invalidate the proof silently, would let a symlinked test path
// redirect the writer outside the module, and would leave an unexplained
// half-applied tree behind a failure. So: every suggestion carries the digest of
// what was verified, every target is preflighted before the first write, any
// mismatch aborts with zero writes, and each write is temp-plus-atomic-rename
// with the file's mode preserved.

// ErrApply reports an apply the protocol refused or could not complete.
var ErrApply = errors.New("suggestions were not applied")

// applySuggestions performs the whole protocol and records what it did.
func applySuggestions(settings Config, result *report.Report, configuration applyContext) {
	if len(settings.Apply) == 0 && !settings.ApplyAll {
		return
	}

	record := &report.AppliedSuggestions{
		Requested: slices.Clone(settings.Apply), Written: []string{},
		Pending: []string{}, Aborted: "", Partial: false,
	}
	result.Apply = record

	selected, err := selectForApply(settings, *result)
	if err != nil {
		record.Aborted = err.Error()

		return
	}

	record.Requested = identifiers(selected)

	planned, err := preflight(configuration, selected)
	if err != nil {
		record.Aborted = err.Error()

		return
	}

	write(record, planned)
}

// applyContext is the little the protocol needs to know about the module.
type applyContext struct {
	moduleDir   string
	closureRoot string
	testDirs    []string
}

// selectForApply resolves the caller's selection and refuses anything that is
// not a verified suggestion.
func selectForApply(settings Config, result report.Report) ([]report.Suggestion, error) {
	if settings.ApplyAll {
		verified := []report.Suggestion{}

		for _, suggestion := range result.Suggestions {
			if suggestion.Status == report.SuggestionVerified {
				verified = append(verified, suggestion)
			}
		}

		return verified, nil
	}

	selected := []report.Suggestion{}
	refused := []string{}

	for _, id := range settings.Apply {
		suggestion, found := result.SuggestionByID(id)

		switch {
		case !found:
			refused = append(refused, id+" (no such suggestion in this report)")
		case suggestion.Status != report.SuggestionVerified:
			refused = append(refused, id+" ("+string(suggestion.Status)+", and only a verified "+
				"suggestion may be applied)")
		default:
			selected = append(selected, suggestion)
		}
	}

	if len(refused) > 0 {
		slices.Sort(refused)

		return nil, fmt.Errorf("%w: %s", ErrApply, strings.Join(refused, "; "))
	}

	return selected, nil
}

// plannedWrite is one target file with every selected suggestion for it
// already applied in memory.
type plannedWrite struct {
	path    string
	rel     string
	mode    os.FileMode
	content []byte
}

// preflight resolves and checks every target before a single byte is written.
// Any refusal here aborts the whole invocation with zero writes.
func preflight(configuration applyContext, selected []report.Suggestion) ([]plannedWrite, error) {
	byFile := map[string][]report.Suggestion{}
	for _, suggestion := range selected {
		byFile[suggestion.TargetFile] = append(byFile[suggestion.TargetFile], suggestion)
	}

	planned := []plannedWrite{}

	for _, file := range sortedTargets(byFile) {
		prepared, err := preflightFile(configuration, file, byFile[file])
		if err != nil {
			return nil, err
		}

		planned = append(planned, prepared)
	}

	return planned, nil
}

func preflightFile(
	configuration applyContext,
	file string,
	suggestions []report.Suggestion,
) (plannedWrite, error) {
	empty := plannedWrite{path: "", rel: file, mode: 0, content: nil}
	path := suggest.TargetPath(configuration.moduleDir, file)

	if strings.HasSuffix(file, discovery.JSONTestSuffix) {
		return empty, fmt.Errorf("%w: %s is a JSON test file, which this tool never writes", ErrApply, file)
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return empty, fmt.Errorf("%w: %s could not be resolved: %w", ErrApply, file, err)
	}

	if resolved != filepath.Clean(path) {
		return empty, fmt.Errorf("%w: %s resolves through a symbolic link to %s, and the "+
			"protocol writes only to a real file inside the module", ErrApply, file, resolved)
	}

	if !within(configuration.closureRoot, resolved) || !withinAny(configuration.testDirs, resolved) {
		return empty, fmt.Errorf("%w: %s lies outside the module's closure or its test roots",
			ErrApply, file)
	}

	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return empty, fmt.Errorf("%w: %s is not a regular file", ErrApply, file)
	}

	current, err := os.ReadFile(resolved)
	if err != nil {
		return empty, fmt.Errorf("%w: reading %s: %w", ErrApply, file, err)
	}

	if _, diagnostics := hclwrite.ParseConfig(current, file, hcl.InitialPos); diagnostics.HasErrors() {
		return empty, fmt.Errorf("%w: %s no longer parses: %s", ErrApply, file, diagnostics.Error())
	}

	digest := suggest.Digest(current)
	for _, suggestion := range suggestions {
		if suggestion.VerifiedDigest != digest {
			return empty, fmt.Errorf("%w: %s changed since suggestion %s was verified "+
				"(verified %s, found %s); re-run before applying, because the proof is about "+
				"the bytes that were verified and not about these",
				ErrApply, file, suggestion.ID, short(suggestion.VerifiedDigest), short(digest))
		}
	}

	content := current

	for _, suggestion := range suggestions {
		content, err = suggest.Apply(content, file, suggestion.TargetRun,
			suggestion.Expression, appliedMessage(suggestion))
		if err != nil {
			return empty, fmt.Errorf("%w: %w", ErrApply, err)
		}
	}

	return plannedWrite{path: resolved, rel: file, mode: info.Mode().Perm(), content: content}, nil
}

// write performs the atomic writes and records exactly what happened, so a
// failure part-way through leaves a state the reader can recover from rather
// than a mystery.
func write(record *report.AppliedSuggestions, planned []plannedWrite) {
	for index, target := range planned {
		if err := atomicWrite(target); err != nil {
			record.Aborted = err.Error()
			record.Partial = len(record.Written) > 0

			for _, pending := range planned[index:] {
				record.Pending = append(record.Pending, pending.rel)
			}

			return
		}

		record.Written = append(record.Written, target.rel)
	}
}

// atomicWrite writes through a temporary file in the same directory and renames
// it over the target, so a reader never sees a half-written test file.
func atomicWrite(target plannedWrite) error {
	directory := filepath.Dir(target.path)

	temporary, err := os.CreateTemp(directory, ".tf-mut-apply-*")
	if err != nil {
		return fmt.Errorf("%w: creating a temporary file beside %s: %w", ErrApply, target.rel, err)
	}

	name := temporary.Name()

	defer func() { _ = os.Remove(name) }()

	if _, err := temporary.Write(target.content); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("%w: writing %s: %w", ErrApply, target.rel, err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: closing the temporary file for %s: %w", ErrApply, target.rel, err)
	}

	if err := os.Chmod(name, target.mode); err != nil {
		return fmt.Errorf("%w: preserving the mode of %s: %w", ErrApply, target.rel, err)
	}

	if err := os.Rename(name, target.path); err != nil {
		return fmt.Errorf("%w: replacing %s: %w", ErrApply, target.rel, err)
	}

	return nil
}

// appliedMessage is the verification renderer, deliberately: the bytes
// written must be the bytes verified.
func appliedMessage(suggestion report.Suggestion) string {
	return suggest.VerifiedMessage(suggestion.ID, suggestion.MutantID)
}

func identifiers(suggestions []report.Suggestion) []string {
	ids := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		ids = append(ids, suggestion.ID)
	}

	slices.Sort(ids)

	return ids
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)

	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func withinAny(roots []string, path string) bool {
	for _, root := range roots {
		if within(root, path) {
			return true
		}
	}

	return false
}

// short renders a digest at the width a reader can compare by eye.
func short(digest string) string {
	if len(digest) <= shortDigestLength {
		return digest
	}

	return digest[:shortDigestLength]
}

const shortDigestLength = 12
