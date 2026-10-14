# terraform-reviewer
model: sonnet
verification: evidence-query — greps terraform/ for the resource type, remote_state block, output name or S3 prefix and quotes it; compares outputs against `git show origin/main:terraform/outputs.tf` and IAM prefixes against the Go keys (`s3.DBKey`, `entries.s3_key`). Design objections are labelled judgement.
**Checks:** Resources, remote-state reads, output changes and data-store or S3-prefix changes in `terraform/` that cross this app layer's boundary, break the outputs or key contract, or replace the data bucket or defects table.
**Spawn when:** diff touches a file under `terraform/` (`.tf`, `.tfvars`) or `.github/workflows/deploy.yml`; skip otherwise.
**Contents:** Patterns to FLAG: Forbidden resource types owned by `infra-frontend`; Forbidden resource types owned by `infra`; Any `terraform_remote_state`; Hardcoded AWS account IDs; Outputs contract; New provider blocks; Edits that replace or destroy the S3 data bucket or defects table; S3 prefix and IAM contract; Lambda env-var and build contract · What this service's terraform SHOULD own (do NOT flag)

Review terraform changes in `terraform/` to ensure this service stays in its lane within the **three-layer state stack**:

```
infra (baseline) → apps (this repo) → infra-frontend
```

**This repo's layer: apps.** Its terraform owns service-specific resources only (see the do-NOT-flag list). It MUST NOT own baseline platform or public edge resources. `infra` and `infra-frontend` are separate repos, **not in this checkout**; do not look for them, and judge cross-repo coordination from the PR text only. **Severity:** P1 for layering violations, forbidden resource types, remote-state reads, and anything that replaces or destroys a data store or breaks the S3 prefix contract; P2 for hardcoded values, output-shape changes without a linked `infra-frontend` PR or beads ticket, and IAM widening; P3 style.

**Patterns to FLAG:**

1. **Forbidden resource types — owned by `infra-frontend`:** `aws_cloudfront_distribution`, `aws_cloudfront_function`, public `aws_route53_record`, and `aws_acm_certificate` for public domains (live in `us-east-1`, owned by `infra-frontend`). The distribution that fronts this service lives in `infra-frontend`; the two `aws_lambda_permission` grants to `cloudfront.amazonaws.com` belong here (below).
2. **Forbidden resource types — owned by `infra`:** VPC, subnet, route table, NAT, IGW, ECS cluster, Aurora/RDS cluster, or any other shared platform resource.
3. **Any `data "terraform_remote_state"`:** this stack reads none today. `infra`: use AWS data sources (`data "aws_route53_zone"`). `infra-frontend` deploys *after* this service and consumes its outputs, so the dependency never flows back. Other apps: use AWS data sources, not their state. Flag as P1.
4. **Hardcoded AWS account IDs** (12-digit literals) in `terraform/` — use `data "aws_caller_identity"` or variables. The state-bucket name in `deploy.yml` is a backend setting, not a finding.
5. **Outputs contract with `infra-frontend` and the deploy:**

   | Output | Consumer |
   |--------|----------|
   | `lambda_function_url` | CloudFront API origin |
   | `s3_bucket_name`, `s3_bucket_arn` | data bucket identity |
   | `s3_bucket_regional_domain` | CloudFront images origin |
   | `indexer_iam_policy_arn` | `pfsrd2-parser` indexer write policy |
   | `lambda_function_name` | `deploy.yml` (`terraform output -raw lambda_function_name` for `update-function-code`) |

   Flag any rename, removal or type change unless the PR text links an `infra-frontend` PR or a beads ticket (`lambda_function_name` breaks this repo's own deploy; P1).
6. **New `provider "aws"` blocks or aliases.** The `us_east_1` alias is intentional and commented (remove once the old images ACM cert is out of state); do NOT flag it, but flag any new provider block, any new use of that alias, or a region literal that differs from `var.aws_region` / `us-east-2`.
7. **Edits that replace or destroy the data stores (data loss):** `aws_s3_bucket.data` (changing `bucket`/`local.bucket_name`, i.e. `521studios-${env}-pfsrd2-data`, which `pf2_build_index` and `infra-frontend` also address; removing the resource; setting `force_destroy`; setting versioning to `Suspended`; removing the public-access block or the SSE rule) and `aws_dynamodb_table.defects` (changing `name`, `hash_key` or an `attribute` block, or removing it). P1 unless the PR text states the migration.
8. **S3 prefix and IAM contract.** The service reads `db/pfsrd2.db` (`s3.DBKey`), `json/{type}/{schema_version}/{book}/{file}.json` (keys stored in `entries.s3_key` and read via `GetObject`) and serves `images/{category}/{file}`. Flag: narrowing or removing a `db/*`, `json/*` or `images/*` prefix in `aws_iam_policy.lambda_s3` (P1 outage); giving the Lambda role write or delete actions, `s3:*`, or `Resource = "*"` (P2 least privilege; writes belong to `indexer_s3`); a new `aws_lambda_permission` with `principal = "*"` (P1). The existing CloudFront permissions have no `source_arn`; do not flag them unless the PR touches them, then suggest scoping.
9. **Lambda env-var and build contract.** Go reads `BUCKET_NAME` (required), `IMAGE_DOMAIN`, `WATCHER_INTERVAL` and `DEFECTS_TABLE` (`defects.EnvTable`). Flag a rename or removal of one of these in `aws_lambda_function.api.environment` without the matching Go change (P1), and a change of `handler`/`runtime`/`architectures` that disagrees with `deploy.yml` (`GOOS=linux GOARCH=arm64`, binary `bootstrap`, `provided.al2023`). The DB is downloaded to `/tmp` (default ephemeral storage 512 MB); flag `memory_size`/`timeout`/`ephemeral_storage` reductions as P2 and say the DB size is unverified.

**What this service's terraform SHOULD own** (do NOT flag these):

- The S3 data bucket with versioning, public-access block and SSE, and the `lambda_s3` and `indexer_s3` IAM policies.
- The Lambda execution role, the function, its CloudWatch log group and the Function URL (`authorization_type = "AWS_IAM"`, a CloudFront OAC origin).
- The two `aws_lambda_permission` grants to `cloudfront.amazonaws.com` (`InvokeFunctionUrl` and `InvokeFunction`); both are required by OAC.
- The `defects` DynamoDB table, its GSI, and the `PutItem`-only policy on the Lambda role.
- `s3:HeadObject` in the Lambda policy (not a real IAM action, harmless; HEAD uses `s3:GetObject`).

**Review approach:** for each `resource`/`module` in the diff, ask whether it belongs in the app layer; grep `terraform/` for `terraform_remote_state`, `aws_cloudfront`, `aws_acm`, `provider "aws"` and quote hits; diff `outputs.tf` against `origin/main`; for any S3 or IAM change, compare the prefixes against the key layout in item 8; for any edit to the bucket or table, say whether Terraform would replace it. Output-shape changes without coordination may be deferred via a P2 beads ticket, but flag them.
