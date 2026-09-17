package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M5-0.5a: the opportunity census and the mined-rung count. The census runs
// the static opportunity survey over both pinned corpora in the `todos`
// posture — no Terraform executes, not even a version probe — and classifies
// every opportunity by re-asking the same closed evaluator, never by
// guessing at what a plan would have decided. This file holds the census's
// machinery and the offline gates over fixture modules; the live measurement
// is `just measure-opportunities` (integration tag, network-gated), and the
// published record is docs/research/19-m5-opportunity-census.md.

// The two gap messages the synthesiser publishes as a module input's final
// state, quoted from internal/characterise. They decide which opportunities
// a probe can classify, so a rewording upstream breaks these gates loudly
// rather than silently reclassifying.
const (
	gapNoTypedCandidate = "the variable declares no default, no minable validation and no type this " +
		"version synthesises a value for"
	gapTypedRefused = "no synthesised value satisfied the variable's declared constraints"

	// The refusal diagnostic the answer path publishes when it decides the
	// candidate false — quoted from internal/characterise. It decides what a
	// probe's outcome means; the withheld class is read from the refusal
	// shape (a still-open judgement point whose answer never bound), never
	// from leaked text.
	probeNotSatisfied = "the answer does not satisfy this validation"

	// The fixture the no-candidate gate reads: the listing alone decides
	// this class, no probe.
	untestedNoTypeFixture = "untested-notype"

	// defaultCensusScenario is the scenario the first invocation plans; the
	// deeper rungs never produce another without an answer on record.
	defaultCensusScenario = "defaults"
)

// errProbeDropped says the probe invocation no longer listed a judgement
// point the first invocation did — a synthesis identity break, never a row.
var errProbeDropped = errors.New("the probe dropped a judgement point the listing carried")

// opportunityClass is why an input stayed a judgement point. The first three
// are the classes issue #152 stage 1 defines; evidence-withheld is the M8
// redaction rule's own outcome — where the only evidence that would classify
// an opportunity is withheld by the product, the census publishes the
// withholding rather than a guess.
type opportunityClass string

const (
	classRefused     opportunityClass = "typed candidate refused by the static evaluator"
	classNoCandidate opportunityClass = "no typed candidate at all"
	classUndecidable opportunityClass = "undecidable constraint"
	classWithheld    opportunityClass = "evidence withheld"
)

// opportunityFinding is one deduplicated unresolved module input.
type opportunityFinding struct {
	Variable   string           `json:"variable"`
	Class      opportunityClass `json:"class"`
	Constraint string           `json:"constraint"`
	File       string           `json:"file"`
	// Probe is the diagnostic the classification probe produced, published
	// verbatim so no classification rests on an invisible step.
	Probe string `json:"probe,omitempty"`
}

// opportunityRow is one corpus module's static reading. The defaults rung leaves
// no input behind — a variable resolved from its own default appears in no
// scenario — so the defaults count is derived: the denominator minus what
// the deeper rungs and the listing left unresolved.
type opportunityRow struct {
	Corpus    string `json:"corpus"`
	Module    string `json:"module"`
	Ref       string `json:"ref"`
	Variables int    `json:"variables"`
	// JSONVariables counts the declared inputs a `.tf.json` file declares.
	JSONVariables int  `json:"json_variables"`
	JSONDeclared  bool `json:"json_declared"`
	// Refused is the reason the module could not be read statically, empty
	// when it was. Retries counts the operational retries spent fetching it.
	Refused string `json:"refused,omitempty"`
	Retries int    `json:"retries,omitempty"`
	// Defaults, Mined and Typed count the inputs the first invocation
	// resolved, by the rung that resolved them. Todos counts the
	// opportunities the same invocation listed. The JSON-suffixed fields
	// carry each count's `.tf.json` stratum, attributed per variable where
	// the declaration syntax is a per-variable fact.
	Defaults     int `json:"defaults"`
	Mined        int `json:"mined"`
	Typed        int `json:"typed"`
	Todos        int `json:"todos"`
	JSONDefaults int `json:"json_defaults"`
	JSONMined    int `json:"json_mined"`
	JSONTyped    int `json:"json_typed"`
	JSONTodos    int `json:"json_todos"`
	// Opportunities is the deduplicated unresolved-input list.
	Opportunities []opportunityFinding `json:"opportunities,omitempty"`
}

