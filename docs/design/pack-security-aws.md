# The shipped `security-aws` pack

**Shipped:** M5c.2 · **Admitted by:** M5-0.3, the pack-seed census
([#161](https://github.com/andrewesweet/tf-mut/issues/161)), plus the one scalar candidate
#176 admitted under the form it implemented ·
**Census and decision record:**
[docs/research/18-m5-pack-seed-census.md](../research/18-m5-pack-seed-census.md) ·
**Embedded file:** `internal/mutation/packs/security-aws.hcl` ·
**Upstream:** rule catalogues published by [Checkov](https://github.com/bridgecrewio/checkov)
(Apache-2.0) and [Trivy](https://github.com/aquasecurity/trivy) (MIT), as read by the census.

## What ships

- The pack is **embedded in the binary** under its reserved name `security-aws`. It is parsed
  at init by the same `ParsePack` contract a user pack goes through; a shipped pack that fails
  its own contract panics at init, which is a broken build, never a silent skip
  (`TestEveryShippedPackSatisfiesTheUserPackContract`).
- `--pack security-aws` selects it on `run`, `preview` and `suggest` **with no registration**
  (`TestTheShippedSecurityAWSPackResolvesWithoutRegistration`). A user pack named
  `security-aws` is refused by name while the shipped pack still loads
  (`TestAUserPackCannotShadowTheShippedSecurityAWSPack`).
- It carries **exactly the eleven entries the census admitted plus the #176 widen-cidr entry**
  (`TestTheShippedSecurityAWSPackShipsExactlyTheAdmittedEntries`). The 105 loadable candidates
  the census saw but could not witness are published below and are **not enabled**.
- **The witnessed set is not empty.** The census explicitly allowed the empty outcome — zero
  witnessed entries shipping nothing — and the measurement refuted it.
- **No pack is ever in `standard`.** The default population is byte-identical with the pack in
  the binary (`TestTheShippedPackSelectionLeavesTheDefaultPopulationAlone`); selecting the pack
  is always opt-in, and admission of any pack to a default remains a separate evidence-carrying
  change.
- Row ownership follows the census: the language operator `BOOL-LITERAL-FLIP` sorts before
  `PACK-FLIP` and owns every row a pack entry also produced, with the pack entry retained in
  the surviving row's origins. **Owned is 0 for every entry**, below — that is the documented
  ownership rule, not a defect. With the pack enabled, every mutant's origins name the pack and
  the entry, so the upstream rule is reachable from any report.
- Trivy `AWS-0104` (permissive egress CIDR) is in the pack under the `widen-cidr` form: #176
  implemented `PACK-WIDEN-CIDR`, measured the entry over the same six targets
  (`just measure-widen-cidr`) and admitted it by the same decision rule. It is not encoded as
  `replace`, and its row is **owned** by the pack operator — no language operator produces
  `0.0.0.0/0`, so unlike the flips the widen row's owner is `PACK-WIDEN-CIDR` itself.

## Witness counts

For every enabled entry, three counts are published, as the census requires:

- **Pre** — mutants whose bytes the entry produced, before cross-catalogue deduplication;
- **Post/origin** — rows carrying the entry as an origin, after deduplication;
- **Owned** — rows the pack operators own (always 0; see above).

The two catalogues overlap exactly on the enabled set: each Checkov entry and its Trivy twin
produce identical bytes, so both entries become origins of every row their shared bytes
produced. The KMS entries witnessed on 15 mutants and the public-access-block entries on 7,
both before and after deduplication — deduplication moved rows between operators' ownership,
not between entries. The #176 widen-cidr entry witnessed on the `aws-mocked` egress site
(1 mutant, pre and post equal, 0% invalid) and its row is owned by `PACK-WIDEN-CIDR`.

## Enabled entries (11)

| Entry | `source_rule` | `source_licence` | Resource type | Attribute | Form | `from` → `to` | Pre | Post/origin | Owned |
| --- | --- | --- | --- | --- | --- | --- | ---: | ---: | ---: |
| `checkov-aws-7` | `CKV_AWS_7` | Apache-2.0 | `aws_kms_key` | `enable_key_rotation` | `flip` | `true` → `false` | 15 | 15 | 0 |
| `checkov-aws-53` | `CKV_AWS_53` | Apache-2.0 | `aws_s3_bucket_public_access_block` | `block_public_acls` | `flip` | `true` → `false` | 7 | 7 | 0 |
| `checkov-aws-54` | `CKV_AWS_54` | Apache-2.0 | `aws_s3_bucket_public_access_block` | `block_public_policy` | `flip` | `true` → `false` | 7 | 7 | 0 |
| `checkov-aws-55` | `CKV_AWS_55` | Apache-2.0 | `aws_s3_bucket_public_access_block` | `ignore_public_acls` | `flip` | `true` → `false` | 7 | 7 | 0 |
| `checkov-aws-56` | `CKV_AWS_56` | Apache-2.0 | `aws_s3_bucket_public_access_block` | `restrict_public_buckets` | `flip` | `true` → `false` | 7 | 7 | 0 |
| `trivy-aws-0065` | `AWS-0065` | MIT | `aws_kms_key` | `enable_key_rotation` | `flip` | `true` → `false` | 15 | 15 | 0 |
| `trivy-aws-0086` | `AWS-0086` | MIT | `aws_s3_bucket_public_access_block` | `block_public_acls` | `flip` | `true` → `false` | 7 | 7 | 0 |
| `trivy-aws-0087` | `AWS-0087` | MIT | `aws_s3_bucket_public_access_block` | `block_public_policy` | `flip` | `true` → `false` | 7 | 7 | 0 |
| `trivy-aws-0091` | `AWS-0091` | MIT | `aws_s3_bucket_public_access_block` | `ignore_public_acls` | `flip` | `true` → `false` | 7 | 7 | 0 |
| `trivy-aws-0093` | `AWS-0093` | MIT | `aws_s3_bucket_public_access_block` | `restrict_public_buckets` | `flip` | `true` → `false` | 7 | 7 | 0 |
| `trivy-aws-0104` | `AWS-0104` | MIT | `aws_vpc_security_group_egress_rule` | `cidr_ipv4` | `widen-cidr` | `any-cidr` → `0.0.0.0/0` | 1 | 1 | 1 |

## Unwitnessed, not enabled (105)

Loadable flip/replace candidates the census saw but that produced bytes for no mutant on the
measured corpus. Published for the next admission pass; none is enabled. Transcribed
mechanically from the census's per-entry table. The one scalar candidate the census deferred
rather than left here — Trivy `AWS-0104` — is enabled above under #176's form.

| Entry | `source_rule` | `source_licence` | Resource type | Attribute | Form | `from` → `to` |
| --- | --- | --- | --- | --- | --- | --- |
| `checkov-aws-131-lb` | `CKV_AWS_131` | Apache-2.0 | `aws_lb` | `drop_invalid_header_fields` | `flip` | `true` → `false` |
| `checkov-aws-131-alb` | `CKV_AWS_131` | Apache-2.0 | `aws_alb` | `drop_invalid_header_fields` | `flip` | `true` → `false` |
| `checkov-aws-235` | `CKV_AWS_235` | Apache-2.0 | `aws_ami_copy` | `encrypted` | `flip` | `true` → `false` |
| `checkov-aws-120` | `CKV_AWS_120` | Apache-2.0 | `aws_api_gateway_stage` | `cache_cluster_enabled` | `flip` | `true` → `false` |
| `checkov-aws-73` | `CKV_AWS_73` | Apache-2.0 | `aws_api_gateway_stage` | `xray_tracing_enabled` | `flip` | `true` → `false` |
| `checkov-aws-214` | `CKV_AWS_214` | Apache-2.0 | `aws_appsync_api_cache` | `at_rest_encryption_enabled` | `flip` | `true` → `false` |
| `checkov-aws-215` | `CKV_AWS_215` | Apache-2.0 | `aws_appsync_api_cache` | `transit_encryption_enabled` | `flip` | `true` → `false` |
| `checkov-aws-96` | `CKV_AWS_96` | Apache-2.0 | `aws_rds_cluster` | `storage_encrypted` | `flip` | `true` → `false` |
| `checkov-aws-216` | `CKV_AWS_216` | Apache-2.0 | `aws_cloudfront_distribution` | `enabled` | `flip` | `true` → `false` |
| `checkov-aws-251` | `CKV_AWS_251` | Apache-2.0 | `aws_cloudtrail` | `enable_logging` | `flip` | `true` → `false` |
| `checkov-aws-36` | `CKV_AWS_36` | Apache-2.0 | `aws_cloudtrail` | `enable_log_file_validation` | `flip` | `true` → `false` |
| `checkov-aws-67` | `CKV_AWS_67` | Apache-2.0 | `aws_cloudtrail` | `is_multi_region_trail` | `flip` | `true` → `false` |
| `checkov-aws-366` | `CKV_AWS_366` | Apache-2.0 | `aws_cognito_identity_pool` | `allow_unauthenticated_identities` | `flip` | `false` → `true` |
| `checkov-aws-226-db` | `CKV_AWS_226` | Apache-2.0 | `aws_db_instance` | `auto_minor_version_upgrade` | `flip` | `true` → `false` |
| `checkov-aws-226-cluster` | `CKV_AWS_226` | Apache-2.0 | `aws_rds_cluster_instance` | `auto_minor_version_upgrade` | `flip` | `true` → `false` |
| `checkov-aws-222` | `CKV_AWS_222` | Apache-2.0 | `aws_dms_replication_instance` | `auto_minor_version_upgrade` | `flip` | `true` → `false` |
| `checkov-aws-74` | `CKV_AWS_74` | Apache-2.0 | `aws_docdb_cluster` | `storage_encrypted` | `flip` | `true` → `false` |
| `checkov-aws-292` | `CKV_AWS_292` | Apache-2.0 | `aws_docdb_global_cluster` | `storage_encrypted` | `flip` | `true` → `false` |
| `checkov-aws-106` | `CKV_AWS_106` | Apache-2.0 | `aws_ebs_encryption_by_default` | `enabled` | `flip` | `true` → `false` |
| `checkov-aws-3` | `CKV_AWS_3` | Apache-2.0 | `aws_ebs_volume` | `encrypted` | `flip` | `true` → `false` |
| `checkov-aws-126` | `CKV_AWS_126` | Apache-2.0 | `aws_instance` | `monitoring` | `flip` | `true` → `false` |
| `checkov-aws-135` | `CKV_AWS_135` | Apache-2.0 | `aws_instance` | `ebs_optimized` | `flip` | `true` → `false` |
| `checkov-aws-42` | `CKV_AWS_42` | Apache-2.0 | `aws_efs_file_system` | `encrypted` | `flip` | `true` → `false` |
| `checkov-aws-138` | `CKV_AWS_138` | Apache-2.0 | `aws_elb` | `cross_zone_load_balancing` | `flip` | `true` → `false` |
| `checkov-aws-390` | `CKV_AWS_390` | Apache-2.0 | `aws_emr_block_public_access_configuration` | `block_public_security_group_rules` | `flip` | `true` → `false` |
| `checkov-aws-322` | `CKV_AWS_322` | Apache-2.0 | `aws_elasticache_cluster` | `auto_minor_version_upgrade` | `flip` | `true` → `false` |
| `checkov-aws-29` | `CKV_AWS_29` | Apache-2.0 | `aws_elasticache_replication_group` | `at_rest_encryption_enabled` | `flip` | `true` → `false` |
| `checkov-aws-30` | `CKV_AWS_30` | Apache-2.0 | `aws_elasticache_replication_group` | `transit_encryption_enabled` | `flip` | `true` → `false` |
| `checkov-aws-238` | `CKV_AWS_238` | Apache-2.0 | `aws_guardduty_detector` | `enable` | `flip` | `true` → `false` |
| `checkov-aws-227` | `CKV_AWS_227` | Apache-2.0 | `aws_kms_key` | `is_enabled` | `flip` | `true` → `false` |
| `checkov-aws-152-lb` | `CKV_AWS_152` | Apache-2.0 | `aws_lb` | `enable_cross_zone_load_balancing` | `flip` | `true` → `false` |
| `checkov-aws-152-alb` | `CKV_AWS_152` | Apache-2.0 | `aws_alb` | `enable_cross_zone_load_balancing` | `flip` | `true` → `false` |
| `checkov-aws-150-lb` | `CKV_AWS_150` | Apache-2.0 | `aws_lb` | `enable_deletion_protection` | `flip` | `true` → `false` |
| `checkov-aws-150-alb` | `CKV_AWS_150` | Apache-2.0 | `aws_alb` | `enable_deletion_protection` | `flip` | `true` → `false` |
| `checkov-aws-207` | `CKV_AWS_207` | Apache-2.0 | `aws_mq_broker` | `auto_minor_version_upgrade` | `flip` | `true` → `false` |
| `checkov-aws-279` | `CKV_AWS_279` | Apache-2.0 | `aws_neptune_cluster_snapshot` | `storage_encrypted` | `flip` | `true` → `false` |
| `checkov-aws-102` | `CKV_AWS_102` | Apache-2.0 | `aws_neptune_cluster_instance` | `publicly_accessible` | `flip` | `false` → `true` |
| `checkov-aws-44` | `CKV_AWS_44` | Apache-2.0 | `aws_neptune_cluster` | `storage_encrypted` | `flip` | `true` → `false` |
| `checkov-aws-362` | `CKV_AWS_362` | Apache-2.0 | `aws_neptune_cluster` | `copy_tags_to_snapshot` | `flip` | `true` → `false` |
| `checkov-aws-359` | `CKV_AWS_359` | Apache-2.0 | `aws_neptune_cluster` | `iam_database_authentication_enabled` | `flip` | `true` → `false` |
| `checkov-aws-344` | `CKV_AWS_344` | Apache-2.0 | `aws_networkfirewall_firewall` | `delete_protection` | `flip` | `true` → `false` |
| `checkov-aws-11` | `CKV_AWS_11` | Apache-2.0 | `aws_iam_account_password_policy` | `require_lowercase_characters` | `flip` | `true` → `false` |
| `checkov-aws-12` | `CKV_AWS_12` | Apache-2.0 | `aws_iam_account_password_policy` | `require_numbers` | `flip` | `true` → `false` |
| `checkov-aws-14` | `CKV_AWS_14` | Apache-2.0 | `aws_iam_account_password_policy` | `require_symbols` | `flip` | `true` → `false` |
| `checkov-aws-15` | `CKV_AWS_15` | Apache-2.0 | `aws_iam_account_password_policy` | `require_uppercase_characters` | `flip` | `true` → `false` |
| `checkov-aws-172` | `CKV_AWS_172` | Apache-2.0 | `aws_qldb_ledger` | `deletion_protection` | `flip` | `true` → `false` |
| `checkov-aws-313` | `CKV_AWS_313` | Apache-2.0 | `aws_rds_cluster` | `copy_tags_to_snapshot` | `flip` | `true` → `false` |
| `checkov-aws-162` | `CKV_AWS_162` | Apache-2.0 | `aws_rds_cluster` | `iam_database_authentication_enabled` | `flip` | `true` → `false` |
| `checkov-aws-146` | `CKV_AWS_146` | Apache-2.0 | `aws_db_cluster_snapshot` | `storage_encrypted` | `flip` | `true` → `false` |
| `checkov-aws-139` | `CKV_AWS_139` | Apache-2.0 | `aws_rds_cluster` | `deletion_protection` | `flip` | `true` → `false` |
| `checkov-aws-16` | `CKV_AWS_16` | Apache-2.0 | `aws_db_instance` | `storage_encrypted` | `flip` | `true` → `false` |
| `checkov-aws-161` | `CKV_AWS_161` | Apache-2.0 | `aws_db_instance` | `iam_database_authentication_enabled` | `flip` | `true` → `false` |
| `checkov-aws-293` | `CKV_AWS_293` | Apache-2.0 | `aws_db_instance` | `deletion_protection` | `flip` | `true` → `false` |
| `checkov-aws-353-cluster` | `CKV_AWS_353` | Apache-2.0 | `aws_rds_cluster_instance` | `performance_insights_enabled` | `flip` | `true` → `false` |
| `checkov-aws-353-db` | `CKV_AWS_353` | Apache-2.0 | `aws_db_instance` | `performance_insights_enabled` | `flip` | `true` → `false` |
| `checkov-aws-157` | `CKV_AWS_157` | Apache-2.0 | `aws_db_instance` | `multi_az` | `flip` | `true` → `false` |
| `checkov-aws-141` | `CKV_AWS_141` | Apache-2.0 | `aws_redshift_cluster` | `allow_version_upgrade` | `flip` | `true` → `false` |
| `checkov-aws-64` | `CKV_AWS_64` | Apache-2.0 | `aws_redshift_cluster` | `encrypted` | `flip` | `true` → `false` |
| `checkov-aws-321` | `CKV_AWS_321` | Apache-2.0 | `aws_redshift_cluster` | `enhanced_vpc_routing` | `flip` | `true` → `false` |
| `checkov-aws-87` | `CKV_AWS_87` | Apache-2.0 | `aws_redshift_cluster` | `publicly_accessible` | `flip` | `false` → `true` |
| `checkov-aws-370` | `CKV_AWS_370` | Apache-2.0 | `aws_sagemaker_model` | `enable_network_isolation` | `flip` | `true` → `false` |
| `checkov-aws-123` | `CKV_AWS_123` | Apache-2.0 | `aws_vpc_endpoint_service` | `acceptance_required` | `flip` | `true` → `false` |
| `checkov-aws-156` | `CKV_AWS_156` | Apache-2.0 | `aws_workspaces_workspace` | `root_volume_encryption_enabled` | `flip` | `true` → `false` |
| `checkov-aws-155` | `CKV_AWS_155` | Apache-2.0 | `aws_workspaces_workspace` | `user_volume_encryption_enabled` | `flip` | `true` → `false` |
| `checkov-aws-389` | `CKV_AWS_389` | Apache-2.0 | `aws_launch_configuration` | `associate_public_ip_address` | `flip` | `false` → `true` |
| `checkov-aws-319` | `CKV_AWS_319` | Apache-2.0 | `aws_cloudwatch_metric_alarm` | `actions_enabled` | `flip` | `true` → `false` |
| `checkov-aws-89` | `CKV_AWS_89` | Apache-2.0 | `aws_dms_replication_instance` | `publicly_accessible` | `flip` | `false` → `true` |
| `checkov-aws-88-instance` | `CKV_AWS_88` | Apache-2.0 | `aws_instance` | `associate_public_ip_address` | `flip` | `false` → `true` |
| `checkov-aws-69` | `CKV_AWS_69` | Apache-2.0 | `aws_mq_broker` | `publicly_accessible` | `flip` | `false` → `true` |
| `checkov-aws-202` | `CKV_AWS_202` | Apache-2.0 | `aws_memorydb_cluster` | `tls_enabled` | `flip` | `true` → `false` |
| `checkov-aws-17-db` | `CKV_AWS_17` | Apache-2.0 | `aws_db_instance` | `publicly_accessible` | `flip` | `false` → `true` |
| `checkov-aws-17-cluster` | `CKV_AWS_17` | Apache-2.0 | `aws_rds_cluster_instance` | `publicly_accessible` | `flip` | `false` → `true` |
| `checkov-aws-377` | `CKV_AWS_377` | Apache-2.0 | `aws_route53domains_registered_domain` | `transfer_lock` | `flip` | `true` → `false` |
| `checkov-aws-130` | `CKV_AWS_130` | Apache-2.0 | `aws_subnet` | `map_public_ip_on_launch` | `flip` | `false` → `true` |
| `checkov-aws-239` | `CKV_AWS_239` | Apache-2.0 | `aws_dax_cluster` | `cluster_endpoint_encryption_type` | `replace` | `"TLS"` → `"NONE"` |
| `checkov-aws-51` | `CKV_AWS_51` | Apache-2.0 | `aws_ecr_repository` | `image_tag_mutability` | `replace` | `"IMMUTABLE"` → `"MUTABLE"` |
| `checkov-aws-43` | `CKV_AWS_43` | Apache-2.0 | `aws_kinesis_stream` | `encryption_type` | `replace` | `"KMS"` → `"NONE"` |
| `checkov-aws-170` | `CKV_AWS_170` | Apache-2.0 | `aws_qldb_ledger` | `permissions_mode` | `replace` | `"STANDARD"` → `"ALLOW_ALL"` |
| `checkov-aws-122` | `CKV_AWS_122` | Apache-2.0 | `aws_sagemaker_notebook_instance` | `direct_internet_access` | `replace` | `"Disabled"` → `"Enabled"` |
| `checkov-aws-307` | `CKV_AWS_307` | Apache-2.0 | `aws_sagemaker_notebook_instance` | `root_access` | `replace` | `"Disabled"` → `"Enabled"` |
| `checkov-aws-331` | `CKV_AWS_331` | Apache-2.0 | `aws_ec2_transit_gateway` | `auto_accept_shared_attachments` | `replace` | `"disable"` → `"enable"` |
| `checkov-aws-258` | `CKV_AWS_258` | Apache-2.0 | `aws_lambda_function_url` | `authorization_type` | `replace` | `"AWS_IAM"` → `"NONE"` |
| `trivy-aws-0003` | `AWS-0003` | MIT | `aws_api_gateway_stage` | `xray_tracing_enabled` | `flip` | `true` → `false` |
| `trivy-aws-0004` | `AWS-0004` | MIT | `aws_api_gateway_method` | `authorization` | `replace` | `"AWS_IAM"` → `"NONE"` |
| `trivy-aws-0009` | `AWS-0009` | MIT | `aws_launch_configuration` | `associate_public_ip_address` | `flip` | `false` → `true` |
| `trivy-aws-0021` | `AWS-0021` | MIT | `aws_docdb_cluster` | `storage_encrypted` | `flip` | `true` → `false` |
| `trivy-aws-0026` | `AWS-0026` | MIT | `aws_ebs_volume` | `encrypted` | `flip` | `true` → `false` |
| `trivy-aws-0031` | `AWS-0031` | MIT | `aws_ecr_repository` | `image_tag_mutability` | `replace` | `"IMMUTABLE"` → `"MUTABLE"` |
| `trivy-aws-0037` | `AWS-0037` | MIT | `aws_efs_file_system` | `encrypted` | `flip` | `true` → `false` |
| `trivy-aws-0045` | `AWS-0045` | MIT | `aws_elasticache_replication_group` | `at_rest_encryption_enabled` | `flip` | `true` → `false` |
| `trivy-aws-0051` | `AWS-0051` | MIT | `aws_elasticache_replication_group` | `transit_encryption_enabled` | `flip` | `true` → `false` |
| `trivy-aws-0052` | `AWS-0052` | MIT | `aws_alb` | `drop_invalid_header_fields` | `flip` | `true` → `false` |
| `trivy-aws-0053` | `AWS-0053` | MIT | `aws_alb` | `internal` | `flip` | `true` → `false` |
| `trivy-aws-0054` | `AWS-0054` | MIT | `aws_alb_listener` | `protocol` | `replace` | `"HTTPS"` → `"HTTP"` |
| `trivy-aws-0064` | `AWS-0064` | MIT | `aws_kinesis_stream` | `encryption_type` | `replace` | `"KMS"` → `"NONE"` |
| `trivy-aws-0072` | `AWS-0072` | MIT | `aws_mq_broker` | `publicly_accessible` | `flip` | `false` → `true` |
| `trivy-aws-0076` | `AWS-0076` | MIT | `aws_neptune_cluster` | `storage_encrypted` | `flip` | `true` → `false` |
| `trivy-aws-0092-bucket` | `AWS-0092` | MIT | `aws_s3_bucket` | `acl` | `replace` | `"private"` → `"public-read"` |
| `trivy-aws-0092-acl` | `AWS-0092` | MIT | `aws_s3_bucket_acl` | `acl` | `replace` | `"private"` → `"authenticated-read"` |
| `trivy-aws-0109-root` | `AWS-0109` | MIT | `aws_workspaces_workspace` | `root_volume_encryption_enabled` | `flip` | `true` → `false` |
| `trivy-aws-0109-user` | `AWS-0109` | MIT | `aws_workspaces_workspace` | `user_volume_encryption_enabled` | `flip` | `true` → `false` |
| `trivy-aws-0133` | `AWS-0133` | MIT | `aws_rds_cluster_instance` | `performance_insights_enabled` | `flip` | `true` → `false` |
| `trivy-aws-0161` | `AWS-0161` | MIT | `aws_s3_bucket` | `acl` | `replace` | `"private"` → `"public-read"` |
| `trivy-aws-0164` | `AWS-0164` | MIT | `aws_subnet` | `map_public_ip_on_launch` | `flip` | `false` → `true` |
| `trivy-aws-0180` | `AWS-0180` | MIT | `aws_db_instance` | `publicly_accessible` | `flip` | `false` → `true` |

## Gates

The offline gate `just gate-m5` carries the shipped pack by name: the admitted-list pin, the
unregistered resolution, the shadowing refusal, the default-population invariance and the
init-time contract. The network-gated witness
(`TestTheShippedSecurityAWSPackIsWitnessedOnAWSMocked`) drives `--pack security-aws` through
preview, run and suggest on the checked-in `aws-mocked` fixture and asserts every admitted
entry's origin witness, the widen-cidr entry's included. The network-gated measurement
`just measure-widen-cidr` re-executes the #176 admission over the same six targets as the
census, narrowed by `--operator` to the new form.
