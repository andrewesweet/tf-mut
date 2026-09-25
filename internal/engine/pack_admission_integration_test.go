//go:build integration

package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M5-0.3 security-aws admission measurement (#161). The candidate pack is
// deliberately registered as a user pack: security-aws is reserved for the
// later M5c.2 shipping change. Every target gets one complete preview and one
// complete run through engine.Run. An operational failure is retried once and
// retained as a fact about this measurement; one target never aborts the rest.

const (
	securityAWSCensusPack     = "security-aws-census"
	securityAWSCandidateCount = 115
	securityAWSAdmittedCount  = 10
	packAdmissionOutput       = "../../.artifacts/measurement/m5-security-aws-admission.json"
	packAdmissionRows         = "../../.artifacts/measurement/m5-security-aws-admission-rows.jsonl"
)

type securityAWSCandidate struct {
	ID            string
	SourceRule    string
	SourceLicence string
	ResourceType  string
	Attribute     string
	Form          string
	From          string
	To            string
}

func checkovFlip(id, rule, resourceType, attribute string, from bool) securityAWSCandidate {
	return securityAWSCandidate{
		ID: id, SourceRule: rule, SourceLicence: "Apache-2.0",
		ResourceType: resourceType, Attribute: attribute,
		Form: mutation.FormFlip, From: strconv.FormatBool(from), To: strconv.FormatBool(!from),
	}
}

func trivyFlip(id, rule, resourceType, attribute string, from bool) securityAWSCandidate {
	candidate := checkovFlip(id, rule, resourceType, attribute, from)
	candidate.SourceLicence = "MIT"
	return candidate
}

func replacement(id, rule, resourceType, attribute, from, to string) securityAWSCandidate {
	licence := "Apache-2.0"
	if strings.HasPrefix(rule, "AWS-") {
		licence = "MIT"
	}
	return securityAWSCandidate{
		ID: id, SourceRule: rule, SourceLicence: licence,
		ResourceType: resourceType, Attribute: attribute,
		Form: mutation.FormReplace, From: from, To: to,
	}
}

// securityAWSCandidates is the loadable flip/replace slice of the offline
// census. The document also records the CIDR rows that need PACK-WIDEN-CIDR;
// #176 measures those in a dedicated pack and measurement, so they stay out
// of this user pack.
var securityAWSCandidates = []securityAWSCandidate{ //nolint:gochecknoglobals // immutable measurement pin.
	checkovFlip("checkov-aws-131-lb", "CKV_AWS_131", "aws_lb", "drop_invalid_header_fields", true),
	checkovFlip("checkov-aws-131-alb", "CKV_AWS_131", "aws_alb", "drop_invalid_header_fields", true),
	checkovFlip("checkov-aws-235", "CKV_AWS_235", "aws_ami_copy", "encrypted", true),
	checkovFlip("checkov-aws-120", "CKV_AWS_120", "aws_api_gateway_stage", "cache_cluster_enabled", true),
	checkovFlip("checkov-aws-73", "CKV_AWS_73", "aws_api_gateway_stage", "xray_tracing_enabled", true),
	checkovFlip("checkov-aws-214", "CKV_AWS_214", "aws_appsync_api_cache", "at_rest_encryption_enabled", true),
	checkovFlip("checkov-aws-215", "CKV_AWS_215", "aws_appsync_api_cache", "transit_encryption_enabled", true),
	checkovFlip("checkov-aws-96", "CKV_AWS_96", "aws_rds_cluster", "storage_encrypted", true),
	checkovFlip("checkov-aws-216", "CKV_AWS_216", "aws_cloudfront_distribution", "enabled", true),
	checkovFlip("checkov-aws-251", "CKV_AWS_251", "aws_cloudtrail", "enable_logging", true),
	checkovFlip("checkov-aws-36", "CKV_AWS_36", "aws_cloudtrail", "enable_log_file_validation", true),
	checkovFlip("checkov-aws-67", "CKV_AWS_67", "aws_cloudtrail", "is_multi_region_trail", true),
	checkovFlip("checkov-aws-366", "CKV_AWS_366", "aws_cognito_identity_pool", "allow_unauthenticated_identities", false),
	checkovFlip("checkov-aws-226-db", "CKV_AWS_226", "aws_db_instance", "auto_minor_version_upgrade", true),
	checkovFlip("checkov-aws-226-cluster", "CKV_AWS_226", "aws_rds_cluster_instance", "auto_minor_version_upgrade", true),
	checkovFlip("checkov-aws-222", "CKV_AWS_222", "aws_dms_replication_instance", "auto_minor_version_upgrade", true),
	checkovFlip("checkov-aws-74", "CKV_AWS_74", "aws_docdb_cluster", "storage_encrypted", true),
	checkovFlip("checkov-aws-292", "CKV_AWS_292", "aws_docdb_global_cluster", "storage_encrypted", true),
	checkovFlip("checkov-aws-106", "CKV_AWS_106", "aws_ebs_encryption_by_default", "enabled", true),
	checkovFlip("checkov-aws-3", "CKV_AWS_3", "aws_ebs_volume", "encrypted", true),
	checkovFlip("checkov-aws-126", "CKV_AWS_126", "aws_instance", "monitoring", true),
	checkovFlip("checkov-aws-135", "CKV_AWS_135", "aws_instance", "ebs_optimized", true),
	checkovFlip("checkov-aws-42", "CKV_AWS_42", "aws_efs_file_system", "encrypted", true),
	checkovFlip("checkov-aws-138", "CKV_AWS_138", "aws_elb", "cross_zone_load_balancing", true),
	checkovFlip(
		"checkov-aws-390", "CKV_AWS_390",
		"aws_emr_block_public_access_configuration", "block_public_security_group_rules", true,
	),
	checkovFlip("checkov-aws-322", "CKV_AWS_322", "aws_elasticache_cluster", "auto_minor_version_upgrade", true),
	checkovFlip("checkov-aws-29", "CKV_AWS_29", "aws_elasticache_replication_group", "at_rest_encryption_enabled", true),
	checkovFlip("checkov-aws-30", "CKV_AWS_30", "aws_elasticache_replication_group", "transit_encryption_enabled", true),
	checkovFlip("checkov-aws-238", "CKV_AWS_238", "aws_guardduty_detector", "enable", true),
	checkovFlip("checkov-aws-227", "CKV_AWS_227", "aws_kms_key", "is_enabled", true),
	checkovFlip("checkov-aws-7", "CKV_AWS_7", "aws_kms_key", "enable_key_rotation", true),
	checkovFlip("checkov-aws-152-lb", "CKV_AWS_152", "aws_lb", "enable_cross_zone_load_balancing", true),
	checkovFlip("checkov-aws-152-alb", "CKV_AWS_152", "aws_alb", "enable_cross_zone_load_balancing", true),
	checkovFlip("checkov-aws-150-lb", "CKV_AWS_150", "aws_lb", "enable_deletion_protection", true),
	checkovFlip("checkov-aws-150-alb", "CKV_AWS_150", "aws_alb", "enable_deletion_protection", true),
	checkovFlip("checkov-aws-207", "CKV_AWS_207", "aws_mq_broker", "auto_minor_version_upgrade", true),
	checkovFlip("checkov-aws-279", "CKV_AWS_279", "aws_neptune_cluster_snapshot", "storage_encrypted", true),
	checkovFlip("checkov-aws-102", "CKV_AWS_102", "aws_neptune_cluster_instance", "publicly_accessible", false),
	checkovFlip("checkov-aws-44", "CKV_AWS_44", "aws_neptune_cluster", "storage_encrypted", true),
	checkovFlip("checkov-aws-362", "CKV_AWS_362", "aws_neptune_cluster", "copy_tags_to_snapshot", true),
	checkovFlip("checkov-aws-359", "CKV_AWS_359", "aws_neptune_cluster", "iam_database_authentication_enabled", true),
	checkovFlip("checkov-aws-344", "CKV_AWS_344", "aws_networkfirewall_firewall", "delete_protection", true),
	checkovFlip("checkov-aws-11", "CKV_AWS_11", "aws_iam_account_password_policy", "require_lowercase_characters", true),
	checkovFlip("checkov-aws-12", "CKV_AWS_12", "aws_iam_account_password_policy", "require_numbers", true),
	checkovFlip("checkov-aws-14", "CKV_AWS_14", "aws_iam_account_password_policy", "require_symbols", true),
	checkovFlip("checkov-aws-15", "CKV_AWS_15", "aws_iam_account_password_policy", "require_uppercase_characters", true),
	checkovFlip("checkov-aws-172", "CKV_AWS_172", "aws_qldb_ledger", "deletion_protection", true),
	checkovFlip("checkov-aws-313", "CKV_AWS_313", "aws_rds_cluster", "copy_tags_to_snapshot", true),
	checkovFlip("checkov-aws-162", "CKV_AWS_162", "aws_rds_cluster", "iam_database_authentication_enabled", true),
	checkovFlip("checkov-aws-146", "CKV_AWS_146", "aws_db_cluster_snapshot", "storage_encrypted", true),
	checkovFlip("checkov-aws-139", "CKV_AWS_139", "aws_rds_cluster", "deletion_protection", true),
	checkovFlip("checkov-aws-16", "CKV_AWS_16", "aws_db_instance", "storage_encrypted", true),
	checkovFlip("checkov-aws-161", "CKV_AWS_161", "aws_db_instance", "iam_database_authentication_enabled", true),
	checkovFlip("checkov-aws-293", "CKV_AWS_293", "aws_db_instance", "deletion_protection", true),
	checkovFlip(
		"checkov-aws-353-cluster", "CKV_AWS_353",
		"aws_rds_cluster_instance", "performance_insights_enabled", true,
	),
	checkovFlip("checkov-aws-353-db", "CKV_AWS_353", "aws_db_instance", "performance_insights_enabled", true),
	checkovFlip("checkov-aws-157", "CKV_AWS_157", "aws_db_instance", "multi_az", true),
	checkovFlip("checkov-aws-141", "CKV_AWS_141", "aws_redshift_cluster", "allow_version_upgrade", true),
	checkovFlip("checkov-aws-64", "CKV_AWS_64", "aws_redshift_cluster", "encrypted", true),
	checkovFlip("checkov-aws-321", "CKV_AWS_321", "aws_redshift_cluster", "enhanced_vpc_routing", true),
	checkovFlip("checkov-aws-87", "CKV_AWS_87", "aws_redshift_cluster", "publicly_accessible", false),
	checkovFlip("checkov-aws-53", "CKV_AWS_53", "aws_s3_bucket_public_access_block", "block_public_acls", true),
	checkovFlip("checkov-aws-54", "CKV_AWS_54", "aws_s3_bucket_public_access_block", "block_public_policy", true),
	checkovFlip("checkov-aws-55", "CKV_AWS_55", "aws_s3_bucket_public_access_block", "ignore_public_acls", true),
	checkovFlip("checkov-aws-56", "CKV_AWS_56", "aws_s3_bucket_public_access_block", "restrict_public_buckets", true),
	checkovFlip("checkov-aws-370", "CKV_AWS_370", "aws_sagemaker_model", "enable_network_isolation", true),
	checkovFlip("checkov-aws-123", "CKV_AWS_123", "aws_vpc_endpoint_service", "acceptance_required", true),
	checkovFlip("checkov-aws-156", "CKV_AWS_156", "aws_workspaces_workspace", "root_volume_encryption_enabled", true),
	checkovFlip("checkov-aws-155", "CKV_AWS_155", "aws_workspaces_workspace", "user_volume_encryption_enabled", true),
	checkovFlip("checkov-aws-389", "CKV_AWS_389", "aws_launch_configuration", "associate_public_ip_address", false),
	checkovFlip("checkov-aws-319", "CKV_AWS_319", "aws_cloudwatch_metric_alarm", "actions_enabled", true),
	checkovFlip("checkov-aws-89", "CKV_AWS_89", "aws_dms_replication_instance", "publicly_accessible", false),
	checkovFlip("checkov-aws-88-instance", "CKV_AWS_88", "aws_instance", "associate_public_ip_address", false),
	checkovFlip("checkov-aws-69", "CKV_AWS_69", "aws_mq_broker", "publicly_accessible", false),
	checkovFlip("checkov-aws-202", "CKV_AWS_202", "aws_memorydb_cluster", "tls_enabled", true),
	checkovFlip("checkov-aws-17-db", "CKV_AWS_17", "aws_db_instance", "publicly_accessible", false),
	checkovFlip("checkov-aws-17-cluster", "CKV_AWS_17", "aws_rds_cluster_instance", "publicly_accessible", false),
	checkovFlip("checkov-aws-377", "CKV_AWS_377", "aws_route53domains_registered_domain", "transfer_lock", true),
	checkovFlip("checkov-aws-130", "CKV_AWS_130", "aws_subnet", "map_public_ip_on_launch", false),
	replacement(
		"checkov-aws-239", "CKV_AWS_239",
		"aws_dax_cluster", "cluster_endpoint_encryption_type", `"TLS"`, `"NONE"`,
	),
	replacement("checkov-aws-51", "CKV_AWS_51", "aws_ecr_repository", "image_tag_mutability", `"IMMUTABLE"`, `"MUTABLE"`),
	replacement("checkov-aws-43", "CKV_AWS_43", "aws_kinesis_stream", "encryption_type", `"KMS"`, `"NONE"`),
	replacement("checkov-aws-170", "CKV_AWS_170", "aws_qldb_ledger", "permissions_mode", `"STANDARD"`, `"ALLOW_ALL"`),
	replacement(
		"checkov-aws-122", "CKV_AWS_122",
		"aws_sagemaker_notebook_instance", "direct_internet_access", `"Disabled"`, `"Enabled"`,
	),
	replacement(
		"checkov-aws-307", "CKV_AWS_307",
		"aws_sagemaker_notebook_instance", "root_access", `"Disabled"`, `"Enabled"`,
	),
	replacement(
		"checkov-aws-331", "CKV_AWS_331",
		"aws_ec2_transit_gateway", "auto_accept_shared_attachments", `"disable"`, `"enable"`,
	),
	replacement("checkov-aws-258", "CKV_AWS_258", "aws_lambda_function_url", "authorization_type", `"AWS_IAM"`, `"NONE"`),
	trivyFlip("trivy-aws-0003", "AWS-0003", "aws_api_gateway_stage", "xray_tracing_enabled", true),
	replacement("trivy-aws-0004", "AWS-0004", "aws_api_gateway_method", "authorization", `"AWS_IAM"`, `"NONE"`),
	trivyFlip("trivy-aws-0009", "AWS-0009", "aws_launch_configuration", "associate_public_ip_address", false),
	trivyFlip("trivy-aws-0021", "AWS-0021", "aws_docdb_cluster", "storage_encrypted", true),
	trivyFlip("trivy-aws-0026", "AWS-0026", "aws_ebs_volume", "encrypted", true),
	replacement("trivy-aws-0031", "AWS-0031", "aws_ecr_repository", "image_tag_mutability", `"IMMUTABLE"`, `"MUTABLE"`),
	trivyFlip("trivy-aws-0037", "AWS-0037", "aws_efs_file_system", "encrypted", true),
	trivyFlip("trivy-aws-0045", "AWS-0045", "aws_elasticache_replication_group", "at_rest_encryption_enabled", true),
	trivyFlip("trivy-aws-0051", "AWS-0051", "aws_elasticache_replication_group", "transit_encryption_enabled", true),
	trivyFlip("trivy-aws-0052", "AWS-0052", "aws_alb", "drop_invalid_header_fields", true),
	trivyFlip("trivy-aws-0053", "AWS-0053", "aws_alb", "internal", true),
	replacement("trivy-aws-0054", "AWS-0054", "aws_alb_listener", "protocol", `"HTTPS"`, `"HTTP"`),
	replacement("trivy-aws-0064", "AWS-0064", "aws_kinesis_stream", "encryption_type", `"KMS"`, `"NONE"`),
	trivyFlip("trivy-aws-0065", "AWS-0065", "aws_kms_key", "enable_key_rotation", true),
	trivyFlip("trivy-aws-0072", "AWS-0072", "aws_mq_broker", "publicly_accessible", false),
	trivyFlip("trivy-aws-0076", "AWS-0076", "aws_neptune_cluster", "storage_encrypted", true),
	trivyFlip("trivy-aws-0086", "AWS-0086", "aws_s3_bucket_public_access_block", "block_public_acls", true),
	trivyFlip("trivy-aws-0087", "AWS-0087", "aws_s3_bucket_public_access_block", "block_public_policy", true),
	trivyFlip("trivy-aws-0091", "AWS-0091", "aws_s3_bucket_public_access_block", "ignore_public_acls", true),
	replacement("trivy-aws-0092-bucket", "AWS-0092", "aws_s3_bucket", "acl", `"private"`, `"public-read"`),
	replacement("trivy-aws-0092-acl", "AWS-0092", "aws_s3_bucket_acl", "acl", `"private"`, `"authenticated-read"`),
	trivyFlip("trivy-aws-0093", "AWS-0093", "aws_s3_bucket_public_access_block", "restrict_public_buckets", true),
	trivyFlip("trivy-aws-0109-root", "AWS-0109", "aws_workspaces_workspace", "root_volume_encryption_enabled", true),
	trivyFlip("trivy-aws-0109-user", "AWS-0109", "aws_workspaces_workspace", "user_volume_encryption_enabled", true),
	trivyFlip("trivy-aws-0133", "AWS-0133", "aws_rds_cluster_instance", "performance_insights_enabled", true),
	replacement("trivy-aws-0161", "AWS-0161", "aws_s3_bucket", "acl", `"private"`, `"public-read"`),
	trivyFlip("trivy-aws-0164", "AWS-0164", "aws_subnet", "map_public_ip_on_launch", false),
	trivyFlip("trivy-aws-0180", "AWS-0180", "aws_db_instance", "publicly_accessible", false),
}

// The admitted list of record, `admittedSecurityAWSEntries`, lives beside the
// shipped-pack pin in `pack_shipped_test.go` so one list governs the
// measurement, the golden test and the shipped file.

type packAdmissionEntry struct {
	ID              string   `json:"entry"`
	SourceRule      string   `json:"source_rule"`
	SourceLicence   string   `json:"source_licence"`
	ResourceType    string   `json:"resource_type"`
	Attribute       string   `json:"attribute"`
	Form            string   `json:"form"`
	From            string   `json:"from"`
	To              string   `json:"to"`
	Sites           int      `json:"sites_found"`
	PreDedup        int      `json:"mutants_pre_deduplication"`
	PostDedup       int      `json:"rows_carrying_origin_post_deduplication"`
	OwnedPostDedup  int      `json:"rows_owned_post_deduplication"`
	RunMutants      int      `json:"attributed_run_mutants"`
	Invalid         int      `json:"invalid"`
	InvalidRate     float64  `json:"invalid_rate"`
	KilledByError   int      `json:"killed_by_error"`
	ErrorRate       float64  `json:"error_rate"`
	Decision        string   `json:"decision"`
	WitnessedTarget []string `json:"witnessed_targets,omitempty"`
}

type packAdmissionTarget struct {
	Name             string                        `json:"target"`
	Repository       string                        `json:"repository,omitempty"`
	Commit           string                        `json:"commit,omitempty"`
	SourceDigest     string                        `json:"source_digest"`
	PackDigest       string                        `json:"pack_digest"`
	PreviewError     string                        `json:"preview_error,omitempty"`
	RunError         string                        `json:"run_error,omitempty"`
	Retries          int                           `json:"operational_retries"`
	TerraformVersion string                        `json:"terraform_version,omitempty"`
	Entries          map[string]packAdmissionCount `json:"entries"`
}

type packAdmissionCount struct {
	Sites          []string `json:"sites,omitempty"`
	PreDedup       int      `json:"mutants_pre_deduplication"`
	PostDedup      int      `json:"rows_carrying_origin_post_deduplication"`
	OwnedPostDedup int      `json:"rows_owned_post_deduplication"`
	RunMutants     int      `json:"attributed_run_mutants"`
	Invalid        int      `json:"invalid"`
	KilledByError  int      `json:"killed_by_error"`
}

type packAdmissionMeasurement struct {
	TerraformVersion string                `json:"terraform_version"`
	CandidatePack    string                `json:"candidate_pack"`
	PackDigest       string                `json:"pack_digest"`
	Targets          []packAdmissionTarget `json:"targets"`
	Entries          []packAdmissionEntry  `json:"entries"`
}

type packAdmissionSource struct {
	Name         string
	Repository   string
	Commit       string
	ModuleSubdir string
	TestRoot     string
	Archive      string
	SourceDigest string
}

//nolint:paralleltest // the six long-running targets share one provider cache and a crash-safe side-car.
func TestTheSecurityAWSPackAdmissionMeasurement(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	pack := renderSecurityAWSCensusPack()
	packDigest := digestBytes([]byte(pack))
	completed := loadPackAdmissionRows(t)
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

		row := runPackAdmissionTarget(t, source, pack, packDigest, source.SourceDigest)
		rows[source.Name] = row
		recordPackAdmissionRow(t, row)
		t.Logf("%s: preview=%q run=%q retries=%d", row.Name, row.PreviewError, row.RunError, row.Retries)
	}

	measurement := assemblePackAdmission(t, packDigest, rows)
	publishPackAdmission(t, measurement)

	var enabled []string
	for _, entry := range measurement.Entries {
		if entry.Decision == "enabled" {
			enabled = append(enabled, entry.ID)
		}
	}

	slices.Sort(enabled)
	want := slices.Sorted(slices.Values(admittedSecurityAWSEntries))
	if !slices.Equal(enabled, want) {
		t.Errorf("enabled entries = %v, want the published admittedSecurityAWSEntries %v", enabled, want)
	}

	t.Logf("security-aws admission: %d enabled of %d loadable candidates", len(enabled), len(measurement.Entries))
}

