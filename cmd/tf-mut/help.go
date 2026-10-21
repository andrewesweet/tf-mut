package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// This file owns the command-line help surface. One description table — the
// command entries below and the flag entries beside them — renders the
// top-level --help, every per-command -h, and docs/reference/cli.md, so the
// three cannot drift apart the way the hand-written usage constant and Go's
// flag dump did. Flag defaults are read off a live flag set built by
// declareFlags, never retyped; the few flags whose zero value is not their
// default name the engine value they stand for instead.

// commandDoc is one row of the description table: everything --help, -h and
// the reference page say about a command.
type commandDoc struct {
	name        string
	synopsis    string
	description string
	arguments   string
	example     string
}

// commandDocs is the table in command-list order. The synopses are the
// command list --help has always printed; the skill audit test holds them to
// the binary, so a renamed command turns that test red.
var commandDocs = []commandDoc{ //nolint:gochecknoglobals // the immutable help table.
	{
		name:     runCommand,
		synopsis: "Mutate the module at PATH and report which resources are pseudo-tested",
		description: "Runs the full mutation loop: generate the mutant population, " +
			"execute the test suite against each mutant in an isolated sandbox, " +
			"and score what the suite observes. Findings fail the run, and " +
			"--min-score turns the score into a gate.",
		arguments: "PATH  The module directory to mutate (default \".\")",
		example:   "tf-mut run --min-score 80 ./modules/vpc",
	},
	{
		name:     previewCommand,
		synopsis: "List the mutants that would be generated, as diffs",
		description: "Prints the mutant population as unified diffs against the module " +
			"source. No test run blocks execute.",
		arguments: "PATH  The module directory to preview (default \".\")",
		example:   "tf-mut preview ./modules/vpc",
	},
	{
		name:     suggestCommand,
		synopsis: "Generate, verify and optionally apply the assertions that kill the survivors",
		description: "Diffs each survivor against the baseline, renders the candidate " +
			"assertion, and verifies it by re-running the suite. --dry-run prints " +
			"candidates and verifies nothing; --apply and --all-verified write " +
			"verified assertions to the test files.",
		arguments: "PATH  The module directory to suggest for (default \".\")",
		example:   "tf-mut suggest --dry-run ./modules/vpc",
	},
	{
		name:     characteriseCommand,
		synopsis: "Scaffold, harvest and pin a first test suite for a module that has none",
		description: "Plans a scaffold suite, runs it against the module to harvest the " +
			"observed values, and pins them as assertions. Values no deterministic " +
			"pipeline can synthesise become judgement points for --answer " +
			"instead of guesses.",
		arguments: "PATH  The module directory to characterise (default \".\")",
		example:   "tf-mut characterise ./modules/vpc",
	},
	{
		name:     todosCommand,
		synopsis: "List the open judgement points characterisation refuses to guess at",
		description: "Reports every open judgement point with the identifier --answer " +
			"needs to resolve it.",
		arguments: "PATH  The module directory to list judgement points for (default \".\")",
		example:   "tf-mut todos ./modules/vpc",
	},
	{
		name:     curateCommand,
		synopsis: "Report redundant assertions from a full, authoritative population",
		description: "Re-checks redundancy against a full, unsampled population: a " +
			"narrowed population is refused rather than reported as the whole.",
		arguments: "PATH  The module directory to curate (default \".\")",
		example:   "tf-mut curate ./modules/vpc",
	},
	{
		name:     skillCommand,
		synopsis: "Install the shipped agent skills (skill install [--agent claude|generic] [--path .])",
		description: "Places the mutation-loop and characterisation-loop skills for the " +
			"chosen agent, preserving user-edited files unless --force is given.",
		arguments: "install  The only skill subcommand: install the skills into the project at --path",
		example:   "tf-mut skill install --agent generic --path .",
	},
	{
		name:        versionCommand,
		synopsis:    "Print the build version",
		description: "Prints the linked build version, or \"dev\" when none was supplied.",
		arguments:   "(none \u2014 version takes no path)",
		example:     "tf-mut version",
	},
}

