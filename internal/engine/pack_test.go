package engine_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	tfconfig "github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M5c.1 pack mechanism. A pack is data under a normative contract: the
// catalogue gains exactly the form operators, a user-defined pack registered
// in `.tf-mut.hcl` parameterises them, deduplication is unchanged, and every
// (pack, entry) whose rewrite produced the surviving bytes is recorded on the
// survivor as its origins. The fixture is the offline witness: user packs over
// `terraform_data.input`, whose builtin schema type is `dynamic`.

const (
	packsFixture   = "packs"
	acmePack       = "acme"
	secondPack     = "second"
	flagSite       = "terraform_data.flag.input"
	aclSite        = "terraform_data.acl.input"
	sizeSite       = "terraform_data.size.input"
	noteSite       = "terraform_data.note.input"
	packConfigFile = ".tf-mut.hcl"
	acmePackFile   = "packs/acme.hcl"
)

// packPreview previews the fixture module with the named packs selected.
func packPreview(t *testing.T, module string, packs ...string) report.Report {
	t.Helper()

	request := previewRequest(t, module)
	request.Packs = packs

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("preview with packs %v: %v", packs, err)
	}

	return result
}

// packRun grades the fixture module, cache off, with the named packs.
func packRun(t *testing.T, module string, packs ...string) report.Report {
	t.Helper()

	request := baseConfig(t, module)
	request.Packs = packs
	request.NoCache = true

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("run with packs %v: %v", packs, err)
	}

	return result
}

// mutantAt finds the one mutant owning a site under an operator.
func mutantAt(t *testing.T, result report.Report, operator, site string) report.Mutant {
	t.Helper()

	for _, mutant := range result.Mutants {
		if mutant.Operator == operator && mutant.Site == site {
			return mutant
		}
	}

	t.Fatalf("no %s mutant at %s; sites were %v", operator, site, sites(result))

	return report.Mutant{}
}

func origin(operator, pack, entry string) report.Origin {
	return report.Origin{Operator: operator, Pack: pack, Entry: entry}
}

// TestAUserPackGeneratesClassifiesAndSuggestsThroughTheSeam is the mechanism
// end to end: preview lists the pack mutants with their tier and origins, run
// grades them like any Tier 1–3 mutant, and the pack survivor reaches the
// suggestion engine through the existing adapters and comes back verified.
func TestAUserPackGeneratesClassifiesAndSuggestsThroughTheSeam(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)

	previewed := packPreview(t, module, acmePack)

	acl := mutantAt(t, previewed, string(mutation.PackReplace), aclSite)
	if acl.Tier != string(mutation.TierPack) || acl.State != report.Pending {
		t.Fatalf("preview pack mutant = tier %s state %s, want pack Pending", acl.Tier, acl.State)
	}

	if !reflect.DeepEqual(acl.Origins, []report.Origin{origin("PACK-REPLACE", acmePack, "acl-public")}) {
		t.Fatalf("preview origins = %+v", acl.Origins)
	}

	if !strings.Contains(acl.Diff, `"public-read"`) {
		t.Fatalf("the pack rewrite is not the entry's: %s", acl.Diff)
	}

	graded := packRun(t, module, acmePack)

	if state := mutantAt(t, graded, string(mutation.PackReplace), aclSite).State; state != report.Killed {
		t.Fatalf("the asserted pack mutant is %s, want Killed", state)
	}

	note := mutantAt(t, graded, string(mutation.PackReplace), noteSite)
	if note.State != report.Survived || note.Tier != string(mutation.TierPack) {
		t.Fatalf("the unasserted pack mutant is %s tier %s, want Survived pack", note.State, note.Tier)
	}

	scored := false
	for _, mutant := range graded.Mutants {
		if mutant.ID == note.ID && mutant.Provenance != nil {
			scored = true
		}
	}

	if !scored {
		t.Fatal("the pack survivor carries no provenance; it is outside the run's population")
	}

	request := suggestRequest(t, module)
	request.Packs = []string{acmePack}
	request.NoCache = true

	suggested := runSuggest(t, request)

	// The pack survivor shares its killing assertion with the language
	// operators' survivors at the same site, and survivors sharing one
	// assertion collapse into one suggestion: the pack mutant is either the
	// suggestion's survivor or among the others it also kills.
	verified := withStatus(suggested, report.SuggestionVerified)
	index := slices.IndexFunc(verified, func(suggestion report.Suggestion) bool {
		return suggestion.MutantID == note.ID || slices.Contains(suggestion.AlsoKills, note.ID)
	})

	if index < 0 {
		t.Fatalf("no verified suggestion for the pack survivor %s; statuses were %s",
			note.ID, statusSummary(suggested.Suggestions))
	}

	if verified[index].Expression != `output.note == "keep"` {
		t.Fatalf("expression = %q, want the assertion that kills the pack fault", verified[index].Expression)
	}
}

