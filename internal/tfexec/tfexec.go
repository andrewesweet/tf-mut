// Package tfexec runs the Terraform CLI and decodes its machine-readable
// output.
//
// Every command is executed with the global -chdir option so that the caller's
// process working directory is never changed, which keeps concurrent mutant
// execution safe. The test stream is decoded incrementally rather than
// buffered, because a verbose run's output is measured in gigabytes.
package tfexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrNotExecuted reports an operational failure to start or complete a Terraform
// invocation. It includes context cancellation and preflight deadline expiry.
// Raw Run and Test calls record deadline expiry in their TimedOut fields instead.
var ErrNotExecuted = errors.New("terraform could not be executed")

// ErrCommandFailed reports a Terraform subcommand that exited non-zero where a
// non-zero exit carries no product meaning.
var ErrCommandFailed = errors.New("terraform command failed")

// nonForwardedVariables never reach a Terraform child. The git location
// variables pin git at one repository: Terraform shells out to git to
// install remote modules, in directories of its own choosing, so inheriting
// these turns a module download into "fatal: working tree ... already exists".
// Anyone running the tool from a git hook exports them without knowing it.
// The session bookkeeping variables identify a shell, terminal, unit or trace
// rather than configuring anything Terraform runs, so withholding them
// cannot change a verdict — the M3 spec review C4 addendum (25 October 2026)
// carries one line of evidence per name. Only the location variables are
// removed from git's surface: GIT_SSH_COMMAND, credential helpers and the
// rest are how a private module source authenticates.
//
//nolint:gochecknoglobals // an immutable lookup table.
var nonForwardedVariables = map[string]bool{
	// Git location variables (pre-existing).
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR":                   true,
	"GIT_NAMESPACE":                    true,
	"GIT_PREFIX":                       true,
	// Trace-context propagation.
	"TRACEPARENT": true,
	"TRACESTATE":  true,
	// Shell bookkeeping.
	"SHLVL":  true,
	"OLDPWD": true,
	"_":      true,
	// Multiplexer and terminal identity.
	"TMUX":                 true,
	"TMUX_PANE":            true,
	"TERM_PROGRAM":         true,
	"TERM_PROGRAM_VERSION": true,
	// systemd per-invocation identifiers.
	"INVOCATION_ID":         true,
	"SYSTEMD_EXEC_PID":      true,
	"MEMORY_PRESSURE_WATCH": true,
	"MEMORY_PRESSURE_WRITE": true,
	"JOURNAL_STREAM":        true,
	// SSH session identifiers.
	"SSH_CLIENT":     true,
	"SSH_CONNECTION": true,
	"SSH_TTY":        true,
	"XDG_SESSION_ID": true,
}

// nonForwardedPrefix is tf-mut's own namespace: the debug flag the CLI reads
// for logging, and the markers tests address the child through — except
// those travel as run additions, which stripping never touches. Nothing
// Terraform, its providers or git reads lives under this prefix, so the
// whole namespace stays out of the inherited environment and out of the key.
const nonForwardedPrefix = "TF_MUT_"

// Runner executes Terraform CLI commands.
type Runner struct {
	// Binary is the Terraform executable name or path.
	Binary string
	// Env holds run-specific entries appended after inherited entries and child
	// defaults. The last entry for a name wins.
	Env []string
}

// Usage is the operating system's accounting for one child Terraform
// process, read from the rusage the kernel reports when the process is
// reaped.
//
// The accounting scope is documented under process_usage in
// docs/schema/report-2.13.0.json. Killing a leftover provider with ReapGroup
// does not add that provider's usage to the reaped Terraform process.
type Usage struct {
	// UserCPU and SystemCPU are the child's user and system processor time.
	UserCPU   time.Duration
	SystemCPU time.Duration
	// PeakRSSKB is the maximum resident set size in kibibytes, the kernel's
	// ru_maxrss unit on Linux.
	PeakRSSKB int64
}

// childUsage reads the usage out of a reaped process's state. A process that
// never started has no state and no usage.
func childUsage(state *os.ProcessState) Usage {
	if state == nil {
		return Usage{}
	}

	usage := Usage{
		UserCPU:   state.UserTime(),
		SystemCPU: state.SystemTime(),
	}

	if rusage, ok := state.SysUsage().(*syscall.Rusage); ok {
		usage.PeakRSSKB = rusage.Maxrss
	}

	return usage
}

// Result holds the raw outcome of one Terraform invocation.
type Result struct {
	Started  bool
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	TimedOut bool
	Duration time.Duration
	// Usage is the child process's resource accounting, read when it was
	// reaped.
	Usage Usage
}

