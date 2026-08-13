# pfsrd2-data-api

Go Lambda + API Gateway service that serves Pathfinder 2e data from S3/SQLite.

## Quick Start (local dev)

```bash
# From repo root
docker compose up
# API available at http://localhost:8090
```

## Architecture

- **Lambda** (`service/main.go`): cold start downloads SQLite DB from S3, serves via chi router
- **Local dev** (`service/cmd/local/main.go`): same code, plain net/http, Air hot-reload
- **DB watcher**: hourly background goroutine checks S3 ETag, auto-refreshes if changed
- **Indexer**: Python in `pfsrd2-parser/pfsrd2/indexer/` — builds the DB and uploads to S3

## Terraform Ownership

This service owns all its own resources: S3 bucket, IAM roles/policies, Lambda function + Function URL, CloudWatch logs.

This service does **not** read from `infra` or `infra-frontend` remote state. Use AWS `data` sources to look up shared primitives by name (e.g. `data "aws_route53_zone"`).

Public-facing DNS, CloudFront distributions, and ACM certs are owned by `infra-frontend` — see workspace CLAUDE.md for the full rules.

## Terraform Outputs (consumed by infra-frontend)

| Output | Description |
|--------|-------------|
| `lambda_function_url` | Lambda Function URL — CloudFront API origin |
| `s3_bucket_name` | Data bucket name |
| `s3_bucket_regional_domain` | S3 regional domain — CloudFront images origin |
| `s3_bucket_arn` | Data bucket ARN |
| `indexer_iam_policy_arn` | IAM policy for pfsrd2-parser to write to S3 |

## Game IDs

The `game_id` is an MD5 hash of `"{source_name}: {page}: {name}"` — a content-addressable identifier tied to the published citation, not any particular data source. Any parser processing the same source material produces the same game_id, enabling reconciliation across sources (AoN HTML, direct PDF parsing, etc.).

All API lookups use game_id, not aonid (which is Archives of Nethys-specific).

## Key endpoints

```
GET /search?q=dragon&type=monsters&level=5-10&traits=fire,dragon&category=Runes&subcategory=Property%20Runes
GET /search/suggest?q=dragon&type=monsters&traits=fire&category=Runes
GET /search/suggest/unified?q=orc&type=monsters  (edition-aware, with alternates; also takes traits/category/subcategory)
GET /search/facets?type=equipment&type=armor     → {"categories": {"Runes": ["Property Runes", ...], ...}}
GET /search/traits?q=fi&type=creatures&trait=undead  → co-occurring trait typeahead (narrowed by type + selected chips)
GET /types
GET /sources
GET /{type}?source=Bestiary&edition=legacy
GET /{type}/{schema_version}/{book}/{filename}
GET /entries/{game_id}
GET /entries/{game_id}/full?version=1.3
GET /images/{category}/{filename}   → 302 to CloudFront
GET /db/status
POST /db/refresh
```

## Dev workflow

- Edit any `.go` file → Air recompiles automatically → requests use new code
- `POST /db/refresh` to pull a fresh DB from staging S3
- Env: `BUCKET_NAME`, `AWS_PROFILE`, `WATCHER_INTERVAL`, `IMAGE_DOMAIN`

## S3 structure

```
s3://521studios-{env}-pfsrd2-data/
├── db/pfsrd2.db
├── json/{type}/{schema_version}/{book}/{file}.json
└── images/{category}/{file}
```

## Deploy

### Staging
Deploys automatically on push to `main`. Trigger manually:
```bash
gh workflow run deploy.yml --ref main --field environment=staging
```

### Production
Manual only, must be triggered from `main`:
```bash
gh workflow run deploy.yml --ref main --field environment=production
```