// TestOriginsNameThePackEntryOnACollapsedBooleanFlip is the origins case.
// BOOL-LITERAL-FLIP sorts before PACK-FLIP and owns the row at the boolean
// site; the row keeps its operator, tier and identity, and carries every
// contributing entry from both packs, sorted and deduplicated by (pack,
// entry). Two packs asking for one identical numeric rewrite both appear on
// the NUM-ZERO row. Nothing else carries origins, and a run with no pack
// carries none anywhere, with identical identifiers, baseline and verdicts.
func TestOriginsNameThePackEntryOnACollapsedBooleanFlip(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)

	with := packRun(t, module, acmePack, secondPack)
	without := packRun(t, module)

	flag := mutantAt(t, with, string(mutation.BoolLiteralFlip), flagSite)
	wantFlag := []report.Origin{
		origin("PACK-FLIP", acmePack, "flag-off"),
		origin("PACK-FLIP", acmePack, "flag-off-again"),
		origin("PACK-FLIP", secondPack, "flag-clear"),
	}

	if !reflect.DeepEqual(flag.Origins, wantFlag) {
		t.Fatalf("collapsed boolean flip origins = %+v, want %+v", flag.Origins, wantFlag)
	}

	unpacked := mutantAt(t, without, string(mutation.BoolLiteralFlip), flagSite)
	if flag.Tier != string(mutation.TierStandard) || flag.ID != unpacked.ID {
		t.Fatal("the surviving row's tier or identity moved under the pack")
	}

	size := mutantAt(t, with, string(mutation.NumZero), sizeSite)
	wantSize := []report.Origin{
		origin("PACK-REPLACE", acmePack, "size-zero"),
		origin("PACK-REPLACE", secondPack, "size-nought"),
	}

	if !reflect.DeepEqual(size.Origins, wantSize) {
		t.Fatalf("two packs requesting identical bytes = %+v, want %+v", size.Origins, wantSize)
	}

	for _, result := range []report.Report{with, without} {
		for _, mutant := range result.Mutants {
			if len(mutant.Origins) == 0 && mutant.Tier == string(mutation.TierPack) {
				t.Fatalf("pack-tier mutant %s carries no origins", mutant.ID)
			}

			if len(mutant.Origins) > 0 && mutant.Site != flagSite && mutant.Site != sizeSite &&
				mutant.Site != aclSite && mutant.Site != noteSite {
				t.Fatalf("%s at %s carries origins %+v where no pack contributed",
					mutant.Operator, mutant.Site, mutant.Origins)
			}
		}
	}

	for _, mutant := range without.Mutants {
		if len(mutant.Origins) > 0 || mutant.Tier == string(mutation.TierPack) {
			t.Fatalf("a run with no pack carries origins or a pack tier on %s", mutant.ID)
		}
	}

	// Identity, baseline and verdicts are unchanged by the pack: every mutant
	// of the no-pack run is in the pack run under the same identifier with
	// the same state, and the baseline fingerprint is the same.
	states := map[string]report.State{}
	for _, mutant := range with.Mutants {
		states[mutant.ID] = mutant.State
	}

	for _, mutant := range without.Mutants {
		if state, found := states[mutant.ID]; !found || state != mutant.State {
			t.Fatalf("mutant %s (%s at %s) is %s without the pack and %s with it",
				mutant.ID, mutant.Operator, mutant.Site, mutant.State, state)
		}
	}

	if with.Baseline.Fingerprint != without.Baseline.Fingerprint {
		t.Fatal("the baseline fingerprint moved under the pack")
	}
}

