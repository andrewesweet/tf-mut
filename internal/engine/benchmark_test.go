package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M5d benchmark's published arithmetic (issue #164): the two tables, the
// aggregation over them, and the portable assertions the two cold/warm legs
// have to satisfy. The live measurement is TestTheBenchmarkOverThePinnedCorpus
// (integration-tagged, network-gated); this file holds the pure values and
// comparators so the arithmetic is pinned offline, on recorded shapes, before
// the measurement runs — and every verdict the shapes lead to is still
// asserted through the engine seam by the live test.

// benchmarkLeg is one leg's published facts: one availability fact and one
// row outcome, each decided by that leg's own invocation, plus the scored
// leg's report-derived publications. The states and verdicts maps exist only
// on a scored leg; they carry the per-mutant facts the verdict-identity
// assertion compares across cold and warm.
type benchmarkLeg struct {
	PopulationKnown bool   `json:"population_known"`
	PopulationSize  int    `json:"population_size"`
	UnknownReason   string `json:"unknown_population_reason,omitempty"`
	Row             string `json:"row_outcome"`
	// OperationalReason is the failed attempt's error text, truncated, when
	// the leg needed a retry; it is a fact about this run, not the module.
	OperationalReason string `json:"operational_reason,omitempty"`
	Retries           int    `json:"operational_retries"`
	PreviewMS         int64  `json:"preview_ms,omitempty"`
	RunMS             int64  `json:"run_ms,omitempty"`
	// TerraformVersion is the version string the leg's own reports carried.
	TerraformVersion string `json:"terraform_version,omitempty"`

	// Scored-leg publications. Metrics is the report's own metrics value;
	// PseudoTested counts the report's pseudo-tested findings.
	Metrics      *report.Metrics   `json:"metrics,omitempty"`
	PseudoTested int               `json:"pseudo_tested_findings,omitempty"`
	States       map[string]string `json:"states,omitempty"`
	Verdicts     map[string]string `json:"verdicts,omitempty"`
}

// benchmarkModuleResult is one pinned module's published pair of legs.
type benchmarkModuleResult struct {
	Module     string        `json:"module"`
	Repository string        `json:"repository"`
	Commit     string        `json:"commit"`
	Cold       benchmarkLeg  `json:"cold"`
	Warm       benchmarkLeg  `json:"warm"`
	WallClock  benchmarkMove `json:"wall_clock"`
}

// benchmarkMove is the wall-clock publication: the only non-portable facts in
// the measurement, published on named hardware and asserted about nothing.
type benchmarkMove struct {
	ColdMS int64 `json:"cold_ms"`
	WarmMS int64 `json:"warm_ms"`
}

// assertBenchmarkLegsAgree holds the portable assertions between one module's
// two legs. Each is stated so the live measurement can fail: a violation is
// collected, not fatal, so the measurement still publishes everything the
// legs produced and names what broke.
func assertBenchmarkLegsAgree(collected *[]string, module string, cold, warm benchmarkLeg) {
	violate := func(format string, args ...any) {
		*collected = append(*collected, module+": "+fmt.Sprintf(format, args...))
	}

	// refusalRows are the row outcomes the portable assertion holds
	// deterministic across cold and warm: every refusal class of the
	// vocabulary. Operational and scored-class rows are deliberately absent —
	// an operational row is a fact about one run, and the scored pair carries
	// its own identity claim.
	refusalRows := map[censusRow]bool{
		rowUnparseableSource:         true,
		rowUnsupportedConstruct:      true,
		rowRealInfrastructure:        true,
		rowUnsandboxedEffects:        true,
		rowNoSuite:                   true,
		rowRedBaseline:               true,
		rowUnsupportedPayloadVersion: true,
	}

	// rowVocabulary is M5d's total row vocabulary, the only values a
	// published row may carry.
	rowVocabulary := map[censusRow]bool{
		rowScored:                    true,
		rowScoredIncomplete:          true,
		rowUnparseableSource:         true,
		rowUnsupportedConstruct:      true,
		rowRealInfrastructure:        true,
		rowUnsandboxedEffects:        true,
		rowNoSuite:                   true,
		rowRedBaseline:               true,
		rowUnsupportedPayloadVersion: true,
		rowOperational:               true,
	}

	if !rowVocabulary[censusRow(cold.Row)] {
		violate("cold row %q is outside the total vocabulary", cold.Row)
	}

	if !rowVocabulary[censusRow(warm.Row)] {
		violate("warm row %q is outside the total vocabulary", warm.Row)
	}

	// Availability is a fact of the preview invocation, published from the
	// cold leg; the warm leg's own preview must agree, count included.
	if cold.PopulationKnown != warm.PopulationKnown {
		violate("availability moved between legs: cold known=%v, warm known=%v",
			cold.PopulationKnown, warm.PopulationKnown)
	} else if cold.PopulationKnown && cold.PopulationSize != warm.PopulationSize {
		violate("population moved between legs: cold %d, warm %d",
			cold.PopulationSize, warm.PopulationSize)
	}

	// The refusal classes are decided before any Terraform runs, so the two
	// legs must refuse identically. A refusal that moves is not a finding
	// about the module; it breaks the pin.
	if refusalRows[censusRow(cold.Row)] && cold.Row != warm.Row {
		violate("refusal row moved between legs: cold %q, warm %q", cold.Row, warm.Row)
	}

	// Verdict identity per mutant across the legs, over scored modules — the
	// M3b invariance shape: state first, then the whole verdict.
	if censusRow(cold.Row) == rowScored && censusRow(warm.Row) == rowScored {
		assertVerdictIdentity(violate, module, cold, warm)
	}
}

