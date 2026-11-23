#!/bin/sh
# test-install.sh — Tests for scripts/install.sh checksum verification and error handling.
# Can be run locally and in CI: bash scripts/test-install.sh
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_SH="${SCRIPT_DIR}/install.sh"

TMPDIR_ROOT=$(mktemp -d)
trap 'rm -rf "${TMPDIR_ROOT}"' EXIT

PASS_COUNT=0
FAIL_COUNT=0

pass() {
    printf "  \033[1;32mPASS\033[0m: %s\n" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

fail() {
    printf "  \033[1;31mFAIL\033[0m: %s\n" "$1"
    if [ -n "${2:-}" ]; then
        printf "        %s\n" "$2"
    fi
    FAIL_COUNT=$((FAIL_COUNT + 1))
}

# Helper to detect platform the same way install.sh does
detect_os() {
    case "$(uname -s)" in
        Linux*)   echo "linux"   ;;
        Darwin*)  echo "darwin"  ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *)        echo "linux"   ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   echo "amd64"  ;;
        aarch64|arm64)  echo "arm64"  ;;
        *)              echo "amd64"  ;;
    esac
}

OS=$(detect_os)
ARCH=$(detect_arch)
VERSION="v9.9.9"
ASSET="gleann-${VERSION}-${OS}-${ARCH}.tar.gz"

# Compute sha256 helper
compute_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        echo "NO_SHA_TOOL"
    fi
}

setup_fixtures() {
    FIXTURE_DIR="${TMPDIR_ROOT}/fixtures"
    DOWNLOAD_DIR="${FIXTURE_DIR}/download/${VERSION}"
    rm -rf "${FIXTURE_DIR}"
    mkdir -p "${DOWNLOAD_DIR}"

    # Create dummy binary and archive
    BUILD_DIR="${TMPDIR_ROOT}/build"
    rm -rf "${BUILD_DIR}"
    mkdir -p "${BUILD_DIR}"
    cat <<'EOF' > "${BUILD_DIR}/gleann"
#!/bin/sh
echo "gleann test binary v9.9.9"
exit 0
EOF
    chmod +x "${BUILD_DIR}/gleann"

    tar -czf "${DOWNLOAD_DIR}/${ASSET}" -C "${BUILD_DIR}" gleann

    # Compute valid checksum
    VALID_SUM=$(compute_sha256 "${DOWNLOAD_DIR}/${ASSET}")
    printf "%s  %s\n" "${VALID_SUM}" "${ASSET}" > "${DOWNLOAD_DIR}/SHA256SUMS"
}

printf "\n\033[1mRunning install.sh checksum and validation tests...\033[0m\n\n"

# ── Test 1: Valid download and checksum verification ─────────────────────────
setup_fixtures
INSTALL_DIR="${TMPDIR_ROOT}/install_valid"
OUTPUT="${TMPDIR_ROOT}/out_valid.txt"

if GLEANN_VERSION="${VERSION}" \
   GLEANN_INSTALL_DIR="${INSTALL_DIR}" \
   GLEANN_RELEASES_URL="file://${FIXTURE_DIR}" \
   HOME="${TMPDIR_ROOT}/home" \
   sh "${INSTALL_SH}" > "${OUTPUT}" 2>&1; then
    if [ -x "${INSTALL_DIR}/gleann" ] && grep -q "Checksum verified" "${OUTPUT}"; then
        pass "Valid archive verified against SHA256SUMS and installed successfully"
    else
        fail "Valid archive succeeded but verification output or binary was missing" "$(cat "${OUTPUT}")"
    fi
else
    fail "Valid archive installation failed unexpectedly" "$(cat "${OUTPUT}")"
fi

# ── Test 2: Tampered archive fails verification ──────────────────────────────
setup_fixtures
INSTALL_DIR="${TMPDIR_ROOT}/install_tampered"
OUTPUT="${TMPDIR_ROOT}/out_tampered.txt"

# Tamper with the archive (corrupt bytes)
printf "CORRUPTED_BYTES" >> "${DOWNLOAD_DIR}/${ASSET}"

if GLEANN_VERSION="${VERSION}" \
   GLEANN_INSTALL_DIR="${INSTALL_DIR}" \
   GLEANN_RELEASES_URL="file://${FIXTURE_DIR}" \
   HOME="${TMPDIR_ROOT}/home" \
   sh "${INSTALL_SH}" > "${OUTPUT}" 2>&1; then
    fail "Tampered archive should have failed verification but install.sh exited 0" "$(cat "${OUTPUT}")"
else
    if grep -q -i "checksum" "${OUTPUT}" && [ ! -f "${INSTALL_DIR}/gleann" ]; then
        pass "Tampered archive rejected with checksum failure; binary not installed"
    else
        fail "Tampered archive failed but did not mention checksum or left binary installed" "$(cat "${OUTPUT}")"
    fi
fi

# ── Test 3: Missing SHA256SUMS fails closed ──────────────────────────────────
setup_fixtures
INSTALL_DIR="${TMPDIR_ROOT}/install_missing_sums"
OUTPUT="${TMPDIR_ROOT}/out_missing_sums.txt"
rm -f "${DOWNLOAD_DIR}/SHA256SUMS"

