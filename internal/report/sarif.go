package report

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// The SARIF document this reporter emits.
const (
	sarifVersion = "2.1.0"
	sarifSchema  = "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/" +
		"sarif-schema-2.1.0.json"
	sarifTool  = "tf-mut"
	sarifRules = "https://github.com/andrewesweet/tf-mut/blob/master/docs/design/" +
		"mutation-operators.md"
)

// SARIF levels, which the normative result set maps states and diagnoses onto.
const (
	levelError   = "error"
	levelWarning = "warning"
	levelNote    = "note"
)

// sarifDocument is the subset of SARIF 2.1.0 the reporter produces.
// sarifDocument and the types under it mirror SARIF 2.1.0, member names
// included; see the linter exclusion in .golangci.yml.
type sarifDocument struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifToolBlock `json:"tool"`
	Results []sarifResult  `json:"results"`
	// Invocations carries the run summary, which is where the states SARIF
	// deliberately omits stay visible.
	Invocations []sarifInvocation `json:"invocations"`
}

type sarifToolBlock struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	ShortDescription sarifText         `json:"shortDescription"`
	FullDescription  sarifText         `json:"fullDescription"`
	Help             sarifText         `json:"help"`
	Properties       map[string]string `json:"properties"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations"`
	// PartialFingerprints let a code-scanning service track one finding across
	// commits, which is what the stable identifier exists for.
	PartialFingerprints map[string]string `json:"partialFingerprints"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           sarifRegion   `json:"region"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool      `json:"executionSuccessful"`
	ExitCode            int       `json:"exitCode"`
	Message             sarifText `json:"message"`
}

// RuleDescription supplies the catalogue text for one operator. The report
// package does not import the operator catalogue — the dependency runs the
// other way — so the description is injected once at start-up.
//
//nolint:gochecknoglobals // a package-level registry filled once at start-up.
var ruleDescriptions = map[string]RuleDescription{}

// RuleDescription is one operator's published documentation.
type RuleDescription struct {
	// Tier is the breadth band the operator belongs to.
	Tier string
	// Description states the fault the operator models.
	Description string
	// Killer states what an assertion would have to inspect to catch it.
	Killer string
}

// RegisterRules publishes the operator catalogue for the SARIF reporter.
func RegisterRules(descriptions map[string]RuleDescription) {
	maps.Copy(ruleDescriptions, descriptions)
}

// sarifLevel is the normative result set: which states reach SARIF at all, and
// at what level.
//
// `Survived` is an error where the diagnosis names something the reader can
// fix, and a note where the oracle could not decide. `StructurallyUnassertable`
// is a warning: it is a real finding with a mechanical fix, but it is not a
// defect in the assertion the annotation sits next to. `NoCoverage` is a note.
// Everything else is JSON-only, and the run summary says so.
//
//nolint:exhaustive // every other state is JSON-only by the normative table.
func sarifLevel(mutant Mutant) (string, bool) {
	switch mutant.State {
	case Survived:
		if mutant.Verdict != nil && !mutant.Verdict.Diagnosis.Actionable() {
			return levelNote, true
		}

		return levelError, true
	case StructurallyUnassertable:
		return levelWarning, true
	case NoCoverage:
		return levelNote, true
	default:
		return "", false
	}
}

// WriteSARIF renders the code-scanning document.
//
// Results are computed over the post-suppression population by construction:
// a suppressed mutant carries the `Ignored` state, which the result set omits.
func WriteSARIF(writer io.Writer, value Report) error {
	document := sarifDocument{
		Schema:  sarifSchema,
		Version: sarifVersion,
		Runs: []sarifRun{{
			Tool:        sarifToolBlock{Driver: driver(value)},
			Results:     results(value),
			Invocations: []sarifInvocation{summary(value)},
		}},
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("encoding SARIF: %w", err)
	}

	return nil
}