// assertVerdictIdentity compares every mutant the two scored legs share: the
// identifier set, each state, each canonical verdict.
func assertVerdictIdentity(violate func(string, ...any), module string, cold, warm benchmarkLeg) {
	shared := 0

	for id, coldState := range cold.States {
		warmState, found := warm.States[id]
		if !found {
			violate("%s: mutant %s is in the cold leg but not the warm one", module, id)

			continue
		}

		shared++

		if coldState != warmState {
			violate("%s: mutant %s state moved between legs: cold %s, warm %s",
				module, id, coldState, warmState)

			continue
		}

		if cold.Verdicts[id] != warm.Verdicts[id] {
			violate("%s: mutant %s verdict moved between legs", module, id)
		}
	}

	for id := range warm.States {
		if _, found := cold.States[id]; !found {
			violate("%s: mutant %s is in the warm leg but not the cold one", module, id)
		}
	}

	if shared == 0 && len(cold.States) > 0 {
		violate("%s: the scored legs share no mutants; the identity claim is vacuous", module)
	}
}

// moduleAdmissionTable is the benchmark's first published table, in modules.
// Every module rate carries both denominators, and the unknown-population
// count is published beside them — never folded into a denominator.
type moduleAdmissionTable struct {
	Pinned             int            `json:"modules_pinned"`
	RowOutcomes        map[string]int `json:"row_outcomes"`
	UnknownPopulations int            `json:"populations_unknown"`
	Known              int            `json:"populations_known"`
	Scored             int            `json:"scored_modules"`
	ScoredOverKnown    string         `json:"admission_rate_over_known_populations"`
	ScoredOverPinned   string         `json:"admission_rate_over_pinned_corpus"`
}

// aggregateModuleAdmission builds the module table from the published rows.
// The module's row outcome and availability are the cold leg's, whose facts
// the portable assertions hold identical to the warm leg's.
func aggregateModuleAdmission(pinned int, rows []benchmarkModuleResult) moduleAdmissionTable {
	table := moduleAdmissionTable{
		Pinned:      pinned,
		RowOutcomes: map[string]int{},
	}

	for _, row := range rows {
		table.RowOutcomes[row.Cold.Row]++

		if row.Cold.PopulationKnown {
			table.Known++
		} else {
			table.UnknownPopulations++
		}

		if censusRow(row.Cold.Row) == rowScored {
			table.Scored++
		}
	}

	table.ScoredOverKnown = fmt.Sprintf("%d/%d", table.Scored, table.Known)
	table.ScoredOverPinned = fmt.Sprintf("%d/%d", table.Scored, table.Pinned)

	return table
}

