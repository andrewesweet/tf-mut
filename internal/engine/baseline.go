package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// warm is the one initialised workspace every mutant sandbox borrows from.
//
// It exists because Terraform's init writes into its working directory and the
// source tree is never written to: the warm workspace absorbs those writes, and
// its provider tree and module payloads are then shared read-only.
type warm struct {
	moduleDir        string
	dataDir          string
	lockFile         string
	schemas          tfexec.Schemas
	baselineRuns     int
	baselineDuration time.Duration
	warnings         []string
}

func prepare(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	config Config,
	workRoot string,
) (warm, error) {
	built, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: configuration.ClosureRoot,
		ModuleRel:  configuration.RootRelative(),
		Target:     filepath.Join(workRoot, "warm"),
		Mutations:  nil,
		Share:      nil,
		Hardlink:   false,
	})
	if err != nil {
		return warm{}, err
	}

	prepared := warm{
		moduleDir: built.ModuleDir,
		dataDir:   filepath.Join(built.ModuleDir, sandbox.DataDirName),
		warnings:  []string{},
	}

	_, hasLock := configuration.LockFilePath()
	if initErr := runner.Init(ctx, prepared.moduleDir, hasLock); initErr != nil {
		return warm{}, initErr
	}

	if path := filepath.Join(prepared.moduleDir, sandbox.LockFileName); fileExists(path) {
		prepared.lockFile = path
	}

	schemas, err := runner.ProvidersSchema(ctx, prepared.moduleDir)
	if err != nil {
		return warm{}, err
	}

	prepared.schemas = schemas

	if unformatted, err := runner.FmtCheck(ctx, prepared.moduleDir); err == nil && len(unformatted) > 0 {
		prepared.warnings = append(prepared.warnings, fmt.Sprintf(
			"%d file(s) are not canonically formatted; mutant diffs may span more than one line: %s",
			len(unformatted), strings.Join(unformatted, ", "),
		))
	}

	// A preview executes nothing: it needs the schemas that gate the deletion
	// operators, and no more. The workspace still exists because init cannot be
	// allowed to write into the source tree.
	if config.Preview {
		return prepared, nil
	}

	if err := runBaseline(ctx, runner, configuration, &prepared); err != nil {
		return warm{}, err
	}

	return prepared, nil
}

// runBaseline proves the suite is green before any mutant is trusted, and times
// it for timeout calibration.
func runBaseline(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	prepared *warm,
) error {
	result, err := runner.Test(ctx, prepared.moduleDir, tfexec.TestOptions{
		TestDirectory: configuration.TestDirRelative(),
		Filters:       nil,
		Timeout:       0,
	})
	if err != nil {
		return err
	}

	prepared.baselineRuns = result.ExecutedRuns()
	prepared.baselineDuration = result.Duration

	if failures := result.FailedRuns(); len(failures) > 0 {
		return fmt.Errorf("%w: %s\n  Fix the suite before trusting any mutation result",
			ErrBaselineRed, describeFailures(failures, result.Diagnostics))
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("%w: terraform test exited %d\n%s",
			ErrBaselineRed, result.ExitCode, describeDiagnostics(result.Diagnostics))
	}

	if prepared.baselineRuns == 0 {
		return fmt.Errorf("%w: %s executed no run blocks, so every mutant would look survived",
			ErrBaselineNoRuns, configuration.TestDirRelative())
	}

	return nil
}

func describeFailures(failures []tfexec.RunOutcome, diagnostics []tfexec.Diagnostic) string {
	lines := make([]string, 0, len(failures))

	for _, failure := range failures {
		lines = append(lines, fmt.Sprintf("\n  %s: run %q %s", failure.File, failure.Run, failure.Status))
	}

	if detail := describeDiagnostics(diagnostics); detail != "" {
		lines = append(lines, "\n"+detail)
	}

	return strings.Join(lines, "")
}

func describeDiagnostics(diagnostics []tfexec.Diagnostic) string {
	lines := []string{}

	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != "error" {
			continue
		}

		lines = append(lines, "  "+diagnostic.Summary+": "+firstLine(diagnostic.Detail))
	}

	return strings.Join(lines, "\n")
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")

	return line
}