// TestEveryEnabledSecurityAWSEntryHasARealProviderWitness re-executes the
// admitted set against hashicorp/aws. Every enabled entry must contribute
// bytes from a checked-in aws-mocked site and none may be Invalid.
//
//nolint:paralleltest // shares the integration provider cache.
func TestEveryEnabledSecurityAWSEntryHasARealProviderWitness(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, awsMockedFixture)
	stageSecurityAWSCensusPack(t, module, renderSecurityAWSCensusPack())

	request := networkConfig(t, module)
	request.NoCache = true
	request.Packs = []string{securityAWSCensusPack}
	request.IncludeOperators = []string{string(mutation.PackFlip), string(mutation.PackReplace)}

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("running the admitted security-aws witnesses: %v", err)
	}

	seen := map[string]bool{}
	invalid := map[string]bool{}
	for _, mutant := range result.Mutants {
		for _, origin := range mutant.Origins {
			if origin.Pack == securityAWSCensusPack {
				seen[origin.Entry] = true
				invalid[origin.Entry] = invalid[origin.Entry] || mutant.State == report.Invalid
			}
		}
	}

	for _, entry := range admittedSecurityAWSEntries {
		if !seen[entry] {
			t.Errorf("enabled entry %q has no aws-mocked witness", entry)
			continue
		}

		if invalid[entry] {
			t.Errorf("enabled entry %q re-executed as Invalid", entry)
		}
	}
}

