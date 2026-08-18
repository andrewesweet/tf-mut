package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
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
	// payloads is the canonical projection of the first baseline run, which is
	// the reference every mutant fingerprint is compared against.
	payloads []fingerprint.Payload
	// mask is the volatile set: the union of the two-run baseline diff and the
	// static impure scan.
	mask fingerprint.Mask
	// scan is the static volatility evidence, kept so that the mutant re-run
	// rule can compare the mutant's own syntax against it.
	scan discovery.VolatilityScan
	// sources are the module files as discovery read them, keyed by absolute
	// path, so that the mutant scan can substitute one and re-read the rest.
	sources map[string][]byte
}

func prepare(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	config Config,
	workRoot string,
) (warm, error) {
	prepared, err := warmUp(ctx, runner, configuration, workRoot)
	if err != nil {
		return warm{}, err
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

// warmUp materialises the closure, initialises it and reads the provider
// schemas. It is everything a run needs before it decides what to execute, and
// the only part a characterisation — which has no suite to baseline — shares.
func warmUp(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
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

	prepared.schemas, err = runner.ProvidersSchema(ctx, prepared.moduleDir)
	if err != nil {
		return warm{}, err
	}

	unformatted, fmtErr := runner.FmtCheck(ctx, prepared.moduleDir)
	if fmtErr == nil && len(unformatted) > 0 {
		prepared.warnings = append(prepared.warnings, fmt.Sprintf(
			"%d file(s) are not canonically formatted; mutant diffs may span more than one line: %s",
			len(unformatted), strings.Join(unformatted, ", "),
		))
	}

	sources, err := moduleSources(configuration)
	if err != nil {
		return warm{}, err
	}

	prepared.sources = sources

	return prepared, nil
}

// runBaseline proves the suite is green before any mutant is trusted, times it
// for timeout calibration, and establishes the fingerprint the oracle compares
// against.
//
// The suite runs twice, verbose. Once is not enough: the difference between two
// runs of the same configuration is the only evidence that separates a value
// the module computes from one the clock or a mock invented, and a single run
// would offer the oracle a fingerprint it could not trust.
func runBaseline(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	prepared *warm,
) error {
	result, err := runner.Test(ctx, prepared.moduleDir, tfexec.TestOptions{
		TestDirectory: configuration.TestDirRelative(),
		Filters:       nil,
		Verbose:       true,
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

	return calibrateOracle(ctx, runner, configuration, prepared, result)
}

// calibrateOracle projects the baseline payloads and derives the volatile mask.
func calibrateOracle(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	prepared *warm,
	first tfexec.TestResult,
) error {
	firstPayloads, err := fingerprint.Canonicalise(first.Payloads)
	if err != nil {
		return err
	}

	second, err := runner.Test(ctx, prepared.moduleDir, tfexec.TestOptions{
		TestDirectory: configuration.TestDirRelative(),
		Filters:       nil,
		Verbose:       true,
		Timeout:       0,
	})
	if err != nil {
		return err
	}

	secondPayloads, err := fingerprint.Canonicalise(second.Payloads)
	if err != nil {
		return err
	}

	prepared.scan = configuration.ScanVolatility(prepared.sources)
	prepared.payloads = firstPayloads
	prepared.mask = fingerprint.Derive(firstPayloads, secondPayloads).
		Merge(staticMask(prepared.scan, firstPayloads))

	return nil
}

// moduleSources reads every module file, keyed by absolute path.
func moduleSources(configuration discovery.Configuration) (map[string][]byte, error) {
	sources := map[string][]byte{}

	for _, module := range configuration.Modules {
		for _, path := range module.Files {
			content, err := os.ReadFile(path) //nolint:gosec // module paths come from discovery.
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", path, err)
			}

			sources[path] = content
		}
	}

	return sources, nil
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