// TestDisablingOriginAggregationTurnsTheOriginsCaseRed is the red proof the
// spec demands: not the sort order, aggregation itself. With aggregation
// disabled, the BOOL-LITERAL-FLIP row a pack entry also produced loses the
// entry, which is the observation the origins case fails on.
//
//nolint:paralleltest // owns the package-global generation hook for its lifetime.
func TestDisablingOriginAggregationTurnsTheOriginsCaseRed(t *testing.T) {
	module := copyFixture(t, packsFixture)
	engine.SetGenerationDefectSeed(t, module, mutation.DefectDropOrigins)

	result := packPreview(t, module, acmePack, secondPack)

	if flag := mutantAt(t, result, string(mutation.BoolLiteralFlip), flagSite); len(flag.Origins) != 0 {
		t.Fatalf("with aggregation disabled the collapsed row still carries %+v; the red proof is void",
			flag.Origins)
	}

	// The pack-owned row keeps its own single origin: the defect removes
	// aggregation, not the survivor's own provenance.
	if acl := mutantAt(t, result, string(mutation.PackReplace), aclSite); len(acl.Origins) != 1 {
		t.Fatalf("the pack-owned row's own origin was lost too: %+v", acl.Origins)
	}
}

// TestReversingOwnershipLosesNoContributor: aggregation is independent of the
// winner. With the operator order reversed the pack operator owns the boolean
// row — same bytes, tier pack — and the origins are exactly the ones the
// language-operator row carried.
//
//nolint:paralleltest // owns the package-global generation hook for its lifetime.
func TestReversingOwnershipLosesNoContributor(t *testing.T) {
	module := copyFixture(t, packsFixture)

	forward := packPreview(t, module, acmePack, secondPack)
	forwardFlag := mutantAt(t, forward, string(mutation.BoolLiteralFlip), flagSite)

	engine.SetGenerationDefectSeed(t, module, mutation.DefectReverseOwnership)

	reversed := packPreview(t, module, acmePack, secondPack)
	reversedFlag := mutantAt(t, reversed, string(mutation.PackFlip), flagSite)

	if reversedFlag.Diff != forwardFlag.Diff || reversedFlag.Tier != string(mutation.TierPack) {
		t.Fatalf("reversal did not hand the same bytes to the pack operator: tier %s\n%s",
			reversedFlag.Tier, reversedFlag.Diff)
	}

	if !reflect.DeepEqual(reversedFlag.Origins, forwardFlag.Origins) {
		t.Fatalf("reversing ownership changed the origins: %+v, was %+v",
			reversedFlag.Origins, forwardFlag.Origins)
	}

	if len(reversed.Mutants) != len(forward.Mutants) {
		t.Fatalf("reversal changed the population size: %d, was %d",
			len(reversed.Mutants), len(forward.Mutants))
	}
}

// TestEveryPackContractRowIsRefusedByName pins the load-time and
// registration rows of the contract through the seam: each is a
// configuration error before any Terraform runs, naming what the contract
// says it names — a position, both ranges, the no-op, the reserved name.
func TestEveryPackContractRowIsRefusedByName(t *testing.T) {
	t.Parallel()

	for name, row := range packContractRows() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, packsFixture)
			selected := acmePack

			if row.pack != "" {
				writeFile(t, filepath.Join(module, acmePackFile), row.pack)
			}

			if row.config != "" {
				writeFile(t, filepath.Join(module, packConfigFile), row.config)
				selected = row.selected
			}

			request := previewRequest(t, module)
			if selected != "" {
				request.Packs = []string{selected}
			}

			_, err := engine.Run(t.Context(), request)
			if !errors.Is(err, tfconfig.ErrConfig) {
				t.Fatalf("error = %v, want a configuration error", err)
			}

			for _, expected := range row.names {
				if !strings.Contains(err.Error(), expected) {
					t.Fatalf("the refusal does not name %q: %v", expected, err)
				}
			}
		})
	}
}

// packContractRow is one refused row: the pack file or the configuration
// that violates it, the selection that reaches it, and what the refusal names.
type packContractRow struct {
	pack     string
	config   string
	selected string
	names    []string
}

func packEntryText(id, form, from, to string) string {
	return "entry \"" + id + "\" {\n  resource_type = \"terraform_data\"\n  attribute = \"input\"\n" +
		"  form = \"" + form + "\"\n  from = " + from + "\n  to = " + to + "\n}\n"
}

