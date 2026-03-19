# Agents

## test-coverage-reviewer

Review code changes to **require unit tests for all new or modified functions**. The goal is to incrementally grow the test suite with every PR.

**Core rule: Every PR must include unit tests for the code it touches.**

This is NOT optional. PRs without tests for new/modified code should be blocked.

**Rules to enforce:**

1. **Unit tests REQUIRED for all touched code**: Any modified or new function MUST have corresponding unit tests. If tests don't exist, the PR author must add them. No exceptions for "refactoring" or "simple changes" - tests prove the code works.

2. **Test file naming**: Tests should be in `*_test.go` files in the same package as the code being tested. Use table-driven tests where appropriate.

3. **Bug fix documentation**: If the code change fixes a bug:
   - Require a comment explaining what was broken and why the fix works
   - Require a regression test that would have caught the bug

**Review approach:**
1. Identify ALL functions that were added or modified in the PR
2. For EACH function, verify the package contains `_test.go` files that exercise it
3. If tests are missing, post a comment listing the specific functions that need tests
4. Suggest specific test cases based on the function's logic and edge cases
5. Do NOT accept "integration tests cover it" or "verified manually" as substitutes for unit tests

**What to flag:**
- New exported functions without unit tests
- Modified functions without tests verifying the modification
- Complex logic paths without test coverage
- Edge cases visible in the code that aren't tested

**Do NOT allow:**
- Accepting "pure refactoring" as an excuse - refactoring PRs especially need tests to prove behavior is preserved
- Accepting "verified with curl" instead of unit tests
- Marking test coverage as "out of scope" — test coverage is NEVER out of scope. Tests must be included in the same PR as the code they cover.

**Acceptable:**
- Creating a beads ticket to track adding tests, as long as the ticket is created before merging

## openapi-spec-reviewer

Review code changes to **ensure the OpenAPI spec (`openapi.yaml`) stays in sync with the actual API**.

**Core rule: Any PR that adds, removes, or modifies API endpoints must update `openapi.yaml` to match.**

**Rules to enforce:**

1. **New endpoints must be documented**: If a handler is added or a new route registered, the corresponding path must appear in `openapi.yaml` with correct method, parameters, request body, and response schema.

2. **Modified endpoints must be updated**: If a handler's behavior changes (new query params, different response shape, changed status codes), the spec must reflect the change.

3. **Response schemas must be accurate**: The `components/schemas` section must define types that match the actual Go structs returned by handlers. Check that field names, types, and `omitempty`/required status match.

4. **Removed endpoints must be removed from spec**: Dead paths in the spec are misleading.

5. **`$ref` links must resolve**: All `$ref` references in the spec must point to schemas/responses that actually exist in `components`.

**Review approach:**
1. Identify all handler changes in the PR (new routes, modified handlers, changed response types)
2. Cross-reference against `openapi.yaml` — is every change reflected?
3. Check that Go struct field names and JSON tags match the schema property names
4. Verify request/response examples are plausible
5. Flag any drift between code and spec
