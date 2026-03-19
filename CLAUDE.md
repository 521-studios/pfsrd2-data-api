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

This service does **not** read from `infra` remote state. Use AWS `data` sources to look up shared primitives by name (e.g. `data "aws_route53_zone"`).

Public-facing DNS, CloudFront distributions, and ACM certs are owned by `infra` — see workspace CLAUDE.md for the full rules.

## Terraform Outputs (consumed by infra)

| Output | Description |
|--------|-------------|
| `lambda_function_url` | Lambda Function URL — CloudFront API origin |
| `s3_bucket_name` | Data bucket name — CloudFront images origin |
| `s3_bucket_arn` | Data bucket ARN |
| `indexer_iam_policy_arn` | IAM policy for pfsrd2-parser to write to S3 |

## Game IDs

The `game_id` is the primary key used throughout this API for looking up entries. It is an MD5 hash of `"{source_name}: {page}: {name}"` — derived from the source book name, page number, and entry name. This makes it a **content-addressable identifier** tied to the published game content itself, not to any particular data source or database.

Because the hash inputs are the published citation (book + page + name), any parser that processes the same source material will produce the same game_id. This allows data from different sources (AoN HTML scraping, direct PDF parsing, etc.) to be compared and reconciled by game_id.

The `game_id` field is stored in the `entries` table and is used by `db.GetByGameID()`. All API lookups use game_id, not aonid (which is Archives of Nethys-specific).

## Key endpoints

```
GET /search?q=dragon&type=monsters&level=5-10
GET /search/suggest?q=dragon&type=monsters
GET /search/suggest/unified?q=orc&type=monsters  (edition-aware, with alternates)
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
