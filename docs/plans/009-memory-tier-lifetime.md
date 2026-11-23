# RFC 009: Memory tier lifetime — short/medium TTL, long-tier permanence

Status: **draft** (2026-11-22). The long-tier non-expiry guarantee (section 4)
is implemented and test-backed in the `mem-validity-decay` lane; the
short/medium lifetime policy (sections 2-3, 5) is **not implemented yet**.
Decision basis: maintainer, 2026-11-21 12:02 — "the long tier must not expire
by age but must stay changeable; the short tier must expire, with a lifetime
that may come from the fact itself, a user setting, or a fixed default"
(`gleann-review/progress/ROADMAP-PROGRESS.md`, row `mem-validity-decay`).
Builds on: `pkg/memory` (`block.go`, `validity.go`, `manager.go`, `store.go`),
`docs/memory_engine.md` "Memory Compaction & Bayesian Pruning".
Release: not assigned. No SQLite schema migration, no index format change,
no renamed or removed REST route, MCP tool, CLI flag or config key.

## 1. Problem & Motivation

Before the `mem-validity-decay` fix, `Block.ValidityScore()` applied a 90-day
half-life decay to **all** tiers and `IsStale()` marked `TierLong` stale after
180 days. An unconfirmed long-term fact crossed `filterValid`'s 0.2 threshold
after ~119 days, disappeared from `BuildContext()`/`BuildScopedContext()` and
was permanently deleted by `Manager.Compact()` / `gleann memory compact`.
`RecordAccess` is never called by the retrieval surfaces, so neither access
recency nor the access boost could rescue it. Permanent knowledge (user
preferences, conventions) silently evaporated after ~4 months.

The fix exempts `TierLong` from age decay and age staleness. What remains
unspecified is the other half of the decision: **short-term notes must
expire**, and their lifetime (and optionally the medium tier's) should be
resolvable from the fact itself, a user setting, or a fixed default. Today:

- `Manager.ShortTermTTL` (default `0` = session-only) stamps `ExpiresAt` on
  short blocks in `AddNote`/`AddScopedNote` (`manager.go:251-254, 277-280`);
  it is settable only in Go code — no config key, env var or CLI flag.
- The medium tier has no expiry at all: `RunMaintenance` promotes medium
  blocks older than `MediumTermMaxAge` (default 30 days) to long
  (`manager.go:521-533`), and age decay slowly drops their validity score.
- There is no per-fact TTL input on any write surface (CLI `memory add`,
  REST `POST /api/blocks`, MCP `memory_add`); only a raw `ExpiresAt` through
  the Go API / block JSON.
- `gleann memory compact` deletes immediately; there is no way to preview
  what would be pruned (`--dry-run` exists only for `memory reflect`).

## 2. Design: three-source TTL precedence

For a block written to tier `T` (short or medium), the effective expiry is
resolved once, at write time, in this order — first match wins:

1. **Per-fact explicit expiry/TTL.** An explicit `expires_at` (already a
   `Block` field, persisted in the `expires_at` column) always wins. A new
   optional per-write `ttl` input (CLI `gleann memory add --ttl 7d`, REST/MCP
   `ttl` field, Go `Manager` option) is converted to
   `ExpiresAt = now + ttl` when no explicit `expires_at` is given. `ttl` and
   `expires_at` together: `expires_at` wins; `ttl <= 0` is rejected.
2. **User setting.** Config keys `memory.short_ttl` / `memory.medium_ttl`
   with env overrides `GLEANN_MEMORY_SHORT_TTL` / `GLEANN_MEMORY_MEDIUM_TTL`
   (Go duration strings, e.g. `12h`, `7d` accepted via a small parser; `0`
   means "no expiry from this source"). Loaded by `NewManager` like the
   existing `GLEANN_BLOCK_CHAR_LIMIT` / `GLEANN_MEMORY_MAX_TOKENS` reads.
3. **Fixed tier default.** Short: **24h** (aligned with the short-tier
   `IsStale` threshold). Medium: **no hard expiry** (`0`); the medium
   lifetime ends in *promotion*, not deletion (section 3).

Resolution result is stamped into `ExpiresAt` at write time, so the existing
`IsExpired()` / `PruneExpired()` / `filterValid` machinery enforces it with
**zero schema change**. Blocks written before this RFC keep their stored
`ExpiresAt` (nil stays nil); defaults are never retroactively applied to
existing rows.

**Long tier exception:** no source may assign a default expiry to `TierLong`.
Only an explicit per-fact `expires_at` (source 1, caller intent) applies;
`ttl` inputs and tier settings are ignored for long writes. This keeps the
section 4 guarantee intact.

### 2.1 Backward compatibility of the short default

Today's fixed default is `0` (session-only: short notes live until
`EndSession`). The proposed 24h default is the only visible behavior change:
a short note older than 24h expires instead of waiting for promotion.
`EndSession` normally runs within a session, so the practical impact is
limited to long-lived processes that never end a session. Setting
`GLEANN_MEMORY_SHORT_TTL=0` (or `memory.short_ttl: 0`) restores the current
session-only behavior exactly. The maintainer may instead choose to keep `0`
as the shipped default and document 24h as the recommended setting; the
precedence machinery is identical either way. **Open question Q1.**

## 3. Promotion: `EndSession()` short → medium

`EndSession` promotes non-expired short blocks to medium
(`manager.go:483-498`). Under this RFC:

- An **explicit** `ExpiresAt` (source 1) survives promotion unchanged —
  caller intent is not silently rewritten by a tier change.