// OpenOpportunities counts the row's unresolved inputs after deduplication.
func (r opportunityRow) OpenOpportunities() int {
	return len(r.Opportunities)
}

// CarriesOpportunities says whether the module gave the census anything to
// count towards the stage-1 decision.
func (r opportunityRow) CarriesOpportunities() bool {
	return len(r.Opportunities) > 0
}

// opportunityStratum is the mined rung's count over one declaration syntax:
// reached is the variables the rung was consulted for, fired the inputs it
// resolved.
type opportunityStratum struct {
	Reached int `json:"reached"`
	Fired   int `json:"fired"`
}

// opportunityStrata is the mined-rung count split by the syntax the variable was
// declared in. A JSON stratum with no measured module is published as
// unmeasured — a nil count, a state, never a zero — the same rule the
// corpus manifest records.
type opportunityStrata struct {
	Native opportunityStratum  `json:"native"`
	JSON   *opportunityStratum `json:"json"`
}

// summariseStrata folds the rows into the mined-rung count. The mined rung
// is reached by every variable that did not resolve from its own default;
// it fires where the first invocation resolved an input by mining its
// validation.
func summariseStrata(rows []opportunityRow) opportunityStrata {
	summary := opportunityStrata{Native: opportunityStratum{}, JSON: nil}

	for _, row := range rows {
		if row.Refused != "" {
			continue
		}

		jsonReached := row.JSONMined + row.JSONTyped + row.JSONTodos
		summary.Native.Reached += row.Mined + row.Typed + row.Todos - jsonReached
		summary.Native.Fired += row.Mined - row.JSONMined

		if !row.JSONDeclared {
			continue
		}

		if summary.JSON == nil {
			summary.JSON = &opportunityStratum{}
		}

		summary.JSON.Reached += jsonReached
		summary.JSON.Fired += row.JSONMined
	}

	return summary
}

// rootVariables is the census's denominator: the root module's declared
// inputs as the discovery boundary publishes them, native and `.tf.json`
// together, with each variable's declaration syntax. Reading the denominator
// from the same boundary the synthesiser reads its inputs from keeps the
// denominator and the opportunities from disagreeing. A root module that
// declares no inputs at all is found with an empty list — a measured zero,
// never a refusal.
func rootVariables(configuration discovery.Configuration) (found bool, names []string, jsonDeclared []bool) {
	for index := range configuration.Modules {
		if configuration.Modules[index].Dir != configuration.ModuleDir {
			continue
		}

		for _, variable := range configuration.Modules[index].Variables {
			names = append(names, variable.Name)
			jsonDeclared = append(jsonDeclared, variable.JSONDeclared)
		}

		return true, names, jsonDeclared
	}

	return false, nil, nil
}

// opportunityDenominator is rootVariables over a module directory, failing the test
// where the directory does not parse — the offline gates' fixture modules
// always do.
func opportunityDenominator(t *testing.T, moduleDir string) (total, jsonCount int) {
	t.Helper()

	configuration, err := discovery.Discover(moduleDir, engine.DefaultTestDirectory)
	if err != nil {
		t.Fatalf("discovering %s: %v", moduleDir, err)
	}

	found, names, jsonDeclared := rootVariables(configuration)
	if !found {
		t.Fatalf("discovering %s: no root module", moduleDir)
	}

	for _, declared := range jsonDeclared {
		if declared {
			jsonCount++
		}
	}

	return len(names), jsonCount
}

// classifyTodos turns one `todos` invocation's listing into the
// deduplicated opportunity list, failing the test where the probe fails.
func classifyTodos(t *testing.T, common engine.Common, todos []report.Todo) []opportunityFinding {
	t.Helper()

	opportunities, err := classifyTodosLive(context.Background(), common, todos)
	if err != nil {
		t.Fatalf("classifying %d judgement points: %v", len(todos), err)
	}

	return opportunities
}