func scoredPackAdmissionModules() []string {
	return []string{
		"aws-platform-starter",
		"platform-design",
		"serverless-architecture-patterns",
		"terraform-datadog-users",
		"terraform-mongodbatlas-project",
	}
}

func runPackAdmissionTarget(
	t *testing.T,
	source packAdmissionSource,
	pack, packDigest, sourceDigest string,
) packAdmissionTarget {
	t.Helper()

	module := materialisePackAdmissionSource(t, source)
	stageSecurityAWSCensusPack(t, module, pack)

	row := packAdmissionTarget{
		Name: source.Name, Repository: source.Repository, Commit: source.Commit,
		SourceDigest: sourceDigest, PackDigest: packDigest, Entries: map[string]packAdmissionCount{},
	}

	previewResult, previewErr := runPackAdmissionPreview(t, module, source.TestRoot)
	if admissionOperational(previewErr, previewResult) {
		row.Retries++
		previewResult, previewErr = runPackAdmissionPreview(t, module, source.TestRoot)
	}
	if previewErr != nil {
		row.PreviewError = censusReason(previewErr)
	} else {
		row.TerraformVersion = previewResult.TerraformVersion
		observePackPreview(previewResult, &row, securityAWSCensusPack,
			string(mutation.PackFlip), string(mutation.PackReplace))
	}

	runResult, runErr := runPackAdmissionRun(t, module, source.TestRoot)
	if admissionOperational(runErr, runResult) {
		row.Retries++
		runResult, runErr = runPackAdmissionRun(t, module, source.TestRoot)
	}
	if runErr != nil {
		row.RunError = censusReason(runErr)
	} else {
		row.TerraformVersion = runResult.TerraformVersion
		observePackRun(runResult, &row, securityAWSCensusPack)
	}

	return row
}

