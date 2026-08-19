package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// `tf-mut curate` reports redundancy, from authoritative populations only.
//
// The M4.5 spec review's C5: a pruning recommendation drawn from a scoped,
// sampled or filtered population is a false recommendation, and "it never
// auto-deletes" does not make a false recommendation honest. So curate takes
// the baseline-write posture — it refuses a partial population at
// configuration time, before any work is done — and the population-authority
// flag is published in every finding so no consumer has to know the rule.
//
// It is report-only. `--apply` is deferred until it earns its own recorded
// write exception; an agent acts on a finding by editing the test files.

// ErrCuratePopulation reports a population curate will not draw conclusions
// from.
var ErrCuratePopulation = errors.New(
	"curate requires a full, unsampled, unfiltered population",
)

// assertionFailure is the diagnostic summary Terraform gives a failed
// assertion. Attribution keys off it, and off the range beside it: measured
// against Terraform v1.15.8, every failed assertion of a run is reported, each
// with its own range, in declaration order and independent of which failed
// first.
const assertionFailure = "Test assertion failed"

// ErrUntilDryPopulation reports a population the convergence loop will not
// draw a conclusion from.
var ErrUntilDryPopulation = errors.New(
	"--until-dry requires a full, unsampled, unfiltered population",
)

// checkCuratePopulation refuses at configuration time, which is the point: a
// refusal that arrived after the population ran would have cost the caller the
// whole run to learn that its evidence is inadmissible.
func checkCuratePopulation(settings Config) error {
	if !settings.Curate {
		return nil
	}

	refusals := populationRefusals(settings)
	if len(refusals) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %s\n"+
		"  A redundancy finding drawn from a partial population is a false finding,\n"+
		"  and reporting it rather than acting on it would not make it true",
		ErrCuratePopulation, strings.Join(refusals, "; "))
}

// checkUntilDryPopulation holds the same posture for convergence.
//
// "Dry" is a claim about a population: the survivors stopped yielding new
// assertions. Under a count lever the loop grades a subset and the claim
// becomes "the survivors *this sample* reached stopped yielding", which is a
// different and much weaker statement — and the report hard-codes the
// selection as full, so nothing downstream could tell the two apart.
func checkUntilDryPopulation(settings Config) error {
	if !settings.UntilDry {
		return nil
	}

	refusals := populationRefusals(settings)
	if len(refusals) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %s\n"+
		"  Convergence measured over part of the population is not convergence,\n"+
		"  and the report cannot say which part it measured",
		ErrUntilDryPopulation, strings.Join(refusals, "; "))
}

// populationRefusals names every reason this population is not the default
// one, which is the standard both commands apply.
func populationRefusals(settings Config) []string {
	refusals := []string{}

	if settings.Since != "" {
		refusals = append(refusals, "--since scopes the population")
	}

	if settings.HasSample {
		refusals = append(refusals, "--sample makes the population non-authoritative")
	}

	if len(settings.IncludeOperators) > 0 || len(settings.ExcludeOperators) > 0 {
		refusals = append(refusals, "an operator selection narrows the population")
	}

	if len(settings.ExcludePaths) > 0 || len(settings.ExcludeResources) > 0 {
		refusals = append(refusals, "an exclusion removes sites from the population")
	}

	// The rule is "exactly the default population", not "not narrowed": a tier
	// selection and the generated-function opt-in both *change* it, and a
	// finding drawn from a population the reader reshaped is a finding about a
	// different program.
	if settings.Tier != "" && settings.Tier != mutation.TierStandard {
		refusals = append(refusals, "a tier selection changes the operator population")
	}

	if settings.GeneratedFunctions {
		refusals = append(refusals,
			"--generated-functions changes the operator population")
	}

	return refusals
}

// checkPopulationObserved refuses a population that did not fully execute.
//
// Timeouts and execution errors both leave mutants unobserved. The gate table
// already distinguishes unobserved from absent for the baseline; curate needs
// the same distinction for a different reason — an assertion looks like it
// senses nothing precisely when the mutants that would have proved otherwise
// never ran.
func checkPopulationObserved(result report.Report) error {
	reasons := []string{}

	if timeouts := result.Count(report.Timeout); timeouts > 0 {
		reasons = append(reasons,
			strconv.Itoa(timeouts)+" mutant(s) timed out, so their kills were never observed")
	}

	if len(result.Errors) > 0 {
		reasons = append(reasons,
			strconv.Itoa(len(result.Errors))+" mutant(s) could not be evaluated at all")
	}

	if len(reasons) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %s\n"+
		"  An unobserved mutant is not an absent one, and an empty kill set drawn over\n"+
		"  mutants that never ran is a false finding",
		ErrCuratePopulation, strings.Join(reasons, "; "))
}