// classifyTodosLive decides each open judgement point's class. Where the
// listing names a refused typed candidate, the class is decided by a second
// invocation in the same posture — one that supplies the refused candidate
// as the answer and reads which refusal comes back. The candidate is
// answered fail-open exactly as a reader would answer it, so an undecidable
// constraint moves and a decidably false one does not; nothing here decides
// what Terraform would decide.
func classifyTodosLive(ctx context.Context, common engine.Common, todos []report.Todo) ([]opportunityFinding, error) {
	opportunities := make([]opportunityFinding, 0, len(todos))
	probe := map[string]opportunityFinding{}

	for _, todo := range todos {
		if todo.Status != report.TodoOpen {
			continue
		}

		if todo.Diagnostic == gapNoTypedCandidate {
			opportunities = append(opportunities, opportunityFinding{
				Variable:   todo.Variable,
				Class:      classNoCandidate,
				Constraint: todo.Constraint,
				File:       todo.Range.File,
			})

			continue
		}

		answer := ""
		if len(todo.Attempted) > 0 {
			answer = todo.Attempted[len(todo.Attempted)-1]
		}

		probe[todo.ID] = opportunityFinding{
			Variable:   todo.Variable,
			Constraint: todo.Constraint,
			File:       todo.Range.File,
			Probe:      answer,
		}
	}

	if len(probe) == 0 {
		return opportunities, nil
	}

	answers := make([]string, 0, len(probe))
	for id, opportunity := range probe {
		if opportunity.Probe == "" {
			continue
		}

		answers = append(answers, id+"="+opportunity.Probe)
	}

	result, err := engine.Run(ctx, &engine.TodosRequest{Common: common, Answers: answers})
	if err != nil {
		return nil, err
	}

	probed := map[string]report.Todo{}
	for _, todo := range result.Characterisation.Todos {
		probed[todo.ID] = todo
	}

	for id, opportunity := range probe {
		todo, found := probed[id]
		if !found {
			return nil, errProbeDropped
		}

		opportunity.Class = probeClass(todo)
		opportunity.Probe = todo.Diagnostic

		opportunities = append(opportunities, opportunity)
	}

	return opportunities, nil
}

// probeClass reads the probe's outcome. Answered means the evaluator could
// not decide the constraint even against the refused candidate — the
// fail-open acceptance moved it. Still refused with the satisfaction
// diagnostic means the evaluator decided the candidate false outright. Any
// other refusal — on this record, the binding refusal against a redacted
// attempted value — means the evidence that would classify the opportunity
// is withheld, and the census withholds rather than guesses.
func probeClass(probed report.Todo) opportunityClass {
	switch {
	case probed.Status == report.TodoAnswered:
		return classUndecidable
	case probed.Diagnostic == probeNotSatisfied:
		return classRefused
	default:
		return classWithheld
	}
}

// opportunityReading is one module's full static reading: its row plus the
// declaration syntax of every declared input.
type opportunityReading struct {
	row    opportunityRow
	strata map[string]bool
}

// readOpportunityReading runs the denominator read, the first `todos` invocation
// and the classification probe for one module directory. A module that
// refuses to parse is published as refused, not fatal — one unreadable
// module never aborts a census. An operational failure of an invocation is
// returned for the caller's retry.
func readOpportunityReading(
	ctx context.Context, corpus, module, ref, moduleDir string, common engine.Common,
) (opportunityReading, error) {
	result := opportunityReading{strata: map[string]bool{}}
	result.row.Corpus = corpus
	result.row.Module = module
	result.row.Ref = ref

	configuration, err := discovery.Discover(moduleDir, engine.DefaultTestDirectory)
	if err != nil {
		result.row.Refused = err.Error()

		// A module whose sources do not parse is a published row, not an
		// abort — the refusal is the fact the census records.
		return result, nil //nolint:nilerr // the refusal is published, not raised.
	}

	found, names, declared := rootVariables(configuration)
	if !found {
		result.row.Refused = "no root module"

		return result, nil
	}

	for index, name := range names {
		result.row.Variables++
		result.strata[name] = declared[index]

		if declared[index] {
			result.row.JSONVariables++
		}
	}

	result.row.JSONDeclared = result.row.JSONVariables > 0

	outcome, err := engine.Run(ctx, &engine.TodosRequest{Common: common})
	if err != nil {
		return result, err
	}

	countInvocation(&result.row, result.strata, outcome)

	opportunities, err := classifyTodosLive(ctx, common, outcome.Characterisation.Todos)
	if err != nil {
		return result, err
	}

	seen := map[string]bool{}

	for _, opportunity := range opportunities {
		if seen[opportunity.Variable] {
			continue
		}

		seen[opportunity.Variable] = true
		result.row.Opportunities = append(result.row.Opportunities, opportunity)
	}

	return result, nil
}