func materialisePackAdmissionSource(t *testing.T, source packAdmissionSource) string {
	t.Helper()

	if source.Name == awsMockedFixture {
		return filepath.Join(source.Archive, source.ModuleSubdir)
	}

	target := filepath.Join(t.TempDir(), source.Name)
	if err := os.CopyFS(target, os.DirFS(source.Archive)); err != nil {
		t.Fatalf("copying %s for admission: %v", source.Name, err)
	}

	return filepath.Join(target, filepath.FromSlash(source.ModuleSubdir))
}

func runPackAdmissionPreview(t *testing.T, module, testRoot string) (report.Report, error) {
	t.Helper()

	base := networkConfig(t, module)
	request := previewRequest(t, module)
	request.Common = base.Common
	request.TestDirectory = testRoot
	request.WorkDir = t.TempDir()
	request.Packs = []string{securityAWSCensusPack}
	request.Tier = mutation.TierStandard

	return engine.Run(t.Context(), request)
}

func runPackAdmissionRun(t *testing.T, module, testRoot string) (report.Report, error) {
	t.Helper()

	request := networkConfig(t, module)
	request.TestDirectory = testRoot
	request.WorkDir = t.TempDir()
	request.NoCache = true
	request.Packs = []string{securityAWSCensusPack}
	request.Tier = mutation.TierStandard

	return engine.Run(t.Context(), request)
}

