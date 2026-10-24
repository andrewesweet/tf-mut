package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// warmWorkspace is the one initialised workspace every mutant sandbox
// borrows from.
//
// It exists because Terraform's init writes into its working directory and the
// source tree is never written to: the warm workspace absorbs those writes, and
// its provider tree and module payloads are then shared read-only.
type warmWorkspace struct {
	moduleDir        string
	dataDir          string
	lockFile         string
	providerGuard    *sandbox.ProviderGuard
	schemas          tfexec.Schemas
	baselineRuns     int
	baselineDuration time.Duration
	// baselineFiles is the per-test-file timing the baseline run's stream
	// carried, published beside the aggregate baseline duration.
	baselineFiles []report.FileTiming
	warnings      []string
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
	settings settings, workRoot string,
	collector *runCollector,
) (warmWorkspace, error) {
	prepared, err := warmUp(ctx, runner, configuration, workRoot, collector, settings)
	if err != nil {
		return warmWorkspace{}, err
	}

	// Preview needs the schemas that gate the deletion operators, but no
	// baseline. The workspace still exists because init cannot be allowed to
	// write into the source tree.
	if settings.mode == previewMode {
		return prepared, nil
	}

	if err := runBaseline(ctx, runner, configuration, &prepared, collector,
		settings.preflightCap(), settings.Jobs > 1); err != nil {
		return warmWorkspace{}, err
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
	collector *runCollector,
	settings settings,
) (warmWorkspace, error) {
	materialisePhase := collector.phase(phaseWarmMaterialise)
	built, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: configuration.ClosureRoot,
		ModuleRel:  configuration.RootRelative(),
		Target:     filepath.Join(workRoot, "warm"),
		Mutations:  nil,
		Share:      nil,
		Hardlink:   false,
	})
	materialisePhase()
	if err != nil {
		return warmWorkspace{}, err
	}

	prepared := warmWorkspace{
		moduleDir: built.ModuleDir,
		dataDir:   filepath.Join(built.ModuleDir, sandbox.DataDirName),
		warnings:  []string{},
	}

	// neutraliseBackends owns the temporary staged rewrite contract.
	backendPhase := collector.phase(phaseWarmBackend)
	rewrites, stripErr := neutraliseBackends(prepared.moduleDir,
		filepath.Join(configuration.ClosureRoot, filepath.FromSlash(configuration.RootRelative())))
	backendPhase()
	if stripErr != nil {
		return warmWorkspace{}, stripErr
	}

	_, hasLock := configuration.LockFilePath()
	initPhase := collector.phase(phaseWarmInit)
	initResult, initErr := runner.Init(ctx, prepared.moduleDir, tfexec.InitOptions{
		TestDirectory: configuration.TestDirRelative(),
		ReadonlyLock:  hasLock,
	}, settings.initCap())
	initPhase(collector.child("init", initResult.Usage, initResult.Started)...)
	if initErr != nil {
		return warmWorkspace{}, initErr
	}

	if path := filepath.Join(prepared.moduleDir, sandbox.LockFileName); fileExists(path) {
		prepared.lockFile = path
	}

	if stampErr := stampProviderTree(&prepared); stampErr != nil {
		return warmWorkspace{}, stampErr
	}

	schemaPhase := collector.phase(phaseWarmSchema)
	prepared.schemas, err = runner.ProvidersSchema(ctx, prepared.moduleDir, settings.preflightCap())
	schemaPhase(collector.child("providers", prepared.schemas.Usage, prepared.schemas.Started)...)
	if err != nil {
		return warmWorkspace{}, err
	}

	for _, rewrite := range rewrites {
		if restoreErr := sandbox.WriteFresh(rewrite.target, rewrite.source, rewrite.original); restoreErr != nil {
			return warmWorkspace{}, fmt.Errorf("restoring %s: %w", filepath.Base(rewrite.target), restoreErr)
		}
	}

	fmtPhase := collector.phase(phaseWarmFmt)
	unformatted, fmtResult, fmtErr := runner.FmtCheck(ctx, prepared.moduleDir, settings.preflightCap())
	fmtPhase(collector.child("fmt", fmtResult.Usage, fmtResult.Started)...)
	if errors.Is(fmtErr, context.Canceled) || errors.Is(fmtErr, context.DeadlineExceeded) ||
		errors.Is(fmtErr, tfexec.ErrNotExecuted) {
		return warmWorkspace{}, fmtErr
	}

	if fmtErr == nil && len(unformatted) > 0 {
		prepared.warnings = append(prepared.warnings, fmt.Sprintf(
			"%d file(s) are not canonically formatted; mutant diffs may span more than one line: %s",
			len(unformatted), strings.Join(unformatted, ", "),
		))
	}

	sources, err := moduleSources(configuration)
	if err != nil {
		return warmWorkspace{}, err
	}

	prepared.sources = sources

	return prepared, nil
}

