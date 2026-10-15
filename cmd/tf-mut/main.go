// Package main exposes the tf-mut command-line entry point.
//
// The command is a thin shell over the engine: it parses flags into an engine
// request — one of the closed set of command values the seam takes — renders
// the returned report, and maps it to an exit code.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

	// The flags that belong to one command rather than to all of them, named
	// because the scoping table and the declaration both spell them.
	pinFlag        = "pin"
	answerFlagName = "answer"
	resumeFlagName = "resume"

	// The population controls, named because the declaration, the parse check
	// and the scoping tables all spell them: a misspelling in one place would
	// silently narrow nothing.
	tierFlag               = "tier"
	operatorFlag           = "operator"
	excludeOperatorFlag    = "exclude-operator"
	excludePathFlag        = "exclude-path"
	excludeResourceFlag    = "exclude-resource"
	sinceFlag              = "since"
	sampleFlagName         = "sample"
	seedFlag               = "seed"
	generatedFunctionsFlag = "generated-functions"
	packFlag               = "pack"

	// The overridable settings, named because the declaration, the override
	// mapping and the scoping tables all spell them: a misspelling in one
	// place would silently stop the flag overriding its configured scalar.
	testDirectoryFlag    = "test-directory"
	jobsFlag             = "jobs"
	timeoutFactorFlag    = "timeout-factor"
	preflightTimeoutFlag = "preflight-timeout"
	minScoreFlag         = "min-score"
	allowIncompleteFlag  = "allow-incomplete-score"
	reporterFlagName     = "reporter"

	// The remaining flags, named for the same reason now that the help table,
	// the declaration and the scoping tables all spell them: the FlagName
	// suffix marks the spellings a test file already claims with a -- prefix.
	failureTripFlag             = "failure-trip"
	allowRealInfrastructureFlag = "allow-real-infrastructure"
	allowUnsandboxedEffectsFlag = "allow-unsandboxed-effects"
	allowSampledGateFlag        = "allow-sampled-gate"
	noCacheFlagName             = "no-cache"
	failOnNewFlag               = "fail-on-new"
	writeBaselineFlagName       = "write-baseline"
	baselineFlag                = "baseline"
	sarifPathFlag               = "sarif-path"
	cpuProfileFlag              = "cpu-profile"
	otlpEndpointFlag            = "otlp-endpoint"
	outputFlagName              = "output"
	dryRunFlagName              = "dry-run"
	allVerifiedFlag             = "all-verified"
	survivorFlag                = "survivor"
	applyFlag                   = "apply"
	writeFlagName               = "write"
	forceFlag                   = "force"
	untilDryFlagName            = "until-dry"
	agentFlag                   = "agent"
	pathFlag                    = "path"

	// The help spellings: explicit help exits 0, and install is skill's only
	// subcommand.
	helpShortFlag  = "-h"
	helpLongFlag   = "--help"
	installCommand = "install"

	// The repeated metavars of the help table.
	pathMetavar = "PATH"
	idsMetavar  = "ID[,ID]"

	reporterTerminal = "terminal"
	reporterJSON     = "json"
	reporterSARIF    = "sarif"
	reporterMTE      = "mte"
	reporterHTML     = "html"
	reporterJUnit    = "junit"
	reporterMarkdown = "markdown"

	exitSuccess = 0
	exitUsage   = 2

	// generateReferenceFlag is the maintainer escape hatch the reference
	// recipe uses: it renders docs/reference/cli.md to standard output. It is
	// deliberately outside the description table, so --help never advertises it.
	generateReferenceFlag = "--generate-reference"
)

// usage is the top-level help, rendered from the description table in
// help.go rather than hand-written: the skill audit and the vocabulary audit
// read the command and flag shapes off this value.
var usage = renderTopHelp() //nolint:gochecknoglobals // the rendered help surface the audits pin.

func main() {
	os.Exit(run(os.Args[1:], version, os.Stdout, os.Stderr))
}