// curateSuite runs the full population and reports what the kill sets show.
func curateSuite(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings Config,
	version tfexec.Version,
	moduleDir string,
) (report.Report, error) {
	graded := settings
	graded.Curate = false

	result, err := mutate(ctx, runner, configuration, graded, version, moduleDir)
	if err != nil {
		return report.Report{}, err
	}

	// The gate table's distinction, reused rather than restated: a mutant that
	// did not run is unobserved, not absent, and an empty kill set drawn over
	// unobserved mutants is a false finding of exactly the kind the population
	// posture exists to prevent.
	if err := checkPopulationObserved(result); err != nil {
		return report.Report{}, err
	}

	assertions := assertionInventory(configuration)
	killSets := attributeKills(result, assertions)
	provenance := assertionProvenance(configuration, assertions)

	// The rung is validated rather than copied through. `--pin` belongs to
	// `characterise`, and an unvalidated value reached `characterisation.rung`,
	// where report-2.3.0 admits only `outputs`, `counts` and `configured`: a
	// published document invalid against its own schema, from a flag this
	// command does not act on.
	rung := characterise.RungOutputs

	if settings.PinRung != "" {
		parsed, err := characterise.ParseRung(settings.PinRung)
		if err != nil {
			return report.Report{}, err
		}

		rung = parsed
	}

	result.Command = report.CommandCurate
	result.Characterisation = &report.Characterisation{ //nolint:exhaustruct // curate reports findings, not a scaffold.
		Rung: string(rung), Complete: true,
		Scenarios: []report.Scenario{}, Pins: []report.Pin{},
		Files:    []report.GeneratedFile{},
		Findings: curateFindings(assertions, killSets, provenance),
		Staged:   true,
	}

	return result, nil
}

// assertion is one assert block, identified by content.
type assertion struct {
	ID string
	// File is the test file relative to the module directory.
	File string
	// Run is the run block.
	Run string
	// Source is the condition verbatim.
	Source string
	// Line is the condition's first line, which is what a diagnostic points at.
	Line int
}

// assertionInventory lists every assertion in the suite, in file and run order.
func assertionInventory(configuration discovery.Configuration) []assertion {
	assertions := []assertion{}

	for _, run := range configuration.Tests.Runs {
		for index, declared := range run.Asserts {
			assertions = append(assertions, assertion{
				ID:   assertionID(run.Rel, run.Name, declared.Source, index),
				File: run.Rel, Run: run.Name, Source: declared.Source,
				Line: declared.Range.Start.Line,
			})
		}
	}

	return assertions
}

// assertionID is a stable, content-derived identifier. The index disambiguates
// two identical conditions in one run, which is legal and which a content hash
// alone would collapse.
func assertionID(file, run, source string, index int) string {
	return characterise.Identify("asrt-", file, run, source, strconv.Itoa(index))
}

// attributeKills records which mutants' deaths each assertion participated in.
//
// Terraform reports every failed assertion of a run, so participation is read
// directly rather than reconstructed by isolating assertions one at a time.
// A killed mutant whose diagnostics name no assertion contributes to no kill
// set: it died of an evaluation error, which the completeness measure
// deliberately does not count.
func attributeKills(result report.Report, assertions []assertion) map[string][]string {
	byLine := map[string]string{}
	for _, declared := range assertions {
		byLine[declared.File+":"+strconv.Itoa(declared.Line)] = declared.ID
	}

	killSets := map[string][]string{}

	for _, mutant := range result.Mutants {
		if mutant.State != report.Killed {
			continue
		}

		for _, diagnostic := range mutant.Diagnostics {
			if diagnostic.Summary != assertionFailure || diagnostic.Range == nil {
				continue
			}

			key := filepath.ToSlash(diagnostic.Range.File) + ":" +
				strconv.Itoa(diagnostic.Range.Start.Line)

			identifier, found := byLine[key]
			if !found {
				continue
			}

			if !slices.Contains(killSets[identifier], mutant.ID) {
				killSets[identifier] = append(killSets[identifier], mutant.ID)
			}
		}
	}

	for identifier := range killSets {
		slices.Sort(killSets[identifier])
	}

	return killSets
}

// assertionProvenance decides each assertion's class against the
// generated-assertion registry, mechanically.
//
// The registry records what this tool wrote and the digest it wrote: a file
// whose digest still matches is generated-and-unmodified, one whose digest has
// moved is generated-and-edited, and a file the registry never claimed is
// pre-existing. The granularity is the file rather than the assertion, which
// is what the registry can honestly support — a digest proves nothing finer.
func assertionProvenance(
	configuration discovery.Configuration,
	assertions []assertion,
) map[string]report.AssertionProvenance {
	registered := loadRegistry(configuration.ModuleDir)
	classes := map[string]report.AssertionProvenance{}

	for _, declared := range assertions {
		recorded, generated := registered.Files[declared.File]
		if !generated {
			classes[declared.ID] = report.PreExisting

			continue
		}

		content, err := os.ReadFile(
			filepath.Join(configuration.ModuleDir, filepath.FromSlash(declared.File)),
		)
		if err != nil || characterise.Digest(content) != recorded.Digest {
			classes[declared.ID] = report.GeneratedEdited

			continue
		}

		classes[declared.ID] = report.GeneratedUnmodified
	}

	return classes
}

