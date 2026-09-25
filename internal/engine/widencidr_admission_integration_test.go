//go:build integration

package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The #176 PACK-WIDEN-CIDR admission measurement. The M5-0.3 census admitted
// the scalar Trivy AWS-0104 candidate's form only in principle and deferred
// it; this measurement admits it for real, in a dedicated user pack, held to
// the same decision rule — dropped for invalid, enabled on witnessed bytes,
// otherwise unwitnessed — over the same six targets. Unlike the census
// measurement, generation is narrowed with `--operator` to the new form
// alone, so a target's run leg grades its baseline plus only the widen rows:
// the measurement measures the entry, not the whole standard population.

const (
	widenCIDRPack     = "security-aws-widencidr"
	widenCIDROutput   = "../../.artifacts/measurement/m5-widen-cidr-admission.json"
	widenCIDRRowsPath = "../../.artifacts/measurement/m5-widen-cidr-admission-rows.jsonl"
)

// widenCIDRCandidates is the admitted-under-test scalar slice: the one
// candidate the census deferred to this form. From and To carry the HCL
// source text of the literal, as the census candidates do.
var widenCIDRCandidates = []securityAWSCandidate{ //nolint:gochecknoglobals // immutable measurement pin.
	{
		ID: "trivy-aws-0104", SourceRule: "AWS-0104", SourceLicence: "MIT",
		ResourceType: "aws_vpc_security_group_egress_rule", Attribute: "cidr_ipv4",
		Form: mutation.FormWidenCIDR, From: `"` + mutation.WidenCIDRSentinel + `"`,
		To: `"` + mutation.WidenCIDRAnyIPv4 + `"`,
	},
}

func renderWidenCIDRPack() string {
	return renderCandidatePack(widenCIDRCandidates)
}

//nolint:paralleltest // the six long-running targets share one provider cache and a crash-safe side-car.
func TestTheWidenCIDRPackAdmissionMeasurement(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	pack := renderWidenCIDRPack()
	packDigest := digestBytes([]byte(pack))
	completed := loadAdmissionRows(t, widenCIDRRowsPath)
	rows := map[string]packAdmissionTarget{}

	awsSource := packAdmissionSource{Name: awsMockedFixture, TestRoot: engine.DefaultTestDirectory}
	awsModule := copyFixture(t, awsMockedFixture)
	awsSource.Archive = filepath.Dir(awsModule)
	awsSource.ModuleSubdir = filepath.Base(awsModule)
	awsSource.Commit = digestTree(t, awsModule)
	awsSource.SourceDigest = awsSource.Commit

	sources := []packAdmissionSource{awsSource}
	corpus := loadBenchmarkCorpus(t)

	for _, module := range corpus.Modules {
		if !slices.Contains(scoredPackAdmissionModules(), module.Name) {
			continue
		}

		archive, err := fetchPinnedRepository(t.Context(), t, module, censusArchives)
		if err != nil {
			archive, err = fetchPinnedRepository(t.Context(), t, module, censusArchives)
		}

		if err != nil {
			rows[module.Name] = packAdmissionTarget{
				Name: module.Name, Repository: module.Repository, Commit: module.Commit,
				SourceDigest: module.SHA256, PackDigest: packDigest,
				PreviewError: censusReason(err), RunError: censusReason(err), Retries: 1,
				Entries: map[string]packAdmissionCount{},
			}
			continue
		}

		sources = append(sources, packAdmissionSource{
			Name: module.Name, Repository: module.Repository, Commit: module.Commit,
			ModuleSubdir: module.ModuleSubdir, TestRoot: censusTestDirectory(module.TestRoot), Archive: archive,
			SourceDigest: module.SHA256,
		})
	}

	for _, source := range sources {
		if row, ok := completed[source.Name]; ok && row.SourceDigest == source.SourceDigest &&
			row.PackDigest == packDigest && row.PreviewError == "" && row.RunError == "" {
			rows[source.Name] = row
			continue
		}

		row := runWidenCIDRAdmissionTarget(t, source, pack, packDigest, source.SourceDigest)
		rows[source.Name] = row
		recordAdmissionRow(t, widenCIDRRowsPath, row)
		t.Logf("%s: preview=%q run=%q retries=%d", row.Name, row.PreviewError, row.RunError, row.Retries)
	}

	measurement := assembleCandidatePackAdmission(t, widenCIDRPack, packDigest, widenCIDRCandidates, rows)
	publishWidenCIDRAdmission(t, measurement)

	var enabled []string
	for _, entry := range measurement.Entries {
		if entry.Decision == "enabled" {
			enabled = append(enabled, entry.ID)
		}
	}

	slices.Sort(enabled)
	if !slices.Equal(enabled, widenCIDRAdmittedEntries) {
		t.Errorf("enabled entries = %v, want the published widenCIDRAdmittedEntries %v",
			enabled, widenCIDRAdmittedEntries)
	}

	t.Logf("widen-cidr admission: %d enabled of %d loadable candidates",
		len(enabled), len(measurement.Entries))
}