func admissionOperational(err error, result report.Report) bool {
	return err != nil && classifyRow(err, result) == rowOperational
}

func observePackPreview(result report.Report, row *packAdmissionTarget, pack string, owners ...string) {
	for _, mutant := range result.Mutants {
		origins := packOrigins(mutant, pack)
		if len(origins) == 0 {
			continue
		}

		for _, origin := range origins {
			count := row.Entries[origin.Entry]
			count.PreDedup++
			count.PostDedup++
			count.Sites = append(count.Sites, packAdmissionSite(row.Name, mutant))
			row.Entries[origin.Entry] = count
		}

		if slices.Contains(owners, mutant.Operator) {
			for _, origin := range origins {
				if origin.Operator == mutant.Operator {
					count := row.Entries[origin.Entry]
					count.OwnedPostDedup++
					row.Entries[origin.Entry] = count
					break
				}
			}
		}
	}

	for entry, count := range row.Entries {
		slices.Sort(count.Sites)
		count.Sites = slices.Compact(count.Sites)
		row.Entries[entry] = count
	}
}

func observePackRun(result report.Report, row *packAdmissionTarget, pack string) {
	for _, mutant := range result.Mutants {
		for _, origin := range packOrigins(mutant, pack) {
			count := row.Entries[origin.Entry]
			count.RunMutants++
			if mutant.State == report.Invalid {
				count.Invalid++
			}
			if mutant.State == report.KilledByError {
				count.KilledByError++
			}
			row.Entries[origin.Entry] = count
		}
	}
}

