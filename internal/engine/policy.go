package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The flag names the command line can override a configured scalar with. The
// precedence is per scalar rather than per file: a repository sets policy, and
// one flag on one run should not discard the rest of it.
const (
	FlagTestDirectory        = "test-directory"
	FlagJobs                 = "jobs"
	FlagTimeoutFactor        = "timeout-factor"
	FlagMinScore             = "min-score"
	FlagAllowIncompleteScore = "allow-incomplete-score"
	FlagTier                 = "tier"
	FlagReporter             = "reporter"
)

// withConfiguration merges `.tf-mut.hcl` into the run's configuration, leaving
// every scalar the caller set explicitly alone.
func (c Config) withConfiguration(moduleDir string) (Config, config.File, error) {
	file, err := config.Load(moduleDir)
	if err != nil {
		return c, file, err
	}

	if !file.Present {
		return c, file, nil
	}

	settings := file.Settings

	if settings.TestDirectory != nil && !c.overridden(FlagTestDirectory) {
		c.TestDirectory = *settings.TestDirectory
	}

	if settings.Jobs != nil && !c.overridden(FlagJobs) {
		c.Jobs = *settings.Jobs
	}

	if settings.TimeoutFactor != nil && !c.overridden(FlagTimeoutFactor) {
		c.TimeoutFactor = *settings.TimeoutFactor
	}

	if settings.MinScore != nil && !c.overridden(FlagMinScore) {
		c.MinScore = *settings.MinScore
		c.HasMinScore = true
	}

	if settings.AllowIncompleteScore != nil && !c.overridden(FlagAllowIncompleteScore) {
		c.AllowIncompleteScore = *settings.AllowIncompleteScore
	}

	if file.Operators.Tier != "" && !c.overridden(FlagTier) {
		c.Tier = mutation.Tier(file.Operators.Tier)
		if !c.Tier.Valid() {
			return c, file, fmt.Errorf("%w: %s is not a tier", config.ErrConfig, file.Operators.Tier)
		}
	}

	c.IncludeOperators = append(slices.Clone(c.IncludeOperators), file.Operators.Include...)
	c.ExcludeOperators = append(slices.Clone(c.ExcludeOperators), file.Operators.Exclude...)

	return c, file, nil
}

func (c Config) overridden(flag string) bool {
	return slices.Contains(c.SetFlags, flag)
}

// suppress applies configured exclusions and inline directives to the described
// population, moving what they cover to `Ignored`.
//
// Both are applied here, after generation and before execution, and never
// before the safety gates: an exclusion is a statement about what is worth
// grading, not a statement about what is safe to run.
func suppress(
	configuration discovery.Configuration,
	exclude config.Exclude,
	described []report.Mutant,
) ([]report.Mutant, []report.Suppression, []string) {
	directives := collectDirectives(configuration)
	applied := map[int]*report.Suppression{}
	warnings := []string{}

	for index, mutant := range described {
		if reason, kind, excluded := excludedBy(exclude, mutant); excluded {
			described[index].State = report.Ignored
			described[index].Suppression = &report.Suppression{
				Kind: kind, Operators: nil, Reason: reason, Accepted: true,
				Range: nil, Mutants: []string{mutant.ID}, Rejection: "",
			}

			continue
		}

		attachDirective(directives, applied, described, index)
	}

	suppressions := make([]report.Suppression, 0, len(directives))

	for index, directive := range directives {
		record, found := applied[index]
		if !found {
			created := describeDirective(directive)
			record = &created
		}

		suppressions = append(suppressions, *record)

		// A rejected directive is warned about whether or not it matched a
		// site: the point of the warning is that somebody wrote a suppression
		// believing it worked.
		if !record.Accepted {
			warnings = append(warnings, fmt.Sprintf("%s:%d: %s",
				directive.File, directive.Line, record.Rejection))
		}
	}

	suppressions = append(suppressions, configured(described)...)

	slices.SortFunc(suppressions, func(left, right report.Suppression) int {
		return strings.Compare(left.Kind+strings.Join(left.Operators, ","),
			right.Kind+strings.Join(right.Operators, ","))
	})

	return described, suppressions, warnings
}

// attachDirective applies the first directive that covers a mutant.
func attachDirective(
	directives []config.Directive,
	applied map[int]*report.Suppression,
	described []report.Mutant,
	index int,
) {
	mutant := described[index]

	for position, directive := range directives {
		if directive.File != mutant.Range.File ||
			!directive.Applies(mutant.Operator, mutant.Range.Start.Line) {
			continue
		}

		record, found := applied[position]
		if !found {
			created := describeDirective(directive)
			record = &created
			applied[position] = record
		}

		record.Mutants = append(record.Mutants, mutant.ID)

		if !directive.Valid() {
			// The finding stands. Recording the mutant here is what lets the
			// report show which finding the rejected directive tried to hide.
			return
		}

		described[index].State = report.Ignored
		described[index].Suppression = record

		return
	}
}

func describeDirective(directive config.Directive) report.Suppression {
	return report.Suppression{
		Kind:      "comment",
		Operators: directive.Operators,
		Reason:    directive.Reason,
		Accepted:  directive.Valid(),
		Range: &report.Range{
			File:  directive.File,
			Start: report.Position{Line: directive.Line, Column: directive.Column},
			End:   report.Position{Line: directive.Line, Column: directive.Column},
		},
		Mutants:   []string{},
		Rejection: directive.Rejection,
	}
}

func excludedBy(exclude config.Exclude, mutant report.Mutant) (reason, kind string, excluded bool) {
	if exclude.ExcludesPath(mutant.Range.File) {
		return "excluded by a configured path", "config-path", true
	}

	if exclude.ExcludesResource(mutant.Resource) {
		return "excluded by a configured resource address", "config-resource", true
	}

	return "", "", false
}

// configured summarises the exclusions that fired, one entry per kind.
func configured(described []report.Mutant) []report.Suppression {
	byKind := map[string]*report.Suppression{}

	for _, mutant := range described {
		if mutant.State != report.Ignored || mutant.Suppression == nil ||
			mutant.Suppression.Kind == "comment" {
			continue
		}

		entry, found := byKind[mutant.Suppression.Kind]
		if !found {
			entry = &report.Suppression{
				Kind: mutant.Suppression.Kind, Operators: nil,
				Reason: mutant.Suppression.Reason, Accepted: true,
				Range: nil, Mutants: []string{}, Rejection: "",
			}
			byKind[mutant.Suppression.Kind] = entry
		}

		entry.Mutants = append(entry.Mutants, mutant.ID)
	}

	records := make([]report.Suppression, 0, len(byKind))
	for _, entry := range byKind {
		records = append(records, *entry)
	}

	return records
}

// collectDirectives reads every module file's suppression comments.
func collectDirectives(configuration discovery.Configuration) []config.Directive {
	directives := []config.Directive{}

	for _, module := range configuration.Modules {
		for _, path := range module.Files {
			content, err := os.ReadFile(path) //nolint:gosec // module paths come from discovery.
			if err != nil {
				continue
			}

			rel := path
			if relative, relErr := filepath.Rel(configuration.ClosureRoot, path); relErr == nil {
				rel = filepath.ToSlash(relative)
			}

			directives = append(directives, config.Directives(rel, content)...)
		}
	}

	slices.SortFunc(directives, func(left, right config.Directive) int {
		if left.File != right.File {
			return strings.Compare(left.File, right.File)
		}

		return left.Line - right.Line
	})

	return directives
}