func packContractRows() map[string]packContractRow {
	entry := packEntryText
	partial := func(fields string) string {
		return "entry \"partial\" {\n  resource_type = \"terraform_data\"\n  attribute = \"input\"\n" +
			"  form = \"replace\"\n" + fields + "}\n"
	}

	return map[string]packContractRow{
		"malformed file": {
			pack: "entry \"broken\" {\n", names: []string{"acme.hcl:1,16", "Unclosed configuration block"},
		},
		"non-entry block": {
			pack: "rule \"x\" {\n}\n", names: []string{"acme.hcl:1,1", "not an entry"},
		},
		"expression in a literal slot": {
			pack:  partial("  from = upper(\"a\")\n  to = \"b\"\n"),
			names: []string{"acme.hcl:5,10", "from is an expression"},
		},
		"interpolation in a literal slot": {
			pack:  partial("  from = \"${var.x}\"\n  to = \"b\"\n"),
			names: []string{"acme.hcl:5,10", "from interpolates"},
		},
		"top-level attribute": {
			pack: "version = 1\n" + entry("ok", "flip", "true", "false"), names: []string{"acme.hcl:1,1", "version"},
		},
		"repeated label": {
			pack:  entry("twice", "flip", "true", "false") + entry("twice", "flip", "false", "true"),
			names: []string{"acme.hcl:1,7", "acme.hcl:8,7", "declared twice"},
		},
		"label outside the grammar": {
			pack: entry("Not_OK", "flip", "true", "false"), names: []string{"acme.hcl:1,7", "[a-z0-9-]+"},
		},
		"flip with a non-boolean from": {
			pack: entry("flip-str", "flip", "\"yes\"", "false"), names: []string{"flip-str", "flip needs a boolean from"},
		},
		"flip whose to is not the other": {
			pack: entry("flip-same", "flip", "true", "true"), names: []string{"flip-same", "both true"},
		},
		"replace no-op": {
			pack: entry("noop", "replace", "\"x\"", "\"x\""), names: []string{"noop", "no-op"},
		},
		"replace across kinds": {
			pack: entry("kinds", "replace", "\"3\"", "3"), names: []string{"kinds", "one kind"},
		},
		"unknown form": {
			pack: entry("cidr", "widen-cidr", "\"any-cidr\"", "\"0.0.0.0/0\""), names: []string{"widen-cidr", "not one of"},
		},
		"missing field": {
			pack:  partial("  from = \"a\"\n"),
			names: []string{"partial", "does not set to"},
		},
		"unknown pack": {
			config:   "pack \"acme\" {\n  file = \"packs/acme.hcl\"\n}\n",
			selected: "nobody",
			names:    []string{"\"nobody\" is not registered"},
		},
		"shadowed reserved name": {
			config: "pack \"security-aws\" {\n  file = \"packs/acme.hcl\"\n}\n",
			names:  []string{"security-aws", "shadows the reserved name"},
		},
	}
}

// TestAnUnsupportedAttributeFormIsANoOpInThePackSummary: an entry naming a
// meta-argument — like a nested block, a dynamic body, a data body or
// string-internal structure — is out of M5's scope. It is not an error; it
// finds no site, and preview's pack summary says so by name.
func TestAnUnsupportedAttributeFormIsANoOpInThePackSummary(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)
	writeFile(t, filepath.Join(module, "counted.tf"),
		"resource \"terraform_data\" \"counted\" {\n  count = 1\n  input = \"counted\"\n}\n")
	writeFile(t, filepath.Join(module, acmePackFile),
		"entry \"count-zero\" {\n  resource_type = \"terraform_data\"\n  attribute = \"count\"\n"+
			"  form = \"replace\"\n  from = 1\n  to = 0\n}\n")

	result := packPreview(t, module, acmePack)

	for _, mutant := range result.Mutants {
		if len(mutant.Origins) > 0 {
			t.Fatalf("a meta-argument entry produced %s at %s", mutant.Operator, mutant.Site)
		}
	}

	if !slices.ContainsFunc(result.Warnings, func(warning string) bool {
		return strings.Contains(warning, `pack "acme": entry "count-zero"`) &&
			strings.Contains(warning, "matched no site")
	}) {
		t.Fatalf("preview's pack summary does not record the no-op entry: %v", result.Warnings)
	}
}