// mutantLevelTable is the benchmark's second published table, in Oasis's
// units, over known populations only. The pooled columns sum the scored
// modules (Oasis's method); generated sums every known population, scored or
// not; the known-but-unscored bucket tabulates those populations by row
// outcome, the analogue of Oasis's 61 unscorable mutants.
type mutantLevelTable struct {
	// Generated is the sum of the known populations, stated over K of N
	// modules. No complete mutant denominator is claimed while any
	// population is unknown.
	Generated      int            `json:"generated"`
	GeneratedBasis string         `json:"generated_basis"`
	KnownModules   int            `json:"modules_with_known_population"`
	PinnedModules  int            `json:"modules_pinned"`
	UnknownPopul   int            `json:"populations_unknown"`
	ScoredSet      int            `json:"scored"`
	Killed         int            `json:"killed"`
	KilledByError  int            `json:"killed_by_error"`
	Survived       int            `json:"survived"`
	MutationScore  float64        `json:"mutation_score"`
	AssertionScore float64        `json:"assertion_score"`
	Reachability   float64        `json:"reachability"`
	Counts         map[string]int `json:"states"`
	Diagnoses      map[string]int `json:"diagnoses"`
	PseudoTested   int            `json:"pseudo_tested_findings"`
	MedianScore    float64        `json:"median_per_module_mutation_score"`
	MedianModules  int            `json:"median_over_modules"`
	UnscoredByRow  map[string]int `json:"unscored_with_known_population_by_row_outcome"`
	UnscoredKnown  int            `json:"unscored_with_known_population"`
}

// aggregateMutantLevel builds the mutant table from the published rows. The
// pooled columns read the scored legs' own metrics; the equations those
// metrics encode are the report's, restated in the published document beside
// the numbers.
func aggregateMutantLevel(pinned int, rows []benchmarkModuleResult) mutantLevelTable {
	table := mutantLevelTable{
		PinnedModules: pinned,
		Counts:        map[string]int{},
		Diagnoses:     map[string]int{},
		UnscoredByRow: map[string]int{},
	}

	perModuleScores := []float64{}

	for _, row := range rows {
		if row.Cold.PopulationKnown {
			table.KnownModules++
			table.Generated += row.Cold.PopulationSize

			if censusRow(row.Cold.Row) != rowScored {
				table.UnscoredKnown += row.Cold.PopulationSize
				table.UnscoredByRow[row.Cold.Row] += row.Cold.PopulationSize
			}
		}

		if censusRow(row.Cold.Row) != rowScored || row.Cold.Metrics == nil {
			continue
		}

		metrics := row.Cold.Metrics
		table.ScoredSet += metrics.Scored
		table.Killed += metrics.Counts[report.Killed]
		table.KilledByError += metrics.Counts[report.KilledByError]
		table.Survived += metrics.Counts[report.Survived]
		table.PseudoTested += row.Cold.PseudoTested

		for state, count := range metrics.Counts {
			table.Counts[string(state)] += count
		}

		for diagnosis, count := range metrics.Diagnoses {
			table.Diagnoses[string(diagnosis)] += count
		}

		perModuleScores = append(perModuleScores, metrics.MutationScore)
	}

	table.UnknownPopul = pinned - table.KnownModules
	table.GeneratedBasis = fmt.Sprintf("over %d of %d modules, with %d populations unknown",
		table.KnownModules, table.PinnedModules, table.UnknownPopul)
	table.MutationScore = ratio(table.Killed+table.KilledByError, table.ScoredSet)
	table.AssertionScore = ratio(table.Killed,
		table.Killed+table.Survived+table.Counts[string(report.StructurallyUnassertable)]+
			table.Counts[string(report.Timeout)])
	table.Reachability = ratio(table.Killed+table.KilledByError+table.Survived+
		table.Counts[string(report.Timeout)], table.ScoredSet)
	table.MedianModules = len(perModuleScores)
	table.MedianScore = unweightedMedian(perModuleScores)

	return table
}

// ratio is the pooled division the three scores share: zero over zero is
// zero, the report's own convention for a vacuous denominator.
func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}

	return float64(numerator) / float64(denominator)
}

// unweightedMedian is the per-module median: every scored module's mutation
// score counts once, whatever its size — never a mutant-weighted mean.
func unweightedMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := slices.Clone(values)
	slices.Sort(sorted)

	middle := len(sorted) / 2

	if len(sorted)%2 == 1 {
		return sorted[middle]
	}

	return (sorted[middle-1] + sorted[middle]) / 2
}

// TestTheBenchmarkAggregationPoolsScoredModulesAndKeepsBothDenominators pins
// the two tables' arithmetic on recorded shapes: pooled sums over scored
// modules; generated over every known population, scored or not; both
// denominators on the module rate; the unknown-population count folded into
// neither denominator.
func TestTheBenchmarkAggregationPoolsScoredModulesAndKeepsBothDenominators(t *testing.T) {
	t.Parallel()

	rows := benchmarkAggregationFixture()

	moduleTable := aggregateModuleAdmission(4, rows)
	requireModuleDenominators(t, moduleTable)

	mutants := aggregateMutantLevel(4, rows)
	requireGeneratedColumn(t, mutants)
	requirePooledColumns(t, mutants)

	if mutants.UnscoredKnown != 50 || mutants.UnscoredByRow[string(rowRealInfrastructure)] != 50 {
		t.Fatalf("the known-but-unscored bucket moved: %d over %v",
			mutants.UnscoredKnown, mutants.UnscoredByRow)
	}
}

