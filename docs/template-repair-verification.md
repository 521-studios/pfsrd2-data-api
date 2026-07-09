# Template & Family Apply Repair — Verification Report

**Date:** 2026-07-09 · **Scope:** every rules-carrying monster template and family (100 documents, both editions) · **Tracking:** beads epic `bd_521Studios-lxb8` (13 children, all closed with evidence) · **Audit artifact:** https://claude.ai/code/artifact/af2c9d64-98c8-4cfb-966f-8eb3957dc8de

## Why the previous claim was invalidated

The 2026-07-07 verification reported "657 clauses / 0 failures," yet hand-testing found
defects in every template tried. Four structural blind spots in the harness made that
green run meaningless (`bd_521Studios-rks1`):

1. **Inventory from output** — the clause inventory was built from parsed JSON, so
   instructions the parser never extracted were invisible (Experimental Cryptid's level
   bump simply wasn't a clause to check).
2. **Engine-mirrored asserts** — expected values copied the engine's own tables, so
   engine-vs-published divergence passed (highest-skill implemented as add-if-absent
   instead of raise-to-highest).
3. **Unproven assertion engine** — badge-array `remove_item` passed ten times per clause
   while no-oping; nothing ever demonstrated the asserts could fail.
4. **Display out of scope** — rendered output was never checked.

## The rebuilt harness

All four gaps are structurally closed (scratchpad `clausev` / `clausev2`):

- **Inventory derived from the published cached HTML** (freshness-verified against live
  AoN), with parser-parity predicates — an unextracted instruction is now a visible
  failure, not a silent omission. Change texts are carried untruncated (a 100-char cap
  had been hiding the Elite/Weak conditionals and trait-sentence tails).
- **Published-semantics text expectations**, independent of the engine
  (`text_expectations.py`): conditional level deltas resolved against the creature's
  starting level, required / optional ("optionally the mindless trait", "usually becomes
  unholy") / either-or ("either the amphibious or aquatic trait") trait grammar,
  magnitude checks, gate-aware skipping.
- **Known-bad self-test**: five deliberately broken fixtures must FAIL the asserts;
  runs before every sweep (`runner2.py --self-test`).
- **Render sweep**: every document applied to a probe creature in the real browser
  harness — 98/98 OK, zero render errors. Badge coverage is asserted in the clause
  sweep as well.
- **Level audit**: every doc whose text mentions level changes checked end-to-end
  (39 docs, 0 mismatches — this caught the werecreature paren-split loss, parser PR #139).

## Final results

**98 sweep documents · 1,337 clauses · 13,216 checks · 0 failures.** Each clause tested
against ≥10 qualifying same-edition creatures; shortfalls documented, never padded.

Annotated (non-silent) exceptions baked into the harness:

- **Rumored Cryptid c5 suppression** — clause 4 raises Stealth to the highest skill
  before clause 5's +1; hand-verified correct per published rules (11 → 13 → 14).
- **Legacy alignment known-gap** — "it becomes evil" is unencoded in 4 legacy families
  (`bd_521Studios-3shq`, below).
- **Movement merge no-ops** — adding a speed the creature already exceeds correctly
  leaves it unchanged (keep-the-faster, PR #50).

## Defects found by the rebuilt harness and fixed

| Defect | Fix |
|---|---|
| `size_increment` was an engine stub — Primeval Cryptid and every werecreature family never changed size | data-api #49 |
| Movement adds duplicated existing speeds (Athamaru swim 25 beside Stingray's swim 30) | data-api #50 (merge by type, keep the faster) |
| JSONPath name filters never matched apostrophe names (`heroes' feast` swaps silently no-oped) | data-api #51 |
| Change `<li>` kept full ability prose after extraction (Amphibious rendered abilities twice); first fix destroyed zombie's plain-link grants — caught in review, consumed-node tracking added | parser #140 |
| Phantom strike traits routed to creature badges instead of attacks (both editions) | parser #141 |
| Werecreature legacy level instruction lost to a sentence-split bug | parser #139 |

Alongside the repair, spell swaps moved fully server-side (data-api #51 + display #24):
selections carry `spell_swaps: [{from, replacement_game_id}]`, the engine validates
trait/rank/cantrip rules against the selection's structured constraint and builds the
replacement (usage markers preserved); the display applies selections live with
sequence/token/identity/freeze guards against response reordering, surfaces engine
rejections, and rehydrates deep links.

## Open items (tracked, non-blocking)

- `bd_521Studios-3shq` — legacy "becomes evil" alignment changes unencoded (4 families)
- `bd_521Studios-reog` — phantom touch grant + bestiary_3 force-damage conversion unencoded
- `bd_521Studios-rm60` — zombie's plain-link grants (Darkvision, Negative Healing) not structured
- `bd_521Studios-o9hy` — engine skips strikes lacking a traits array on trait adds
- `bd_521Studios-ea2f` — count-marker transposition on 8 same-rank-duplicate creatures
- `bd_521Studios-unrc` — spell index carries a mangled name ("Elemental Annihilation Wave to 2 rounds")
- `bd_521Studios-83bo` — harness template-apply failures are console-only
- `bd_521Studios-4tou` — assembled-creature endpoint (server-side stack application; renderer becomes stateless)

## Acceptance gate

The per-entry audit artifact (published AoN text beside our encoding, with per-doc
sweep + render status) is the final visual gate — Devon spot-checks; findings become
tickets or fixes. Nothing in this report is self-certified rendering: the render pass
ran in the real browser harness against staging-parity data.