// curateFindings reports where the oracle has power.
//
// The until-dry loop adds each generated assertion *because* it kills
// something nothing else kills, so its output is already near-minimal under
// kill-set inclusion and set-cover over it prunes almost nothing. Curate is
// therefore scoped to hand-written and edited assertions, and to redundancy
// across scenarios — which is where a suite actually accumulates waste.
func curateFindings(
	assertions []assertion,
	killSets map[string][]string,
	provenance map[string]report.AssertionProvenance,
) []report.CurateFinding {
	findings := []report.CurateFinding{}

	for _, declared := range assertions {
		if !eligible(provenance[declared.ID]) {
			continue
		}

		if len(killSets[declared.ID]) == 0 {
			findings = append(findings, finding(report.EmptyKillSet,
				// An empty slice, not nil: report-2.3.0 declares `mutants` an
				// array, and a nil slice serialises as `null`. The kill set
				// being empty is the whole finding, so this is the one path
				// that reaches the schema with nothing to list — and the one
				// that published an invalid document.
				[]assertion{declared}, provenance, []string{},
				"no mutant's death depended on this assertion: it senses nothing the "+
					"current operator catalogue models"))

			continue
		}

		if container, subsumed := subsumedBy(declared, assertions, killSets); subsumed {
			findings = append(findings, finding(report.Subsumed,
				[]assertion{declared, container}, provenance, killSets[declared.ID],
				"every mutant this assertion catches is also caught by "+container.ID))
		}
	}

	return append(findings, crossScenarioFindings(assertions, killSets, provenance)...)
}

// eligible reports whether curate will draw a conclusion about an assertion.
func eligible(class report.AssertionProvenance) bool {
	return class == report.GeneratedEdited || class == report.PreExisting
}

// subsumedBy finds an assertion whose kill set strictly contains this one's.
func subsumedBy(
	declared assertion,
	assertions []assertion,
	killSets map[string][]string,
) (assertion, bool) {
	mine := killSets[declared.ID]

	for _, other := range assertions {
		if other.ID == declared.ID {
			continue
		}

		theirs := killSets[other.ID]
		if len(theirs) <= len(mine) || !covers(theirs, mine) {
			continue
		}

		return other, true
	}

	return assertion{}, false //nolint:exhaustruct // the not-found sentinel.
}

// crossScenarioFindings reports two scenarios pinning the same behaviour under
// inputs that do not discriminate it.
func crossScenarioFindings(
	assertions []assertion,
	killSets map[string][]string,
	provenance map[string]report.AssertionProvenance,
) []report.CurateFinding {
	findings := []report.CurateFinding{}

	for outer, left := range assertions {
		for _, right := range assertions[outer+1:] {
			if left.Run == right.Run || len(killSets[left.ID]) == 0 {
				continue
			}

			// The same eligibility rule the other two kinds obey. Without it
			// curate recommends deleting the tool's own untouched generated
			// assertions — the direction that does damage — and contradicts
			// its own scoping, which is hand-written and edited assertions
			// plus redundancy across scenarios.
			if !eligible(provenance[left.ID]) || !eligible(provenance[right.ID]) {
				continue
			}

			if !slices.Equal(killSets[left.ID], killSets[right.ID]) {
				continue
			}

			findings = append(findings, finding(report.CrossScenarioRedundant,
				[]assertion{left, right}, provenance, killSets[left.ID],
				"runs "+left.Run+" and "+right.Run+" catch exactly the same mutants: "+
					"their inputs do not discriminate this behaviour"))
		}
	}

	return findings
}

// finding assembles one report entry with its evidence attached.
func finding(
	kind report.CurateKind,
	members []assertion,
	provenance map[string]report.AssertionProvenance,
	mutants []string,
	message string,
) report.CurateFinding {
	identifiers := make([]string, 0, len(members))
	classes := make([]report.AssertionProvenance, 0, len(members))

	for _, member := range members {
		identifiers = append(identifiers, member.ID)
		classes = append(classes, provenance[member.ID])
	}

	return report.CurateFinding{
		ID:   characterise.Identify("cur-", append([]string{string(kind)}, identifiers...)...),
		Kind: kind, Members: identifiers, Provenance: classes,
		Mutants: mutants, PopulationAuthoritative: true, Message: message,
	}
}

// covers reports whether the wider kill set contains every member of the
// narrower one, which is what subsumption means.
func covers(wider, narrower []string) bool {
	for _, member := range narrower {
		if !slices.Contains(wider, member) {
			return false
		}
	}

	return true
}