// TestSchemaEvidenceRefusesAnUndescribedAttribute: an entry naming an
// attribute the provider schema does not describe generates nothing, even
// with a matching literal in front of it; the same entry over an attribute
// the schema does describe — `input`, type dynamic — generates. The pair is
// the red direction the acceptance criterion asks for.
func TestSchemaEvidenceRefusesAnUndescribedAttribute(t *testing.T) {
	t.Parallel()

	for attribute, wantSite := range map[string]bool{"colour": false, "input": true} {
		t.Run(attribute, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, packsFixture)
			writeFile(t, filepath.Join(module, "odd.tf"),
				"resource \"terraform_data\" \"odd\" {\n  "+attribute+" = \"x\"\n}\n")
			writeFile(t, filepath.Join(module, acmePackFile),
				"entry \"odd\" {\n  resource_type = \"terraform_data\"\n  attribute = \""+attribute+"\"\n"+
					"  form = \"replace\"\n  from = \"x\"\n  to = \"y\"\n}\n")

			result := packPreview(t, module, acmePack)

			generated := slices.ContainsFunc(result.Mutants, func(mutant report.Mutant) bool {
				return mutant.Site == "terraform_data.odd."+attribute && len(mutant.Origins) > 0
			})

			if generated != wantSite {
				t.Fatalf("attribute %s: generated = %t, want %t (the schema describes it: %t)",
					attribute, generated, wantSite, wantSite)
			}
		})
	}
}

// TestATypeIncompatibleReplacementFindsNoSite is the evidence rule's second
// half: `to` must be of the schema-declared type. `terraform_data.id` is a
// string, so a numeric pair finds no site where a string pair does.
func TestATypeIncompatibleReplacementFindsNoSite(t *testing.T) {
	t.Parallel()

	for name, row := range map[string]struct {
		literal, from, to string
		wantSite          bool
	}{
		"number into a string attribute": {literal: "1", from: "1", to: "2", wantSite: false},
		"string into a string attribute": {literal: `"1"`, from: `"1"`, to: `"2"`, wantSite: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, packsFixture)
			writeFile(t, filepath.Join(module, "typed.tf"),
				"resource \"terraform_data\" \"typed\" {\n  id = "+row.literal+"\n}\n")
			writeFile(t, filepath.Join(module, acmePackFile),
				"entry \"typed\" {\n  resource_type = \"terraform_data\"\n  attribute = \"id\"\n"+
					"  form = \"replace\"\n  from = "+row.from+"\n  to = "+row.to+"\n}\n")

			result := packPreview(t, module, acmePack)

			generated := slices.ContainsFunc(result.Mutants, func(mutant report.Mutant) bool {
				return mutant.Site == "terraform_data.typed.id" && len(mutant.Origins) > 0
			})

			if generated != row.wantSite {
				t.Fatalf("generated = %t, want %t", generated, row.wantSite)
			}
		})
	}
}

// TestFlagAndConfiguredPacksMergeAsAUnion: `operators { packs }` and the
// flag are merged as a union, deduplicated by name. A configured pack and a
// flag-selected pack both contribute, and a name given in both is one pack.
func TestFlagAndConfiguredPacksMergeAsAUnion(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)
	appendFile(t, filepath.Join(module, packConfigFile),
		"\noperators {\n  packs = [\"second\"]\n}\n")

	union := packPreview(t, module, acmePack)
	flag := mutantAt(t, union, string(mutation.BoolLiteralFlip), flagSite)

	packs := map[string]bool{}
	for _, entry := range flag.Origins {
		packs[entry.Pack] = true
	}

	if !packs[acmePack] || !packs[secondPack] {
		t.Fatalf("the configured and the flag-selected pack do not both contribute: %+v", flag.Origins)
	}

	both := packPreview(t, module, acmePack, secondPack)
	if !reflect.DeepEqual(mutantAt(t, both, string(mutation.BoolLiteralFlip), flagSite).Origins, flag.Origins) ||
		len(both.Mutants) != len(union.Mutants) {
		t.Fatal("a name given in both the flag and the configuration was not deduplicated once")
	}
}

// TestAConfiguredPackSelectionIsRefusedOnCurateAndUntilDry holds the
// maintainer's ruling on #97 for packs: a configuration-narrowed population
// is refused at configuration time by curate and --until-dry.
func TestAConfiguredPackSelectionIsRefusedOnCurateAndUntilDry(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)
	appendFile(t, filepath.Join(module, packConfigFile),
		"\noperators {\n  packs = [\"acme\"]\n}\n")

	curate := curateRequest(t, module)
	curate.NoCache = true

	if _, err := engine.Run(t.Context(), curate); !errors.Is(err, engine.ErrCuratePopulation) ||
		!strings.Contains(err.Error(), "pack selection") {
		t.Fatalf("curate error = %v, want the population refusal naming the pack selection", err)
	}

	characterise := characteriseRequest(t, module)
	characterise.UntilDry = true

	if _, err := engine.Run(t.Context(), characterise); !errors.Is(err, engine.ErrUntilDryPopulation) ||
		!strings.Contains(err.Error(), "pack selection") {
		t.Fatalf("until-dry error = %v, want the population refusal naming the pack selection", err)
	}
}