// countInvocation reads one `todos` outcome into the row's rung counts. The
// rung counts and the opportunity list come off the same invocation, so a
// count and its evidence can never disagree. The defaults rung leaves
// nothing behind to count — a variable resolved from its own default
// appears in no scenario — so the derived defaults total is the denominator
// minus what the deeper rungs and the listing left.
func countInvocation(row *opportunityRow, strata map[string]bool, outcome report.Report) {
	resolved := map[string]bool{}

	for _, scenario := range outcome.Characterisation.Scenarios {
		if scenario.Name != defaultCensusScenario {
			continue
		}

		for _, input := range scenario.Inputs {
			if resolved[input.Name] {
				continue
			}

			resolved[input.Name] = true

			switch input.Provenance { //nolint:exhaustive // the defaults rung leaves no assignment behind.
			case report.FromValidation:
				row.Mined++
				if strata[input.Name] {
					row.JSONMined++
				}
			case report.FromType:
				row.Typed++
				if strata[input.Name] {
					row.JSONTyped++
				}
			default:
				// FromDefault leaves no assignment behind and FromAnswer
				// needs an answer on record; neither rung counts here.
			}
		}
	}

	for _, todo := range outcome.Characterisation.Todos {
		row.Todos++
		if strata[todo.Variable] {
			row.JSONTodos++
		}
	}

	row.Defaults = row.Variables - row.Mined - row.Typed - row.Todos
}

// TestTheOpportunityCensusClassifiesUndecidableConstraints holds the
// undecidable class over the fixture whose constraint the static evaluator
// cannot decide: `can(cidrnetmask(...))` names no function the evaluator
// implements, so the refused candidate, offered back as an answer, is
// accepted fail-open and the judgement point moves.
func TestTheOpportunityCensusClassifiesUndecidableConstraints(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedTodoFixture)

	common := commonRequest(t, module)

	outcome, err := engine.Run(t.Context(), todosRequest(t, module))
	if err != nil {
		t.Fatalf("todos: %v", err)
	}

	todos := outcome.Characterisation.Todos
	if len(todos) != 1 {
		t.Fatalf("listed %d judgement points, want one", len(todos))
	}

	if todos[0].Diagnostic != gapTypedRefused {
		t.Fatalf("diagnostic = %q, want the refused-candidate gap", todos[0].Diagnostic)
	}

	opportunities := classifyTodos(t, common, todos)
	if len(opportunities) != 1 {
		t.Fatalf("classified %d opportunities, want one", len(opportunities))
	}

	if opportunities[0].Class != classUndecidable {
		t.Fatalf("class = %q, want %q", opportunities[0].Class, classUndecidable)
	}

	if opportunities[0].Probe != "" {
		t.Fatalf("the probe left a diagnostic on an accepted answer: %q", opportunities[0].Probe)
	}

	if opportunities[0].Constraint == "" || opportunities[0].File == "" {
		t.Fatalf("the opportunity carries no verbatim evidence: %+v", opportunities[0])
	}
}

// TestTheOpportunityCensusClassifiesRefusedTypedCandidates holds the refused
// class over the fixture whose typed candidate the static evaluator decides
// false: the same candidate offered back as an answer stays refused, with
// the satisfaction diagnostic naming the validation that decided it.
func TestTheOpportunityCensusClassifiesRefusedTypedCandidates(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedJSONTodoFixture)

	common := commonRequest(t, module)

	outcome, err := engine.Run(t.Context(), todosRequest(t, module))
	if err != nil {
		t.Fatalf("todos: %v", err)
	}

	todos := outcome.Characterisation.Todos
	if len(todos) != 1 {
		t.Fatalf("listed %d judgement points, want one", len(todos))
	}

	if todos[0].Diagnostic != gapTypedRefused {
		t.Fatalf("diagnostic = %q, want the refused-candidate gap", todos[0].Diagnostic)
	}

	opportunities := classifyTodos(t, common, todos)
	if len(opportunities) != 1 {
		t.Fatalf("classified %d opportunities, want one", len(opportunities))
	}

	if opportunities[0].Class != classRefused {
		t.Fatalf("class = %q, want %q", opportunities[0].Class, classRefused)
	}

	if opportunities[0].Probe != probeNotSatisfied {
		t.Fatalf("probe diagnostic = %q, want the satisfaction refusal", opportunities[0].Probe)
	}
}