- A tier-default or user-setting expiry does **not** carry over: the promoted
  block's lifetime is re-resolved under the *medium* policy (default: no hard
  expiry; `RunMaintenance` archives it to long after `MediumTermMaxAge`).
  Implementation: record the expiry source in block metadata
  (`expiry_source: fact|setting|default`) at write time so promotion can
  distinguish caller intent from policy without a schema change.
- Expired short blocks are dropped, as today.

`RunMaintenance`'s medium → long promotion is unchanged; once promoted, a
block inherits the long-tier guarantee (section 4). A medium block with an
explicit or setting-based `ExpiresAt` is pruned at expiry instead of promoted
if the expiry comes first — expiry always beats promotion.

## 4. Long-tier guarantee (implemented in this lane)

`TierLong` never loses validity by age and is never stale by age:

- `ValidityScore()` (`pkg/memory/validity.go`): `decayFactor = 1.0` for
  `TierLong`; the Bayesian `Confirms`/`Conflicts` ratio and the access boost
  are unchanged. An unconfirmed, never-accessed long block keeps its 0.5
  prior at 130 days, 365 days, 10 years.
- `IsStale()`: `case TierLong: return false`.
- `IsExpired()` still honors an **explicitly set** `ExpiresAt` in every tier;
  `nil` never expires. No write path sets `ExpiresAt` for long blocks today.

The long tier remains fully **changeable**:

- explicit edit (`Store.Update`, block PATCH surfaces) and duplicate-add
  merge (`Store.Add` dedup, `Confirms++`),
- `Forget`/`Delete` by ID or content match,
- Bayesian confidence: heavy `Conflicts` still drop the score below 0.2, so
  `BuildContext` filters and `Compact` prunes a contradicted long fact — on
  conflict, never on age,
- `Suspect` marking when referenced code changes (rendered separately with a
  verify-against-code warning).

Tests: `TestValidityScore_LongTierNoAgeDecay`,
`TestLongTierSurvivesBuildContextAndCompact`, `TestLongTierRemainsChangeable`
(`pkg/memory`), plus `TestValidityScore_StaleDecay` moved to short/medium to
guard the kept decay.

## 5. `compact --dry-run` visibility

Proposed: `gleann memory compact --dry-run [--min-validity X]` lists the
blocks that *would* be pruned — ID, tier, label, score, reason
(`expired` / `low-validity`) — and the count, without deleting anything.
Same option on the existing REST compact endpoint (`POST /api/blocks/compact`,
optional `dry_run` body field) and MCP `memory_compact` tool, so all surfaces
share one `Manager.CompactPreview(minValidity)` implementation that returns
the prune set; `Compact` then deletes exactly that set. Dry-run and real run
must agree block-for-block when nothing changes in between (test invariant).
No new route or tool name; doccheck stays green by documenting the new field
and flag at implementation time.

## 6. Build tag & platform impact

Pure Go only (`pkg/memory`, `cmd/gleann`, `internal/server`, `internal/mcp`);
no CGo, no build-tag-gated code, no new stubs. Works identically on all six
supported platforms. No Python, no Docker.

## 7. Incremental & migration strategy

- **No SQLite migration.** `expires_at` already exists; TTLs resolve to it at
  write time. The `expiry_source` marker lives in the existing `metadata`
  JSON map.
- Existing blocks are untouched: nil `ExpiresAt` stays nil, stored scores and
  counters are not recomputed. Old binaries read new rows fine (the metadata
  key is inert to them).
- Config/env additions are additive; absent settings fall back to the fixed
  defaults. The only behavior change is the short-tier default (section 2.1,
  Q1), opt-out with `0`.

## 8. Test & verification matrix

| Area | Test | Kind |
|---|---|---|
| Long permanence, 130/365/1000/3650 days | `TestValidityScore_LongTierNoAgeDecay` | landed (this lane) |
| Long survives context + compact at 180 days | `TestLongTierSurvivesBuildContextAndCompact` | landed |
| Long mutable: edit/merge/conflict-prune/forget/explicit expiry | `TestLongTierRemainsChangeable` | landed |
| Short/medium keep age decay | `TestValidityScore_StaleDecay` (short/medium) | landed |
| Precedence: fact > setting > default, per tier | `TestTierLifetimePrecedence` (table-driven, all 3 sources x 2 tiers + long exception) | planned |
| Invalid/zero/negative TTL rejected; `expires_at` beats `ttl` | `TestTTLInputValidation` | planned |
| EndSession: explicit expiry survives, policy expiry re-resolved | `TestEndSessionPromotionLifetime` | planned |
| Expiry beats medium→long promotion | `TestMaintenanceExpiryBeforePromotion` | planned |
| Dry-run set == real prune set; nothing deleted in dry-run | `TestCompactDryRun` (CLI, REST, MCP) | planned |
| Env/config parsing (`GLEANN_MEMORY_*_TTL`) | `TestManagerLifetimeSettings` + docs/env-vars.md entry | planned |
| Doc consistency | `internal/doccheck` suite | gate |

## 9. Open questions

- **Q1:** ship the short default as 24h (decision wording suggests a fixed
  default lifetime) or keep `0` = session-only for strict backward
  compatibility and recommend 24h? Section 2.1.
- **Q2:** should a user-set *medium* TTL delete at expiry, or also convert to
  promotion-to-long? This draft deletes (explicit lifetime intent), keeping
  promotion as the default-only path.
- **Q3:** per-write `ttl` on `memory_remember` as well, or only on
  `memory add`? Remembered facts are long-tier by definition, where TTLs are
  ignored (section 2); this draft exposes `ttl` on note/add surfaces only.