// TestOnlyTheGradingRequestsCarryAPackSelection is the seam half of the
// by-name refusal: characterise, todos and curate requests have no field for
// a pack, so a pack on them is not representable through the seam at all;
// the command line's refusal by flag name is the other half.
func TestOnlyTheGradingRequestsCarryAPackSelection(t *testing.T) {
	t.Parallel()

	carries := func(request any) bool {
		_, found := reflect.TypeOf(request).FieldByName("Packs")

		return found
	}

	//nolint:exhaustruct // the field set is the assertion, not the values.
	grading := []any{engine.RunRequest{}, engine.PreviewRequest{}, engine.SuggestRequest{}}
	//nolint:exhaustruct // the field set is the assertion, not the values.
	others := []any{engine.CharacteriseRequest{}, engine.TodosRequest{}, engine.CurateRequest{}}

	for _, request := range grading {
		if !carries(request) {
			t.Fatalf("%T carries no pack selection", request)
		}
	}

	for _, request := range others {
		if carries(request) {
			t.Fatalf("%T carries a pack selection it must refuse", request)
		}
	}
}

// TestAnEditedUserPackIsACacheMiss: the selected pack names and a user
// pack's bytes are in the cache key, so a pack edit replays nothing.
func TestAnEditedUserPackIsACacheMiss(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)

	request := baseConfig(t, module)
	request.Packs = []string{acmePack}

	cachedRun(t, request)

	warm := cachedRun(t, request)
	if warm.Population.Cached == 0 {
		t.Fatal("an unchanged pack run replayed nothing; the miss below would prove nothing")
	}

	appendFile(t, filepath.Join(module, acmePackFile), "\n# an edit that changes no entry\n")

	if cold := cachedRun(t, request); cold.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived a pack edit", cold.Population.Cached)
	}
}

// TestAStaleVerifiedPackSuggestionIsRefused: a pack edit changes the bytes an
// entry produces, so the survivor's content-derived identity and the
// suggestion verified against it are gone; applying the stale identifier is
// refused by name with zero writes.
func TestAStaleVerifiedPackSuggestionIsRefused(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)

	// The pack operator alone, so the verified suggestion's identity derives
	// from the pack survivor and nothing else.
	request := suggestRequest(t, module)
	request.Packs = []string{acmePack}
	request.IncludeOperators = []string{string(mutation.PackReplace)}
	request.NoCache = true

	verified := withStatus(runSuggest(t, request), report.SuggestionVerified)
	if len(verified) == 0 {
		t.Fatal("no verified suggestion to go stale")
	}

	stale := verified[0].ID

	content := readFile(t, filepath.Join(module, acmePackFile))
	writeFile(t, filepath.Join(module, acmePackFile),
		strings.ReplaceAll(content, `to            = "dropped"`, `to            = "changed"`))

	before := treeDigest(t, module)

	request.Apply = []string{stale}

	result := runSuggest(t, request)
	if result.Apply == nil || !strings.Contains(result.Apply.Aborted, stale) {
		t.Fatalf("the stale suggestion was not refused by name: %+v", result.Apply)
	}

	assertTreeUnchanged(t, module, before)
}

// TestAChangedPackFileForcesTheFullPopulationUnderSince: a selected user
// pack's file joins the changed-configuration class, as `.tf-mut.hcl` does.
func TestAChangedPackFileForcesTheFullPopulationUnderSince(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, packsFixture)

	full := packPreview(t, module, acmePack)

	appendFile(t, filepath.Join(module, acmePackFile), "\n# touched\n")

	request := previewRequest(t, module)
	request.Packs = []string{acmePack}
	request.Since = sinceHead

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("since preview: %v", err)
	}

	if len(result.Mutants) != len(full.Mutants) || result.Selection.ForcedFull == "" {
		t.Fatalf("a pack edit selected %d of %d mutants (forced: %q); it must force the full population",
			len(result.Mutants), len(full.Mutants), result.Selection.ForcedFull)
	}
}