func run(args []string, buildVersion string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, usage)
	}

	if args[0] == generateReferenceFlag {
		// Fprint, not Fprintln: the rendered page already ends in exactly one
		// newline, and the golden test holds the committed page byte-equal.
		if _, err := fmt.Fprint(stdout, renderReferencePage()); err != nil {
			return report.ExitOperational
		}

		return exitSuccess
	}

	if args[0] == helpShortFlag || args[0] == helpLongFlag {
		return printHelp(stdout, renderTopHelp())
	}

	switch args[0] {
	case versionCommand, versionFlag:
		set := flag.NewFlagSet("tf-mut version", flag.ContinueOnError)
		set.SetOutput(stderr)
		set.Usage = func() {}

		if err := set.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return printHelp(stdout, commandHelp(versionCommand))
			}

			return exitUsage
		}

		if set.NArg() > 0 {
			return fail(stderr, "tf-mut version takes no arguments")
		}

		if _, err := fmt.Fprintln(stdout, buildinfo.Resolve(buildVersion)); err != nil {
			return report.ExitOperational
		}

		return exitSuccess
	case runCommand, previewCommand, suggestCommand, characteriseCommand,
		todosCommand, curateCommand:
		return execute(args[0], buildVersion, args[1:], stdout, stderr)
	case skillCommand:
		return skillInstall(args[1:], buildVersion, stdout, stderr)
	default:
		return fail(stderr, usage)
	}
}

// printHelp writes explicitly requested help to standard output. A failed
// write is operational, not a usage error.
func printHelp(stdout io.Writer, text string) int {
	if _, err := fmt.Fprintln(stdout, text); err != nil {
		return report.ExitOperational
	}

	return exitSuccess
}

type options struct {
	config    engine.Request
	gate      report.Gate
	reporter  string
	sarifPath string
	// reporters are the outputs `.tf-mut.hcl` asked for, merged additively with
	// the reporter flag rather than replaced by it.
	reporters []config.Reporter
}

// outputFlag collects repeatable FORMAT=PATH reporter outputs.
type outputFlag []config.Reporter

// String implements flag.Value. The flag is repeatable and its value is the
// accumulated FORMAT=PATH list, so the value's own text form is empty.
func (*outputFlag) String() string { return "" }

// Set parses one FORMAT=PATH pair and appends it to the accumulated outputs,
// refusing an unknown reporter name or a malformed pair.
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

// flagValues collects the parsed flag pointers, so declaring the surface and
// reading it can be two short functions instead of one long one. The request
// context the parser derives — the build version, the module directory, the
// flags the caller actually set, whether the score and sample flags were
// given, and the progress writer — rides on the same value, so the request
// method reads one receiver instead of a positional list of its parts.
type flagValues struct {
	testDirectory, reporter, sarifPath, tier *string
	operators, excludeOperators              *string
	excludePaths, excludeResources, since    *string
	jobs                                     *int
	timeoutFactor, minScore, sample          *float64
	preflightTimeout                         *time.Duration
	seed                                     *int64
	failureTrip                              *int
	allowIncomplete, allowReal, allowEffects *bool
	allowSampledGate, noCache                *bool
	failOnNew, writeBaseline                 *bool
	generatedFunctions                       *bool
	packs                                    *string
	baselinePath                             *string
	outputs                                  *outputFlag
	dryRun, allVerified                      *bool
	survivors, apply                         *string
	pin                                      *string
	write, force, resume, untilDry           *bool
	answers                                  *answerFlag
	cpuProfile, otlpEndpoint                 *string

	// The request context: filled in by parse once the flag set is parsed and
	// the positional argument resolved, and read by request and its helpers.
	buildVersion      string
	moduleDir         string
	given             []string
	overrides         engine.Overrides
	minScoreRequested bool
	sampleRequested   bool
	progress          io.Writer
}

// answerFlag collects repeatable todo-<id>=<value> answers.
type answerFlag []string

// String implements flag.Value. The flag is repeatable and its value is the
// accumulated answer list, so the value's own text form is empty.
func (*answerFlag) String() string { return "" }

// Set appends one todo-<id>=<value> answer to the accumulated list.
func (a *answerFlag) Set(value string) error {
	*a = append(*a, value)

	return nil
}

// helpText reads one flag's description off the help table, so the flag set
// and the rendered help share one source for what each flag means. Defaults
// stay on the flag set itself, where the renderer reads them.
func helpText(name string) string {
	return flagDocs[name].description
}