// flagDoc is one row of the flag side of the table: the presentation of a
// flag, keyed by the flag's name. The default is deliberately absent: it is
// read off the live flag set at render time, so a changed default cannot
// leave stale help behind.
type flagDoc struct {
	metavar     string
	description string
}

// flagDocs names every flag declareFlags declares, with its presentation.
// TestHelpTableCoversEveryDeclaredFlag holds the two to each other.
var flagDocs = map[string]flagDoc{ //nolint:gochecknoglobals // the immutable help table.
	testDirectoryFlag: {pathMetavar, "Test directory relative to the module"},
	jobsFlag:          {"N", "Mutants to execute concurrently"},
	timeoutFactorFlag: {"F", "Multiple of the baseline run time"},
	preflightTimeoutFlag: {
		"D",
		"Absolute cap on preflight Terraform calls: version, init, providers schema, fmt, validate and " +
			"the baseline runs",
	},
	allowRealInfrastructureFlag: {"", "Permit execution against unmocked providers"},
	allowUnsandboxedEffectsFlag: {"", "Permit apply-mode provisioners and unsevered data sources"},
	reporterFlagName: {
		"FORMAT",
		"terminal|json|sarif|mte|html|junit|markdown; characterise, todos and curate accept only terminal|json",
	},
	outputFlagName: {
		"FORMAT=PATH",
		"Write an additional reporter from the same run; repeatable — every output derives from one " +
			"report value",
	},
	sarifPathFlag:  {pathMetavar, "Where to write the SARIF document"},
	cpuProfileFlag: {pathMetavar, "Write a CPU profile of tf-mut itself"},
	otlpEndpointFlag: {
		"URL",
		"Export run, mutant and phase spans over OTLP/HTTP (OTEL_EXPORTER_OTLP_ENDPOINT is honoured when " +
			"unset)",
	},
	minScoreFlag: {"N", "Fail below this mutation score percentage"},
	failureTripFlag: {
		"N",
		"Stop the run after this many consecutive operational failures; the report names the trip and the " +
			"first failure, and the score is marked incomplete",
	},
	allowIncompleteFlag: {
		"",
		"Let an untrustworthy score satisfy --min-score (timeout, consecutive-failure trip, " +
			"unexecuted mutants, execution errors, nothing scored, Invalid-dominated)",
	},

	allowSampledGateFlag:  {"", "Let a sampled run satisfy --min-score (unsafe)"},
	noCacheFlagName:       {"", "Disable the project-local verdict cache"},
	failOnNewFlag:         {"", "Fail on findings the baseline does not accept"},
	writeBaselineFlagName: {"", "Accept the current findings as the baseline"},
	baselineFlag:          {pathMetavar, "Baseline file"},
	tierFlag:              {"LEVEL", "Operator breadth: smoke, standard or deep"},
	sinceFlag: {
		"REF",
		"Run only mutants in configuration changed since REF, including staged, unstaged and untracked " +
			"changes",
	},
	sampleFlagName:         {"N", "Run a deterministic N% sample (non-authoritative)"},
	seedFlag:               {"N", "Seed for --sample"},
	generatedFunctionsFlag: {"", "Opt in to the generated function-family operators"},
	packFlag: {
		"NAME[,NAME]",
		"Enable these domain packs by name: a shipped pack such as security-aws, or one registered in " +
			".tf-mut.hcl; merged as a union with the configured packs list",
	},
	operatorFlag:        {idsMetavar, "Restrict generation to these operators"},
	excludeOperatorFlag: {idsMetavar, "Remove operators from the population"},
	excludePathFlag:     {"GLOB[,GLOB]", "Remove sites in matching files"},
	excludeResourceFlag: {"ADDR[,..]", "Remove sites in matching resources"},
	pinFlag:             {"LEVEL", "Pinning granularity: outputs, counts or configured"},
	writeFlagName:       {"", "Place the verified suite in the test directory"},
	forceFlag:           {"", "Replace generated files nobody has edited"},
	untilDryFlagName:    {"", "Iterate scaffold, mutate and pin until the survivors stop yielding new assertions"},
	answerFlagName:      {"todo-ID=VALUE", "Answer one judgement point; repeatable"},
	resumeFlagName: {
		"",
		"Read answered judgement points from the edited artefact, re-synthesise, " +
			"verify and promote",
	},

	dryRunFlagName:  {"", "Print the candidate patches and verify nothing"},
	survivorFlag:    {idsMetavar, "Suggest only for these survivor identifiers"},
	applyFlag:       {idsMetavar, "Apply these verified suggestions to the test files"},
	allVerifiedFlag: {"", "Apply every verified suggestion"},
	agentFlag:       {"claude|generic", "Target agent for the installed skills"},
	pathFlag:        {"DIR", "The project root to install into"},
}

