#!/usr/bin/env bash
# Builds FAISS (static, with the C API) for the optional `-tags faiss` build
# and installs it where the Makefile looks for it: deps/faiss_install.
#
#   scripts/build-faiss.sh              # into <repo>/deps/faiss_install
#   scripts/build-faiss.sh /some/prefix # elsewhere (set FAISS_LIB_DIR / FAISS_INC_DIR)
#
# Needs: git, cmake, a C++ compiler, OpenBLAS and OpenMP development files
# (Debian/Ubuntu: cmake g++ libopenblas-dev libomp-dev). deps/ is not tracked
# in git; the release workflow builds its own FAISS.
set -euo pipefail

FAISS_VERSION="${FAISS_VERSION:-v1.13.2}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFIX="${1:-$REPO_ROOT/deps/faiss_install}"
JOBS="${JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)}"

for tool in git cmake c++; do
  command -v "$tool" >/dev/null 2>&1 || { echo "build-faiss: $tool not found" >&2; exit 1; }
done

SRC="$(mktemp -d)"
trap 'rm -rf "$SRC"' EXIT

echo "build-faiss: FAISS $FAISS_VERSION -> $PREFIX ($JOBS jobs)"
git clone --quiet --depth 1 --branch "$FAISS_VERSION" https://github.com/facebookresearch/faiss.git "$SRC/faiss"

cmake -S "$SRC/faiss" -B "$SRC/build" \
  -DFAISS_ENABLE_C_API=ON \
  -DFAISS_ENABLE_GPU=OFF \
  -DFAISS_ENABLE_PYTHON=OFF \
  -DBUILD_TESTING=OFF \
  -DBUILD_SHARED_LIBS=OFF \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$PREFIX" >/dev/null
cmake --build "$SRC/build" -j"$JOBS" --target faiss faiss_c
cmake --install "$SRC/build" >/dev/null

# The C API library and its headers are not part of FAISS's install step.
mkdir -p "$PREFIX/lib" "$PREFIX/include/faiss/c_api/impl"
cp "$SRC/build/c_api/libfaiss_c.a" "$PREFIX/lib/"
cp "$SRC/faiss/c_api/"*.h "$PREFIX/include/faiss/c_api/"
cp "$SRC/faiss/c_api/impl/"*.h "$PREFIX/include/faiss/c_api/impl/"

echo "build-faiss: done. Build gleann with: make full"
