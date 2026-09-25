# M5-0.3 `security-aws` seed census and admission

**Date:** 2026-09-17

**Issue:** [#161](https://github.com/andrewesweet/tf-mut/issues/161)
**Result:** 10 of 115 loadable candidates admitted; 105 unwitnessed; none dropped for invalidity

## Decision

M5c.2 may ship these ten entries in `security-aws`:

- Checkov `CKV_AWS_7`, and Trivy `AWS-0065`, for KMS key rotation;
- Checkov `CKV_AWS_53`–`CKV_AWS_56`, and Trivy `AWS-0086`, `AWS-0087`,
  `AWS-0091` and `AWS-0093`, for the four S3 public-access-block flags.

The run found 86 entry-attributed candidate rewrites across 43 distinct post-deduplication
mutants. Each admitted entry had a 0% invalid rate and a 0% execution-error rate. The other 105
loadable entries produced no bytes on this corpus and remain published as
`unwitnessed-not-enabled`. No entry was dropped for invalidity.

All 43 surviving rows were owned by `BOOL-LITERAL-FLIP`, not `PACK-FLIP`. This is expected: the
language operator sorts before the pack operator, while origin aggregation retains every
contributing pack entry. The table therefore publishes both post-deduplication rows carrying the
entry origin (`Post/origin`) and rows the pack entry owns (`Owned`). The former is the useful
provenance count; the latter is zero for every witnessed entry in this run.

The census also found one scalar IPv4 candidate that requires the deferred
`PACK-WIDEN-CIDR` form: Trivy `AWS-0104` on
`aws_vpc_security_group_egress_rule.cidr_ipv4`. It was not loaded or measured here because #161
must not implement a new form. [#176](https://github.com/andrewesweet/tf-mut/issues/176) is the
explicit implementation and admission follow-up. M5c.2 (#162) must not silently encode that row
as `replace`.

## Source census

The offline census read these immutable source revisions:

| Catalogue | Revision | Licence | Material read |
| --- | --- | --- | --- |
| [Checkov](https://github.com/bridgecrewio/checkov) | `8c0ddf99eabe4db75c739320274c7764866cd03d` | Apache-2.0 | Terraform AWS resource and data checks, their base value-check contracts, and rule tests/examples |
| [Trivy checks](https://github.com/aquasecurity/trivy-checks) | `3ae9f4cc196767047a2a1fdae2d24a82944dfd6b` | MIT | AWS Rego metadata and Terraform good/bad examples |

A loadable candidate needed all of the following:

1. an `aws_*` resource type;
2. one top-level resource argument;
3. a single literal secure value and a single literal insecure value stated by the rule or its
   Terraform examples;
4. an inversion expressible by the existing `flip` or `replace` form; and
5. no dependence on another argument to make the mutation a security fault.

For Checkov, the default `BaseResourceValueCheck` expectation supplies `true`, and
`BaseResourceNegativeValueCheck` supplies the complement of its one forbidden boolean. Explicit
enum inversions were included only where the check or its examples identified both ends. For
Trivy, incidental differences between a good and bad example—names, HTTP methods and engine
settings—were not treated as faults. Presence checks (`ANY_VALUE`), missing-attribute rules,
compound predicates and rules with no single literal inverse are not pack entries.

### Syntax and form exclusions

These rows are intentionally outside the loadable candidate table. A grouped rule cell means
each listed rule was inspected and excluded for the same syntax boundary.

| Class | Rule(s), licence | Resource / path or candidate | `from` → `to` | Why excluded |
| --- | --- | --- | --- | --- |
| String-internal | Checkov `CKV_AWS_108`–`CKV_AWS_111`, Apache-2.0 | IAM policy JSON/heredoc or `jsonencode`, including `Statement.Action` | for example `s3:GetObject` → `s3:*` | The fault is inside a string or structured expression, not the value of one resource argument. |
| String-internal | Trivy `AWS-0032`, `AWS-0057`, MIT | ECR/IAM policy `Effect`, `Principal`, `Action` and `Resource` in heredocs, `jsonencode` or policy documents | scoped value → wildcard/public value | Same boundary; replacing the complete policy string would not model the published fault. |
| Nested block | Checkov `CKV_AWS_5`, `47`, `75`, `82`, `83`, `91`, `163`, `197`, `220`, `225`, `242`–`244`, `284`, `285`, `308`, `369`, Apache-2.0 | the named resource's nested boolean (`enabled`, `enforce_https`, logging, encryption, tracing or scanning field) | `true` → `false` | Pack sites are top-level resource arguments only. |
| Nested block | Checkov `CKV_AWS_39`, `276`, `311`, `316`, `333`, Apache-2.0 | EKS public endpoint, API trace data, CodeBuild S3 encryption/privileged mode, ECS public IP | `false` → `true` | Pack sites are top-level resource arguments only. |
| Nested block | Checkov `CKV_AWS_79`, `136`, `218`, `228`, `234`, `341`, `365`, `371`, Apache-2.0 | IMDS token mode, ECR encryption type, TLS policies, certificate transparency, hop limit and IMDS version | `required` → `optional`; `KMS` → `AES256`; TLS 1.2 → older policy; `ENABLED` → `DISABLED`; `1` → `2`; `Require` → `Optional`; `2` → `1` | These are exact scalar inversions, but the scalar is inside a nested block. |
| Nested collection | Checkov `CKV_AWS_28`, `38`, `58`, `129`, `159`, `174`, `223`, `291`, `303`, `309`, `324`, `374`, Apache-2.0 | list elements, set membership, or values accepted from a set | varies | `flip`/`replace` accepts one literal argument, not a collection member; several also lack one canonical secure `from`. |
| Dynamic | The nested rules above, both licences | the same nested body emitted by a Terraform `dynamic` block | same as its underlying nested rule | `dynamic` is a source representation, not a separate upstream rule. Pack matching deliberately never descends into its `content` body. |
| Meta-argument | Checkov `CKV_AWS_217`, `233`, `237`, Apache-2.0 | `lifecycle.create_before_destroy` on API Gateway deployment, ACM certificate and API Gateway REST API | `true` → `false` | `lifecycle` is a Terraform meta-argument and is outside pack matching. |
| Data body | Checkov `CKV_AWS_108`–`CKV_AWS_111`; Trivy `AWS-0032`, `AWS-0057`, `AWS-0123`, `AWS-0344` | `data.aws_iam_policy_document` statements and `data.aws_ami` owner constraints | policy scalar/wildcard inversions, or owner present → absent | Data bodies are excluded; the AMI rule is additionally an omission rather than a closed-form replacement. |
| `PACK-WIDEN-CIDR` | Trivy `AWS-0104`, MIT | `aws_vpc_security_group_egress_rule.cidr_ipv4` | `any-cidr` → `0.0.0.0/0` | Direct scalar candidate, but `widen-cidr` is not a loadable form. Deferred to #176. |
| `PACK-WIDEN-CIDR` | Trivy `AWS-0104`, `AWS-0107`, `AWS-0041`; Checkov `CKV_AWS_24`, `25`, `38`, `229`–`232`, `260`, `277`, `382` | `cidr_blocks`/`public_access_cidrs` lists, nested security-group/NACL rules, or CIDRs whose fault depends on adjacent ports | restrictive CIDR → public CIDR | The current proposed form handles only one top-level scalar IPv4 argument. List/nested sites and adjacent-port predicates remain out of scope even after #176. |
| `PACK-WIDEN-CIDR` | Trivy `AWS-0104`, `AWS-0107`; matching Checkov security-group rules | `cidr_ipv6` or IPv6 collection values | IPv6 CIDR → `::/0` | The proposed sentinel and target are IPv4-only. A second target would be a separate design and census. |

The `AWS-0104` scalar row justifies `PACK-WIDEN-CIDR`, but does not license an implementation in
this issue. #176 records every affected surface: parser/schema and form validation, catalogue,
generation, unchanged origin/dedup semantics, an applicability-matrix row, positive and refusal
fixtures, the pack-author documentation and CLI help (the `--pack` option itself does not
change), and a fresh admission run with an `aws-mocked` witness if the row passes.

## Admission method

The integration-tagged `TestTheSecurityAWSPackAdmissionMeasurement` registered the candidates as
the user pack `security-aws-census`; the reserved shipped name was not used. For each target it
called `engine.Run` once with `PreviewRequest` and once with `RunRequest`, at `standard` breadth
plus the selected pack. It set `NoCache` for runs. An operational failure would be retried once
without aborting later targets; no retry was needed. Neither real-infrastructure nor unsandboxed
effect safety gates were bypassed by an engine request.

The run used Terraform 1.15.8. The `aws-mocked` leg loaded `hashicorp/aws` 6.60.0; public
modules resolved providers under their pinned source constraints. The candidate pack digest was
`0546312a4c0919905243a9cafde736563978ba6f49def319349b0bb2efd442c5`.
`aws-mocked` supplied the realistic AWS provider schema; no null-provider result is being used as
admission evidence. The five public modules are exactly the scorable set admitted by M5-0.4 in
[research 17](17-m5-benchmark-census.md).

Counts have these meanings:

- **Sites**: distinct `(target, module, resource attribute, file, line)` generation sites.
- **Pre**: candidate rewrites before content deduplication, attributed through origins.
- **Post/origin**: surviving post-deduplication rows carrying that entry's origin.
- **Owned**: surviving rows whose owning operator/entry is that pack entry. A language operator
  can own a row while all pack contributors remain in its origins.
- **Invalid**: attributed run mutants classified `Invalid` divided by attributed run mutants.
- **Error**: attributed run mutants classified `KilledByError` divided by attributed run mutants.
  This is execution error, not an operational harness failure.

### Target pins and completion

| Target | Commit / closure | Archive SHA-256 / closure | Pre origins | Post/origin | Owned | Invalid | Error | Retry |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `aws-mocked` | closure digest `87808b0b2b941cb26b2b8b7c646a8229e0811e06ec54518d4ff2a844c811441a` | same | 10 | 10 | 0 | 0 | 0 | 0 |
| `aws-platform-starter` | `596d99d273174a21ea2c92d5dd90f467ab45706e` | `ca59838a603f890ecb835ef6c8f55ceaef72e23f8c461ef9de075eff3ef70c72` | 36 | 36 | 0 | 0 | 0 | 0 |
| `platform-design` | `1243d3748ea8426a667a9469fd10a60849d3421c` | `84b3c9e17420ea9487d8dc82d1b8899a34f0bac014a3dd4a48d4f6c3649da68a` | 0 | 0 | 0 | 0 | 0 | 0 |
| `serverless-architecture-patterns` | `d0890d3e50fc13b9f7aa2bde4bb5d708da1ba485` | `d7386ccdae7b363b346a7b8563a22598a8a15368fe169c9de240e5e5d4b2b985` | 40 | 40 | 0 | 0 | 0 | 0 |
| `terraform-datadog-users` | `f5575b9205e26822b5ca0aae9570713465c1e9a5` | `d66411a2955ece407cf5f32a57c87c7d521e2cb25e879d67ca04fb9b29b71569` | 0 | 0 | 0 | 0 | 0 | 0 |
| `terraform-mongodbatlas-project` | `adabc13eea096058d0f919031223f862625b0cd5` | `abe2bc411cbc63b251296298ba5838ef0f8eaccb898329e0c35453a87eed0bee` | 0 | 0 | 0 | 0 | 0 | 0 |

Every target completed both legs. Three targets correctly produced no candidate bytes. Summing
entry origins counts duplicate upstream rules separately: one KMS row carries both the Checkov
and Trivy origin, as does each S3 public-access row.

### Witness sites

Entries that request identical bytes share the site set shown. This lists every witnessed site;
the 105 unwitnessed entries have an empty set.

- `checkov-aws-7`, `trivy-aws-0065` — `aws_kms_key.enable_key_rotation`:
  - `aws-mocked:.:aws_kms_key.artifacts.enable_key_rotation:storage.tf:6`
  - `aws-platform-starter:.:aws_kms_key.state.enable_key_rotation:main.tf:127`
  - `aws-platform-starter:.:aws_kms_key.state_replica.enable_key_rotation:main.tf:393`
  - `serverless-architecture-patterns:composition/subsystem:aws_kms_key.this.enable_key_rotation:composition/subsystem/main.tf:216`
  - `serverless-architecture-patterns:patterns/bff_service:aws_kms_key.this.enable_key_rotation:patterns/bff_service/main.tf:25`
  - `serverless-architecture-patterns:patterns/control_service:aws_kms_key.this.enable_key_rotation:patterns/control_service/main.tf:27`
  - `serverless-architecture-patterns:patterns/esg_service:aws_kms_key.this.enable_key_rotation:patterns/esg_service/main.tf:14`
  - `serverless-architecture-patterns:patterns/event_hub:aws_kms_key.this.enable_key_rotation:patterns/event_hub/main.tf:28`
  - `serverless-architecture-patterns:patterns/event_lake:aws_kms_key.this.enable_key_rotation:patterns/event_lake/main.tf:11`
  - `serverless-architecture-patterns:patterns/fault_monitor:aws_kms_key.this.enable_key_rotation:patterns/fault_monitor/main.tf:19`
  - `serverless-architecture-patterns:patterns/observability_baseline:aws_kms_key.this.enable_key_rotation:patterns/observability_baseline/main.tf:21`
  - `serverless-architecture-patterns:primitives/api_http:aws_kms_key.this.enable_key_rotation:primitives/api_http/main.tf:17`
  - `serverless-architecture-patterns:primitives/dynamodb_table:aws_kms_key.this.enable_key_rotation:primitives/dynamodb_table/main.tf:9`
  - `serverless-architecture-patterns:primitives/eventbridge_bus:aws_kms_key.this.enable_key_rotation:primitives/eventbridge_bus/main.tf:9`
  - `serverless-architecture-patterns:primitives/lambda_function:aws_kms_key.this.enable_key_rotation:primitives/lambda_function/main.tf:12`
- `checkov-aws-53`, `trivy-aws-0086` — `block_public_acls`:
  - `aws-mocked:.:aws_s3_bucket_public_access_block.artifacts.block_public_acls:storage.tf:25`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.alb_access_logs.block_public_acls:main.tf:579`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state.block_public_acls:main.tf:238`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state_logs.block_public_acls:main.tf:247`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state_replica.block_public_acls:main.tf:428`
  - `serverless-architecture-patterns:patterns/event_lake:aws_s3_bucket_public_access_block.this.block_public_acls:patterns/event_lake/main.tf:36`
  - `serverless-architecture-patterns:patterns/fault_monitor:aws_s3_bucket_public_access_block.this.block_public_acls:patterns/fault_monitor/main.tf:51`
- `checkov-aws-54`, `trivy-aws-0087` — `block_public_policy`:
  - `aws-mocked:.:aws_s3_bucket_public_access_block.artifacts.block_public_policy:storage.tf:26`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.alb_access_logs.block_public_policy:main.tf:580`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state.block_public_policy:main.tf:239`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state_logs.block_public_policy:main.tf:248`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state_replica.block_public_policy:main.tf:429`
  - `serverless-architecture-patterns:patterns/event_lake:aws_s3_bucket_public_access_block.this.block_public_policy:patterns/event_lake/main.tf:37`
  - `serverless-architecture-patterns:patterns/fault_monitor:aws_s3_bucket_public_access_block.this.block_public_policy:patterns/fault_monitor/main.tf:52`
- `checkov-aws-55`, `trivy-aws-0091` — `ignore_public_acls`:
  - `aws-mocked:.:aws_s3_bucket_public_access_block.artifacts.ignore_public_acls:storage.tf:27`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.alb_access_logs.ignore_public_acls:main.tf:581`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state.ignore_public_acls:main.tf:240`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state_logs.ignore_public_acls:main.tf:249`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state_replica.ignore_public_acls:main.tf:430`
  - `serverless-architecture-patterns:patterns/event_lake:aws_s3_bucket_public_access_block.this.ignore_public_acls:patterns/event_lake/main.tf:38`
  - `serverless-architecture-patterns:patterns/fault_monitor:aws_s3_bucket_public_access_block.this.ignore_public_acls:patterns/fault_monitor/main.tf:53`
- `checkov-aws-56`, `trivy-aws-0093` — `restrict_public_buckets`:
  - `aws-mocked:.:aws_s3_bucket_public_access_block.artifacts.restrict_public_buckets:storage.tf:28`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.alb_access_logs.restrict_public_buckets:main.tf:582`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state.restrict_public_buckets:main.tf:241`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state_logs.restrict_public_buckets:main.tf:250`
  - `aws-platform-starter:.:aws_s3_bucket_public_access_block.state_replica.restrict_public_buckets:main.tf:431`
  - `serverless-architecture-patterns:patterns/event_lake:aws_s3_bucket_public_access_block.this.restrict_public_buckets:patterns/event_lake/main.tf:39`
  - `serverless-architecture-patterns:patterns/fault_monitor:aws_s3_bucket_public_access_block.this.restrict_public_buckets:patterns/fault_monitor/main.tf:54`

## Per-entry result

### Enabled (10)

| Entry | Rule | Licence | Resource | Attribute | Form | `from` | `to` | Sites | Pre | Post/origin | Owned | Invalid | Error | Decision |
| --- | --- | --- | --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| checkov-aws-7 | CKV_AWS_7 | Apache-2.0 | aws_kms_key | enable_key_rotation | flip | `true` | `false` | 15 | 15 | 15 | 0 | 0/15 (0%) | 0/15 (0%) | enabled |
| checkov-aws-53 | CKV_AWS_53 | Apache-2.0 | aws_s3_bucket_public_access_block | block_public_acls | flip | `true` | `false` | 7 | 7 | 7 | 0 | 0/7 (0%) | 0/7 (0%) | enabled |
| checkov-aws-54 | CKV_AWS_54 | Apache-2.0 | aws_s3_bucket_public_access_block | block_public_policy | flip | `true` | `false` | 7 | 7 | 7 | 0 | 0/7 (0%) | 0/7 (0%) | enabled |
| checkov-aws-55 | CKV_AWS_55 | Apache-2.0 | aws_s3_bucket_public_access_block | ignore_public_acls | flip | `true` | `false` | 7 | 7 | 7 | 0 | 0/7 (0%) | 0/7 (0%) | enabled |
| checkov-aws-56 | CKV_AWS_56 | Apache-2.0 | aws_s3_bucket_public_access_block | restrict_public_buckets | flip | `true` | `false` | 7 | 7 | 7 | 0 | 0/7 (0%) | 0/7 (0%) | enabled |
| trivy-aws-0065 | AWS-0065 | MIT | aws_kms_key | enable_key_rotation | flip | `true` | `false` | 15 | 15 | 15 | 0 | 0/15 (0%) | 0/15 (0%) | enabled |
| trivy-aws-0086 | AWS-0086 | MIT | aws_s3_bucket_public_access_block | block_public_acls | flip | `true` | `false` | 7 | 7 | 7 | 0 | 0/7 (0%) | 0/7 (0%) | enabled |
| trivy-aws-0087 | AWS-0087 | MIT | aws_s3_bucket_public_access_block | block_public_policy | flip | `true` | `false` | 7 | 7 | 7 | 0 | 0/7 (0%) | 0/7 (0%) | enabled |
| trivy-aws-0091 | AWS-0091 | MIT | aws_s3_bucket_public_access_block | ignore_public_acls | flip | `true` | `false` | 7 | 7 | 7 | 0 | 0/7 (0%) | 0/7 (0%) | enabled |
| trivy-aws-0093 | AWS-0093 | MIT | aws_s3_bucket_public_access_block | restrict_public_buckets | flip | `true` | `false` | 7 | 7 | 7 | 0 | 0/7 (0%) | 0/7 (0%) | enabled |

### Unwitnessed, not enabled (105)

| Entry | Rule | Licence | Resource | Attribute | Form | `from` | `to` | Sites | Pre | Post/origin | Owned | Invalid | Error | Decision |
| --- | --- | --- | --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| checkov-aws-131-lb | CKV_AWS_131 | Apache-2.0 | aws_lb | drop_invalid_header_fields | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-131-alb | CKV_AWS_131 | Apache-2.0 | aws_alb | drop_invalid_header_fields | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-235 | CKV_AWS_235 | Apache-2.0 | aws_ami_copy | encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-120 | CKV_AWS_120 | Apache-2.0 | aws_api_gateway_stage | cache_cluster_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-73 | CKV_AWS_73 | Apache-2.0 | aws_api_gateway_stage | xray_tracing_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-214 | CKV_AWS_214 | Apache-2.0 | aws_appsync_api_cache | at_rest_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-215 | CKV_AWS_215 | Apache-2.0 | aws_appsync_api_cache | transit_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-96 | CKV_AWS_96 | Apache-2.0 | aws_rds_cluster | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-216 | CKV_AWS_216 | Apache-2.0 | aws_cloudfront_distribution | enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-251 | CKV_AWS_251 | Apache-2.0 | aws_cloudtrail | enable_logging | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-36 | CKV_AWS_36 | Apache-2.0 | aws_cloudtrail | enable_log_file_validation | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-67 | CKV_AWS_67 | Apache-2.0 | aws_cloudtrail | is_multi_region_trail | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-366 | CKV_AWS_366 | Apache-2.0 | aws_cognito_identity_pool | allow_unauthenticated_identities | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-226-db | CKV_AWS_226 | Apache-2.0 | aws_db_instance | auto_minor_version_upgrade | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-226-cluster | CKV_AWS_226 | Apache-2.0 | aws_rds_cluster_instance | auto_minor_version_upgrade | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-222 | CKV_AWS_222 | Apache-2.0 | aws_dms_replication_instance | auto_minor_version_upgrade | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-74 | CKV_AWS_74 | Apache-2.0 | aws_docdb_cluster | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-292 | CKV_AWS_292 | Apache-2.0 | aws_docdb_global_cluster | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-106 | CKV_AWS_106 | Apache-2.0 | aws_ebs_encryption_by_default | enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-3 | CKV_AWS_3 | Apache-2.0 | aws_ebs_volume | encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-126 | CKV_AWS_126 | Apache-2.0 | aws_instance | monitoring | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-135 | CKV_AWS_135 | Apache-2.0 | aws_instance | ebs_optimized | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-42 | CKV_AWS_42 | Apache-2.0 | aws_efs_file_system | encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-138 | CKV_AWS_138 | Apache-2.0 | aws_elb | cross_zone_load_balancing | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-390 | CKV_AWS_390 | Apache-2.0 | aws_emr_block_public_access_configuration | block_public_security_group_rules | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-322 | CKV_AWS_322 | Apache-2.0 | aws_elasticache_cluster | auto_minor_version_upgrade | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-29 | CKV_AWS_29 | Apache-2.0 | aws_elasticache_replication_group | at_rest_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-30 | CKV_AWS_30 | Apache-2.0 | aws_elasticache_replication_group | transit_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-238 | CKV_AWS_238 | Apache-2.0 | aws_guardduty_detector | enable | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-227 | CKV_AWS_227 | Apache-2.0 | aws_kms_key | is_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-152-lb | CKV_AWS_152 | Apache-2.0 | aws_lb | enable_cross_zone_load_balancing | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-152-alb | CKV_AWS_152 | Apache-2.0 | aws_alb | enable_cross_zone_load_balancing | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-150-lb | CKV_AWS_150 | Apache-2.0 | aws_lb | enable_deletion_protection | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-150-alb | CKV_AWS_150 | Apache-2.0 | aws_alb | enable_deletion_protection | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-207 | CKV_AWS_207 | Apache-2.0 | aws_mq_broker | auto_minor_version_upgrade | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-279 | CKV_AWS_279 | Apache-2.0 | aws_neptune_cluster_snapshot | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-102 | CKV_AWS_102 | Apache-2.0 | aws_neptune_cluster_instance | publicly_accessible | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-44 | CKV_AWS_44 | Apache-2.0 | aws_neptune_cluster | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-362 | CKV_AWS_362 | Apache-2.0 | aws_neptune_cluster | copy_tags_to_snapshot | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-359 | CKV_AWS_359 | Apache-2.0 | aws_neptune_cluster | iam_database_authentication_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-344 | CKV_AWS_344 | Apache-2.0 | aws_networkfirewall_firewall | delete_protection | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-11 | CKV_AWS_11 | Apache-2.0 | aws_iam_account_password_policy | require_lowercase_characters | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-12 | CKV_AWS_12 | Apache-2.0 | aws_iam_account_password_policy | require_numbers | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-14 | CKV_AWS_14 | Apache-2.0 | aws_iam_account_password_policy | require_symbols | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-15 | CKV_AWS_15 | Apache-2.0 | aws_iam_account_password_policy | require_uppercase_characters | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-172 | CKV_AWS_172 | Apache-2.0 | aws_qldb_ledger | deletion_protection | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-313 | CKV_AWS_313 | Apache-2.0 | aws_rds_cluster | copy_tags_to_snapshot | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-162 | CKV_AWS_162 | Apache-2.0 | aws_rds_cluster | iam_database_authentication_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-146 | CKV_AWS_146 | Apache-2.0 | aws_db_cluster_snapshot | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-139 | CKV_AWS_139 | Apache-2.0 | aws_rds_cluster | deletion_protection | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-16 | CKV_AWS_16 | Apache-2.0 | aws_db_instance | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-161 | CKV_AWS_161 | Apache-2.0 | aws_db_instance | iam_database_authentication_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-293 | CKV_AWS_293 | Apache-2.0 | aws_db_instance | deletion_protection | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-353-cluster | CKV_AWS_353 | Apache-2.0 | aws_rds_cluster_instance | performance_insights_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-353-db | CKV_AWS_353 | Apache-2.0 | aws_db_instance | performance_insights_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-157 | CKV_AWS_157 | Apache-2.0 | aws_db_instance | multi_az | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-141 | CKV_AWS_141 | Apache-2.0 | aws_redshift_cluster | allow_version_upgrade | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-64 | CKV_AWS_64 | Apache-2.0 | aws_redshift_cluster | encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-321 | CKV_AWS_321 | Apache-2.0 | aws_redshift_cluster | enhanced_vpc_routing | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-87 | CKV_AWS_87 | Apache-2.0 | aws_redshift_cluster | publicly_accessible | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-370 | CKV_AWS_370 | Apache-2.0 | aws_sagemaker_model | enable_network_isolation | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-123 | CKV_AWS_123 | Apache-2.0 | aws_vpc_endpoint_service | acceptance_required | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-156 | CKV_AWS_156 | Apache-2.0 | aws_workspaces_workspace | root_volume_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-155 | CKV_AWS_155 | Apache-2.0 | aws_workspaces_workspace | user_volume_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-389 | CKV_AWS_389 | Apache-2.0 | aws_launch_configuration | associate_public_ip_address | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-319 | CKV_AWS_319 | Apache-2.0 | aws_cloudwatch_metric_alarm | actions_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-89 | CKV_AWS_89 | Apache-2.0 | aws_dms_replication_instance | publicly_accessible | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-88-instance | CKV_AWS_88 | Apache-2.0 | aws_instance | associate_public_ip_address | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-69 | CKV_AWS_69 | Apache-2.0 | aws_mq_broker | publicly_accessible | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-202 | CKV_AWS_202 | Apache-2.0 | aws_memorydb_cluster | tls_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-17-db | CKV_AWS_17 | Apache-2.0 | aws_db_instance | publicly_accessible | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-17-cluster | CKV_AWS_17 | Apache-2.0 | aws_rds_cluster_instance | publicly_accessible | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-377 | CKV_AWS_377 | Apache-2.0 | aws_route53domains_registered_domain | transfer_lock | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-130 | CKV_AWS_130 | Apache-2.0 | aws_subnet | map_public_ip_on_launch | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-239 | CKV_AWS_239 | Apache-2.0 | aws_dax_cluster | cluster_endpoint_encryption_type | replace | `"TLS"` | `"NONE"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-51 | CKV_AWS_51 | Apache-2.0 | aws_ecr_repository | image_tag_mutability | replace | `"IMMUTABLE"` | `"MUTABLE"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-43 | CKV_AWS_43 | Apache-2.0 | aws_kinesis_stream | encryption_type | replace | `"KMS"` | `"NONE"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-170 | CKV_AWS_170 | Apache-2.0 | aws_qldb_ledger | permissions_mode | replace | `"STANDARD"` | `"ALLOW_ALL"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-122 | CKV_AWS_122 | Apache-2.0 | aws_sagemaker_notebook_instance | direct_internet_access | replace | `"Disabled"` | `"Enabled"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-307 | CKV_AWS_307 | Apache-2.0 | aws_sagemaker_notebook_instance | root_access | replace | `"Disabled"` | `"Enabled"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-331 | CKV_AWS_331 | Apache-2.0 | aws_ec2_transit_gateway | auto_accept_shared_attachments | replace | `"disable"` | `"enable"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| checkov-aws-258 | CKV_AWS_258 | Apache-2.0 | aws_lambda_function_url | authorization_type | replace | `"AWS_IAM"` | `"NONE"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0003 | AWS-0003 | MIT | aws_api_gateway_stage | xray_tracing_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0004 | AWS-0004 | MIT | aws_api_gateway_method | authorization | replace | `"AWS_IAM"` | `"NONE"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0009 | AWS-0009 | MIT | aws_launch_configuration | associate_public_ip_address | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0021 | AWS-0021 | MIT | aws_docdb_cluster | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0026 | AWS-0026 | MIT | aws_ebs_volume | encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0031 | AWS-0031 | MIT | aws_ecr_repository | image_tag_mutability | replace | `"IMMUTABLE"` | `"MUTABLE"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0037 | AWS-0037 | MIT | aws_efs_file_system | encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0045 | AWS-0045 | MIT | aws_elasticache_replication_group | at_rest_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0051 | AWS-0051 | MIT | aws_elasticache_replication_group | transit_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0052 | AWS-0052 | MIT | aws_alb | drop_invalid_header_fields | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0053 | AWS-0053 | MIT | aws_alb | internal | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0054 | AWS-0054 | MIT | aws_alb_listener | protocol | replace | `"HTTPS"` | `"HTTP"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0064 | AWS-0064 | MIT | aws_kinesis_stream | encryption_type | replace | `"KMS"` | `"NONE"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0072 | AWS-0072 | MIT | aws_mq_broker | publicly_accessible | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0076 | AWS-0076 | MIT | aws_neptune_cluster | storage_encrypted | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0092-bucket | AWS-0092 | MIT | aws_s3_bucket | acl | replace | `"private"` | `"public-read"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0092-acl | AWS-0092 | MIT | aws_s3_bucket_acl | acl | replace | `"private"` | `"authenticated-read"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0109-root | AWS-0109 | MIT | aws_workspaces_workspace | root_volume_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0109-user | AWS-0109 | MIT | aws_workspaces_workspace | user_volume_encryption_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0133 | AWS-0133 | MIT | aws_rds_cluster_instance | performance_insights_enabled | flip | `true` | `false` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0161 | AWS-0161 | MIT | aws_s3_bucket | acl | replace | `"private"` | `"public-read"` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0164 | AWS-0164 | MIT | aws_subnet | map_public_ip_on_launch | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |
| trivy-aws-0180 | AWS-0180 | MIT | aws_db_instance | publicly_accessible | flip | `false` | `true` | 0 | 0 | 0 | 0 | — | — | unwitnessed-not-enabled |

## Decision rule applied

The published states apply #161's rule without an unstated threshold:

- `enabled`: `Pre > 0` and no attributed `Invalid` mutant;
- `unwitnessed-not-enabled`: `Pre = 0`;
- `dropped-for-invalid`: one or more attributed `Invalid` mutants.

Execution-error rate is published as demanded, but it does not replace the explicit invalidity
rule. This run had no execution errors. The witnessed set is non-empty, so the empty-pack outcome
does not apply.

M5c.2 must embed exactly the ten enabled entries, retaining both catalogue origins even where
they request identical bytes. It must publish the unwitnessed rows rather than imply that this
six-module corpus disproved them. No pack enters `standard` through this decision.

## Reproduction

The dedicated recipe runs the candidate-load check, the six-target preview/run measurement and
the checked-in real-provider witness gate:

```bash
TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1 mise exec -- just measure-security-aws
```

The long measurement is excluded from `just test-integration`, as is the M5-0.4 corpus census.
It writes a crash-safe row sidecar and assembled JSON under
`.artifacts/measurement/`; those are reproducibility artefacts, while this document is the
published result. `TestEveryEnabledSecurityAWSEntryHasARealProviderWitness` re-runs every enabled
entry against the checked-in `aws-mocked` KMS and S3 sites and fails if any origin is absent or
any attributed mutant is `Invalid`. `TestTheSecurityAWSCandidatePackIsLoadableAndHasStableIdentities`
checks the full candidate pack through the engine seam.

## Addendum: the `PACK-WIDEN-CIDR` admission (#176)

**Date:** 2026-09-19

**Issue:** [#176](https://github.com/andrewesweet/tf-mut/issues/176)
**Result:** the one deferred scalar candidate, Trivy `AWS-0104`, admitted; 0% invalid; witnessed
on the checked-in `aws-mocked` fixture

This addendum resolves the deferral recorded in [Decision](#decision) above. #176 implemented
`PACK-WIDEN-CIDR` — `from` pinned to the sentinel `any-cidr`, `to` pinned to the IPv4
any-prefix `0.0.0.0/0`, the site rule a value rule: any top-level scalar string literal that
parses as an IPv4 CIDR other than `to` itself; malformed CIDRs, bare addresses, IPv6 prefixes
(including IPv4-mapped ones), list-valued and nested CIDRs, `dynamic` bodies, meta-arguments
and `data` bodies stay refused — and measured the entry in a dedicated user pack over the same
six targets as the M5-0.3 measurement, under the same decision rule.

| Target | Sites | Pre | Post/origin | Owned | Run | Invalid | KilledByError |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `aws-mocked` | 1 | 1 | 1 | 1 | 1 | 0 | 0 |
| `aws-platform-starter` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `platform-design` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `serverless-architecture-patterns` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `terraform-datadog-users` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `terraform-mongodbatlas-project` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

`trivy-aws-0104` (`AWS-0104`, MIT, `aws_vpc_security_group_egress_rule.cidr_ipv4`,
`any-cidr` → `0.0.0.0/0`): **enabled**. The decision rule takes any witnessed bytes with a
zero invalid rate, as in M5-0.3; the five public modules carry no scalar egress CIDR and are
not required to. The witnessed row is **owned** by `PACK-WIDEN-CIDR`, unlike the census's
flip rows, which `BOOL-LITERAL-FLIP` owned: no language operator produces `0.0.0.0/0` from
`10.0.0.0/8`, so the pack operator keeps the row. Terraform v1.15.8; wall clock about four
minutes, against the census's hours, because generation was narrowed by `--operator` to the
new form so a run grades the baseline plus the widen rows only.

The shipped pack grows from ten entries to eleven; `admittedSecurityAWSEntries` stays the
census's ten and governs `measure-security-aws` unchanged, so this addendum's result never
re-runs the eight-hour census measurement. Reproduction:

```bash
TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1 mise exec -- just measure-widen-cidr
```

It writes a crash-safe row sidecar and assembled JSON under `.artifacts/measurement/`
(`m5-widen-cidr-admission*.json[l]`); this addendum is the published result.
`TestTheWidenCIDRCandidatePackIsLoadableAndHasStableIdentities` checks the candidate pack
through the engine seam offline, and
`TestEveryWidenCIDREntryHasARealProviderWitness` re-runs the entry against the checked-in
`aws-mocked` egress site.