// TestEveryWidenCIDREntryHasARealProviderWitness re-executes the admitted
// widen-cidr set against hashicorp/aws. The entry must contribute bytes from
// the checked-in aws-mocked egress site and none may be Invalid.
//
//nolint:paralleltest // shares the integration provider cache.
func TestEveryWidenCIDREntryHasARealProviderWitness(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, awsMockedFixture)
	stageUserPack(t, module, widenCIDRPack, "security-aws-widencidr.hcl", renderWidenCIDRPack())

	request := networkConfig(t, module)
	request.NoCache = true
	request.Packs = []string{widenCIDRPack}
	request.IncludeOperators = []string{string(mutation.PackWidenCIDR)}

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("running the widen-cidr witnesses: %v", err)
	}

	seen := map[string]bool{}
	invalid := map[string]bool{}
	for _, mutant := range result.Mutants {
		for _, origin := range mutant.Origins {
			if origin.Pack == widenCIDRPack {
				seen[origin.Entry] = true
				invalid[origin.Entry] = invalid[origin.Entry] || mutant.State == report.Invalid
			}
		}
	}

	for _, entry := range widenCIDRAdmittedEntries {
		if !seen[entry] {
			t.Errorf("enabled entry %q has no aws-mocked witness", entry)
			continue
		}

		if invalid[entry] {
			t.Errorf("enabled entry %q re-executed as Invalid", entry)
		}
	}
}

func runWidenCIDRAdmissionTarget(
	t *testing.T,
	source packAdmissionSource,
	pack, packDigest, sourceDigest string,
) packAdmissionTarget {
	t.Helper()

	module := materialisePackAdmissionSource(t, source)
	stageUserPack(t, module, widenCIDRPack, "security-aws-widencidr.hcl", pack)

	row := packAdmissionTarget{
		Name: source.Name, Repository: source.Repository, Commit: source.Commit,
		SourceDigest: sourceDigest, PackDigest: packDigest, Entries: map[string]packAdmissionCount{},
	}

	previewResult, previewErr := runWidenCIDRAdmissionPreview(t, module, source.TestRoot)
	if admissionOperational(previewErr, previewResult) {
		row.Retries++
		previewResult, previewErr = runWidenCIDRAdmissionPreview(t, module, source.TestRoot)
	}
	if previewErr != nil {
		row.PreviewError = censusReason(previewErr)
	} else {
		row.TerraformVersion = previewResult.TerraformVersion
		observePackPreview(previewResult, &row, widenCIDRPack, string(mutation.PackWidenCIDR))
	}

	runResult, runErr := runWidenCIDRAdmissionRun(t, module, source.TestRoot)
	if admissionOperational(runErr, runResult) {
		row.Retries++
		runResult, runErr = runWidenCIDRAdmissionRun(t, module, source.TestRoot)
	}
	if runErr != nil {
		row.RunError = censusReason(runErr)
	} else {
		row.TerraformVersion = runResult.TerraformVersion
		observePackRun(runResult, &row, widenCIDRPack)
	}

	return row
}

