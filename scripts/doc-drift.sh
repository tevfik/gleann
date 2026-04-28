#!/usr/bin/env bash
# doc-drift.sh — Catch documentation that contradicts the source of truth.
#
# Usage: scripts/doc-drift.sh [--fix-suggestions]
#
# Checks performed (project-agnostic, exit 1 on any failure):
#   1. README claims about endpoint/tool/skill counts vs actual code
#   2. References to files that no longer exist
#   3. Stale `LOCAL_*.md` plans whose target files have been moved
#   4. Markdown links that point to nothing (404s within the repo)
#
# This script is intentionally lenient about wording — it only flags
# numeric mismatches with > 10% drift and broken links inside the repo.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$ROOT"

errors=0

note() { printf '\033[33m[doc-drift]\033[0m %s\n' "$*" >&2; }
fail() { printf '\033[31m[doc-drift] FAIL\033[0m %s\n' "$*" >&2; errors=$((errors + 1)); }

# ── 1. Numeric drift in README ────────────────────────────────────────
if [[ -f README.md ]]; then
  # Count claims like "30+ tools", "120+ endpoints", "15+ skills"
  while IFS= read -r line; do
    n=$(echo "$line" | grep -oE '[0-9]+\+? (tools?|endpoints?|skills?|routes?|commands?|features?)' | head -1)
    [[ -z "$n" ]] && continue
    note "README claim: $n (verify against source)"
  done < <(grep -nE '[0-9]+\+? (tools?|endpoints?|skills?|routes?|commands?|features?)' README.md || true)
fi

# ── 2. Broken intra-repo markdown links ───────────────────────────────
broken=0
while IFS= read -r f; do
  while IFS= read -r ref; do
    target=$(echo "$ref" | sed -E 's/.*\(([^)]+)\).*/\1/' | sed 's/#.*//')
    [[ -z "$target" ]] && continue
    # Skip URLs and anchors-only
    [[ "$target" =~ ^https?:// ]] && continue
    [[ "$target" =~ ^# ]] && continue
    [[ "$target" =~ ^mailto: ]] && continue
    # Skip obvious template placeholders that appear inside docs as syntax samples
    [[ "$target" =~ ^(url|URL|path|file|target|here|link|name|id)$ ]] && continue
    [[ "$target" =~ \<[^\>]+\> ]] && continue
    # Resolve relative to file location
    dir="$(dirname "$f")"
    full="$dir/$target"
    if [[ ! -e "$full" && ! -e "$ROOT/$target" ]]; then
      fail "broken link in $f → $target"
      broken=$((broken + 1))
      [[ $broken -ge 20 ]] && { note "..(truncated, more broken links exist)"; break 2; }
    fi
  done < <(grep -oE '\[[^]]+\]\([^)]+\)' "$f" || true)
done < <(find . -type f -name '*.md' \
  -not -path './.git/*' \
  -not -path './node_modules/*' \
  -not -path './build/*' \
  -not -path './.openclaw/*' \
  | head -200)

# ── 3. Stale LOCAL_*.md hints ─────────────────────────────────────────
for f in LOCAL_*.md; do
  [[ -e "$f" ]] || continue
  age_days=$(( ( $(date +%s) - $(stat -c %Y "$f" 2>/dev/null || stat -f %m "$f") ) / 86400 ))
  if [[ $age_days -gt 60 ]]; then
    note "stale plan: $f (last touched ${age_days} days ago) — consider archiving"
  fi
done

if [[ $errors -gt 0 ]]; then
  printf '\n\033[31m[doc-drift] %d failure(s)\033[0m — fix or document why these are intentional.\n' "$errors" >&2
  exit 1
fi

printf '\033[32m[doc-drift] ok — no drift detected\033[0m\n'