const skillForceDescription = "Overwrite user-edited skill files"

var skillFlagDocs = map[string]flagDoc{ //nolint:gochecknoglobals // the immutable help table.
	forceFlag: {"", skillForceDescription},
}

// hiddenDefault names the flags whose zero value means unset rather than a
// default: min-score 0 is no gate, sample 0 is no sampling. Every other flag
// renders its live default from the flag set.
var hiddenDefault = map[string]bool{ //nolint:gochecknoglobals // an immutable set.
	minScoreFlag:   true,
	sampleFlagName: true,
}

// flagGroup is one headed flag block of the top-level help.
type flagGroup struct {
	header string
	flags  []string
}

// flagGroups is the top-level help's flag order. The first header names the
// six report-producing commands; the vocabulary audit test reads that list
// off this rendering, so the header must keep its shape.
var flagGroups = []flagGroup{ //nolint:gochecknoglobals // the immutable help table.
	{
		header: "Flags for run, preview, suggest, characterise, todos and curate:",
		flags: []string{
			testDirectoryFlag, jobsFlag, timeoutFactorFlag, preflightTimeoutFlag,
			allowRealInfrastructureFlag, allowUnsandboxedEffectsFlag,
			reporterFlagName, outputFlagName, sarifPathFlag, cpuProfileFlag, otlpEndpointFlag,
		},
	},
	{
		header: "Flags for run, suggest and curate:",
		flags: []string{
			minScoreFlag, failureTripFlag, allowIncompleteFlag, allowSampledGateFlag,
			noCacheFlagName, failOnNewFlag, writeBaselineFlagName, baselineFlag,
		},
	},
	{
		header: "Flags for run, preview and suggest:",
		flags: []string{
			tierFlag, sinceFlag, sampleFlagName, seedFlag, generatedFunctionsFlag,
			packFlag, operatorFlag, excludeOperatorFlag, excludePathFlag,
			excludeResourceFlag,
		},
	},
	{
		header: "Flags for characterise:",
		flags: []string{
			pinFlag, writeFlagName, forceFlag, untilDryFlagName, answerFlagName, resumeFlagName,
		},
	},
	{
		header: "Flags for suggest:",
		flags:  append([]string{}, suggestOnlyFlags...),
	},
}

// skillFlags is the install subcommand's flag order.
var skillFlags = []string{agentFlag, pathFlag, forceFlag} //nolint:gochecknoglobals // an immutable list.

// exitCodes renders the three exit codes the command-line contract promises,
// with their numbers read off the report constants rather than retyped.
func exitCodes() string {
	return fmt.Sprintf("Exit codes:\n"+
		"  %d  The command completed with nothing to report (explicit --help exits %d)\n"+
		"  %d  The command completed and found something worth failing on\n"+
		"  %d  The command could not produce a trustworthy result, or the invocation "+
		"was wrong: a bad flag, an unknown command, or a flag after the module path",
		report.ExitClean, exitSuccess, report.ExitFindings, report.ExitOperational)
}