func packOrigins(mutant report.Mutant, pack string) []report.Origin {
	origins := []report.Origin{}
	for _, origin := range mutant.Origins {
		if origin.Pack == pack {
			origins = append(origins, origin)
		}
	}
	return origins
}

func packAdmissionSite(target string, mutant report.Mutant) string {
	return target + ":" + mutant.Module + ":" + mutant.Site + ":" + mutant.Range.File +
		fmt.Sprintf(":%d", mutant.Range.Start.Line)
}

func renderSecurityAWSCensusPack() string {
	return renderCandidatePack(securityAWSCandidates)
}

// renderCandidatePack renders any candidate slice in the user-pack format.
func renderCandidatePack(candidates []securityAWSCandidate) string {
	builder := strings.Builder{}
	for _, candidate := range candidates {
		fmt.Fprintf(&builder, "entry %q {\n", candidate.ID)
		fmt.Fprintf(&builder, "  resource_type = %q\n", candidate.ResourceType)
		fmt.Fprintf(&builder, "  attribute = %q\n", candidate.Attribute)
		fmt.Fprintf(&builder, "  form = %q\n", candidate.Form)
		fmt.Fprintf(&builder, "  from = %s\n", candidate.From)
		fmt.Fprintf(&builder, "  to = %s\n", candidate.To)
		fmt.Fprintf(&builder, "  source_rule = %q\n", candidate.SourceRule)
		fmt.Fprintf(&builder, "  source_licence = %q\n", candidate.SourceLicence)
		fmt.Fprintln(&builder, "}")
	}
	return builder.String()
}