// stampProviderTree records the warm provider tree's binaries after the init
// that verified the lock against it and before anything executes against it:
// a binary that changes from here on is a refusal at the next sandbox rather
// than a silently different verdict. The warm workspace itself keeps the full
// lock, so its own invocations still pay the checksum they verify.
func stampProviderTree(prepared *warmWorkspace) error {
	guard, err := sandbox.StampProviders(prepared.dataDir)
	if err != nil {
		return err
	}

	prepared.providerGuard = guard

	return nil
}

// runBaseline proves the suite is green before any mutant is trusted, times it
// for timeout calibration, and establishes the fingerprint the oracle compares
// against.
//
// The suite runs twice, verbose. Once is not enough: the difference between two
// runs of the same configuration is the only evidence that separates a value
// the module computes from one the clock or a mock invented, and a single run
// would offer the oracle a fingerprint it could not trust.
//
// The second run — the volatility leg — depends on the first only through "the
// baseline is green". When the job budget has a second slot, the leg starts
// beside the baseline run and its outcome is judged once the baseline proves
// green; the leg runs the same suite in the same workspace, so the mask it
// derives is the mask the sequential schedule derives. A budget of one keeps
// the sequential schedule: the overlap never runs more concurrent Terraform
// children than the caller allowed.
func runBaseline(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	prepared *warmWorkspace,
	collector *runCollector,
	limit time.Duration,
	overlap bool,
) error {
	var volatility *fingerprintRun
	if overlap {
		volatility = beginFingerprintRun(ctx, runner, configuration, prepared, collector, limit)
		// Every return from here on either has harvested the leg's outcome or
		// must stop it and wait for the reaping: a red baseline and a cancelled
		// run leave no Terraform process behind.
		defer volatility.abandon()
	}

	testPhase := collector.phase(phaseBaselineTest)
	result, err := runner.Test(ctx, prepared.moduleDir, tfexec.TestOptions{
		TestDirectory: configuration.TestDirRelative(),
		Filters:       nil,
		Verbose:       true,
		Timeout:       limit,
	})
	testPhase(collector.child("test", result.Usage, result.Started)...)
	if result.TimedOut {
		return fmt.Errorf("%w: the baseline terraform test run did not finish within %s",
			tfexec.ErrNotExecuted, limit)
	}

	if err != nil {
		return err
	}

	prepared.baselineRuns = result.ExecutedRuns()
	prepared.baselineDuration = result.Duration
	prepared.baselineFiles = baselineFileTimings(result.Files)

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

	return calibrateOracle(ctx, runner, configuration, prepared, result, collector, limit, volatility)
}

// fingerprintOutcome is the volatility leg's decoded result with the
// operational error, if any, that the invocation itself produced.
type fingerprintOutcome struct {
	result tfexec.TestResult
	err    error
}

// fingerprintRun is the volatility leg started beside the baseline run.
//
// Its context is derived from the run's, so a cancelled run stops the leg the
// same way it stops everything else; abandon stops it for the caller when the
// baseline turned red first. The outcome channel is buffered, so the leg's
// goroutine always finishes — a leg nobody harvests reaps itself and ends.
type fingerprintRun struct {
	cancel context.CancelFunc
	// outcome carries the one result the leg produces.
	outcome chan fingerprintOutcome
	// mutex guards the single harvest; collect and abandon may race.
	mutex   sync.Mutex
	harvest fingerprintOutcome
	taken   bool
}

