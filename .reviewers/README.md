# Reviewer notes (orchestrator only; reviewers don't see this file)

Go reviewers for pfsrd2-data-api (`service/` Go module serving S3 JSON plus a SQLite index; `terraform/` app layer; `openapi.yaml` contract).

**Dropped defaults:** `silent-failure-hunter` (covered by error-handling-reviewer), `comment-analyzer` (clarity-reviewer), `code-simplifier` (complexity-reviewer + dead-code-reviewer). Kept as starters: `code-reviewer`, `pr-test-analyzer`, `type-design-analyzer`.

**Deferral:** a P2 or P3 finding may be deferred with a beads ticket linked in the PR; P1 is not deferrable.

**CI today** (`.github/workflows/ci.yml`): gofmt and `go test ./...` only. No `go vet`, no `-race`, no staticcheck, gocognit or deadcode; their absence is not a finding. Pre-commit is gofmt only. `deploy.yml` builds and applies terraform on push to `main` (staging) or manual dispatch.

Gemini Code Assist is retired from this org, so `config.json` disables it.
