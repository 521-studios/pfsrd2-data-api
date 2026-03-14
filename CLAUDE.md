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

## Key endpoints

```
GET /search?q=dragon&type=monsters&level=5-10
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

Push to `main` → GitHub Actions runs `deploy.yml` → Terraform + Lambda update.