// driver publishes one rule per operator that appears in the population.
func driver(value Report) sarifDriver {
	seen := map[string]bool{}
	rules := []sarifRule{}

	for _, mutant := range value.Mutants {
		if seen[mutant.Operator] {
			continue
		}

		seen[mutant.Operator] = true
		rules = append(rules, rule(mutant.Operator))
	}

	slices.SortFunc(rules, func(left, right sarifRule) int {
		return strings.Compare(left.ID, right.ID)
	})

	return sarifDriver{Name: sarifTool, InformationURI: sarifRules, Rules: rules}
}

func rule(operator string) sarifRule {
	described, found := ruleDescriptions[operator]
	if !found {
		described = RuleDescription{Tier: "", Description: operator, Killer: ""}
	}

	return sarifRule{
		ID:               operator,
		Name:             operator,
		ShortDescription: sarifText{Text: described.Description},
		FullDescription: sarifText{Text: described.Description +
			". Killed when " + described.Killer + "."},
		Help:       sarifText{Text: "See " + sarifRules + " for the operator catalogue."},
		Properties: map[string]string{"tier": described.Tier},
	}
}

func results(value Report) []sarifResult {
	found := []sarifResult{}

	for _, mutant := range value.Mutants {
		level, reported := sarifLevel(mutant)
		if !reported {
			continue
		}

		found = append(found, sarifResult{
			RuleID:    mutant.Operator,
			Level:     level,
			Message:   sarifText{Text: message(mutant)},
			Locations: []sarifLocation{physicalLocation(mutant.Range)},
			PartialFingerprints: map[string]string{
				"tfMutMutantId/v1": mutant.ID,
			},
		})
	}

	return found
}

// message states the finding and its fix, because a code-scanning annotation is
// often the only place a reader will see either.
func message(mutant Mutant) string {
	builder := strings.Builder{}

	fmt.Fprintf(&builder, "%s survived at %s: ", mutant.Operator, mutant.Site)

	if mutant.State == NoCoverage {
		builder.Reset()
		fmt.Fprintf(&builder, "%s at %s is never instantiated by any run block, "+
			"so this mutation was not executed. Add a run block that reaches this module.",
			mutant.Operator, mutant.Site)

		return builder.String()
	}

	if mutant.Verdict == nil {
		return builder.String() + "no diagnosis was recorded."
	}

	if mutant.Verdict.Diagnosis != "" {
		fmt.Fprintf(&builder, "%s. ", mutant.Verdict.Diagnosis)
	}

	builder.WriteString(mutant.Verdict.Message + ". Fix: " + mutant.Verdict.Fix + ".")

	return builder.String()
}

func physicalLocation(source Range) sarifLocation {
	return sarifLocation{PhysicalLocation: sarifPhysicalLocation{
		ArtifactLocation: sarifArtifact{URI: source.File},
		Region: sarifRegion{
			StartLine:   source.Start.Line,
			StartColumn: source.Start.Column,
			EndLine:     source.End.Line,
			EndColumn:   source.End.Column,
		},
	}}
}

// summary explains where the states SARIF omits remain visible, so that a
// reader of an annotated pull request is not left believing the annotations are
// the whole result.
func summary(value Report) sarifInvocation {
	omitted := []string{}

	for _, state := range []State{Invalid, Killed, KilledByError, Timeout, Unobservable, Ignored} {
		if count := value.Count(state); count > 0 {
			omitted = append(omitted, fmt.Sprintf("%s %d", state, count))
		}
	}

	text := fmt.Sprintf("tf-mut scored %.1f%% over %d mutants",
		value.Metrics.MutationScore*percent, value.Metrics.Scored)

	if len(omitted) > 0 {
		text += ". Not annotated here, and in the JSON report: " + strings.Join(omitted, ", ")
	}

	return sarifInvocation{
		ExecutionSuccessful: len(value.Errors) == 0,
		ExitCode:            value.ExitCode(Gate{}), //nolint:exhaustruct // the gate is the caller's.
		Message:             sarifText{Text: text + "."},
	}
}
