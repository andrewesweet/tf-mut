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
	"github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/skill"
)

var version = "dev"

const (
	runCommand          = "run"
	previewCommand      = "preview"
	suggestCommand      = "suggest"
	characteriseCommand = "characterise"
	todosCommand        = "todos"
	curateCommand       = "curate"
	skillCommand        = "skill"
	versionCommand      = "version"
	versionFlag         = "--version"

	reporterTerminal = "terminal"
	reporterJSON     = "json"
	reporterSARIF    = "sarif"
	reporterMTE      = "mte"
	reporterHTML     = "html"
	reporterJUnit    = "junit"
	reporterMarkdown = "markdown"

	usage = `usage: tf-mut <command> [flags] [PATH]

Commands:
  run          Mutate the module at PATH and report which resources are pseudo-tested
  preview      List the mutants that would be generated, as diffs, executing nothing
  suggest      Generate, verify and optionally apply the assertions that kill the survivors
  characterise Scaffold, harvest and pin a first test suite for a module that has none
  todos        List the open judgement points characterisation refuses to guess at
  curate       Report redundant assertions from a full, authoritative population
  skill        Install the shipped agent skills (skill install [--agent claude|generic] [--path .])
  version      Print the build version

Flags for run, preview, suggest, characterise, todos and curate:
  --test-directory PATH        Test directory relative to the module (default "tests")
  --jobs N                     Mutants to execute concurrently (default: CPU count)
  --timeout-factor F           Multiple of the baseline run time (default 10)
  --min-score N                Fail below this mutation score percentage
  --allow-incomplete-score     Let a timeout-affected score satisfy --min-score
  --allow-real-infrastructure  Permit execution against unmocked providers
  --allow-unsandboxed-effects  Permit apply-mode provisioners and unsevered data sources
  --tier smoke|standard|deep   Operator breadth (default standard)
  --since REF                  Run only mutants in configuration changed since REF,
                               including staged, unstaged and untracked changes
  --sample N                   Run a deterministic N% sample (non-authoritative)
  --seed N                     Seed for --sample (default 0)
  --allow-sampled-gate         Let a sampled run satisfy --min-score (unsafe)
  --no-cache                   Disable the project-local verdict cache
  --fail-on-new                Fail on findings the baseline does not accept
  --write-baseline             Accept the current findings as the baseline
  --baseline PATH              Baseline file (default ".tf-mut-baseline.json")
  --generated-functions        Opt in to the generated function-family operators
  --operator ID[,ID]           Restrict generation to these operators
  --exclude-operator ID[,ID]   Remove operators from the population
  --exclude-path GLOB[,GLOB]   Remove sites in matching files
  --exclude-resource ADDR[,..] Remove sites in matching resources
  --reporter FORMAT            terminal|json|sarif|mte|html|junit|markdown (default terminal)
  --output FORMAT=PATH         Write an additional reporter from the same run;
                               repeatable — every output derives from one report value
  --sarif-path PATH            Where to write the SARIF document

Flags for characterise:
  --pin LEVEL                  Pinning granularity: outputs, counts or configured
                               (default outputs; a module with no outputs escalates)
  --write                      Place the verified suite in the test directory
  --force                      Replace generated files nobody has edited
  --until-dry                  Iterate scaffold, mutate and pin until the
                               survivors stop yielding new assertions
  --answer todo-ID=VALUE       Answer one judgement point; repeatable
  --resume                     Read answered judgement points from the edited
                               artefact, re-synthesise, verify and promote

Flags for suggest:
  --dry-run                    Print the candidate patches and verify nothing
  --survivor ID[,ID]           Suggest only for these survivor identifiers
  --apply ID[,ID]              Apply these verified suggestions to the test files
  --all-verified               Apply every verified suggestion

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
	case runCommand, previewCommand, suggestCommand, characteriseCommand,
		todosCommand, curateCommand:
		return execute(args[0], args[1:], stdout, stderr)
	case skillCommand:
		return skillInstall(args[1:], buildVersion, stdout, stderr)
	default:
		return fail(stderr, usage)
	}
}

type options struct {
	config    engine.Config
	gate      report.Gate
	reporter  string
	sarifPath string
	// reporters are the outputs `.tf-mut.hcl` asked for, merged additively with
	// the reporter flag rather than replaced by it.
	reporters []config.Reporter
}

// flagValues collects the parsed flag pointers, so declaring the surface and
// reading it can be two short functions instead of one long one.
// outputFlag collects repeatable FORMAT=PATH reporter outputs.
type outputFlag []config.Reporter

func (*outputFlag) String() string { return "" }

func (o *outputFlag) Set(value string) error {
	name, path, found := strings.Cut(value, "=")
	if !found || name == "" || path == "" {
		return fmt.Errorf("%w: --output wants FORMAT=PATH, got %q", errUnknownReporter, value)
	}

	if !knownReporter(name) {
		return fmt.Errorf("%w: %s", errUnknownReporter, name)
	}

	*o = append(*o, config.Reporter{Name: name, Path: path})

	return nil
}

type flagValues struct {
	testDirectory, reporter, sarifPath, tier *string
	operators, excludeOperators              *string
	excludePaths, excludeResources, since    *string
	jobs                                     *int
	timeoutFactor, minScore, sample          *float64
	seed                                     *int64
	allowIncomplete, allowReal, allowEffects *bool
	allowSampledGate, noCache                *bool
	failOnNew, writeBaseline                 *bool
	generatedFunctions                       *bool
	baselinePath                             *string
	outputs                                  *outputFlag
	dryRun, allVerified                      *bool
	survivors, apply                         *string
	pin                                      *string
	write, force, resume, untilDry           *bool
	answers                                  *answerFlag
}

// answerFlag collects repeatable todo-<id>=<value> answers.
type answerFlag []string

func (*answerFlag) String() string { return "" }

func (a *answerFlag) Set(value string) error {
	*a = append(*a, value)

	return nil
}

func declareFlags(set *flag.FlagSet) flagValues {
	return flagValues{
		testDirectory: set.String("test-directory", engine.DefaultTestDirectory,
			"test directory relative to the module"),
		jobs: set.Int("jobs", 0, "mutants to execute concurrently"),
		timeoutFactor: set.Float64("timeout-factor", engine.DefaultTimeoutFactor,
			"multiple of the baseline run time"),
		minScore: set.Float64("min-score", 0, "fail below this mutation score percentage"),
		allowIncomplete: set.Bool("allow-incomplete-score", false,
			"let a timeout-affected score satisfy --min-score"),
		allowReal: set.Bool("allow-real-infrastructure", false,
			"permit execution against unmocked providers"),
		allowEffects: set.Bool("allow-unsandboxed-effects", false,
			"permit apply-mode provisioners and unsevered data sources"),
		reporter: set.String("reporter", reporterTerminal,
			"output format: terminal, json, sarif, mte, html, junit or markdown"),
		sarifPath:        set.String("sarif-path", "", "where to write the SARIF document"),
		tier:             set.String("tier", "", "operator breadth: smoke, standard or deep"),
		operators:        set.String("operator", "", "restrict generation to these operator identifiers"),
		excludeOperators: set.String("exclude-operator", "", "remove these operator identifiers"),
		excludePaths:     set.String("exclude-path", "", "remove sites in files matching these globs"),
		excludeResources: set.String("exclude-resource", "", "remove sites in these resource addresses"),
		since:            set.String("since", "", "run only mutants in configuration changed since this git ref"),
		sample:           set.Float64("sample", 0, "run a deterministic percentage sample of the population"),
		seed:             set.Int64("seed", 0, "seed for --sample"),
		allowSampledGate: set.Bool("allow-sampled-gate", false,
			"let a sampled run satisfy a gate (unsafe)"),
		noCache: set.Bool("no-cache", false, "disable the project-local verdict cache"),
		failOnNew: set.Bool("fail-on-new", false,
			"fail on findings the baseline does not accept"),
		writeBaseline: set.Bool("write-baseline", false,
			"accept the current findings as the baseline"),
		baselinePath: set.String("baseline", "", "baseline file location"),
		generatedFunctions: set.Bool("generated-functions", false,
			"opt in to the generated function-family operators"),
		outputs: declareOutputFlag(set),
		dryRun: set.Bool("dry-run", false,
			"print the candidate patches and verify nothing"),
		allVerified: set.Bool("all-verified", false, "apply every verified suggestion"),
		survivors:   set.String("survivor", "", "suggest only for these survivor identifiers"),
		apply:       set.String("apply", "", "apply these verified suggestions"),
		pin:         set.String("pin", "", "pinning granularity: outputs, counts or configured"),
		write:       set.Bool("write", false, "place the verified suite in the test directory"),
		force:       set.Bool("force", false, "replace generated files nobody has edited"),
		resume: set.Bool("resume", false,
			"read answered judgement points from the edited artefact and promote them"),
		untilDry: set.Bool("until-dry", false,
			"iterate until the survivors stop yielding new assertions"),
		answers: declareAnswerFlag(set),
	}
}

func declareAnswerFlag(set *flag.FlagSet) *answerFlag {
	answers := &answerFlag{}
	set.Var(answers, "answer", "answer one judgement point as todo-<id>=<value>; repeatable")

	return answers
}

func declareOutputFlag(set *flag.FlagSet) *outputFlag {
	outputs := &outputFlag{}
	set.Var(outputs, "output",
		"write an additional FORMAT=PATH reporter from the same run; repeatable")

	return outputs
}

func parse(command string, args []string, stderr io.Writer) (options, error) {
	set := flag.NewFlagSet("tf-mut "+command, flag.ContinueOnError)
	set.SetOutput(stderr)

	values := declareFlags(set)

	if err := set.Parse(args); err != nil {
		return options{}, fmt.Errorf("parsing flags: %w", err)
	}

	moduleDir, err := modulePathArgument(set)
	if err != nil {
		return options{}, err
	}

	if !knownReporter(*values.reporter) {
		return options{}, fmt.Errorf("%w: %s", errUnknownReporter, *values.reporter)
	}

	configured, err := configuredReporters(moduleDir)
	if err != nil {
		return options{}, err
	}

	requested := false
	sampled := false
	given := []string{}

	set.Visit(func(flagged *flag.Flag) {
		given = append(given, flagged.Name)

		if flagged.Name == engine.FlagMinScore {
			requested = true
		}

		if flagged.Name == "sample" {
			sampled = true
		}
	})

	return options{
		config: engineConfig(command, values, moduleDir, given, requested, sampled),
		gate: report.Gate{
			MinScore:             *values.minScore,
			HasMinScore:          requested,
			AllowIncompleteScore: *values.allowIncomplete,
			FailOnNew:            *values.failOnNew,
		},
		reporter:  *values.reporter,
		sarifPath: *values.sarifPath,
		reporters: append(configured, *values.outputs...),
	}, nil
}

// engineConfig maps the parsed flags onto the engine's one input value.
func engineConfig(
	command string,
	values flagValues,
	moduleDir string,
	given []string,
	requested, sampled bool,
) engine.Config {
	return engine.Config{
		ModuleDir:               moduleDir,
		TestDirectory:           *values.testDirectory,
		Jobs:                    *values.jobs,
		TimeoutFactor:           *values.timeoutFactor,
		TimeoutFloor:            0,
		MinScore:                *values.minScore,
		HasMinScore:             requested,
		AllowIncompleteScore:    *values.allowIncomplete,
		AllowRealInfrastructure: *values.allowReal,
		AllowUnsandboxedEffects: *values.allowEffects,
		Preview:                 command == previewCommand,
		TerraformBinary:         "",
		Env:                     nil,
		WorkDir:                 "",
		TestSelection:           nil,
		Tier:                    mutation.Tier(*values.tier),
		IncludeOperators:        commaSeparated(*values.operators),
		ExcludeOperators:        commaSeparated(*values.excludeOperators),
		ExcludePaths:            commaSeparated(*values.excludePaths),
		ExcludeResources:        commaSeparated(*values.excludeResources),
		SetFlags:                given,
		Since:                   *values.since,
		SamplePercent:           *values.sample,
		HasSample:               sampled,
		SampleSeed:              *values.seed,
		AllowSampledGate:        *values.allowSampledGate,
		NoCache:                 *values.noCache,
		FailOnNew:               *values.failOnNew,
		WriteBaseline:           *values.writeBaseline,
		BaselinePath:            *values.baselinePath,
		GeneratedFunctions:      *values.generatedFunctions,
		Suggest:                 command == suggestCommand,
		SuggestDryRun:           *values.dryRun,
		SurvivorIDs:             commaSeparated(*values.survivors),
		Apply:                   commaSeparated(*values.apply),
		ApplyAll:                *values.allVerified,
		Characterise:            command == characteriseCommand,
		PinRung:                 *values.pin,
		CharacteriseWrite:       *values.write,
		CharacteriseForce:       *values.force,
		Todos:                   command == todosCommand,
		Curate:                  command == curateCommand,
		UntilDry:                *values.untilDry,
		Answers:                 *values.answers,
		Resume:                  *values.resume,
	}
}

func knownReporter(name string) bool {
	switch name {
	case reporterTerminal, reporterJSON, reporterSARIF,
		reporterMTE, reporterHTML, reporterJUnit, reporterMarkdown:
		return true
	default:
		return false
	}
}

// configuredReporters reads the module's own `reporter` blocks.
//
// The engine reads the same file for its own settings; this is the one part of
// it the engine cannot act on, because writing a file is the command line's job
// and not the seam's.
func configuredReporters(moduleDir string) ([]config.Reporter, error) {
	file, err := config.Load(moduleDir)
	if err != nil {
		return nil, fmt.Errorf("reading configuration: %w", err)
	}

	for _, configured := range file.Reporters {
		if !knownReporter(configured.Name) {
			return nil, fmt.Errorf("%w: %s", errUnknownReporter, configured.Name)
		}
	}

	return file.Reporters, nil
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

// errTrailingArguments reports arguments after the module path, which Go's
// flag package would otherwise silently discard.
var errTrailingArguments = errors.New("arguments after the module path are not parsed")

// modulePathArgument resolves the one positional argument and refuses any
// others: Go's flag parsing stops at the first non-flag argument, so anything
// after the module path would be a flag the caller believes is in force and
// the run silently ignores — including the two whose whole point is bounding
// cost (round-3 review, PR #69).
func modulePathArgument(set *flag.FlagSet) (string, error) {
	moduleDir := "."
	if set.NArg() > 0 {
		moduleDir = set.Arg(0)
	}

	if set.NArg() > 1 {
		return "", fmt.Errorf("%w: %s — flags must come before the module path",
			errTrailingArguments, strings.Join(set.Args()[1:], " "))
	}

	return moduleDir, nil
}

// skillInstall handles `tf-mut skill install`: the never-write contract's
// fourth recorded exception, performed by internal/skill under its
// preserve-user-edits protocol.
func skillInstall(args []string, buildVersion string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "install" {
		return fail(stderr, "usage: tf-mut skill install [--agent claude|generic] [--path .] [--force]")
	}

	set := flag.NewFlagSet("tf-mut skill install", flag.ContinueOnError)
	set.SetOutput(stderr)

	agent := set.String("agent", skill.AgentClaude, "target agent: claude or generic")
	path := set.String("path", ".", "the project root to install into")
	force := set.Bool("force", false, "replace a user-edited skill file")

	if err := set.Parse(args[1:]); err != nil {
		return report.ExitOperational
	}

	results, err := skill.Install(*path, *agent, buildinfo.Resolve(buildVersion), *force)
	if err != nil {
		return fail(stderr, "tf-mut: "+err.Error())
	}

	for _, result := range results {
		if _, err := fmt.Fprintf(stdout, "%s: %s\n", result.Path, result.Outcome); err != nil {
			return report.ExitOperational
		}
	}

	return exitSuccess
}

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

// render writes every requested output.
//
// Reporters merge additively: the flag chooses what goes to standard output,
// and every `reporter` block in `.tf-mut.hcl` writes its own file as well. A
// repository that has asked for a SARIF artefact on every run should not lose
// it because someone passed `--reporter json` once.
func render(stdout io.Writer, parsed options, result report.Report) error {
	if err := renderTo(stdout, parsed.reporter, result); err != nil {
		return err
	}

	for _, configured := range parsed.reporters {
		if configured.Path == "" {
			continue
		}

		if err := renderFile(configured, result); err != nil {
			return err
		}
	}

	if parsed.reporter == reporterSARIF && parsed.sarifPath != "" {
		return renderFile(config.Reporter{Name: reporterSARIF, Path: parsed.sarifPath}, result)
	}

	return nil
}

func renderTo(writer io.Writer, reporter string, result report.Report) error {
	switch reporter {
	case reporterJSON:
		return report.WriteJSON(writer, result)
	case reporterSARIF:
		return report.WriteSARIF(writer, result, engine.RuleDescriptions())
	case reporterMTE:
		return report.WriteMTE(writer, result)
	case reporterHTML:
		return report.WriteHTML(writer, result)
	case reporterJUnit:
		return report.WriteJUnit(writer, result)
	case reporterMarkdown:
		return report.WriteMarkdown(writer, result)
	default:
		return report.WriteTerminal(writer, result)
	}
}

func renderFile(configured config.Reporter, result report.Report) error {
	file, err := os.Create(configured.Path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", configured.Path, err)
	}

	defer func() { _ = file.Close() }()

	return renderTo(file, configured.Name, result)
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