// TestNoPackEntersTheStandardPopulation: no pack is ever in `standard`. A
// registered but unselected pack changes nothing, and the operator-matrix
// fixture's standard population carries neither a pack tier nor origins.
func TestNoPackEntersTheStandardPopulation(t *testing.T) {
	t.Parallel()

	registered := packPreview(t, copyFixture(t, packsFixture))
	for _, mutant := range registered.Mutants {
		if mutant.Tier == string(mutation.TierPack) || len(mutant.Origins) > 0 {
			t.Fatalf("a registered, unselected pack reached standard: %s at %s", mutant.Operator, mutant.Site)
		}
	}

	for _, tier := range []mutation.Tier{"", mutation.TierStandard, mutation.TierDeep} {
		request := previewRequest(t, copyFixture(t, operatorsFixture))
		request.Tier = tier

		result, err := engine.Run(t.Context(), request)
		if err != nil {
			t.Fatalf("preview at tier %q: %v", tier, err)
		}

		for _, mutant := range result.Mutants {
			if strings.HasPrefix(mutant.Operator, "PACK-") || mutant.Tier == string(mutation.TierPack) {
				t.Fatalf("tier %q carries the pack operator %s", tier, mutant.Operator)
			}
		}
	}

	if mutation.TierDeep.Includes(mutation.TierPack) || mutation.Tier("pack").Valid() {
		t.Fatal("the pack tier is selectable by breadth")
	}
}

// TestEveryPackOperatorHasASiteInTheOfflineFixture is the M5 gate's own
// offline site witness for the form operators: each fires alone on the
// fixture, without the provider mirror.
func TestEveryPackOperatorHasASiteInTheOfflineFixture(t *testing.T) {
	t.Parallel()

	for _, operator := range []mutation.Operator{mutation.PackFlip, mutation.PackReplace} {
		module := copyFixture(t, packsFixture)

		request := previewRequest(t, module)
		request.Packs = []string{acmePack}
		request.IncludeOperators = []string{string(operator)}

		result, err := engine.Run(t.Context(), request)
		if err != nil {
			t.Fatalf("preview %s: %v", operator, err)
		}

		if len(result.Mutants) == 0 {
			t.Fatalf("%s has no site in the pack fixture", operator)
		}

		for _, mutant := range result.Mutants {
			if mutant.Operator != string(operator) || mutant.Tier != string(mutation.TierPack) || len(mutant.Origins) == 0 {
				t.Fatalf("isolated %s produced %s tier %s origins %+v", operator, mutant.Operator, mutant.Tier, mutant.Origins)
			}
		}
	}
}

// TestARealPackReportValidatesAgainstThePublishedSchema brings the pack runs
// to the 2.4.0 file: run, preview and suggest, with and without a pack, each
// a real document the binary emits. The with-pack documents are the first to
// carry a `pack` tier and `origins`.
func TestARealPackReportValidatesAgainstThePublishedSchema(t *testing.T) {
	t.Parallel()

	schema := loadPublishedSchema(t)

	for name, packs := range map[string][]string{"with-pack": {acmePack, secondPack}, "without-pack": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, packsFixture)

			suggest := suggestRequest(t, module)
			suggest.Packs = packs
			suggest.NoCache = true

			for command, result := range map[string]report.Report{
				"preview": packPreview(t, module, packs...),
				"run":     packRun(t, module, packs...),
				"suggest": runSuggest(t, suggest),
			} {
				carriesOrigins := slices.ContainsFunc(result.Mutants, func(mutant report.Mutant) bool {
					return len(mutant.Origins) > 0
				})

				if carriesOrigins != (len(packs) > 0) {
					t.Fatalf("%s: origins present = %t with packs %v", command, carriesOrigins, packs)
				}

				builder := strings.Builder{}
				if err := report.WriteJSON(&builder, result); err != nil {
					t.Fatalf("rendering %s: %v", command, err)
				}

				document := any(nil)
				if err := json.Unmarshal([]byte(builder.String()), &document); err != nil {
					t.Fatalf("decoding %s: %v", command, err)
				}

				if problems := validateAgainst(schema, schema, document, "$"); len(problems) > 0 {
					t.Fatalf("the real %s report does not validate:\n  %s", command, strings.Join(problems, "\n  "))
				}
			}
		})
	}
}