func stageSecurityAWSCensusPack(t *testing.T, module, content string) {
	t.Helper()
	stageUserPack(t, module, securityAWSCensusPack, "security-aws-census.hcl", content)
}

// stageUserPack registers a candidate pack as a user pack on a module copy:
// the pack file under tf-mut-packs/ and the registration appended to
// .tf-mut.hcl.
func stageUserPack(t *testing.T, module, name, file, content string) {
	t.Helper()

	directory := filepath.Join(module, "tf-mut-packs")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatalf("creating the census pack directory: %v", err)
	}

	packPath := filepath.Join(directory, file)
	if err := os.WriteFile(packPath, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the census pack: %v", err)
	}

	configPath := filepath.Join(module, ".tf-mut.hcl")
	config, err := os.ReadFile(configPath) //nolint:gosec // the module is a disposable measurement copy.
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading the module configuration: %v", err)
	}

	registration := "\npack \"" + name + "\" {\n" +
		"  file = \"tf-mut-packs/" + file + "\"\n}\n"
	//nolint:gosec // configPath is under the disposable module copy made by the measurement.
	if err := os.WriteFile(configPath, append(config, registration...), 0o600); err != nil {
		t.Fatalf("registering the census pack: %v", err)
	}
}

func assemblePackAdmission(
	t *testing.T,
	packDigest string,
	rows map[string]packAdmissionTarget,
) packAdmissionMeasurement {
	t.Helper()

	return assembleCandidatePackAdmission(t, securityAWSCensusPack, packDigest, securityAWSCandidates, rows)
}

// assembleCandidatePackAdmission folds the per-target rows into the published
// measurement and applies the admission decision rule per entry.
func assembleCandidatePackAdmission(
	t *testing.T,
	packName, packDigest string,
	candidates []securityAWSCandidate,
	rows map[string]packAdmissionTarget,
) packAdmissionMeasurement {
	t.Helper()

	measurement := packAdmissionMeasurement{
		CandidatePack: packName, PackDigest: packDigest,
		Targets: []packAdmissionTarget{}, Entries: []packAdmissionEntry{},
	}

	orderedTargets := append([]string{awsMockedFixture}, scoredPackAdmissionModules()...)
	for _, name := range orderedTargets {
		row, found := rows[name]
		if !found {
			t.Fatalf("the admission target %s has no published row", name)
		}
		measurement.Targets = append(measurement.Targets, row)
		if measurement.TerraformVersion == "" && row.TerraformVersion != "" {
			measurement.TerraformVersion = row.TerraformVersion
		}
	}

	for _, candidate := range candidates {
		entry := packAdmissionEntry{
			ID: candidate.ID, SourceRule: candidate.SourceRule, SourceLicence: candidate.SourceLicence,
			ResourceType: candidate.ResourceType, Attribute: candidate.Attribute,
			Form: candidate.Form, From: candidate.From, To: candidate.To,
			WitnessedTarget: []string{},
		}

		for _, row := range measurement.Targets {
			count := row.Entries[candidate.ID]
			entry.Sites += len(count.Sites)
			entry.PreDedup += count.PreDedup
			entry.PostDedup += count.PostDedup
			entry.OwnedPostDedup += count.OwnedPostDedup
			entry.RunMutants += count.RunMutants
			entry.Invalid += count.Invalid
			entry.KilledByError += count.KilledByError
			if count.PreDedup > 0 {
				entry.WitnessedTarget = append(entry.WitnessedTarget, row.Name)
			}
		}

		if entry.RunMutants > 0 {
			entry.InvalidRate = float64(entry.Invalid) / float64(entry.RunMutants)
			entry.ErrorRate = float64(entry.KilledByError) / float64(entry.RunMutants)
		}

		switch {
		case entry.Invalid > 0:
			entry.Decision = "dropped-for-invalid"
		case entry.PreDedup > 0:
			entry.Decision = "enabled"
		default:
			entry.Decision = "unwitnessed-not-enabled"
		}

		measurement.Entries = append(measurement.Entries, entry)
	}

	return measurement
}

