#!/usr/bin/env bash
# coverage.sh — Reproduce the Codecov total of the root module locally.
#
# Usage: scripts/coverage.sh [--no-run] [--packages]
#
#   --no-run    reuse coverage.out, coverage-ts.out and coverage-chunking.out
#               from an earlier run (or from a CI artifact) instead of testing
#   --packages  also print the per-package table (lowest coverage first)
#
# It runs the same three test passes as .github/workflows/ci.yml:
#   1. default tags, every package except tests/benchmarks  -> coverage.out
#   2. -tags treesitter: internal/graph/..., internal/graphstore/...,
#      internal/indexing/...                                -> coverage-ts.out
#   3. -tags treesitter: modules/chunking/...               -> coverage-chunking.out
# (2 and 3 need CGo and a C compiler; they are skipped with a warning when
# CGO is unavailable, which lowers the total of those packages.)
#
# The profiles are merged the way Codecov merges uploads: a statement block
# is covered when any profile covers it. The paths ignored in codecov.yml
# (IGNORE_RE below; keep the two in sync) are dropped before the total.
# The figure is statement-based like `go tool cover`; Codecov reports lines,
# so its number can differ by a few tenths of a percent.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MODULE="$(go list -m)"
# Mirrors the ignore: list of codecov.yml.
IGNORE_RE="^${MODULE}/(tests/|internal/graph/analysisgolden/|internal/testutil/)"

run=1
per_package=0
for arg in "$@"; do
	case "$arg" in
	--no-run) run=0 ;;
	--packages) per_package=1 ;;
	-h | --help)
		sed -n '2,22p' "$0"
		exit 0
		;;
	*)
		echo "unknown argument: $arg" >&2
		exit 2
		;;
	esac
done

profiles=(coverage.out)
if [ "$run" -eq 1 ]; then
	# Tests must not see the developer's ~/.gleann. Pin the Go caches first so
	# the temporary HOME does not force a cold build.
	GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOPATH="$(go env GOPATH)"
	export GOCACHE GOMODCACHE GOPATH
	test_home="$(mktemp -d)"
	trap 'rm -rf "$test_home"' EXIT

	echo "==> default tags (all packages)" >&2
	# shellcheck disable=SC2046
	HOME="$test_home" GLEANN_TEST_MODE=true go test -count=1 -timeout 15m \
		-coverprofile=coverage.out $(go list ./... | grep -v /tests/benchmarks) >&2

	if [ "$(go env CGO_ENABLED)" = "1" ] && command -v "$(go env CC)" >/dev/null 2>&1; then
		echo "==> -tags treesitter (graph, graph store, indexing)" >&2
		HOME="$test_home" GLEANN_TEST_MODE=true CGO_ENABLED=1 go test -count=1 -tags treesitter -timeout 15m \
			-coverprofile=coverage-ts.out ./internal/graph/... ./internal/graphstore/... ./internal/indexing/... >&2
		echo "==> -tags treesitter (modules/chunking)" >&2
		HOME="$test_home" GLEANN_TEST_MODE=true CGO_ENABLED=1 go test -count=1 -tags treesitter -timeout 15m \
			-coverprofile=coverage-chunking.out ./modules/chunking/... >&2
	else
		echo "warning: no CGo toolchain; skipping the treesitter passes" >&2
		rm -f coverage-ts.out coverage-chunking.out
	fi
fi
for p in coverage-ts.out coverage-chunking.out; do
	[ -f "$p" ] && profiles+=("$p")
done
if [ ! -f coverage.out ]; then
	echo "coverage.out not found (run without --no-run first)" >&2
	exit 1
fi

echo "==> merging: ${profiles[*]}" >&2
# Profile lines: <file>:<start>,<end> <statements> <count>
awk -v ignore="$IGNORE_RE" -v per_package="$per_package" -v module="$MODULE/" '
FNR == 1 && /^mode:/ { next }
{
	split($1, loc, ":")
	if (loc[1] ~ ignore) next
	stmts[$1] = $2
	if ($3 > 0) hit[$1] = 1
	pkg = loc[1]; sub(/\/[^\/]*$/, "", pkg)
	pkg_of[$1] = pkg
}
END {
	for (b in stmts) {
		pt[pkg_of[b]] += stmts[b]; total += stmts[b]
		if (b in hit) { pc[pkg_of[b]] += stmts[b]; covered += stmts[b] }
	}
	if (per_package) {
		for (p in pt) {
			name = p; sub(module, "", name)
			printf "%6.1f%%  %6d/%-6d  %s\n", 100 * pc[p] / pt[p], pc[p], pt[p], name | "sort -n"
		}
		close("sort -n")
	}
	if (total == 0) { print "no statements"; exit 1 }
	printf "total: %d/%d statements, %.1f%%\n", covered, total, 100 * covered / total
}' "${profiles[@]}"