// liveFlags builds the flag set the renderer reads defaults off. It is the
// same declaration the parser uses, so help and parsing share one surface.
func liveFlags() *flag.FlagSet {
	set := flag.NewFlagSet("tf-mut", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	declareFlags(set)

	return set
}

// liveSkillFlags builds the install subcommand's flag set for the same
// purpose: the skill flags live on their own FlagSet because install parses
// them separately.
func liveSkillFlags() *flag.FlagSet {
	set := flag.NewFlagSet("tf-mut skill install", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	declareSkillFlags(set)

	return set
}

// flagLookup reads one flag's live declaration from whichever set declares
// it, so defaults render for engine and skill flags alike.
func flagLookup(command string) func(string) *flag.Flag {
	if command == skillCommand {
		return liveSkillFlags().Lookup
	}

	return liveFlags().Lookup
}

// flagDefault renders one flag's default from the live flag set. The five
// named cases are the flags whose zero value is not their default: each names
// the engine value it stands for, so the text follows the code. Booleans and
// the hidden unset-zero flags carry no suffix.
func flagDefault(name string, lookup func(string) *flag.Flag) string {
	switch name {
	case jobsFlag:
		return " (default " + engine.DefaultJobsDescription + ")"
	case preflightTimeoutFlag:
		return " (default " + shortDuration(engine.DefaultInitTimeout) +
			" for init, " + shortDuration(engine.DefaultPreflightTimeout) + " for the rest)"
	case tierFlag:
		return " (default " + string(mutation.TierStandard) + ")"
	case pinFlag:
		return " (default " + string(characterise.RungOutputs) +
			"; a module with no outputs escalates)"
	case baselineFlag:
		return " (default " + strconv.Quote(engine.DefaultBaselineName) + ")"
	}

	if hiddenDefault[name] {
		return ""
	}

	found := lookup(name)
	if found == nil || found.DefValue == "" {
		return ""
	}

	if booleanFlag(found) {
		return ""
	}

	if _, err := strconv.ParseFloat(found.DefValue, 64); err == nil {
		return " (default " + found.DefValue + ")"
	}

	if name == reporterFlagName {
		return " (default " + found.DefValue + ")"
	}

	return " (default " + strconv.Quote(found.DefValue) + ")"
}

// booleanFlag reports whether the flag takes no value, in which case no
// default is rendered.
func booleanFlag(found *flag.Flag) bool {
	boolean, ok := found.Value.(interface{ IsBoolFlag() bool })

	return ok && boolean.IsBoolFlag()
}

// shortDuration renders whole-minute engine timeouts the way help states
// them: "10m", not "10m0s". It is intentionally narrow — it only shortens a
// trailing zero-seconds component — so an unexpected timeout shape stays
// verbatim rather than rendering as something it is not.
func shortDuration(timeout time.Duration) string {
	if strings.HasSuffix(timeout.String(), "m0s") {
		return strings.TrimSuffix(timeout.String(), "0s")
	}

	return timeout.String()
}

// helpPadding separates a padded name from its description in help output.
const helpPadding = 2

// renderFlag renders one flag line: two-space indent, double-dash name,
// metavar, padded description, live default.
func renderFlag(name string, lookup func(string) *flag.Flag, width int) string {
	doc := flagDocs[name]

	line := "  --" + name
	if doc.metavar != "" {
		line += " " + doc.metavar
	}

	return line + strings.Repeat(" ", width-len(line)+helpPadding) +
		lookup(name).Usage + flagDefault(name, lookup)
}

// flagWidth is the display width flag lines align their descriptions to.
func flagWidth(names []string) int {
	width := 0

	for _, name := range names {
		line := "  --" + name
		if flagDocs[name].metavar != "" {
			line += " " + flagDocs[name].metavar
		}

		width = max(width, len(line))
	}

	return width
}

// renderTopHelp renders the top-level --help from the table: the usage line,
// the command list, the flag groups with live defaults, one example per
// command, and the three exit codes. The examples are four-space indented so
// the vocabulary audit's two-space command pattern keeps reading only the
// command list.
func renderTopHelp() string {
	lookup := flagLookup("")

	var rendered strings.Builder

	rendered.WriteString("usage: tf-mut <command> [flags] [PATH]\n")
	rendered.WriteString("\nMutation testing for Terraform modules: " +
		"score what the test suite observes, then prove the gaps.\n")
	rendered.WriteString("\nCommands:\n")

	for _, doc := range commandDocs {
		rendered.WriteString("  " + doc.name +
			strings.Repeat(" ", commandWidth()-len(doc.name)+helpPadding) +
			doc.synopsis + "\n")
	}

	for _, group := range flagGroups {
		rendered.WriteString("\n" + group.header + "\n")

		width := flagWidth(group.flags)

		for _, name := range group.flags {
			rendered.WriteString(renderFlag(name, lookup, width) + "\n")
		}
	}

	rendered.WriteString("\nExamples:\n")

	for _, doc := range commandDocs {
		rendered.WriteString("    $ " + doc.example + "\n")
	}

	rendered.WriteString("\n" + exitCodes() + "\n")
	rendered.WriteString("\nSettings are also readable from .tf-mut.hcl at the module root. " +
		"A flag given on the\ncommand line overrides the configured value of that scalar " +
		"and nothing else.\n")
	rendered.WriteString("The full command-line reference is docs/reference/cli.md.\n")

	return strings.TrimRight(rendered.String(), "\n")
}

// commandWidth is the display width the command list aligns to.
func commandWidth() int {
	width := 0

	for _, doc := range commandDocs {
		width = max(width, len(doc.name))
	}

	return width
}

// applicableFlags lists the flags a command acts on, in canonical group
// order: every universal flag plus exactly the scoped flags the parser's
// commandFlags row accepts. It is computed from the parser's own tables, so
// per-command help cannot list a flag the command refuses. Version takes no
// flags, and skill takes only the install subcommand's own.
func applicableFlags(command string) []string {
	if command == versionCommand {
		return nil
	}

	if command == skillCommand {
		return append([]string{}, skillFlags...)
	}

	var names []string

	for _, group := range flagGroups {
		for _, name := range group.flags {
			if !scopedFlags[name] || commandFlags[command][name] {
				names = append(names, name)
			}
		}
	}

	return names
}

// commandHelp renders one command's -h from the same table: usage, synopsis,
// description, arguments, applicable flags with live defaults, one example,
// and the three exit codes.
func commandHelp(command string) string {
	lookup := flagLookup(command)

	var doc commandDoc

	for _, entry := range commandDocs {
		if entry.name == command {
			doc = entry
		}
	}

	var rendered strings.Builder

	var arguments string

	switch command {
	case skillCommand:
		arguments = "install [flags]"
	case versionCommand:
		arguments = ""
	default:
		arguments = "[flags] [PATH]"
	}

	usageLine := "usage: tf-mut " + command
	if arguments != "" {
		usageLine += " " + arguments
	}

	rendered.WriteString(usageLine + "\n")
	rendered.WriteString("\n" + doc.synopsis + "\n")
	rendered.WriteString("\n" + doc.description + "\n")
	rendered.WriteString("\nArguments:\n  " + doc.arguments + "\n")

	names := applicableFlags(command)
	if len(names) == 0 {
		rendered.WriteString("\nFlags: none — this command takes no flags.\n")
	} else {
		rendered.WriteString("\nFlags:\n")

		width := flagWidth(names)

		for _, name := range names {
			rendered.WriteString(renderFlag(name, lookup, width) + "\n")
		}
	}

	rendered.WriteString("\nExample:\n    $ " + doc.example + "\n")
	rendered.WriteString("\n" + exitCodes() + "\n")

	return strings.TrimRight(rendered.String(), "\n")
}

// renderReferencePage renders docs/reference/cli.md from the same table: one
// section per command with usage, synopsis, description, arguments, flags
// with defaults, and an example, plus the three exit codes once at the end.
func renderReferencePage() string {
	var rendered strings.Builder

	rendered.WriteString("# Command-line reference\n")
	rendered.WriteString("\nGenerated from the binary's help table: `tf-mut --help` and every " +
		"`tf-mut <command> -h` render from the same source, and a golden test fails " +
		"when this page drifts from it. Regenerate with `just reference`.\n")

	for _, doc := range commandDocs {
		lookup := flagLookup(doc.name)

		rendered.WriteString("\n## " + doc.name + "\n")

		var arguments string

		switch doc.name {
		case skillCommand:
			arguments = "install [flags]"
		case versionCommand:
			arguments = ""
		default:
			arguments = "[flags] [PATH]"
		}

		usageLine := "`tf-mut " + doc.name
		if arguments != "" {
			usageLine += " " + arguments
		}

		rendered.WriteString("\n" + usageLine + "`\n")
		rendered.WriteString("\n" + doc.synopsis + "\n")
		rendered.WriteString("\n" + doc.description + "\n")
		rendered.WriteString("\nArguments: `" + doc.arguments + "`\n")

		names := applicableFlags(doc.name)
		if len(names) == 0 {
			rendered.WriteString("\nTakes no flags.\n")
		} else {
			rendered.WriteString("\n| Flag | Description | Default |\n")
			rendered.WriteString("| --- | --- | --- |\n")

			for _, name := range names {
				cells := []string{
					"`--" + name + flagMetavar(name) + "`",
					lookup(name).Usage,
					referenceDefault(name, lookup),
				}
				for i, cell := range cells {
					cells[i] = strings.ReplaceAll(cell, "|", "\\|")
				}

				rendered.WriteString("| " + strings.Join(cells, " | ") + " |\n")
			}
		}

		rendered.WriteString("\nExample:\n```bash\n$ " + doc.example + "\n```\n")
	}

	rendered.WriteString("\n## Debug logging\n")
	rendered.WriteString("\nSet `TF_MUT_DEBUG=1` to write debug logs to standard error, including " +
		"the verdict cache key and each component's digest. Compare component digests between " +
		"invocations to identify what changed. Each line carries the report's `[run_id]` prefix. " +
		"Logs contain digests, never cache-key input values. " +
		"Debug logging is disabled by default; report output on standard output is unchanged.\n")
	rendered.WriteString("\n```bash\nTF_MUT_DEBUG=1 tf-mut suggest ./modules/vpc\n```\n")

	rendered.WriteString("\n## Exit codes\n")
	rendered.WriteString("\n" + exitCodeProse() + "\n")

	return strings.TrimRight(rendered.String(), "\n") + "\n"
}

// flagMetavar renders the reference table's metavar suffix for one flag.
func flagMetavar(name string) string {
	if flagDocs[name].metavar == "" {
		return ""
	}

	return " " + flagDocs[name].metavar
}

// referenceDefault renders the reference table's default cell from the live
// flag set: the same default the terminal help shows, or an em dash where the
// terminal help shows none.
func referenceDefault(name string, lookup func(string) *flag.Flag) string {
	display := strings.TrimPrefix(flagDefault(name, lookup), " (default ")
	display = strings.TrimSuffix(display, ")")

	if display == "" {
		return "—"
	}

	return "`" + display + "`"
}

// exitCodeProse states the three exit codes for the reference page, with
// their numbers from the report constants.
func exitCodeProse() string {
	return fmt.Sprintf("`%d` — the command completed with nothing to report "+
		"(explicit `--help` exits `%d`).\n\n`%d` — the command completed and found "+
		"something worth failing on.\n\n`%d` — the command could not produce a "+
		"trustworthy result, or the invocation was wrong: a bad flag, an unknown "+
		"command, or a flag after the module path.",
		report.ExitClean, exitSuccess, report.ExitFindings, report.ExitOperational)
}