// TestTheOpportunityCensusClassifiesMissingTypedCandidates holds the
// no-candidate class over the fixture whose declared type nests past the
// synthesiser's limit: no candidate exists to refuse, so the listing's gap
// alone classifies the opportunity and no probe runs.
func TestTheOpportunityCensusClassifiesMissingTypedCandidates(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedNoTypeFixture)

	outcome, err := engine.Run(t.Context(), todosRequest(t, module))
	if err != nil {
		t.Fatalf("todos: %v", err)
	}

	todos := outcome.Characterisation.Todos
	if len(todos) != 1 {
		t.Fatalf("listed %d judgement points, want one", len(todos))
	}

	if todos[0].Diagnostic != gapNoTypedCandidate {
		t.Fatalf("diagnostic = %q, want the no-candidate gap", todos[0].Diagnostic)
	}

	opportunities := classifyTodos(t, commonRequest(t, module), todos)
	if len(opportunities) != 1 {
		t.Fatalf("classified %d opportunities, want one", len(opportunities))
	}

	if opportunities[0].Class != classNoCandidate {
		t.Fatalf("class = %q, want %q", opportunities[0].Class, classNoCandidate)
	}

	if opportunities[0].Probe != "" {
		t.Fatalf("the no-candidate class ran a probe: %q", opportunities[0].Probe)
	}
}

// TestTheOpportunityCensusWithholdsRedactedEvidence holds the withheld
// outcome over the secret fixture: the attempted value on record is the
// redaction marker, so the probe's answer never binds, and the census
// publishes the withholding instead of inventing a class the withheld
// evidence cannot support. The redaction itself survives the probe — the
// marker is all that ever leaves the module.
func TestTheOpportunityCensusWithholdsRedactedEvidence(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedSecretFixture)

	common := commonRequest(t, module)

	outcome, err := engine.Run(t.Context(), todosRequest(t, module))
	if err != nil {
		t.Fatalf("todos: %v", err)
	}

	todos := outcome.Characterisation.Todos
	if len(todos) != 1 {
		t.Fatalf("listed %d judgement points, want one", len(todos))
	}

	if len(todos[0].Attempted) == 0 {
		t.Fatal("the listing names no attempted value")
	}

	for _, attempted := range todos[0].Attempted {
		if attempted != report.SensitiveWithheld {
			t.Fatalf("the listing leaked a value: %q", attempted)
		}
	}

	opportunities := classifyTodos(t, common, todos)
	if len(opportunities) != 1 {
		t.Fatalf("classified %d opportunities, want one", len(opportunities))
	}

	if opportunities[0].Class != classWithheld {
		t.Fatalf("class = %q, want %q", opportunities[0].Class, classWithheld)
	}

	// The redaction rule replaces the whole diagnostic for a sensitive
	// variable, so the probe's refusal surfaces as the marker itself — the
	// census reads the class from the refusal shape, never from leaked text.
	if opportunities[0].Probe != report.SensitiveWithheld {
		t.Fatalf("probe diagnostic = %q, want the withheld marker", opportunities[0].Probe)
	}
}

// TestTheCensusDenominatorCountsJSONDeclaredVariables holds the denominator
// over both declaration syntaxes: the same boundary the synthesiser reads
// its inputs from counts a `.tf.json` input into the census denominator,
// with its stratum recorded, exactly as a native one.
func TestTheCensusDenominatorCountsJSONDeclaredVariables(t *testing.T) {
	t.Parallel()

	native := copyFixture(t, untestedTodoFixture)
	total, jsonCount := opportunityDenominator(t, native)
	if total != 1 || jsonCount != 0 {
		t.Fatalf("native denominator = (%d, %d), want (1, 0)", total, jsonCount)
	}

	declared := copyFixture(t, untestedJSONVariableFixture)
	total, jsonCount = opportunityDenominator(t, declared)
	if total != 1 || jsonCount != 1 {
		t.Fatalf("JSON denominator = (%d, %d), want (1, 1)", total, jsonCount)
	}
}

