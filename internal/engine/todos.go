package engine

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/oracle"
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

// collectAnswers gathers the answers this invocation has, from the flag and —
// under --resume — from the edited artefact. An explicit answer wins over the
// file, because it is the more recent statement of intent.
func collectAnswers(
	configuration discovery.Configuration,
	settings settings,
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
// artefacts in the test directory, through the parser the artefact class's
// owning context publishes.
func readArtefactAnswers(configuration discovery.Configuration) (map[string]string, error) {
	pattern := filepath.Join(configuration.Tests.Dir, "*"+characterise.ArtefactSuffix)

	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("looking for answered artefacts: %w", err)
	}

	answers := map[string]string{}

	for _, path := range paths {
		fromFile, err := characterise.ParseArtefactAnswers(path)
		if err != nil {
			return nil, err
		}

		maps.Copy(answers, fromFile)
	}

	return answers, nil
}

// listTodos is the `tf-mut todos` command: the open judgement points and the
// evidence an answer needs, and nothing that costs a Terraform run.
//
// Cheapness is a contract, not an optimisation: an agent drains this list once
// per iteration, and a surface that planned providers and initialised a
// workspace to print a handful of constraints would be the wrong shape for the
// loop it exists to serve.
// listTodos runs no Terraform at all — not even the version gate. The shipped
// skill promises a cheap local inspection an agent calls every iteration, and
// a surface that refused to work because Terraform was absent or broken would
// be the wrong shape for the loop it serves.
func listTodos(
	configuration discovery.Configuration,
	settings settings, version string,
) (report.Report, error) {
	sources, err := declarationSources(configuration)
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
		Version:    settings.toolVersion(),
		Sources:    sources,
		Answers:    answers,
	})

	result := shell(configuration, settings, version, configuration.ModuleDir,
		warm{}, []string{}) //nolint:exhaustruct // nothing was prepared: this surface runs no Terraform.
	result.Command = report.CommandTodos
	result.Selection = report.Selection{Mode: scopeLabel(true), Ref: "", ForcedFull: ""}
	result.Metrics = projectMetrics(oracle.ComputeMetrics(nil))
	block := &report.Characterisation{ //nolint:exhaustruct // a listing carries no scaffold.
		Rung: string(rung), Complete: false, Scenarios: projectScenarios(scenarios),
		Pins: []report.Pin{}, Todos: projectTodos(todos),
		Files: []report.GeneratedFile{}, Staged: true,
	}
	// A listing is complete when it has nothing outstanding to list, which is
	// what makes its exit code mean the same thing as every other command's.
	block.Complete = block.OpenTodos() == 0
	result.Characterisation = block

	return result, nil
}

// promoted advances every answered judgement point through the promotion
// transition, which only the verification that precedes it makes true: the
// evidence the transition demands is the verification leg that passed. Every
// other point is carried through untouched.
func promoted(
	scaffold characterise.SuitePlan,
	verification characterise.Verification,
) []characterise.Todo {
	todos := make([]characterise.Todo, len(scaffold.Todos))

	for index, todo := range scaffold.Todos {
		handle, answered := scaffold.Answered[todo.ID()]
		if todo.Status() != characterise.TodoAnswered || !answered {
			todos[index] = todo

			continue
		}

		todos[index] = handle.Promote(verification)
	}

	return todos
}