// beginFingerprintRun starts the volatility leg on its own goroutine and
// returns immediately. The phase the leg records spans its own invocation, so
// the published telemetry shows the two legs' honest, overlapping wall time.
func beginFingerprintRun(
	parent context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	prepared *warmWorkspace,
	collector *runCollector,
	limit time.Duration,
) *fingerprintRun {
	ctx, cancel := context.WithCancel(parent)
	run := &fingerprintRun{cancel: cancel, outcome: make(chan fingerprintOutcome, 1)}

	go func() {
		phase := collector.phase(phaseBaselineFingerprint)
		result, err := runner.Test(ctx, prepared.moduleDir, tfexec.TestOptions{
			TestDirectory: configuration.TestDirRelative(),
			Filters:       nil,
			Verbose:       true,
			Timeout:       limit,
		})
		phase(collector.child("test", result.Usage, result.Started)...)
		run.outcome <- fingerprintOutcome{result: result, err: err}
	}()

	return run
}

// collect waits for the volatility leg and returns its outcome, once. A leg
// that is still running when a green baseline asks for its outcome is waited
// for, never stopped.
func (f *fingerprintRun) collect() fingerprintOutcome {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if !f.taken {
		f.harvest = <-f.outcome
		f.taken = true
		// The leg has finished; release the derived context either way.
		f.cancel()
	}

	return f.harvest
}

// abandon stops the volatility leg and waits for the reaping. It is the
// caller's way of saying the outcome will not be judged: a red baseline, a
// deadline, a cancelled run. On a leg already harvested it does nothing.
func (f *fingerprintRun) abandon() fingerprintOutcome {
	f.cancel()

	return f.collect()
}

// calibrateOracle projects the baseline payloads and derives the volatile mask.
func calibrateOracle(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	prepared *warmWorkspace,
	first tfexec.TestResult,
	collector *runCollector,
	limit time.Duration,
	volatility *fingerprintRun,
) error {
	firstPayloads, err := fingerprint.Canonicalise(first.Payloads)
	if err != nil {
		return err
	}

	var second tfexec.TestResult

	if volatility != nil {
		// The leg started beside the baseline run; its phase already recorded
		// itself when the invocation ended.
		outcome := volatility.collect()
		second = outcome.result

		if outcome.err != nil && !second.TimedOut {
			return outcome.err
		}
	} else {
		fingerprintPhase := collector.phase(phaseBaselineFingerprint)
		var secondErr error
		second, secondErr = runner.Test(ctx, prepared.moduleDir, tfexec.TestOptions{
			TestDirectory: configuration.TestDirRelative(),
			Filters:       nil,
			Verbose:       true,
			Timeout:       limit,
		})
		fingerprintPhase(collector.child("test", second.Usage, second.Started)...)

		if secondErr != nil && !second.TimedOut {
			return secondErr
		}
	}

	if second.TimedOut {
		return fmt.Errorf("%w: the baseline fingerprint run did not finish within %s",
			tfexec.ErrNotExecuted, limit)
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

// declarationSources widens the module sources to the `.tf.json` configuration
// files discovery read, so a judgement point over a JSON-declared variable
// still quotes its constraint verbatim. The widened map is characterisation's
// alone: the module sources the mutants overlay stay native.
func declarationSources(configuration discovery.Configuration) (map[string][]byte, error) {
	sources, err := moduleSources(configuration)
	if err != nil {
		return nil, err
	}

	for _, file := range configuration.JSONFiles() {
		if !file.Read || file.Class != discovery.JSONConfiguration {
			continue
		}

		content, err := os.ReadFile(file.Path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file.Path, err)
		}

		sources[file.Path] = content
	}

	return sources, nil
}

// baselineFileTimings projects the baseline stream's per-file timings onto
// the published DTO, in completion order. No file completed — a stream cut
// short, a suite with none — publishes nothing rather than an empty list.
func baselineFileTimings(files []tfexec.FileTiming) []report.FileTiming {
	if len(files) == 0 {
		return nil
	}

	projected := make([]report.FileTiming, 0, len(files))
	for _, file := range files {
		projected = append(projected, report.FileTiming{
			File:      file.File,
			Runs:      file.Runs,
			ElapsedMS: file.ElapsedMS,
		})
	}

	return projected
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