func declareFlags(set *flag.FlagSet) flagValues {
	return flagValues{
		testDirectory: set.String(testDirectoryFlag, engine.DefaultTestDirectory,
			helpText(testDirectoryFlag)),
		jobs: set.Int(jobsFlag, 0, helpText(jobsFlag)),
		timeoutFactor: set.Float64(timeoutFactorFlag, engine.DefaultTimeoutFactor,
			helpText(timeoutFactorFlag)),
		failureTrip: set.Int(failureTripFlag, engine.DefaultFailureTripThreshold,
			helpText(failureTripFlag)),
		preflightTimeout: set.Duration(preflightTimeoutFlag, 0,
			helpText(preflightTimeoutFlag)),
		minScore: set.Float64(minScoreFlag, 0, helpText(minScoreFlag)),
		allowIncomplete: set.Bool(allowIncompleteFlag, false,
			helpText(allowIncompleteFlag)),
		allowReal: set.Bool(allowRealInfrastructureFlag, false,
			helpText(allowRealInfrastructureFlag)),
		allowEffects: set.Bool(allowUnsandboxedEffectsFlag, false,
			helpText(allowUnsandboxedEffectsFlag)),
		reporter: set.String(reporterFlagName, reporterTerminal,
			helpText(reporterFlagName)),
		sarifPath:        set.String(sarifPathFlag, "", helpText(sarifPathFlag)),
		tier:             set.String(tierFlag, "", helpText(tierFlag)),
		operators:        set.String(operatorFlag, "", helpText(operatorFlag)),
		excludeOperators: set.String(excludeOperatorFlag, "", helpText(excludeOperatorFlag)),
		excludePaths:     set.String(excludePathFlag, "", helpText(excludePathFlag)),
		excludeResources: set.String(excludeResourceFlag, "", helpText(excludeResourceFlag)),
		since:            set.String(sinceFlag, "", helpText(sinceFlag)),
		sample:           set.Float64(sampleFlagName, 0, helpText(sampleFlagName)),
		seed:             set.Int64(seedFlag, 0, helpText(seedFlag)),
		allowSampledGate: set.Bool(allowSampledGateFlag, false,
			helpText(allowSampledGateFlag)),
		noCache: set.Bool(noCacheFlagName, false, helpText(noCacheFlagName)),
		failOnNew: set.Bool(failOnNewFlag, false,
			helpText(failOnNewFlag)),
		writeBaseline: set.Bool(writeBaselineFlagName, false,
			helpText(writeBaselineFlagName)),
		baselinePath: set.String(baselineFlag, "", helpText(baselineFlag)),
		generatedFunctions: set.Bool(generatedFunctionsFlag, false,
			helpText(generatedFunctionsFlag)),
		packs:   set.String(packFlag, "", helpText(packFlag)),
		outputs: declareOutputFlag(set),
		dryRun: set.Bool(dryRunFlagName, false,
			helpText(dryRunFlagName)),
		allVerified: set.Bool(allVerifiedFlag, false, helpText(allVerifiedFlag)),
		survivors:   set.String(survivorFlag, "", helpText(survivorFlag)),
		apply:       set.String(applyFlag, "", helpText(applyFlag)),
		pin:         set.String(pinFlag, "", helpText(pinFlag)),
		write:       set.Bool(writeFlagName, false, helpText(writeFlagName)),
		force:       set.Bool(forceFlag, false, helpText(forceFlag)),
		resume: set.Bool(resumeFlagName, false,
			helpText(resumeFlagName)),
		untilDry: set.Bool(untilDryFlagName, false,
			helpText(untilDryFlagName)),
		answers: declareAnswerFlag(set),
		cpuProfile: set.String(cpuProfileFlag, "",
			helpText(cpuProfileFlag)),
		otlpEndpoint: set.String(otlpEndpointFlag, "",
			helpText(otlpEndpointFlag)),
	}
}

func declareAnswerFlag(set *flag.FlagSet) *answerFlag {
	answers := &answerFlag{}
	set.Var(answers, answerFlagName, helpText(answerFlagName))

	return answers
}

func declareOutputFlag(set *flag.FlagSet) *outputFlag {
	outputs := &outputFlag{}
	set.Var(outputs, outputFlagName, helpText(outputFlagName))

	return outputs
}

// declareSkillFlags declares the install subcommand's flags from the help
// table, sharing one source with the skill help the renderer prints.
func declareSkillFlags(set *flag.FlagSet) (agent, path *string, force *bool) {
	agent = set.String(agentFlag, skill.AgentClaude, helpText(agentFlag))
	path = set.String(pathFlag, ".", helpText(pathFlag))
	force = set.Bool(forceFlag, false, skillFlagDocs[forceFlag].description)

	return agent, path, force
}