if GLEANN_VERSION="${VERSION}" \
   GLEANN_INSTALL_DIR="${INSTALL_DIR}" \
   GLEANN_RELEASES_URL="file://${FIXTURE_DIR}" \
   HOME="${TMPDIR_ROOT}/home" \
   sh "${INSTALL_SH}" > "${OUTPUT}" 2>&1; then
    fail "Missing SHA256SUMS should have failed closed but install.sh exited 0" "$(cat "${OUTPUT}")"
else
    if [ ! -f "${INSTALL_DIR}/gleann" ]; then
        pass "Missing SHA256SUMS failed closed; binary not installed"
    else
        fail "Missing SHA256SUMS failed but binary was installed" "$(cat "${OUTPUT}")"
    fi
fi

# ── Test 4: Missing asset entry in SHA256SUMS fails closed ────────────────────
setup_fixtures
INSTALL_DIR="${TMPDIR_ROOT}/install_missing_entry"
OUTPUT="${TMPDIR_ROOT}/out_missing_entry.txt"
printf "1111111111111111111111111111111111111111111111111111111111111111  other-asset.tar.gz\n" > "${DOWNLOAD_DIR}/SHA256SUMS"

if GLEANN_VERSION="${VERSION}" \
   GLEANN_INSTALL_DIR="${INSTALL_DIR}" \
   GLEANN_RELEASES_URL="file://${FIXTURE_DIR}" \
   HOME="${TMPDIR_ROOT}/home" \
   sh "${INSTALL_SH}" > "${OUTPUT}" 2>&1; then
    fail "Missing entry in SHA256SUMS should have failed closed but install.sh exited 0" "$(cat "${OUTPUT}")"
else
    if grep -q -i "not found in SHA256SUMS" "${OUTPUT}" && [ ! -f "${INSTALL_DIR}/gleann" ]; then
        pass "Missing entry in SHA256SUMS failed closed; binary not installed"
    else
        fail "Missing entry in SHA256SUMS failed without clear message or installed binary" "$(cat "${OUTPUT}")"
    fi
fi

# ── Test 5: GLEANN_SKIP_CHECKSUM=1 allows installing tampered archive ─────────
setup_fixtures
INSTALL_DIR="${TMPDIR_ROOT}/install_skip"
OUTPUT="${TMPDIR_ROOT}/out_skip.txt"
printf "EXTRA_DATA" >> "${DOWNLOAD_DIR}/${ASSET}"
# Recreate tar with valid header plus extra data so tar still extracts
tar -czf "${DOWNLOAD_DIR}/${ASSET}" -C "${BUILD_DIR}" gleann
# Modify checksum in SHA256SUMS to differ
printf "0000000000000000000000000000000000000000000000000000000000000000  %s\n" "${ASSET}" > "${DOWNLOAD_DIR}/SHA256SUMS"

if GLEANN_VERSION="${VERSION}" \
   GLEANN_INSTALL_DIR="${INSTALL_DIR}" \
   GLEANN_RELEASES_URL="file://${FIXTURE_DIR}" \
   GLEANN_SKIP_CHECKSUM=1 \
   HOME="${TMPDIR_ROOT}/home" \
   sh "${INSTALL_SH}" > "${OUTPUT}" 2>&1; then
    if [ -x "${INSTALL_DIR}/gleann" ] && grep -q -i "Skipping checksum" "${OUTPUT}"; then
        pass "GLEANN_SKIP_CHECKSUM=1 successfully opted out and logged warning"
    else
        fail "GLEANN_SKIP_CHECKSUM=1 succeeded but binary or warning was missing" "$(cat "${OUTPUT}")"
    fi
else
    fail "GLEANN_SKIP_CHECKSUM=1 failed unexpectedly" "$(cat "${OUTPUT}")"
fi

# ── Test 6: armv7 architecture rejection ─────────────────────────────────────
# Test detect_arch rejects armv7 by running a subshell that mocks uname -m
OUTPUT="${TMPDIR_ROOT}/out_armv7.txt"
if (
    fail() {
        printf "%s\n" "$1" >&2
        exit 1
    }
    uname() {
        if [ "$1" = "-m" ]; then
            echo "armv7l"
        else
            command uname "$@"
        fi
    }
    # Run detect_arch from install.sh
    eval "$(sed -n '/detect_arch()/,/^}/p' "${INSTALL_SH}")"
    detect_arch
) > "${OUTPUT}" 2>&1; then
    fail "armv7 architecture was accepted; want rejection" "$(cat "${OUTPUT}")"
else
    if grep -q -i "Unsupported architecture.*armv7" "${OUTPUT}"; then
        pass "armv7 architecture cleanly rejected with descriptive error"
    else
        fail "armv7 failed but did not produce expected descriptive error" "$(cat "${OUTPUT}")"
    fi
fi

printf "\n\033[1mResults:\033[0m %d passed, %d failed\n\n" "${PASS_COUNT}" "${FAIL_COUNT}"

if [ "${FAIL_COUNT}" -gt 0 ]; then
    exit 1
fi
exit 0