// runWidenCIDRAdmissionPreview and runWidenCIDRAdmissionRun mirror the census
// measurement's legs with the pack narrowed to the widen-cidr operator, so a
// run grades the target's baseline suite once per widen row instead of once
// per standard mutant.
func runWidenCIDRAdmissionPreview(t *testing.T, module, testRoot string) (report.Report, error) {
	t.Helper()

	base := networkConfig(t, module)
	request := previewRequest(t, module)
	request.Common = base.Common
	request.TestDirectory = testRoot
	request.WorkDir = t.TempDir()
	request.Packs = []string{widenCIDRPack}
	request.Tier = mutation.TierStandard
	request.IncludeOperators = []string{string(mutation.PackWidenCIDR)}

	return engine.Run(t.Context(), request)
}

func runWidenCIDRAdmissionRun(t *testing.T, module, testRoot string) (report.Report, error) {
	t.Helper()

	request := networkConfig(t, module)
	request.TestDirectory = testRoot
	request.WorkDir = t.TempDir()
	request.NoCache = true
	request.Packs = []string{widenCIDRPack}
	request.Tier = mutation.TierStandard
	request.IncludeOperators = []string{string(mutation.PackWidenCIDR)}

	return engine.Run(t.Context(), request)
}

func publishWidenCIDRAdmission(t *testing.T, measurement packAdmissionMeasurement) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(widenCIDROutput), 0o750); err != nil {
		t.Fatalf("creating the admission measurement directory: %v", err)
	}
	encoded, err := json.MarshalIndent(measurement, "", "  ")
	if err != nil {
		t.Fatalf("encoding the admission measurement: %v", err)
	}
	if err := os.WriteFile(widenCIDROutput, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing the admission measurement: %v", err)
	}
}

// TestTheWidenCIDRCandidatePackIsLoadableAndHasStableIdentities is the
// offline half: the candidate slice is exactly the published list, loads
// through the engine seam on the offline fixture, and no candidate is shared
// with the census pack — one admission, one pack, no double shipping.
func TestTheWidenCIDRCandidatePackIsLoadableAndHasStableIdentities(t *testing.T) {
	t.Parallel()

	if len(widenCIDRCandidates) != len(widenCIDRAdmittedEntries) {
		t.Errorf("candidate count = %d, want the published admitted count %d",
			len(widenCIDRCandidates), len(widenCIDRAdmittedEntries))
	}

	seen := map[string]bool{}
	for _, candidate := range widenCIDRCandidates {
		if seen[candidate.ID] {
			t.Errorf("candidate identity %q is repeated", candidate.ID)
		}
		seen[candidate.ID] = true

		if candidate.Form != mutation.FormWidenCIDR {
			t.Errorf("candidate %q is a %s, want only %s candidates in this measurement",
				candidate.ID, candidate.Form, mutation.FormWidenCIDR)
		}

		if slices.ContainsFunc(securityAWSCandidates, func(other securityAWSCandidate) bool {
			return other.ID == candidate.ID
		}) {
			t.Errorf("candidate %q is also a census candidate; one admission must not ship twice",
				candidate.ID)
		}
	}

	for _, enabled := range widenCIDRAdmittedEntries {
		if !seen[enabled] {
			t.Errorf("admitted entry %q is not a candidate", enabled)
		}
	}

	pack := renderWidenCIDRPack()
	t.Logf("widen-cidr candidate pack digest: %s", digestBytes([]byte(pack)))

	module := copyFixture(t, packsFixture)
	stageUserPack(t, module, widenCIDRPack, "security-aws-widencidr.hcl", pack)
	request := previewRequest(t, module)
	request.Packs = []string{widenCIDRPack}

	if _, err := engine.Run(t.Context(), request); err != nil {
		t.Fatalf("the widen-cidr candidate pack does not load through the engine seam: %v", err)
	}
}
