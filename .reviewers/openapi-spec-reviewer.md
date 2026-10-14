# openapi-spec-reviewer
model: sonnet
verification: evidence-query — lists the chi routes in `NewRouter` and the `paths:` keys of `openapi.yaml` and quotes the mismatch; JSON-tag drift quotes the Go struct field and the schema property. "Observable contract change" calls are judgement.
**Checks:** Drift between the chi routes in `service/internal/api/handlers.go` (and the response structs they return) and `openapi.yaml`: missing, stale or mismatched paths, parameters, status codes, schemas and `$ref`s.
**Spawn when:** diff adds, removes or changes a chi route registration or a handler's parameters, status codes or response struct in `service/internal/api/*.go`, changes a JSON-tagged struct returned by a handler (`internal/db`, `internal/startup.DBStatus`, `internal/template/types.go`, `internal/defects`), or touches `openapi.yaml`; skip otherwise.
**Contents:** Patterns to FLAG: New handler or route without a spec entry; Modified handler whose contract is not reflected; Removed handlers still in the spec; `$ref` references that don't resolve; Go struct JSON tags out of sync with schemas · Do NOT flag · Review approach

Review code changes to **keep `openapi.yaml` (repo root) in sync with the API**. Drift between spec and handlers is a contract bug for the consumers (lets-roll and other clients), not a doc nit. **Severity:** P2 for drift introduced or touched by this PR; P3 for pre-existing drift in routes the PR does not touch (note it, do not block). Spec-only edits to unrelated paths get no finding.

**Repo facts:** routes are registered in `NewRouter` (`service/internal/api/handlers.go`) under `r.Route("/api/pfsrd2", ...)`; the spec's `servers:` urls already end in `/api/pfsrd2`, so `paths:` keys omit that prefix. `cmd/local` and `cmd/lambda` both call `api.NewRouter`, so there are no dev-only routes. There is no spec lint or codegen in CI. Today (measured) these registered routes have no `paths:` entry: `/search/facets`, `/search/traits`, `/skills`, `/entries/{gameID}/eligible`, `/entries/{itemGameID}/apply/{effectGameID}` (GET and POST), `/defects` (POST). All 17 `$ref`s resolve.

**Patterns to FLAG:**

1. **New handler or route without a spec entry:** a `r.Get`/`r.Post`/`r.Put`/`r.Delete`/`r.Mount`/`r.Route` added in `NewRouter` with no matching `paths:` entry (path, method, parameters, request body, responses). A stub the author can flesh out still counts as an entry.
2. **Modified handler whose contract is not reflected:** observable changes — a new query param (e.g. a new `r.URL.Query().Get("x")` or `r.URL.Query()["x"]`), a new path param, changed response shape, new status code (`jsonError(w, ..., 4xx)`), renamed field, changed `omitempty`/required status — need the matching `in: query`/`in: path` parameter, schema or response in the spec.
3. **Removed handlers still in the spec:** a route removed in the diff must lose its `paths:` entry. Dead documented paths are worse than missing ones.
4. **`$ref` references that don't resolve:** every `$ref: '#/components/...'` added or touched must point at a schema, response or parameter that exists under `components:`.
5. **Go JSON tags out of sync with schemas:** response schemas in `components/schemas` (`Entry`, `EntryVersion`, `SearchResult`, `TypeCount`, `Source`, `Suggestion`, `UnifiedSuggestion`, `DBStatus`, the template types) must match the structs handlers return — names via `json:` tags, types, optionality (`omitempty`, pointer fields). Flag drift in either direction.

**Do NOT flag:**
- Description/example text-only edits to the spec.
- New internal Go types never returned from a handler.
- The missing entries listed under Repo facts, unless the PR touches those routes or handlers (then P2).
- Path-parameter name differences between router and spec (`{itemGameID}` vs `{gameID}`) when the position and meaning match.
- Responses that proxy S3 JSON verbatim (`/{type}/{schemaVersion}/{book}/{filename}`, `/entries/{gameID}/full`): their body is the stored document, not a Go struct.

**Review approach:**

1. List routes added, modified or removed in the diff (`git diff` on `NewRouter` and the handler it points to).
2. Locate each in `openapi.yaml` (`grep -n '^  /' openapi.yaml`); quote any mismatch.
3. For each modified handler, compare its query/path params, status codes and returned struct tags against the spec.
4. Walk every new or modified `$ref` and confirm the target exists.
5. A PR that adds a handler with no spec change at all is a P2, quoting the missing path.