// TestAnEmptyJSONStratumIsPublishedAsUnmeasured holds the publication rule
// over a corpus whose rows carry no JSON-declared input: the JSON stratum is
// published as unmeasured — a state, not a zero — and a corpus that does
// carry one publishes measured counts.
func TestAnEmptyJSONStratumIsPublishedAsUnmeasured(t *testing.T) {
	t.Parallel()

	const corpusName = "corpus"

	empty := summariseStrata([]opportunityRow{
		{Corpus: corpusName, Module: "native", Variables: 2, Defaults: 0, Mined: 1, Todos: 1},
		{Corpus: corpusName, Module: "refused", Refused: "does not parse"},
	})

	if empty.JSON != nil {
		t.Fatalf("the JSON stratum of a corpus with no JSON-declared input published %+v, want unmeasured", *empty.JSON)
	}

	if empty.Native.Reached != 2 || empty.Native.Fired != 1 {
		t.Fatalf("native stratum = %+v, want reached 2 fired 1", empty.Native)
	}

	declared := summariseStrata([]opportunityRow{
		{
			Corpus: "corpus", Module: "json", Variables: 1, JSONVariables: 1,
			JSONDeclared: true, Defaults: 0, Mined: 1, JSONMined: 1,
		},
	})

	if declared.JSON == nil {
		t.Fatal("the JSON stratum of a corpus with a JSON-declared input published unmeasured")
	}

	if declared.JSON.Reached != 1 || declared.JSON.Fired != 1 {
		t.Fatalf("JSON stratum = %+v, want reached 1 fired 1", *declared.JSON)
	}

	if declared.Native.Reached != 0 || declared.Native.Fired != 0 {
		t.Fatalf("native stratum = %+v, want both zero", declared.Native)
	}
}

// TestTheMinedCountsSplitByStratum holds the split over the two mined
// fixtures: the native declaration and the `.tf.json` declaration each land
// in their own stratum, so the count a rung-removal decision would read is
// per syntax, not corpus-wide.
func TestTheMinedCountsSplitByStratum(t *testing.T) {
	t.Parallel()

	native := readOpportunityRow(t, untestedMinedFixture)
	if native.row.Variables != 1 || native.row.Mined != 1 || native.row.Todos != 0 || native.row.Defaults != 0 {
		t.Fatalf("native mined row = %+v, want one mined input and no opportunities", native.row)
	}

	declared := readOpportunityRow(t, untestedJSONMinedFixture)
	if declared.row.Variables != 1 || declared.row.Mined != 1 || declared.row.Todos != 0 || declared.row.Defaults != 0 {
		t.Fatalf("JSON mined row = %+v, want one mined input and no opportunities", declared.row)
	}

	if !declared.row.JSONDeclared || declared.row.JSONMined != 1 {
		t.Fatalf("JSON row stratum = (%t, mined %d), want declared with the mined input",
			declared.row.JSONDeclared, declared.row.JSONMined)
	}

	summary := summariseStrata([]opportunityRow{native.row, declared.row})
	if summary.Native.Reached != 1 || summary.Native.Fired != 1 {
		t.Fatalf("native stratum = %+v, want reached 1 fired 1", summary.Native)
	}

	if summary.JSON == nil || summary.JSON.Reached != 1 || summary.JSON.Fired != 1 {
		t.Fatalf("JSON stratum published %+v, want reached 1 fired 1", summary.JSON)
	}
}

// TestTheCensusReadingIsInternallyConsistent holds the census's arithmetic
// over a fixture with opportunities: every declared input is accounted for
// exactly once across the derived defaults total, the resolved deeper-rung
// counts and the listing.
func TestTheCensusReadingIsInternallyConsistent(t *testing.T) {
	t.Parallel()

	read := readOpportunityRow(t, untestedTodoFixture)

	row := read.row
	if row.Variables != row.Defaults+row.Mined+row.Typed+row.Todos {
		t.Fatalf("the reading lost an input: %+v", row)
	}

	if row.Defaults != 0 || row.Todos != 1 || len(row.Opportunities) != 1 {
		t.Fatalf("the reading disagrees with the fixture: %+v", row)
	}

	if row.JSONDefaults+row.JSONMined+row.JSONTyped+row.JSONTodos != 0 {
		t.Fatalf("a native module grew a JSON stratum: %+v", row)
	}
}

// readOpportunityRow runs the census's reading over a fixture and returns it,
// for the offline gates.
func readOpportunityRow(t *testing.T, fixture string) opportunityReading {
	t.Helper()

	module := copyFixture(t, fixture)

	read, err := readOpportunityReading(
		t.Context(), "fixtures", fixture, "", module, commonRequest(t, module),
	)
	if err != nil {
		t.Fatalf("census over %s: %v", fixture, err)
	}

	if read.row.Refused != "" {
		t.Fatalf("census over %s refused: %s", fixture, read.row.Refused)
	}

	return read
}
