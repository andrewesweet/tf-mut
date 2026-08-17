//go:build integration

package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3e.1 (#53): the admission measurement, per the C7 disposition — the
// generated catalogue "may widen standard only after published candidate
// counts, generated-mutant counts and per-pair invalid/error rates on a real
// module". This test produces that publication on the network-gated
// real-provider fixture; admission itself is a separate, evidence-carrying
// change that cites it.

// admissionMeasurement is the published shape.
type admissionMeasurement struct {
	Fixture          string                  `json:"fixture"`
	CandidateSites   int                     `json:"candidate_sites"`
	GeneratedCount   int                     `json:"generated_mutants"`
	PerPair          map[string]pairRow      `json:"per_pair"`
	OperatorErrors   []report.OperatorErrors `json:"operator_errors"`
	TerraformVersion string                  `json:"terraform_version"`
}

type pairRow struct {
	Generated     int `json:"generated"`
	Invalid       int `json:"invalid"`
	KilledByError int `json:"killed_by_error"`
	Killed        int `json:"killed"`
	Survived      int `json:"survived"`
}

// It shares the plugin cache; nothing else may run beside it.
//
//nolint:paralleltest // shares the integration plugin cache serially.
func TestAdmissionMeasurementOnTheRealModule(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, "aws-applied")

	config := networkConfig(t, module)
	config.NoCache = true
	config.GeneratedFunctions = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	pairs := map[string]pairRow{}
	generated := 0
	sites := map[string]bool{}
	pair := regexp.MustCompile(`(?m)^-.*?([a-z_:]+)\(`)
	replacement := regexp.MustCompile(`(?m)^\+.*?([a-z_:]+)\(`)

	for _, mutant := range result.Mutants {
		if mutant.Operator != generatedOperator {
			continue
		}

		generated++
		sites[mutant.Site] = true

		from := pair.FindStringSubmatch(mutant.Diff)
		to := replacement.FindStringSubmatch(mutant.Diff)

		if from == nil || to == nil {
			continue
		}

		key := strings.TrimPrefix(from[1], "core::") + "->" + strings.TrimPrefix(to[1], "core::")
		row := pairs[key]
		row.Generated++

		//nolint:exhaustive // the measured outcome classes; the rest are not admission evidence.
		switch mutant.State {
		case report.Invalid:
			row.Invalid++
		case report.KilledByError:
			row.KilledByError++
		case report.Killed:
			row.Killed++
		case report.Survived:
			row.Survived++
		default:
		}

		pairs[key] = row
	}

	if generated == 0 {
		t.Fatal("the real module generated no family mutants; the measurement is empty")
	}

	publishAdmission(t, admissionMeasurement{
		Fixture:          "aws-applied",
		CandidateSites:   len(sites),
		GeneratedCount:   generated,
		PerPair:          pairs,
		OperatorErrors:   result.OperatorErrors,
		TerraformVersion: result.TerraformVersion,
	})

	t.Logf("%d family mutants over %d sites; per-pair rows: %d", generated, len(sites), len(pairs))
}

func publishAdmission(t *testing.T, recorded admissionMeasurement) {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		return
	}

	directory := filepath.Join(root, ".artifacts", "performance")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatalf("creating %s: %v", directory, err)
	}

	encoded, err := json.MarshalIndent(recorded, "", "  ")
	if err != nil {
		t.Fatalf("encoding measurement: %v", err)
	}

	path := filepath.Join(directory, "m3e-admission.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