// benchmarkAggregationFixture is the recorded shape the aggregation test
// reads: two scored modules of different sizes, a known population refused by
// the gates, and an operational run with an unknown population.
func benchmarkAggregationFixture() []benchmarkModuleResult {
	return []benchmarkModuleResult{
		{
			Module: "scored-a",
			Cold: benchmarkLeg{
				PopulationKnown: true, PopulationSize: 100, Row: string(rowScored),
				Metrics: &report.Metrics{
					Scored:        100,
					MutationScore: 0.5,
					Counts: map[report.State]int{
						report.Killed: 40, report.KilledByError: 10, report.Survived: 50,
					},
				},
			},
		},
		{
			Module: "scored-b",
			Cold: benchmarkLeg{
				PopulationKnown: true, PopulationSize: 10, Row: string(rowScored),
				Metrics: &report.Metrics{
					Scored:        10,
					MutationScore: 1.0,
					Counts:        map[report.State]int{report.Killed: 10},
				},
			},
		},
		{
			// Known population, refused row: its 50 mutants are generated
			// and known-but-unscored, never scored-set.
			Module: "refused-known",
			Cold: benchmarkLeg{
				PopulationKnown: true, PopulationSize: 50,
				Row: string(rowRealInfrastructure),
			},
		},
		{
			// Unknown population: outside generated entirely, and one of the
			// N−K the generated basis names.
			Module: "operational-unknown",
			Cold: benchmarkLeg{
				PopulationKnown: false, Row: string(rowOperational),
				UnknownReason: "terraform command failed: providers schema",
			},
		},
	}
}

// requireModuleDenominators holds the module table's shape: every rate in
// modules, both denominators published, the unknown count beside them.
func requireModuleDenominators(t *testing.T, moduleTable moduleAdmissionTable) {
	t.Helper()

	if moduleTable.Pinned != 4 || moduleTable.Known != 3 || moduleTable.UnknownPopulations != 1 {
		t.Fatalf("module denominators moved: pinned=%d known=%d unknown=%d",
			moduleTable.Pinned, moduleTable.Known, moduleTable.UnknownPopulations)
	}

	if moduleTable.Scored != 2 || moduleTable.ScoredOverKnown != "2/3" || moduleTable.ScoredOverPinned != "2/4" {
		t.Fatalf("module admission rates moved: scored=%d over-known=%s over-pinned=%s",
			moduleTable.Scored, moduleTable.ScoredOverKnown, moduleTable.ScoredOverPinned)
	}

	if moduleTable.RowOutcomes[string(rowScored)] != 2 ||
		moduleTable.RowOutcomes[string(rowRealInfrastructure)] != 1 ||
		moduleTable.RowOutcomes[string(rowOperational)] != 1 {
		t.Fatalf("row outcome counts moved: %v", moduleTable.RowOutcomes)
	}
}

// requireGeneratedColumn holds the generated column's rule: the sum of the
// known populations, stated over K of N, the unknown count outside it.
func requireGeneratedColumn(t *testing.T, mutants mutantLevelTable) {
	t.Helper()

	if mutants.Generated != 160 || mutants.KnownModules != 3 || mutants.UnknownPopul != 1 {
		t.Fatalf("generated moved: %d over %d known of %d pinned",
			mutants.Generated, mutants.KnownModules, mutants.PinnedModules)
	}

	if mutants.GeneratedBasis != "over 3 of 4 modules, with 1 populations unknown" {
		t.Fatalf("generated basis moved: %q", mutants.GeneratedBasis)
	}
}

