package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

const literalOutputKeysModule = `locals {
  tags = {
    "cost$" = "steady"
    "tax%" = "steady"
    "a\u0008b" = "steady"
    "[prod]" = "steady"
  }
}
output "regions" {
  value = tomap(local.tags)
}
`

func TestNulledSetOutputSuggestionsVerifyThroughTerraform(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, suggestOutputList)
	testsupport.WriteFile(t, filepath.Join(module, mainFile), "output \"regions\" {\n  value = toset([\"steady\"])\n}\n")
	testsupport.WriteFile(t, filepath.Join(module, unitTestFile), "run \"observe\" {\n  command = plan\n}\n")
	request := suggestRequest(t, module)
	request.IncludeOperators = []string{warmupOutputNullOperator}
	result := runSuggest(t, request)

	for _, suggestion := range result.Suggestions {
		if suggestion.Expression == "output.regions != null" && suggestion.Status == report.SuggestionVerified &&
			suggestion.Verification != nil && suggestion.Verification.Baseline.Passed && suggestion.Verification.Mutant.Passed {
			return
		}
	}
	t.Fatalf("no verified non-null suggestion: %+v", result.Suggestions)
}

func TestLiteralOutputMemberSuggestionsVerifyThroughTerraform(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, suggestOutputList)
	testsupport.WriteFile(t, filepath.Join(module, mainFile), literalOutputKeysModule)
	testsupport.WriteFile(t, filepath.Join(module, unitTestFile), "run \"observe\" {\n  command = apply\n}\n")
	request := suggestRequest(t, module)
	request.IncludeOperators = []string{"STR-CASE"}
	result := runSuggest(t, request)

	for _, key := range []string{`"cost$"`, `"tax%"`, `"a\u0008b"`, `"[prod]"`} {
		want := "try(output.regions[" + key + "], null) == \"steady\""
		found := false
		for _, suggestion := range result.Suggestions {
			if suggestion.Expression == want && suggestion.Status == report.SuggestionVerified &&
				suggestion.Verification != nil && suggestion.Verification.Baseline.Passed && suggestion.Verification.Mutant.Passed {
				found = true
			}
		}
		if !found {
			t.Fatalf("no verified suggestion %s: %+v", want, result.Suggestions)
		}
	}
}

func TestLiteralOutputMemberPinsVerifyThroughTerraform(t *testing.T) {
	t.Parallel()

	module := t.TempDir()
	testsupport.WriteFile(t, filepath.Join(module, mainFile), literalOutputKeysModule)
	result, err := engine.Run(t.Context(), characteriseRequest(t, module))
	if err != nil {
		t.Fatal(err)
	}
	if result.Characterisation == nil || !result.Characterisation.Complete {
		t.Fatalf("the suite did not verify green: %+v", result.Characterisation)
	}
	for _, key := range []string{`"cost$"`, `"tax%"`, `"a\u0008b"`, `"[prod]"`} {
		want := "try(output.regions[" + key + "], null) == \"steady\""
		found := false
		for _, pin := range result.Characterisation.Pins {
			if pin.Expression == want && pin.Status == report.Pinned {
				found = true
			}
		}
		if !found {
			t.Fatalf("no verified pin %s: %+v", want, result.Characterisation.Pins)
		}
	}
}
