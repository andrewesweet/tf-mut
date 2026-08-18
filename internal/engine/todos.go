package engine

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The three TODO surfaces, and why they are contracted rather than incidental.
//
// A judgement point the tool refuses to guess at is only useful if something
// can find it, answer it and resume from it. `tf-mut todos` lists them with
// the evidence an answer needs; `--answer` supplies one from a script; and
// `--resume` reads the answers a human or an agent edited into the
// non-executable artefact. Promotion — re-synthesise, pin-verify, then write —
// is the only route from any of them into executable test content.

// ErrAnswer reports a malformed or unmatched TODO answer.
var ErrAnswer = errors.New("the answer names no open judgement point")

// answerSeparator splits `todo-<id>=<value>`.
const answerSeparator = "="

// todoPlaceholder is the unanswered value in a non-executable artefact.
const todoPlaceholder = "TFMUT_TODO"

// collectAnswers gathers the answers this invocation has, from the flag and —
// under --resume — from the edited artefact. An explicit answer wins over the
// file, because it is the more recent statement of intent.
func collectAnswers(
	configuration discovery.Configuration,
	settings Config,
) (map[string]string, error) {
	answers := map[string]string{}

	if settings.Resume {
		fromFile, err := readArtefactAnswers(configuration)
		if err != nil {
			return nil, err
		}

		maps.Copy(answers, fromFile)
	}

	for _, answer := range settings.Answers {
		identifier, value, found := strings.Cut(answer, answerSeparator)
		if !found || identifier == "" || value == "" {
			return nil, fmt.Errorf("%w: --answer wants todo-<id>=<value>, got %q",
				ErrAnswer, answer)
		}

		answers[identifier] = value
	}

	return answers, nil
}

// readArtefactAnswers reads every answered TODO out of the non-executable
// artefacts in the test directory.
func readArtefactAnswers(configuration discovery.Configuration) (map[string]string, error) {
	pattern := filepath.Join(configuration.Tests.Dir, "*"+characterise.ArtefactSuffix)

	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("looking for answered artefacts: %w", err)
	}

	answers := map[string]string{}
	parser := hclparse.NewParser()

	for _, path := range paths {
		content, readErr := os.ReadFile(path) //nolint:gosec // a discovery-owned test directory.
		if readErr != nil {
			return nil, fmt.Errorf("reading %s: %w", path, readErr)
		}

		file, diagnostics := parser.ParseHCL(content, path)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("%w: %s: %s", discovery.ErrParse, path, diagnostics.Error())
		}

		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			return nil, fmt.Errorf("%w: %s: unexpected body type", discovery.ErrParse, path)
		}

		collectArtefactAnswers(body, content, answers)
	}

	return answers, nil
}

// collectArtefactAnswers reads the answered value of each todo block, verbatim,
// so the answer written into the run block is the text the reader wrote.
func collectArtefactAnswers(body *hclsyntax.Body, content []byte, answers map[string]string) {
	for _, block := range body.Blocks {
		if block.Type != "todo" || len(block.Labels) != 1 {
			continue
		}

		attribute, found := block.Body.Attributes["value"]
		if !found {
			continue
		}

		source := expressionSource(content, attribute.Expr)
		if source == "" || source == todoPlaceholder {
			continue
		}

		answers[block.Labels[0]] = source
	}
}

func expressionSource(content []byte, expr hclsyntax.Expression) string {
	span := expr.Range()
	if span.Start.Byte < 0 || span.End.Byte > len(content) || span.Start.Byte >= span.End.Byte {
		return ""
	}

	return strings.TrimSpace(string(content[span.Start.Byte:span.End.Byte]))
}

// listTodos is the `tf-mut todos` command: the open judgement points and the
// evidence an answer needs, and nothing that costs a Terraform run.
//
// Cheapness is a contract, not an optimisation: an agent drains this list once
// per iteration, and a surface that planned providers and initialised a
// workspace to print a handful of constraints would be the wrong shape for the
// loop it exists to serve.
func listTodos(
	configuration discovery.Configuration,
	settings Config,
	version string,
) (report.Report, error) {
	sources, err := moduleSources(configuration)
	if err != nil {
		return report.Report{}, err
	}

	answers, err := collectAnswers(configuration, settings)
	if err != nil {
		return report.Report{}, err
	}

	rung, err := characterise.ParseRung(settings.PinRung)
	if err != nil {
		return report.Report{}, err
	}

	scenarios, todos := characterise.PlanInputs(configuration, characterise.Options{
		Rung:       rung,
		TestDirRel: configuration.TestDirRelative(),
		Version:    version,
		Sources:    sources,
		Answers:    answers,
	})

	result := shell(configuration, settings, version, configuration.ModuleDir,
		warm{}, []string{}) //nolint:exhaustruct // nothing was prepared: this surface runs no Terraform.
	result.Command = report.CommandTodos
	result.Selection = report.Selection{Mode: scopeLabel(true), Ref: "", ForcedFull: ""}
	result.Metrics = report.ComputeMetrics(nil)
	result.Characterisation = &report.Characterisation{ //nolint:exhaustruct // a listing carries no scaffold.
		Rung: string(rung), Complete: false, Scenarios: scenarios,
		Pins: []report.Pin{}, Todos: todos, Files: []report.GeneratedFile{}, Staged: true,
	}

	return result, nil
}

// promote marks every answered judgement point promoted, which only the
// verification that precedes it makes true.
func promote(block *report.Characterisation) {
	for index, todo := range block.Todos {
		if todo.Status == report.TodoAnswered {
			block.Todos[index].Status = report.TodoPromoted
		}
	}
}