func loadPackAdmissionRows(t *testing.T) map[string]packAdmissionTarget {
	t.Helper()
	return loadAdmissionRows(t, packAdmissionRows)
}

// loadAdmissionRows reads one crash-safe side-car, tolerating its absence.
func loadAdmissionRows(t *testing.T, path string) map[string]packAdmissionTarget {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]packAdmissionTarget{}
	}

	rows := map[string]packAdmissionTarget{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(content)), "\n") {
		if line == "" {
			continue
		}
		var row packAdmissionTarget
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decoding a pack-admission row: %v", err)
		}
		rows[row.Name] = row
	}
	return rows
}

func recordPackAdmissionRow(t *testing.T, row packAdmissionTarget) {
	t.Helper()
	recordAdmissionRow(t, packAdmissionRows, row)
}

// recordAdmissionRow appends one row to a crash-safe side-car.
func recordAdmissionRow(t *testing.T, path string, row packAdmissionTarget) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating the admission measurement directory: %v", err)
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("encoding the admission row: %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening the admission side-car: %v", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		t.Fatalf("appending the admission row: %v", err)
	}
}

func publishPackAdmission(t *testing.T, measurement packAdmissionMeasurement) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(packAdmissionOutput), 0o750); err != nil {
		t.Fatalf("creating the admission measurement directory: %v", err)
	}
	encoded, err := json.MarshalIndent(measurement, "", "  ")
	if err != nil {
		t.Fatalf("encoding the admission measurement: %v", err)
	}
	if err := os.WriteFile(packAdmissionOutput, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing the admission measurement: %v", err)
	}
}

func digestBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func digestTree(t *testing.T, root string) string {
	t.Helper()

	digests := treeDigest(t, root)
	paths := make([]string, 0, len(digests))
	for path := range digests {
		paths = append(paths, path)
	}
	slices.Sort(paths)

	builder := strings.Builder{}
	for _, path := range paths {
		builder.WriteString(path)
		builder.WriteByte(0)
		builder.WriteString(digests[path])
		builder.WriteByte('\n')
	}
	return digestBytes([]byte(builder.String()))
}

func TestTheSecurityAWSCandidatePackIsLoadableAndHasStableIdentities(t *testing.T) {
	t.Parallel()

	if len(securityAWSCandidates) != securityAWSCandidateCount {
		t.Errorf("candidate count = %d, want the published %d", len(securityAWSCandidates), securityAWSCandidateCount)
	}
	if len(admittedSecurityAWSEntries) != securityAWSAdmittedCount {
		t.Errorf("admitted count = %d, want the published %d", len(admittedSecurityAWSEntries), securityAWSAdmittedCount)
	}

	seen := map[string]bool{}
	for _, candidate := range securityAWSCandidates {
		if seen[candidate.ID] {
			t.Errorf("candidate identity %q is repeated", candidate.ID)
		}
		seen[candidate.ID] = true
	}

	for _, enabled := range admittedSecurityAWSEntries {
		if !seen[enabled] {
			t.Errorf("admitted entry %q is not a candidate", enabled)
		}
	}

	pack := renderSecurityAWSCensusPack()
	t.Logf("candidate pack digest: %s", digestBytes([]byte(pack)))

	module := copyFixture(t, packsFixture)
	stageSecurityAWSCensusPack(t, module, pack)
	request := previewRequest(t, module)
	request.Packs = []string{securityAWSCensusPack}

	if _, err := engine.Run(t.Context(), request); err != nil {
		t.Fatalf("the census candidate pack does not load through the engine seam: %v", err)
	}
}