// requirePooledColumns holds the pooled scored columns and the three scores'
// equations, restated: mutation score is the killed share of the scored set —
// 60 of 110 here; assertion score counts assertion kills against the killable
// survivors — 50 of (50 killed + 50 survived); reachability is the
// non-excluded share of the scored set.
func requirePooledColumns(t *testing.T, mutants mutantLevelTable) {
	t.Helper()

	if mutants.ScoredSet != 110 || mutants.Killed != 50 || mutants.KilledByError != 10 ||
		mutants.Survived != 50 {
		t.Fatalf("pooled scored columns moved: scored=%d killed=%d by-error=%d survived=%d",
			mutants.ScoredSet, mutants.Killed, mutants.KilledByError, mutants.Survived)
	}

	if mutants.MutationScore <= 0.545 || mutants.MutationScore >= 0.546 {
		t.Fatalf("pooled mutation score moved: %f, want 60/110", mutants.MutationScore)
	}

	if mutants.AssertionScore <= 0.499 || mutants.AssertionScore >= 0.501 {
		t.Fatalf("pooled assertion score moved: %f, want 50/100", mutants.AssertionScore)
	}

	if mutants.Reachability <= 0.999 && mutants.Reachability != 1.0 {
		t.Fatalf("pooled reachability moved: %f, want 110/110", mutants.Reachability)
	}
}

// TestTheBenchmarkMedianIsUnweightedAcrossModules pins the median's unit: one
// scored module, one vote, whatever its population — a 1,000-mutant module
// moves the median no more than a 3-mutant one.
func TestTheBenchmarkMedianIsUnweightedAcrossModules(t *testing.T) {
	t.Parallel()

	leg := func(score float64) benchmarkLeg {
		return benchmarkLeg{
			PopulationKnown: true, Row: string(rowScored),
			Metrics: &report.Metrics{MutationScore: score},
		}
	}

	odd := []benchmarkModuleResult{
		{Module: "small", Cold: leg(0.1)},
		{Module: "middle", Cold: leg(0.5)},
		{Module: "large", Cold: leg(0.9)},
	}

	if got := aggregateMutantLevel(3, odd).MedianScore; got != 0.5 {
		t.Fatalf("the odd-count median is %f, want the middle module's 0.5", got)
	}

	even := append(slices.Clone(odd), benchmarkModuleResult{Module: "fourth", Cold: leg(0.7)})
	if got := aggregateMutantLevel(4, even).MedianScore; got != 0.6 {
		t.Fatalf("the even-count median is %f, want the two middle modules' mean 0.6", got)
	}

	if got := aggregateMutantLevel(0, nil).MedianScore; got != 0 {
		t.Fatalf("the median over no scored modules is %f, want zero", got)
	}
}

// TestTheUnscoredKnownPopulationsTabulateByRowOutcome pins the known-but-
// unscored bucket's coverage rule: every non-scored row outcome with a known
// population lands in the bucket, tabulated by that row outcome, and the
// bucket plus the scored set partitions the known populations.
func TestTheUnscoredKnownPopulationsTabulateByRowOutcome(t *testing.T) {
	t.Parallel()

	rows := []benchmarkModuleResult{
		{Module: "scored", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 10, Row: string(rowScored),
		}},
		{Module: "gated", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 6, Row: string(rowUnsandboxedEffects),
		}},
		{Module: "real", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 7, Row: string(rowRealInfrastructure),
		}},
		{Module: "red-baseline", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 3, Row: string(rowRedBaseline),
		}},
		{Module: "baseline-quiet", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 4, Row: string(rowNoSuite),
		}},
		{Module: "payload", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 2, Row: string(rowUnsupportedPayloadVersion),
		}},
		{Module: "incomplete", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 8, Row: string(rowScoredIncomplete),
		}},
		{Module: "down", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 9, Row: string(rowOperational),
		}},
		{Module: "unparseable", Cold: benchmarkLeg{
			PopulationKnown: true, PopulationSize: 5, Row: string(rowUnparseableSource),
		}},
		{Module: "unknown", Cold: benchmarkLeg{
			PopulationKnown: false, Row: string(rowOperational),
		}},
	}

	mutants := aggregateMutantLevel(10, rows)

	want := map[string]int{
		string(rowUnsandboxedEffects):        6,
		string(rowRealInfrastructure):        7,
		string(rowRedBaseline):               3,
		string(rowNoSuite):                   4,
		string(rowUnsupportedPayloadVersion): 2,
		string(rowScoredIncomplete):          8,
		string(rowOperational):               9,
		string(rowUnparseableSource):         5,
	}

	for row, count := range want {
		if mutants.UnscoredByRow[row] != count {
			t.Errorf("the bucket for %q is %d, want %d", row, mutants.UnscoredByRow[row], count)
		}
	}

	if len(mutants.UnscoredByRow) != len(want) {
		t.Fatalf("the bucket carries %d row outcomes, want %d", len(mutants.UnscoredByRow), len(want))
	}

	// Partition: bucket + scored-module populations = every known population.
	if mutants.UnscoredKnown+10 != mutants.Generated {
		t.Fatalf("the bucket (%d) plus the scored population (10) does not cover the known total (%d)",
			mutants.UnscoredKnown, mutants.Generated)
	}

	if mutants.UnscoredKnown != 44 {
		t.Fatalf("the bucket total is %d, want 6+7+3+4+2+8+9+5=44", mutants.UnscoredKnown)
	}
}

