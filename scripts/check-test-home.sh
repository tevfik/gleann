#!/usr/bin/env bash
# Runs the test suite with HOME (and USERPROFILE) pointed at an empty
# directory and fails if any file appears in it: a test that writes into the
# user's home (~/.gleann, ~/.config, caches) pollutes real data and slows later
# runs. Go's own telemetry under $HOME/.config/go is the only path allowed.
#
# usage: scripts/check-test-home.sh [go test arguments / packages ...]
# default: every package except /tests/benchmarks, with -short, no race.
set -u
cd "$(dirname "$0")/.." || exit 1

# Toolchain locations that default to paths under HOME: pin them to the real
# ones first so the build and module caches are reused (and stay out of HOME).
GOCACHE=$(go env GOCACHE); GOMODCACHE=$(go env GOMODCACHE); GOPATH=$(go env GOPATH)
export GOCACHE GOMODCACHE GOPATH GOFLAGS="${GOFLAGS:-} -modcacherw"
# Absolute GOTOOLCHAIN downloads live in the module cache; keep auto-switching.
H=$(mktemp -d "${TMPDIR:-/tmp}/gleann-test-home-XXXXXX") || exit 1
trap 'rm -rf "$H"' EXIT

if [ $# -eq 0 ]; then
  mapfile -t pkgs < <(go list ./... | grep -v /tests/benchmarks)
  set -- -short "${pkgs[@]}"
fi

env -u XDG_CONFIG_HOME -u XDG_CONFIG_DIRS -u XDG_DATA_HOME -u XDG_DATA_DIRS \
    -u XDG_CACHE_HOME -u XDG_STATE_HOME -u XDG_RUNTIME_DIR \
    HOME="$H" USERPROFILE="$H" \
  go test -count=1 -timeout 20m "$@"
rc=$?

# Anything but Go's telemetry directory counts. (The directory entries
# themselves are listed too, so an empty ~/.gleann is caught.)
left=$(find "$H" -mindepth 1 -not -path "$H/.config" -not -path "$H/.config/go" -not -path "$H/.config/go/*")
if [ -n "$left" ]; then
  echo "FAIL: the tests wrote into the empty HOME ($H):" >&2
  echo "$left" | sed "s#^$H/##" | sed -E 's#[0-9a-f]{16,}.*#<hash>#' | sort | uniq -c | sort -rn | head -20 >&2
  exit 1
fi
if [ $rc -ne 0 ]; then
  echo "FAIL: go test exited with $rc" >&2
  exit $rc
fi
echo "OK: the empty HOME stayed empty"
