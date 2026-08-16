// Package main exposes the tf-mut command-line entry point.
//
// The command is a thin shell over the engine: it parses flags into an
// engine.Config, renders the returned report, and maps it to an exit code.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/buildinfo"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

var version = "dev"

const (
	runCommand     = "run"
	previewCommand = "preview"
	versionCommand = "version"
	versionFlag    = "--version"

	reporterTerminal = "terminal"
	reporterJSON     = "json"
	reporterSARIF    = "sarif"

	usage = `usage: tf-mut <command> [flags] [PATH]

Commands:
  run       Mutate the module at PATH and report which resources are pseudo-tested
  preview   List the mutants that would be generated, as diffs, executing nothing
  version   Print the build version

Flags for run and preview:
  --test-directory PATH        Test directory relative to the module (default "tests")
  --jobs N                     Mutants to execute concurrently (default: CPU count)
  --timeout-factor F           Multiple of the baseline run time (default 10)
  --min-score N                Fail below this mutation score percentage
  --allow-incomplete-score     Let a timeout-affected score satisfy --min-score
  --allow-real-infrastructure  Permit execution against unmocked providers
  --allow-unsandboxed-effects  Permit apply-mode provisioners and unsevered data sources
  --tier smoke|standard|deep   Operator breadth (default standard)
  --operator ID[,ID]           Restrict generation to these operators
  --exclude-operator ID[,ID]   Remove operators from the population
  --exclude-path GLOB[,GLOB]   Remove sites in matching files
  --exclude-resource ADDR[,..] Remove sites in matching resources
  --reporter terminal|json|sarif  Output format (default terminal)
  --sarif-path PATH            Where to write the SARIF document

Settings also readable from .tf-mut.hcl at the module root. A flag given on the
command line overrides the configured value of that scalar and nothing else.`

	exitSuccess = 0
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], version, os.Stdout, os.Stderr))
}

func run(args []string, buildVersion string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, usage)
	}

	switch args[0] {
	case versionCommand, versionFlag:
		if _, err := fmt.Fprintln(stdout, buildinfo.Resolve(buildVersion)); err != nil {
			return report.ExitOperational
		}

		return exitSuccess
	case runCommand, previewCommand:
		return execute(args[0], args[1:], stdout, stderr)
	default:
		return fail(stderr, usage)
	}
}

type options struct {
	config    engine.Config
	gate      report.Gate
	reporter  string
	sarifPath string
}

func parse(command string, args []string, stderr io.Writer) (options, error) {
	set := flag.NewFlagSet("tf-mut "+command, flag.ContinueOnError)
	set.SetOutput(stderr)

	testDirectory := set.String("test-directory", engine.DefaultTestDirectory,
		"test directory relative to the module")
	jobs := set.Int("jobs", 0, "mutants to execute concurrently")
	timeoutFactor := set.Float64("timeout-factor", engine.DefaultTimeoutFactor,
		"multiple of the baseline run time")
	minScore := set.Float64("min-score", 0, "fail below this mutation score percentage")
	allowIncomplete := set.Bool("allow-incomplete-score", false,
		"let a timeout-affected score satisfy --min-score")
	allowReal := set.Bool("allow-real-infrastructure", false,
		"permit execution against unmocked providers")
	allowEffects := set.Bool("allow-unsandboxed-effects", false,
		"permit apply-mode provisioners and unsevered data sources")
	reporter := set.String("reporter", reporterTerminal, "output format: terminal, json or sarif")
	sarifPath := set.String("sarif-path", "", "where to write the SARIF document")
	tier := set.String("tier", "", "operator breadth: smoke, standard or deep")
	operators := set.String("operator", "", "restrict generation to these operator identifiers")
	excludeOperators := set.String("exclude-operator", "", "remove these operator identifiers")
	excludePaths := set.String("exclude-path", "", "remove sites in files matching these globs")
	excludeResources := set.String("exclude-resource", "", "remove sites in these resource addresses")

	if err := set.Parse(args); err != nil {
		return options{}, fmt.Errorf("parsing flags: %w", err)
	}

	moduleDir := "."
	if set.NArg() > 0 {
		moduleDir = set.Arg(0)
	}

	if *reporter != reporterTerminal && *reporter != reporterJSON && *reporter != reporterSARIF {
		return options{}, fmt.Errorf("%w: %s", errUnknownReporter, *reporter)
	}

	requested := false
	given := []string{}

	set.Visit(func(flagged *flag.Flag) {
		given = append(given, flagged.Name)

		if flagged.Name == engine.FlagMinScore {
			requested = true
		}
	})

	return options{
		config: engine.Config{
			ModuleDir:               moduleDir,
			TestDirectory:           *testDirectory,
			Jobs:                    *jobs,
			TimeoutFactor:           *timeoutFactor,
			TimeoutFloor:            0,
			MinScore:                *minScore,
			HasMinScore:             requested,
			AllowIncompleteScore:    *allowIncomplete,
			AllowRealInfrastructure: *allowReal,
			AllowUnsandboxedEffects: *allowEffects,
			Preview:                 command == previewCommand,
			TerraformBinary:         "",
			Env:                     nil,
			WorkDir:                 "",
			TestSelection:           nil,
			Tier:                    mutation.Tier(*tier),
			IncludeOperators:        commaSeparated(*operators),
			ExcludeOperators:        commaSeparated(*excludeOperators),
			ExcludePaths:            commaSeparated(*excludePaths),
			ExcludeResources:        commaSeparated(*excludeResources),
			SetFlags:                given,
		},
		gate: report.Gate{
			MinScore:             *minScore,
			HasMinScore:          requested,
			AllowIncompleteScore: *allowIncomplete,
		},
		reporter:  *reporter,
		sarifPath: *sarifPath,
	}, nil
}

// commaSeparated splits a repeated-value flag, which keeps the flag surface the
// same shape as the configuration file's lists.
func commaSeparated(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	trimmed := make([]string, 0, len(parts))

	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			trimmed = append(trimmed, name)
		}
	}

	return trimmed
}

var errUnknownReporter = errors.New("unknown reporter")

func execute(command string, args []string, stdout, stderr io.Writer) int {
	parsed, err := parse(command, args, stderr)
	if err != nil {
		return fail(stderr, err.Error())
	}

	result, err := engine.Run(context.Background(), parsed.config)
	if err != nil {
		return fail(stderr, "tf-mut: "+err.Error())
	}

	if err := render(stdout, parsed, result); err != nil {
		return fail(stderr, "tf-mut: "+err.Error())
	}

	return result.ExitCode(parsed.gate)
}

func render(stdout io.Writer, parsed options, result report.Report) error {
	switch parsed.reporter {
	case reporterJSON:
		return report.WriteJSON(stdout, result)
	case reporterSARIF:
		return writeSARIF(stdout, parsed.sarifPath, result)
	default:
		return report.WriteTerminal(stdout, result)
	}
}

// writeSARIF renders the code-scanning document, to a file when one was named
// and to standard output otherwise.
func writeSARIF(stdout io.Writer, path string, result report.Report) error {
	if path == "" {
		return report.WriteSARIF(stdout, result)
	}

	file, err := os.Create(path) //nolint:gosec // the path is the caller's own choice.
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	return report.WriteSARIF(file, result)
}

func fail(stderr io.Writer, message string) int {
	if _, err := fmt.Fprintln(stderr, strings.TrimRight(message, "\n")); err != nil {
		return report.ExitOperational
	}

	if message == usage {
		return exitUsage
	}

	return report.ExitOperational
}
