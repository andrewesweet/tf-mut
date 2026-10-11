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
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrNotExecuted reports a Terraform invocation that never produced output
// because the binary could not be started.
var ErrNotExecuted = errors.New("terraform could not be executed")

// ErrCommandFailed reports a Terraform subcommand that exited non-zero where a
// non-zero exit carries no product meaning.
var ErrCommandFailed = errors.New("terraform command failed")

// gitLocationVariables pin git at one repository. Terraform shells out to git
// to install remote modules, in directories of its own choosing, so inheriting
// these turns a module download into "fatal: working tree ... already exists".
// Anyone running the tool from a git hook exports them without knowing it.
//
// Only the location variables are removed: GIT_SSH_COMMAND, credential helpers
// and the rest are how a private module source authenticates.
//
//nolint:gochecknoglobals // an immutable lookup table.
var gitLocationVariables = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR":                   true,
	"GIT_NAMESPACE":                    true,
	"GIT_PREFIX":                       true,
}

// Runner executes Terraform CLI commands.
type Runner struct {
	// Binary is the Terraform executable name or path.
	Binary string
	// Env holds additional environment entries appended to the process environment.
	Env []string
}

// Usage is the operating system's accounting for one child Terraform
// process, read from the rusage the kernel reports when the process is
// reaped.
//
// The numbers cover the terraform process and the descendants it waited for
// before exiting: on Linux, a child's rusage includes its own reaped
// children. Terraform starts each provider plugin and reaps it before
// exiting, so provider CPU and memory are counted here. What the numbers do
// not cover is a descendant the child never waited on — there is none in a
// terraform invocation — and anything still running at timeout kill time.
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

// Run executes Terraform in dir with the supplied arguments.
//
// A non-zero exit status is not an error: Terraform uses exit codes to report
// test outcomes, so the caller inspects Result.ExitCode instead.
// Run executes Terraform in dir with the supplied arguments, buffering stdout.
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

// Version reports the Terraform version of the configured binary.
func (r Runner) Version(ctx context.Context, dir string) (Version, error) {
	result, err := r.Run(ctx, dir, "version", "-json")
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
// telemetry; callers that want nothing from it discard it.
func (r Runner) Init(ctx context.Context, dir string, options InitOptions) (Result, error) {
	args := []string{"init", "-backend=false", "-input=false", "-no-color"}
	if options.TestDirectory != "" {
		args = append(args, "-test-directory="+options.TestDirectory)
	}

	if options.ReadonlyLock {
		args = append(args, "-lockfile=readonly")
	}

	result, err := r.Run(ctx, dir, args...)
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

// Validate statically checks the configuration in dir.
func (r Runner) Validate(ctx context.Context, dir string) (ValidateResult, error) {
	result, err := r.Run(ctx, dir, "validate", "-json", "-no-color")
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
// run's telemetry.
func (r Runner) FmtCheck(ctx context.Context, dir string) ([]string, Result, error) {
	result, err := r.Run(ctx, dir, "fmt", "-check", "-recursive", "-list=true", "-no-color")
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

// inheritedEnvironment is the process environment minus the variables that
// would redirect Terraform's git subprocesses at the caller's repository.
func inheritedEnvironment() []string {
	environment := make([]string, 0, len(os.Environ()))

	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if gitLocationVariables[name] {
			continue
		}

		environment = append(environment, entry)
	}

	return environment
}

func combinedTail(result Result) string {
	combined := strings.TrimSpace(string(result.Stderr))
	if combined == "" {
		combined = strings.TrimSpace(string(result.Stdout))
	}

	return combined
}