const (
	// exitCodeUnavailable marks a command whose exit status could not be read.
	exitCodeUnavailable = -1
)

// Run executes Terraform in dir with the supplied arguments, buffering stdout.
//
// A non-zero exit status alone is not an error; inspect Result.ExitCode.
//
// It is StreamOutput with a consumer that keeps everything, rather than a second
// implementation: the exit-status ladder and the environment stripping standing
// process rule 5 requires are subtle enough that two copies would eventually
// disagree, and the disagreement would be a verdict.
func (r Runner) Run(ctx context.Context, dir string, args ...string) (Result, error) {
	stdout := bytes.Buffer{}

	result, err := r.StreamOutput(ctx, dir, func(reader io.Reader) error {
		_, copyErr := io.Copy(&stdout, reader)
		if copyErr != nil {
			return fmt.Errorf("reading terraform output: %w", copyErr)
		}

		return nil
	}, args...)

	result.Stdout = stdout.Bytes()

	return result, err
}

// Version describes the Terraform release reported by version -json.
type Version struct {
	Terraform string `json:"terraform_version"`
	Platform  string `json:"platform"`
	// Usage is the invocation's child-process accounting, not part of the
	// decoded document.
	Usage   Usage `json:"-"`
	Started bool  `json:"-"`
}

// Version reports the Terraform version of the configured binary. The limit
// bounds the invocation; an expired limit is an operational failure.
func (r Runner) Version(ctx context.Context, dir string, limit time.Duration) (Version, error) {
	result, err := r.runLimited(ctx, dir, limit, "version", "version", "-json")
	version := Version{Usage: result.Usage, Started: result.Started}
	if err != nil {
		return version, err
	}

	if result.ExitCode != 0 {
		return version, fmt.Errorf("%w: version: %s", ErrCommandFailed, strings.TrimSpace(string(result.Stderr)))
	}

	if err := json.Unmarshal(result.Stdout, &version); err != nil {
		return version, fmt.Errorf("decoding terraform version: %w", err)
	}

	return version, nil
}

// AtLeast reports whether the Terraform version is at least major.minor.
func (v Version) AtLeast(major, minor int) bool {
	haveMajor, haveMinor := v.parts()

	if haveMajor != major {
		return haveMajor > major
	}

	return haveMinor >= minor
}

func (v Version) parts() (major, minor int) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(v.Terraform), "v")
	fields := strings.SplitN(trimmed, ".", 3) //nolint:mnd // major, minor and the ignored remainder.

	if len(fields) > 0 {
		major, _ = strconv.Atoi(fields[0])
	}

	if len(fields) > 1 {
		minor, _ = strconv.Atoi(fields[1])
	}

	return major, minor
}

// InitOptions selects the behaviour of Init.
type InitOptions struct {
	// TestDirectory is passed to init as -test-directory. Terraform installs
	// only the modules called by the test files in the directory init is told
	// about, so init must receive the same test directory the test run will
	// use: a suite configured outside the default tests/ calls modules init
	// would otherwise never install, and its baseline runs red with "Module
	// not installed".
	TestDirectory string
	// ReadonlyLock treats the lock file as read-only, which keeps runs
	// against a pre-locked fixture hermetic.
	ReadonlyLock bool
}

// Init installs providers and modules for the module in dir. The returned
// Result carries the invocation's child-process usage for the run's
// telemetry; callers that want nothing from it discard it. The limit bounds
// the invocation; an expired limit is an operational failure.
func (r Runner) Init(ctx context.Context, dir string, options InitOptions, limit time.Duration) (Result, error) {
	args := []string{"init", "-backend=false", "-input=false", "-no-color"}
	if options.TestDirectory != "" {
		args = append(args, "-test-directory="+options.TestDirectory)
	}

	if options.ReadonlyLock {
		args = append(args, "-lockfile=readonly")
	}

	result, err := r.runLimited(ctx, dir, limit, "init", args...)
	if err != nil {
		return result, err
	}

	if result.ExitCode != 0 {
		return result, fmt.Errorf("%w: init: %s", ErrCommandFailed, combinedTail(result))
	}

	return result, nil
}

// Diagnostic is one Terraform diagnostic with its source range.
type Diagnostic struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
	Range    *Range `json:"range"`
	TestFile string `json:"-"`
	TestRun  string `json:"-"`
}

// assertionFailure is the summary Terraform reports for a failed test
// assertion. The message text is a fact about Terraform's output, so it is
// known here, at the boundary that decodes that output, and nowhere else.
const assertionFailure = "Test assertion failed"