// overrideOf maps a flag onto the engine setting its explicit value
// overrides. It is the whole translation between the command line's vocabulary
// and the engine's setting names: the engine sees the setting, never the flag.
//
//nolint:gochecknoglobals // an immutable table.
var overrideOf = map[string]engine.Setting{
	testDirectoryFlag:    engine.SettingTestDirectory,
	jobsFlag:             engine.SettingJobs,
	timeoutFactorFlag:    engine.SettingTimeoutFactor,
	preflightTimeoutFlag: engine.SettingPreflightTimeout,
	minScoreFlag:         engine.SettingMinScore,
	allowIncompleteFlag:  engine.SettingAllowIncompleteScore,
	tierFlag:             engine.SettingTier,
	reporterFlagName:     engine.SettingReporter,
}

func parse(command, buildVersion string, args []string, stderr io.Writer) (options, error) {
	set := flag.NewFlagSet("tf-mut "+command, flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() {}

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
	overrides := engine.Overrides{}

	set.Visit(func(flagged *flag.Flag) {
		given = append(given, flagged.Name)

		if setting, overridden := overrideOf[flagged.Name]; overridden {
			overrides[setting] = true
		}

		if flagged.Name == minScoreFlag {
			requested = true
		}

		if flagged.Name == sampleFlagName {
			sampled = true
		}
	})

	if err := refuseInapplicableFlags(command, given); err != nil {
		return options{}, err
	}

	if err := refuseUncarryingReporter(command, *values.reporter); err != nil {
		return options{}, err
	}

	values.buildVersion = buildVersion
	values.moduleDir = moduleDir
	values.given = given
	values.overrides = overrides
	values.minScoreRequested = requested
	values.sampleRequested = sampled
	values.progress = stderr

	return options{
		config: values.request(command),
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

// errInapplicableFlag reports a flag the named command does not act on.
var errInapplicableFlag = errors.New("flag does not apply to this command")

// errUncarryingReporter reports a reporter that cannot carry what the command
// produces.
var errUncarryingReporter = errors.New(
	"reporter cannot carry a characterisation: use --reporter json or terminal",
)

// gateFlags are the acceptance controls. The Gate embed of the run, suggest
// and curate requests is their only carrier, so they are scoped flags: a
// command whose request has no gate — preview describes a population it never
// scores; characterise and todos produce no population at all — refuses them
// at the parser instead of accepting and ignoring them.
//
//nolint:gochecknoglobals // an immutable list.
var gateFlags = []string{
	minScoreFlag, allowIncompleteFlag, allowSampledGateFlag,
	failOnNewFlag, writeBaselineFlagName, baselineFlag,
}

// commandFlags names the flags each command acts on, for the flags that belong
// to one command and are declared on the set every command shares. A flag is
// in a command's row exactly when that command's engine request has a field
// for it, so a flag spelled on a command that could not carry it is refused by
// the parser rather than accepted and silently ignored — the run, preview and
// suggest rows name the population controls their requests' Population embed
// carries, and curate's row names its Gate and NoCache fields.
//
// A flag accepted and ignored is worse than one refused: `--write` and
// `--force` both name write behaviour and were silently no-ops under `run`,
// `--apply` let a caller request a destructive action from `curate` and
// receive an ordinary report, and `--min-score` promised preview a gate over
// a population it never executes. This repository already refuses misapplied
// flags elsewhere; the table makes the two agree.
//
//nolint:gochecknoglobals // an immutable table.
var commandFlags = map[string]map[string]bool{
	runCommand:     flagSet(gradingFlags()...),
	previewCommand: flagSet(populationFlags...),
	suggestCommand: flagSet(append(suggestOnlyFlags, gradingFlags()...)...),
	characteriseCommand: flagSet(
		writeFlagName, forceFlag, pinFlag, untilDryFlagName, answerFlagName, resumeFlagName,
	),
	todosCommand:  flagSet(pinFlag, answerFlagName, resumeFlagName),
	curateCommand: flagSet(append([]string{noCacheFlagName}, gateFlags...)...),
}

// suggestOnlyFlags are the suggest command's own controls, which no other
// request has a field for.
//
//nolint:gochecknoglobals // an immutable list.
var suggestOnlyFlags = []string{applyFlag, allVerifiedFlag, survivorFlag, dryRunFlagName}

// gradingFlags is what the run and suggest requests share beyond the
// population controls: the acceptance gate and the cache switch.
func gradingFlags() []string {
	return append(append([]string{noCacheFlagName}, gateFlags...), populationFlags...)
}

// populationFlags are the controls that select or narrow the mutant population;
// only the grading commands' requests carry a Population embed.
//
//nolint:gochecknoglobals // an immutable list.
var populationFlags = []string{
	tierFlag, operatorFlag, excludeOperatorFlag,
	excludePathFlag, excludeResourceFlag, sinceFlag,
	sampleFlagName, seedFlag, generatedFunctionsFlag, packFlag,
}

// scopedFlags is every flag that belongs to some command rather than to all of
// them: the union of the commandFlags rows. A flag outside this set applies
// everywhere and is never refused.
//
//nolint:gochecknoglobals // an immutable set.
var scopedFlags = func() map[string]bool {
	scoped := map[string]bool{}

	for _, acted := range commandFlags {
		for name := range acted {
			scoped[name] = true
		}
	}

	return scoped
}()

func flagSet(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))

	for _, name := range names {
		set[name] = true
	}

	return set
}

func refuseInapplicableFlags(command string, given []string) error {
	acted := commandFlags[command]

	for _, name := range given {
		if scopedFlags[name] && !acted[name] {
			return fmt.Errorf("%w: --%s is not a %s flag", errInapplicableFlag, name, command)
		}
	}

	return nil
}

// refuseUncarryingReporter keeps the characterisation commands to the two
// reporters that publish what they produce.
//
// The generated suite lives in `Characterisation.Files`, which the SARIF, MTE,
// HTML, JUnit and Markdown adapters do not carry: `characterise --reporter
// markdown` wrote nothing, returned no suite, and exited as though it had
// succeeded. Refusing is the honest half of the choice — teaching five
// adapters to carry a generated suite is a schema decision, not a flag one.
func refuseUncarryingReporter(command, reporter string) error {
	switch command {
	case characteriseCommand, todosCommand, curateCommand:
	default:
		return nil
	}

	if reporter == reporterTerminal || reporter == reporterJSON {
		return nil
	}

	return fmt.Errorf("%w: %s does not carry one, and %s produces one",
		errUncarryingReporter, reporter, command)
}

// request maps the parsed flags onto the engine's closed request set.
// Each command builds its own request: a request with no field for a control
// is what makes the parser's refusal of the corresponding flag complete, so
// the mode booleans a legacy input once carried are gone — the request's type
// is the command.
func (values flagValues) request(command string) engine.Request {
	switch command {
	case characteriseCommand:
		return engine.CharacteriseRequest{
			Common:   values.common(),
			PinRung:  *values.pin,
			Write:    *values.write,
			Force:    *values.force,
			UntilDry: *values.untilDry,
			Resume:   *values.resume,
			Answers:  *values.answers,
		}

	case todosCommand:
		return engine.TodosRequest{
			Common:  values.common(),
			PinRung: *values.pin,
			Answers: *values.answers,
			Resume:  *values.resume,
		}

	case curateCommand:
		return engine.CurateRequest{
			Common:  values.common(),
			Gate:    values.gate(),
			NoCache: *values.noCache,
		}

	case previewCommand:
		return engine.PreviewRequest{
			Common:     values.common(),
			Population: values.population(),
		}

	case suggestCommand:
		return engine.SuggestRequest{
			Common:      values.common(),
			Population:  values.population(),
			Gate:        values.gate(),
			NoCache:     *values.noCache,
			DryRun:      *values.dryRun,
			SurvivorIDs: commaSeparated(*values.survivors),
			Apply:       commaSeparated(*values.apply),
			ApplyAll:    *values.allVerified,
		}

	default:
		return engine.RunRequest{
			Common:     values.common(),
			Population: values.population(),
			Gate:       values.gate(),
			NoCache:    *values.noCache,
		}
	}
}

// population projects the population flags onto the Population embed
// the run, preview and suggest requests carry.
func (values flagValues) population() engine.Population {
	return engine.Population{
		Tier:               mutation.Tier(*values.tier),
		IncludeOperators:   commaSeparated(*values.operators),
		ExcludeOperators:   commaSeparated(*values.excludeOperators),
		ExcludePaths:       commaSeparated(*values.excludePaths),
		ExcludeResources:   commaSeparated(*values.excludeResources),
		Since:              *values.since,
		SamplePercent:      *values.sample,
		HasSample:          values.sampleRequested,
		SampleSeed:         *values.seed,
		GeneratedFunctions: *values.generatedFunctions,
		Packs:              commaSeparated(*values.packs),
	}
}

// gate projects the acceptance flags onto the Gate embed the run,
// suggest and curate requests carry.
func (values flagValues) gate() engine.Gate {
	return engine.Gate{
		MinScore:             *values.minScore,
		HasMinScore:          values.minScoreRequested,
		AllowIncompleteScore: *values.allowIncomplete,
		AllowSampledGate:     *values.allowSampledGate,
		FailOnNew:            *values.failOnNew,
		WriteBaseline:        *values.writeBaseline,
		BaselinePath:         *values.baselinePath,
	}
}

// common projects the flags every command shares onto the Common embed
// the typed requests carry.
func (values flagValues) common() engine.Common {
	return engine.Common{
		ModuleDir:               values.moduleDir,
		TestDirectory:           *values.testDirectory,
		Jobs:                    *values.jobs,
		TimeoutFactor:           *values.timeoutFactor,
		FailureTripThreshold:    *values.failureTrip,
		TimeoutFloor:            0,
		PreflightTimeout:        *values.preflightTimeout,
		AllowRealInfrastructure: *values.allowReal,
		AllowUnsandboxedEffects: *values.allowEffects,
		TerraformBinary:         "",
		Env:                     nil,
		WorkDir:                 "",
		ToolVersion:             buildinfo.Resolve(values.buildVersion),
		Overrides:               values.overrides,
		CPUProfile:              *values.cpuProfile,
		OTLPEndpoint:            *values.otlpEndpoint,
		Progress:                values.progress,
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
	set := flag.NewFlagSet("tf-mut skill install", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() {}

	if len(args) == 0 || args[0] != installCommand {
		if err := set.Parse(args); errors.Is(err, flag.ErrHelp) {
			return printHelp(stdout, commandHelp(skillCommand))
		}

		return fail(stderr, "usage: tf-mut skill install [--agent claude|generic] [--path .] [--force]")
	}

	agent, path, force := declareSkillFlags(set)

	if err := set.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp(stdout, commandHelp(skillCommand))
		}

		return report.ExitOperational
	}

	if set.NArg() > 0 {
		return fail(stderr, "tf-mut skill install takes no positional arguments")
	}

	results, err := skill.Install(*path, *agent, buildinfo.Resolve(buildVersion), *force)

	// What landed is printed whether or not the install completed: a partial
	// install has already changed the caller's tree, and an error alone would
	// not say which files it changed.
	for _, result := range results {
		if _, printErr := fmt.Fprintf(stdout, "%s: %s\n", result.Path, result.Outcome); printErr != nil {
			return report.ExitOperational
		}
	}

	if err != nil {
		return fail(stderr, "tf-mut: "+err.Error())
	}

	return exitSuccess
}

func execute(command, buildVersion string, args []string, stdout, stderr io.Writer) int {
	parsed, err := parse(command, buildVersion, args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp(stdout, commandHelp(command))
		}

		return fail(stderr, err.Error())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	result, err := engine.New(engine.DefaultDependencies()).Run(ctx, parsed.config)
	if err == nil {
		err = ctx.Err()
	}
	stop()
	if err != nil {
		return fail(stderr, "tf-mut: "+renderError(err))
	}

	if err := render(stdout, parsed, result); err != nil {
		message := "tf-mut: " + strings.TrimRight(err.Error(), "\n")
		if result.Telemetry != nil {
			prefix := "[" + result.Telemetry.RunID + "] "
			message = prefix + strings.ReplaceAll(message, "\n", "\n"+prefix)
		}

		return fail(stderr, message)
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

// renderError spells the command-line vocabulary an engine refusal's reason
// leaves implicit. The engine reports the failure; the flag that selects the
// mode or remedies it is this package's to name, so every dashed spelling a
// refusal prints is assembled here and nowhere else.
func renderError(err error) string {
	switch {
	case errors.Is(err, engine.ErrSinceRef),
		errors.Is(err, engine.ErrSinceRepository),
		errors.Is(err, engine.ErrSinceConflict):
		return "--since " + err.Error()
	case errors.Is(err, engine.ErrSampleRange):
		return "--sample " + err.Error()
	case errors.Is(err, engine.ErrSampledGate):
		return "a sampled run is non-authoritative and cannot satisfy a gate without --allow-sampled-gate"
	default:
		return err.Error()
	}
}
