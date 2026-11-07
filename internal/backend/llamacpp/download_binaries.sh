#!/bin/bash
set -euo pipefail

# Downloads llama-server and its shared libraries into bin/<os>-<arch>/, where
# the embed_<os>_<arch>.go files embed them (each build embeds only its own
# platform). Run once from this directory when bumping LLAMA_VERSION.
#
# Since b5xxx the release is a tar.gz with versioned, symlinked sonames
# (libllama.so.0 -> libllama.so.0.9.0) and the CPU backend split into
# per-microarchitecture plugins (libggml-cpu-<arch>.so) that ggml loads from
# the executable's directory at run time. go:embed cannot store symlinks, so
# each soname is copied as a regular file under the name the loader asks for.
#
# Runtime requirements on the host: glibc, libstdc++, libgomp.so.1 and
# OpenSSL 3 (libssl.so.3, libcrypto.so.3).

LLAMA_VERSION="b12176"
BASE="https://github.com/ggml-org/llama.cpp/releases/download/${LLAMA_VERSION}"

# platform | release asset suffix | sha256 | CPU backends to ship
# ggml picks the best CPU backend the machine supports; the first one listed
# is the baseline that runs everywhere.
PLATFORMS=(
	"linux-amd64|ubuntu-x64|d19cad855c437851b0556c67f08e7ae398f064524ba9e81ed8a9316ffb4218c4|x64 sse42 haswell alderlake skylakex icelake zen4"
	"linux-arm64|ubuntu-arm64|2aa4437cb9ede7408be5db92538471e2c23aff1cfe09ebea204ae6c752136b52|armv8.0_1 armv8.2_1 armv8.2_2 armv8.2_3 armv8.6_1 armv8.6_2 armv9.2_1 armv9.2_2"
)

cd "$(dirname "$0")"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for p in "${PLATFORMS[@]}"; do
	IFS='|' read -r plat asset sum variants <<<"$p"
	echo "Downloading llama.cpp ${LLAMA_VERSION} (${plat})..."
	curl -fsSL -o "$tmp/$plat.tar.gz" "${BASE}/llama-${LLAMA_VERSION}-bin-${asset}.tar.gz"
	echo "${sum}  $tmp/$plat.tar.gz" | sha256sum -c -
	mkdir -p "$tmp/$plat"
	tar -xzf "$tmp/$plat.tar.gz" -C "$tmp/$plat"
	src="$tmp/$plat/llama-${LLAMA_VERSION}"

	dst="bin/$plat"
	rm -rf "${dst:?}"
	mkdir -p "$dst"
	cp "$src/llama-server" "$dst/llama-server-$plat"
	for f in libllama-server-impl.so libllama-common.so.0 libllama.so.0 libmtmd.so.0 libggml.so.0 libggml-base.so.0; do
		cp -L "$src/$f" "$dst/$f"
	done
	for v in $variants; do
		cp -L "$src/libggml-cpu-$v.so" "$dst/"
	done
	echo "$LLAMA_VERSION" >"$dst/VERSION"
	chmod 0755 "$dst"/*
	echo "  $(du -sh "$dst" | cut -f1) in $dst ($(ls "$dst" | wc -l) files)"
done