// brokenLegFixture is the scored-leg shape the red-proof test bends: a known
// population, a scored row, two mutants with states and verdicts, one
// survivor carrying a weak-assertion verdict.
func brokenLegFixture() benchmarkLeg {
	return benchmarkLeg{
		PopulationKnown: true, PopulationSize: 10, Row: string(rowScored),
		States:   map[string]string{"m1": string(report.Killed), "m2": string(report.Survived)},
		Verdicts: map[string]string{"m1": "", "m2": `"weak-assertion"`},
		Metrics: &report.Metrics{
			Scored: 10, MutationScore: 0.5,
			Counts: map[report.State]int{report.Killed: 5},
		},
	}
}

// TestABrokenLegAgreementTurnsThePortableAssertionsRed proves each portable
// assertion can fail: an availability move, a population move, a refusal that
// is not deterministic, and a verdict that moves between legs are each
// collected, named, and never silently published as agreeing.
func TestABrokenLegAgreementTurnsThePortableAssertionsRed(t *testing.T) {
	t.Parallel()

	for _, broken := range []struct {
		name string
		cold benchmarkLeg
		warm benchmarkLeg
		want string
	}{
		{
			name: "availability moves",
			cold: benchmarkLeg{PopulationKnown: false, Row: string(rowOperational)},
			warm: benchmarkLeg{PopulationKnown: true, PopulationSize: 5, Row: string(rowOperational)},
			want: "availability moved",
		},
		{
			name: "population count moves",
			cold: benchmarkLeg{PopulationKnown: true, PopulationSize: 5, Row: string(rowScored)},
			warm: benchmarkLeg{PopulationKnown: true, PopulationSize: 6, Row: string(rowScored)},
			want: "population moved",
		},
		{
			name: "a refusal is not deterministic",
			cold: benchmarkLeg{
				PopulationKnown: true, PopulationSize: 5,
				Row: string(rowRealInfrastructure),
			},
			warm: benchmarkLeg{
				PopulationKnown: true, PopulationSize: 5,
				Row: string(rowUnsandboxedEffects),
			},
			want: "refusal row moved",
		},
		{
			name: "a state moves between scored legs",
			cold: brokenLegFixture(),
			warm: func() benchmarkLeg {
				moved := brokenLegFixture()
				moved.States["m2"] = string(report.Killed)

				return moved
			}(),
			want: "state moved",
		},
		{
			name: "a verdict moves between scored legs",
			cold: brokenLegFixture(),
			warm: func() benchmarkLeg {
				moved := brokenLegFixture()
				moved.Verdicts["m2"] = `"no-coverage"`

				return moved
			}(),
			want: "verdict moved",
		},
	} {
		t.Run(broken.name, func(t *testing.T) {
			t.Parallel()

			collected := []string{}
			assertBenchmarkLegsAgree(&collected, broken.name, broken.cold, broken.warm)

			joined := strings.Join(collected, "\n")
			if !strings.Contains(joined, broken.want) {
				t.Fatalf("the violations %q do not name %q", joined, broken.want)
			}
		})
	}

	// The agreeing pair collects nothing: the assertion is not vacuously red.
	agreeing := brokenLegFixture()
	collected := []string{}
	assertBenchmarkLegsAgree(&collected, "agreeing", agreeing, agreeing)
	if len(collected) != 0 {
		t.Fatalf("the agreeing pair collected %v", collected)
	}

	// An out-of-vocabulary row is named, whatever the legs agree on.
	offVocabulary := benchmarkLeg{PopulationKnown: true, PopulationSize: 1, Row: "excellent"}
	assertBenchmarkLegsAgree(&collected, "off-vocabulary", offVocabulary, offVocabulary)
	if !strings.Contains(strings.Join(collected, "\n"), "outside the total vocabulary") {
		t.Fatalf("an off-vocabulary row was not named: %v", collected)
	}
}