// IsAssertionFailure reports whether a diagnostic summary is Terraform's
// report of a failed test assertion — the classification the curate
// attribution reads its kill sets from. It takes the summary because the
// published diagnostic is the publication context's own DTO, which this
// boundary never imports.
func IsAssertionFailure(summary string) bool {
	return summary == assertionFailure
}

// Range is a source range reported by Terraform.
type Range struct {
	Filename string `json:"filename"`
	Start    Pos    `json:"start"`
	End      Pos    `json:"end"`
}

// Pos is a position within a source file.
type Pos struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Byte   int `json:"byte"`
}

// ValidateResult is the decoded output of terraform validate -json.
type ValidateResult struct {
	Valid       bool         `json:"valid"`
	ErrorCount  int          `json:"error_count"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	// Usage is the invocation's child-process accounting, not part of the
	// decoded document.
	Usage   Usage `json:"-"`
	Started bool  `json:"-"`
}

// Validate statically checks the configuration in dir. The limit bounds the
// invocation; an expired limit is an operational failure.
func (r Runner) Validate(ctx context.Context, dir string, limit time.Duration) (ValidateResult, error) {
	result, err := r.runLimited(ctx, dir, limit, "validate", "validate", "-json", "-no-color")
	validation := ValidateResult{Usage: result.Usage, Started: result.Started}
	if err != nil {
		return validation, err
	}

	if err := json.Unmarshal(result.Stdout, &validation); err != nil {
		return validation, fmt.Errorf("decoding terraform validate output: %w", err)
	}

	return validation, nil
}

// FmtCheck reports the files under dir that are not canonically formatted.
// The returned Result carries the invocation's child-process usage for the
// run's telemetry. The limit bounds the invocation; an expired limit is an
// operational failure.
func (r Runner) FmtCheck(ctx context.Context, dir string, limit time.Duration) ([]string, Result, error) {
	result, err := r.runLimited(ctx, dir, limit, "fmt", "fmt", "-check", "-recursive", "-list=true", "-no-color")
	if err != nil {
		return nil, result, err
	}

	unformatted := []string{}

	for line := range strings.SplitSeq(string(result.Stdout), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			unformatted = append(unformatted, trimmed)
		}
	}

	return unformatted, result, nil
}

// runLimited bounds one invocation at limit and reports an expired deadline
// as an operational failure naming the subcommand. In particular, validate
// supplies verdict evidence, so its overrun must not be treated as a mutant
// timeout or as a completed validation result. A non-positive limit leaves
// the invocation bounded only by ctx.
//
// The deadline is enforced through the context, so the same process-group
// stop that bounds a test run kills this command and everything it started.
func (r Runner) runLimited(
	ctx context.Context,
	dir string,
	limit time.Duration,
	subcommand string,
	args ...string,
) (Result, error) {
	if limit <= 0 {
		return r.Run(ctx, dir, args...)
	}

	limited, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	result, err := r.Run(limited, dir, args...)
	if result.TimedOut {
		return result, fmt.Errorf("%w: terraform %s did not finish within %s",
			ErrNotExecuted, subcommand, limit)
	}

	return result, err
}

// waitDelay bounds how long a stopped command's I/O may keep Wait waiting:
// long enough that a mid-shutdown descendant cannot flip a healthy run into
// an operational failure, short enough that a stuck pipe is reported rather
// than hung on.
const waitDelay = 5 * time.Second

// GroupCommand builds a command that runs in its own process group, so that
// stopping it stops everything it started.
//
// exec.CommandContext kills the direct child alone when the context expires.
// A Terraform command starts one provider plugin per configured provider, and
// a plugin survives its parent's SIGKILL: reparented, still running, still
// executing the plugin cache's binary — so the next command that installs
// that provider version fails with "text file busy". This constructor puts
// the child in a process group of its own and makes the context's
// cancellation signal the whole group, so no descendant outlives the command.
//
// The signal is SIGKILL so cancellation, including an expired deadline, does
// not spend more time gracefully stopping processes the caller has stopped
// waiting for. exec invokes Cancel only after the process has started, so
// Process is always set when it runs. A normal exit needs the same kill —
// see ReapGroup, which every waiter calls after Wait — because Terraform
// leaves provider plugins behind that Cancel never runs for. Setpgid and the
// group signal are Unix interfaces, shared by every platform the project
// builds for; the package does not build on Windows today in any case.
func GroupCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...) //nolint:gosec // tool-controlled values.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Bound Wait's I/O cleanup if an inherited pipe remains open after the
	// command exits.
	command.WaitDelay = waitDelay
	command.Cancel = func() error {
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}

	return command
}

// ReapGroup kills everything left in a GroupCommand child's process group.
// Cancel covers the cancelled-context path; this covers the normal exit,
// where Terraform may leave provider plugins behind that keep running —
// each holding hundreds of megabytes — and accumulate over a mutation run.
//
// Best effort: a group that already exited reports ESRCH, which is the
// common case and is ignored, and after Wait there is nothing actionable to
// do about any other kill failure either, so no error is reported. A
// command that never started has no process and nothing is signalled.
func ReapGroup(command *exec.Cmd) {
	if command.Process == nil {
		return
	}

	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

// inheritedEnvironment is the process environment minus the variables that
// would redirect Terraform's git subprocesses at the caller's repository and
// minus the session bookkeeping variables no verdict can depend on. The
// run's own additions are appended later, unstripped: they are explicit
// per-run input, and they enter the key beside the child.
func inheritedEnvironment() []string {
	return stripNonForwarded(os.Environ())
}

// stripNonForwarded withholds the non-forwarded variables — the table plus
// tf-mut's own namespace — from an inherited environment. It is the one
// stripping step: inheritedEnvironment applies it to the process environment,
// and the cache key replays it over recorded environments.
func stripNonForwarded(environment []string) []string {
	stripped := make([]string, 0, len(environment))

	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if nonForwardedVariables[name] || strings.HasPrefix(name, nonForwardedPrefix) {
			continue
		}

		stripped = append(stripped, entry)
	}

	return stripped
}

// The Terraform child defaults trade memory for CPU time. README.md's
// "Using it" section documents the defaults and caller overrides.
//
// The measured shape, on three real suites under Terraform v1.15.8: total
// CPU fell 18–26% and wall-clock 15–24% with identical verdicts across 960
// sampled mutants; peak child memory rose 54–85%. Terraform core spends
// 24–28% of its CPU in garbage collection at the default GOGC=100, and with
// TF_LOG unset the SDK writes megabytes of log lines to stderr per test file
// that no one reads —
// TF_LOG_SDK=off took one sampled six-run file's stderr from 3.9 MB to
// 1.9 KB. A GOGC sweep put 400 at the knee: most of the CPU saving at a
// fraction of the memory growth of 800.
//
// The soft limit covers memory managed by the Go runtime, not just its heap.
// It increases collection pressure as memory approaches the limit, but can
// be exceeded to preserve progress. It does not cap RSS or aggregate worker
// memory. Peak child RSS with the widened collection target alone reached
// about 691 MB on the measured suites. The runtime's garbage-collection CPU
// limiter is the safety valve against excessive collection. A soft limit
// never fails an allocation.
const (
	goGCVariable          = "GOGC"
	goMemoryLimitVariable = "GOMEMLIMIT"
	tfLogSDKVariable      = "TF_LOG_SDK"
)

//nolint:gochecknoglobals // an immutable table, like nonForwardedVariables.
var performanceDefaults = []struct {
	name  string
	value string
}{
	{name: goGCVariable, value: "400"},
	{name: goMemoryLimitVariable, value: "1073741824"},
	{name: tfLogSDKVariable, value: "off"},
}

// childEnvironment assembles the environment a Terraform child runs under:
// the inherited environment — already stripped of the non-forwarded
// variables — then the performance defaults for managed variables the
// caller has not set, then the run's own additions last, which win over
// both. The inherited slice is not retained: the caller may pass one it
// intends to reuse.
func childEnvironment(inherited, additions []string) []string {
	set := make(map[string]bool, len(inherited))

	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		set[name] = true
	}

	environment := slices.Clone(inherited)

	for _, managed := range performanceDefaults {
		if set[managed.name] {
			continue
		}

		environment = append(environment, managed.name+"="+managed.value)
	}

	return append(environment, additions...)
}

// ChildEnvironment returns the exact environment the runner hands a
// Terraform child for the run's additions: the inherited environment with
// the non-forwarded variables stripped, the performance defaults for
// managed variables the caller has not set, and the run's own additions
// last. The verdict cache keys this value, so the key and the child are one
// computation and can no longer disagree.
func ChildEnvironment(additions []string) []string {
	return childEnvironment(inheritedEnvironment(), additions)
}

func combinedTail(result Result) string {
	combined := strings.TrimSpace(string(result.Stderr))
	if combined == "" {
		combined = strings.TrimSpace(string(result.Stdout))
	}

	return combined
}
