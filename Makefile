# Gleann Makefile
export PATH := $(HOME)/.local/go/bin:$(PATH)
ORIGINAL_HOME ?= $(HOME)
export PYTHONUSERBASE ?= $(ORIGINAL_HOME)/.local
# Usage:
#   make               — build pure Go binary (gleann)
#   make full          — build FAISS + tree-sitter + native + EIF binary (gleann-full)
#   make install       — install gleann to /usr/local/bin
#   make install-user  — install gleann to ~/.local/bin
#   make test          — run all tests
#   make test-faiss    — run FAISS backend tests
#   make release       — build all release artifacts into dist/
#   make clean         — remove built binaries

# ── Variables ──────────────────────────────────────────────────────────────
BUILD_DIR   := build
BINARY      := $(BUILD_DIR)/gleann
BINARY_FULL := $(BUILD_DIR)/gleann-full
CMD         := ./cmd/gleann
# Extra C flags of the arm64 cross builds. bitllm.c's dot-product kernels sit in OpenMP regions, where
# GCC does not apply the file's "+dotprod" target pragma, so a cross build for a CPU with the
# dot-product extension sets it here, e.g. ARM64_CFLAGS=-march=armv8.2-a+dotprod+fp16.
ARM64_CFLAGS ?=
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -s -w -X main.version=$(VERSION)
INSTALL_DIR ?= /usr/local/bin
REPO_ROOT   := $(CURDIR)
USER_BIN_DIR ?= $(HOME)/.local/bin
USER_LIB_DIR ?= $(HOME)/.local/lib

# v2 adds the Rust toolchain: the native tag links ext/gleann-core-rs, so the
# Docker build path needs cargo. Bumping the tag forces a rebuild instead of
# silently reusing an image without Rust.
BUILDER_IMAGE ?= gleann-builder:2

# FAISS shared lib locations
FAISS_LIB_DIR ?= $(REPO_ROOT)/deps/faiss_install/lib
FAISS_INC_DIR ?= $(REPO_ROOT)/deps/faiss_install/include

# The single tag set of every gleann-full build: FAISS vectors (faiss),
# tree-sitter ASTs (treesitter), the embedded Rust engine (native) and the EIF
# runtime (eif). Keep it in sync with FULL_TAGS in Taskfile.yml — the release
# workflow (.github/workflows/release.yml) builds with `task full`, while
# `make full` must produce the same binary.
FULL_TAGS ?= faiss treesitter native eif

# Platform detection
UNAME_S := $(shell uname -s)

# The Docker branch always produces a Linux ELF, even when the developer runs
# `make full` on macOS, so it must use the Linux spelling regardless of the
# host. Only the install layout, never /usr/local/lib or a per-user directory:
# a bundled build must resolve its libraries through $ORIGIN so a missing
# bundle fails loudly instead of silently loading a system FAISS.
# \$ORIGIN (not '$ORIGIN'): the value lands inside a double-quoted
# CGO_LDFLAGS assignment, and the linker would keep the quotes as part of
# the RUNPATH.
LINUX_RPATH_FLAGS := -Wl,-rpath,\$$ORIGIN -Wl,-rpath,\$$ORIGIN/../lib
LINUX_ZNOW_FLAG   := -Wl,-z,now

ifeq ($(UNAME_S),Darwin)
    RPATH_FLAGS := -Wl,-rpath,@loader_path -Wl,-rpath,@loader_path/../lib
    ZNOW_FLAG   := 
    SO_EXT      := dylib
    OMP_PREFIX  := $(shell brew --prefix libomp 2>/dev/null)
    OMP_CFLAGS  := $(if $(OMP_PREFIX),-I$(OMP_PREFIX)/include,)
    OMP_LDFLAGS := $(if $(OMP_PREFIX),-L$(OMP_PREFIX)/lib -lomp,)
    BLAS_LDFLAGS :=
    STDCXX_LDFLAGS := -lc++
else
    RPATH_FLAGS := $(LINUX_RPATH_FLAGS)
    ZNOW_FLAG   := $(LINUX_ZNOW_FLAG)
    SO_EXT      := so
    OMP_CFLAGS  :=
    OMP_LDFLAGS := -lgomp
    BLAS_LDFLAGS := -lopenblas
    STDCXX_LDFLAGS := -lstdc++
endif

# ── Default target ──────────────────────────────────────────────────────────
.PHONY: all build
all: $(BINARY)
build: $(BINARY)

# ── Web UI ──────────────────────────────────────────────────────────────────
.PHONY: build-web prepare-assets

build-web:
	@echo "🎨 Building Web UI..."
	@if command -v npm >/dev/null 2>&1; then \
		echo "Found npm locally. Building..."; \
		cd ui && npm install && npm run build; \
	elif command -v docker >/dev/null 2>&1; then \
		echo "npm not found. Building using Docker (node:20-alpine)..."; \
		docker run --rm -v $$(pwd):/app -w /app/ui node:20-alpine sh -c "(npm run build || (npm ci && npm run build)) && chown -R $$(id -u):$$(id -g) dist"; \
	fi
	@if [ ! -f ui/dist/index.html ]; then echo "❌ ui/dist/index.html missing! Build failed."; exit 1; fi

# prepare-assets copies ui/dist into internal/server/dist, which go:embed
# reads. Both the UI build and the copy mutate tracked files, so neither may
# sit on a recipe line containing $(MAKE): GNU Make executes such lines even
# under `make -n`, which used to delete internal/server/dist during a dry run.
# The dependency is resolved at parse time instead, and the recipe below
# contains no recursive make, so `make -n full` has no side effects.
ifneq ($(SKIP_WEB),1)
ifeq ($(wildcard ui/dist/index.html),)
WEB_ASSETS_DEP := build-web
endif
endif

prepare-assets: $(WEB_ASSETS_DEP)
	@if [ "$$SKIP_WEB" = "1" ]; then \
		echo "⚠️  SKIP_WEB=1: Creating minimal placeholder for API-only build..."; \
		mkdir -p internal/server/dist; \
		echo '<!DOCTYPE html><html><body><h1>API-Only Mode</h1><p>Web UI not included in this build.</p></body></html>' > internal/server/dist/index.html; \
	elif [ -d ui/dist ]; then \
		echo "📦 Copying web assets..."; \
		rm -rf internal/server/dist; \
		cp -r ui/dist internal/server/dist; \
	else \
		echo "❌ ui/dist was not built (build-web did not run)."; exit 1; \
	fi

# ── Pure Go binary ─────────────────────────────────────────────────────────
$(BINARY): prepare-assets
	@mkdir -p $(BUILD_DIR)
	@if command -v go >/dev/null 2>&1; then \
		go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD); \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm -v gleann-go-cache:/go/pkg/mod -v gleann-build-cache:/root/.cache/go-build -v $$(pwd):/app -w /app golang:1.25 sh -c "go build -buildvcs=false -ldflags '$(LDFLAGS)' -o $(BINARY) $(CMD) && chown -R $$(id -u):$$(id -g) /app/$(BUILD_DIR)"; \
	else \
		echo "❌ Neither 'go' nor 'docker' found!"; exit 1; \
	fi
	@echo "✅ Built $(BINARY)"

# ── Native / CGo builds ────────────────────────────────────────────────────
.PHONY: build-cgo
build-cgo: prepare-assets
	@mkdir -p $(BUILD_DIR)
	@if command -v go >/dev/null 2>&1; then \
		CGO_ENABLED=1 CGO_CFLAGS="-w" go build -tags "treesitter" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/gleann-cgo $(CMD); \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm -v gleann-go-cache:/go/pkg/mod -v gleann-build-cache:/root/.cache/go-build -v $$(pwd):/app -w /app golang:1.25 sh -c "CGO_ENABLED=1 go build -buildvcs=false -tags 'treesitter' -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/gleann-cgo $(CMD) && chown -R $$(id -u):$$(id -g) /app/$(BUILD_DIR)"; \
	fi
	@echo "✅ Built $(BUILD_DIR)/gleann-cgo (with CGo and tree-sitter)"

.PHONY: build-eif-lib
build-eif-lib:
	@if [ ! -f ext/bitllm.c/CMakeLists.txt ]; then \
		echo "📦 Initializing ext/bitllm.c submodule..."; \
		git submodule update --init --recursive ext/bitllm.c; \
	fi
	@if [ -f ext/bitllm.c/CMakeLists.txt ] && command -v cmake >/dev/null 2>&1; then \
		echo "🔧 Building bitllm.c static library..."; \
		mkdir -p ext/bitllm.c/build && \
		cmake -B ext/bitllm.c/build -S ext/bitllm.c -DCMAKE_BUILD_TYPE=Release && \
		cmake --build ext/bitllm.c/build --target bitllm_static; \
	elif ! command -v cmake >/dev/null 2>&1; then \
		echo "⚠️ Warning: cmake not found. Please install cmake to build bitllm.c."; \
	fi

.PHONY: build-eif
build-eif: build-eif-lib prepare-assets
	@mkdir -p $(BUILD_DIR)
	@if command -v go >/dev/null 2>&1; then \
		CGO_ENABLED=1 go build -tags "eif" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/gleann-eif $(CMD); \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm -v gleann-go-cache:/go/pkg/mod -v gleann-build-cache:/root/.cache/go-build -v $$(pwd):/app -w /app golang:1.25 sh -c "CGO_ENABLED=1 go build -buildvcs=false -tags 'eif' -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/gleann-eif $(CMD) && chown -R $$(id -u):$$(id -g) /app/$(BUILD_DIR)"; \
	fi
	@echo "✅ Built $(BUILD_DIR)/gleann-eif (with EIF-Runtime in-process BERT)"

# ── ARM64 Builds (Native / Cross-Compiler / Docker) ─────────────────────────
.PHONY: build-arm64
build-arm64: prepare-assets
	@mkdir -p $(BUILD_DIR)
	@echo "🔧 Building pure Go ARM64 binary..."
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/gleann-linux-arm64 $(CMD)
	@echo "✅ Built $(BUILD_DIR)/gleann-linux-arm64 (Pure Go arm64)"

.PHONY: build-eif-lib-arm64
build-eif-lib-arm64:
	@if [ ! -f ext/bitllm.c/CMakeLists.txt ]; then \
		echo "📦 Initializing ext/bitllm.c submodule..."; \
		git submodule update --init --recursive ext/bitllm.c; \
	fi
	@if [ -f ext/bitllm.c/CMakeLists.txt ] && command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then \
		echo "🔧 Cross-compiling bitllm.c static library for ARM64..."; \
		mkdir -p ext/bitllm.c/build-arm64 && \
		cmake -B ext/bitllm.c/build-arm64 -S ext/bitllm.c \
			-DCMAKE_SYSTEM_NAME=Linux \
			-DCMAKE_SYSTEM_PROCESSOR=aarch64 \
			-DCMAKE_C_COMPILER=aarch64-linux-gnu-gcc \
			-DCMAKE_BUILD_TYPE=Release \
			-DEIF_ENABLE_VULKAN=OFF \
			-DCMAKE_C_FLAGS="-fno-tree-vectorize -fmath-errno" && \
		cmake --build ext/bitllm.c/build-arm64 --target bitllm_static; \
	else \
		echo "⚠️ aarch64-linux-gnu-gcc not found. Run 'make docker-build-eif-arm64' for containerized ARM build."; \
		exit 1; \
	fi

.PHONY: build-eif-arm64
build-eif-arm64: build-eif-lib-arm64 prepare-assets
	@mkdir -p $(BUILD_DIR) ext/bitllm.c/build
	@cp ext/bitllm.c/build-arm64/libbitllm.a ext/bitllm.c/build/libbitllm.a
	@echo "🔧 Building gleann-eif for ARM64..."
	CC=aarch64-linux-gnu-gcc CXX=aarch64-linux-gnu-g++ \
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 \
	CGO_CFLAGS="-w -fno-tree-vectorize -fmath-errno" \
	CGO_CXXFLAGS="-w -fno-tree-vectorize -fmath-errno" \
	go build -tags "eif" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/gleann-eif-linux-arm64 $(CMD)
	@echo "✅ Built $(BUILD_DIR)/gleann-eif-linux-arm64 (Embedded EIF runtime arm64)"

.PHONY: docker-build-arm64
docker-build-arm64: prepare-assets
	@mkdir -p $(BUILD_DIR)
	@echo "🐳 Building pure Go ARM64 binary in Docker..."
	docker run --rm \
		-v gleann-go-cache:/go/pkg/mod \
		-v gleann-build-cache:/root/.cache/go-build \
		-v $$(pwd):/app -w /app golang:1.25 \
		sh -c "GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -buildvcs=false -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/gleann-linux-arm64 $(CMD) && chown -R $$(id -u):$$(id -g) /app/$(BUILD_DIR)"
	@echo "✅ Built $(BUILD_DIR)/gleann-linux-arm64 via Docker"

.PHONY: docker-builder-arm64
docker-builder-arm64:
	@if command -v docker >/dev/null 2>&1; then \
		if ! docker image inspect gleann-builder-arm64:latest >/dev/null 2>&1; then \
			echo "📦 Creating gleann-builder-arm64:latest (Debian Bookworm + aarch64 cross-compiler)..."; \
			printf 'FROM golang:1.25-bookworm\nRUN apt-get update && apt-get install -y --no-install-recommends cmake gcc-aarch64-linux-gnu g++-aarch64-linux-gnu libgomp1-arm64-cross patchelf && rm -rf /var/lib/apt/lists/*\n' | docker build -t gleann-builder-arm64:latest -; \
		fi; \
	fi

.PHONY: docker-build-eif-arm64
docker-build-eif-arm64: docker-builder-arm64 prepare-assets
	@mkdir -p $(BUILD_DIR)
	@echo "🐳 Building gleann-eif for ARM64 in Docker container..."
	docker run --rm \
		-v gleann-go-cache:/go/pkg/mod \
		-v gleann-build-cache:/root/.cache/go-build \
		-v $$(pwd):/app -w /app gleann-builder-arm64:latest \
		sh -c "set -e && \
			mkdir -p ext/bitllm.c/build-arm64 && \
			cmake -B ext/bitllm.c/build-arm64 -S ext/bitllm.c \
				-DCMAKE_SYSTEM_NAME=Linux \
				-DCMAKE_SYSTEM_PROCESSOR=aarch64 \
				-DCMAKE_C_COMPILER=aarch64-linux-gnu-gcc \
				-DCMAKE_BUILD_TYPE=Release \
				-DEIF_ENABLE_VULKAN=OFF \
				-DCMAKE_C_FLAGS='-fno-tree-vectorize -fmath-errno $(ARM64_CFLAGS)' && \
			cmake --build ext/bitllm.c/build-arm64 --target bitllm_static && \
			mkdir -p ext/bitllm.c/build && \
			cp ext/bitllm.c/build-arm64/libbitllm.a ext/bitllm.c/build/libbitllm.a && \
			CC=aarch64-linux-gnu-gcc CXX=aarch64-linux-gnu-g++ \
			GOOS=linux GOARCH=arm64 CGO_ENABLED=1 \
			CGO_CFLAGS='-w -fno-tree-vectorize -fmath-errno $(ARM64_CFLAGS)' \
			CGO_CXXFLAGS='-w -fno-tree-vectorize -fmath-errno $(ARM64_CFLAGS)' \
			go build -buildvcs=false -tags 'eif' -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/gleann-eif-linux-arm64 $(CMD) && \
			chown -R $$(id -u):$$(id -g) /app/$(BUILD_DIR) ext/bitllm.c/build"
	@echo "✅ Built $(BUILD_DIR)/gleann-eif-linux-arm64 via Docker"

.PHONY: build-rust-core
build-rust-core:
	@if command -v cargo >/dev/null 2>&1; then \
		echo "🔧 Building Rust core..."; \
		cd ext/gleann-core-rs && cargo build --release; \
	elif [ -f ext/gleann-core-rs/target/release/libgleann_core_rs.$(SO_EXT) ]; then \
		echo "✅ Using prebuilt ext/gleann-core-rs/target/release/libgleann_core_rs.$(SO_EXT) (cargo not found)"; \
	else \
		echo "❌ cargo not found and libgleann_core_rs.$(SO_EXT) is not built."; \
		echo "   FULL_TAGS ($(FULL_TAGS)) includes 'native', which links ext/gleann-core-rs."; \
		echo "   Install Rust: https://rustup.rs"; \
		exit 1; \
	fi

# ── Full Binary (Tree-sitter + FAISS CGo + native Rust + EIF-Runtime) ───────
# Both paths below build build/stage/gleann-full-bin with FULL_TAGS and bundle
# every library it links (libgleann_core_rs, libfaiss*, libbitllm, OpenMP/
# OpenBLAS/gfortran): RUNPATH is only $ORIGIN:$ORIGIN/../lib, so nothing may be
# left to a system /usr/local/lib.
#
# The path is chosen before any prerequisite runs. A host-only prerequisite
# such as build-rust-core would otherwise abort `make full` with "cargo not
# found" on a machine that only has Docker, so the container fallback (which
# installs Rust in $(BUILDER_IMAGE)) could never be reached. Force a path with
# `make full FULL_BUILD_MODE=docker`.
HAVE_GO     := $(shell command -v go >/dev/null 2>&1 && echo yes)
HAVE_DOCKER := $(shell command -v docker >/dev/null 2>&1 && echo yes)
HAVE_CARGO  := $(shell command -v cargo >/dev/null 2>&1 && echo yes)
NATIVE_CORE_LIB := ext/gleann-core-rs/target/release/libgleann_core_rs.$(SO_EXT)
HAVE_NATIVE_CORE := $(if $(HAVE_CARGO),yes,$(if $(wildcard $(NATIVE_CORE_LIB)),yes,))

# FULL_BUILD_MODE is an input: a value from the command line
# (`make full FULL_BUILD_MODE=docker`) or the environment wins, otherwise the
# probe above decides. The prerequisites follow the mode, so overriding it
# cannot leave a host-only prerequisite behind.
ifeq ($(origin FULL_BUILD_MODE),undefined)
    ifeq ($(HAVE_GO)/$(HAVE_NATIVE_CORE),yes/yes)
        FULL_BUILD_MODE := host
    else ifeq ($(HAVE_DOCKER),yes)
        FULL_BUILD_MODE := docker
    else
        # No usable host toolchain and no Docker: keep the host path so
        # build-rust-core can print its actionable "install Rust" guidance.
        FULL_BUILD_MODE := host
    endif
endif

ifeq ($(FULL_BUILD_MODE),docker)
    FULL_BUILD_DEPS := prepare-assets builder-image
else
    FULL_BUILD_DEPS := build-rust-core build-eif-lib prepare-assets
endif

.PHONY: builder-image
builder-image:
	@if command -v docker >/dev/null 2>&1; then \
		if ! docker image inspect $(BUILDER_IMAGE) >/dev/null 2>&1; then \
			echo "📦 Creating $(BUILDER_IMAGE) (one-time setup, eliminates apt-get on every build)..."; \
			printf 'FROM golang:1.25\nRUN apt-get update && apt-get install -y --no-install-recommends build-essential cmake curl ca-certificates libopenblas-dev libgomp1 pkg-config patchelf && rm -rf /var/lib/apt/lists/*\nRUN curl --proto "=https" --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal\nENV PATH=/root/.cargo/bin:$$PATH\n' | docker build -t $(BUILDER_IMAGE) -; \
		fi; \
	fi

.PHONY: full
full: $(BINARY_FULL)

ifeq ($(FULL_BUILD_MODE),docker)

# Container path: compiles the Rust core, the EIF static library and the Go
# binary inside $(BUILDER_IMAGE), so it also works from a macOS host (which
# cannot link a Mach-O libgleann_core_rs/libbitllm.a into a Linux ELF). The
# container always links Linux libraries, hence the literal .so check.
$(BINARY_FULL): $(FULL_BUILD_DEPS)
	@if [ ! -f "$(FAISS_LIB_DIR)/libfaiss.a" ] && [ ! -f "$(FAISS_LIB_DIR)/libfaiss.so" ]; then \
		echo "❌ FAISS not found in $(FAISS_LIB_DIR). Build it with: scripts/build-faiss.sh (see docs/faiss.md)"; exit 1; \
	fi
	@echo "🔧 Building $(BINARY_FULL) with FAISS + Tree-sitter CGo + native Rust + EIF Runtime in $(BUILDER_IMAGE) (standalone single executable)..."
	@docker run --rm \
		-v gleann-go-cache:/go/pkg/mod \
		-v gleann-build-cache:/root/.cache/go-build \
		-v $$(pwd):/app -w /app $(BUILDER_IMAGE) sh -c "\
			set -e; \
			if [ ! -f ext/bitllm.c/CMakeLists.txt ]; then \
				echo '📦 Initializing ext/bitllm.c submodule...'; \
				git submodule update --init --recursive ext/bitllm.c; \
			fi; \
			if [ ! -f ext/bitllm.c/CMakeLists.txt ]; then \
				echo '❌ ext/bitllm.c is not initialized; run git submodule update --init --recursive ext/bitllm.c'; \
				exit 1; \
			fi; \
			mkdir -p $(BUILD_DIR)/stage ext/bitllm.c/build; \
			cmake -B ext/bitllm.c/build -S ext/bitllm.c -DCMAKE_BUILD_TYPE=Release -DEIF_ENABLE_VULKAN=OFF; \
			cmake --build ext/bitllm.c/build --target bitllm_static; \
			cargo build --release --manifest-path ext/gleann-core-rs/Cargo.toml; \
			cp -L ext/gleann-core-rs/target/release/libgleann_core_rs.so $(BUILD_DIR)/stage/; \
			cp -L /app/deps/faiss_install/lib/libfaiss* $(BUILD_DIR)/stage/ 2>/dev/null || true; \
			CGO_ENABLED=1 \
			CGO_CFLAGS='-w -I/app/deps/faiss_install/include' \
			CGO_CXXFLAGS='-w -I/app/deps/faiss_install/include' \
			CGO_LDFLAGS='$(LINUX_RPATH_FLAGS) $(LINUX_ZNOW_FLAG) -L$(BUILD_DIR)/stage -L/app/deps/faiss_install/lib -lgleann_core_rs -lfaiss_c -lfaiss -lopenblas -lgomp -lstdc++ -lm' \
			go build -buildvcs=false -tags '$(FULL_TAGS)' -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/stage/gleann-full-bin $(CMD); \
			cp -L /usr/lib/x86_64-linux-gnu/libopenblas.so.0 $(BUILD_DIR)/stage/ 2>/dev/null || true; \
			cp -L /usr/lib/x86_64-linux-gnu/libgomp.so.1 $(BUILD_DIR)/stage/ 2>/dev/null || true; \
			cp -L /usr/lib/x86_64-linux-gnu/libgfortran.so.5 $(BUILD_DIR)/stage/ 2>/dev/null || true; \
			patchelf --set-rpath '\$$ORIGIN:\$$ORIGIN/../lib' $(BUILD_DIR)/stage/gleann-full-bin 2>/dev/null || true; \
			for so in $(BUILD_DIR)/stage/*.so*; do \
				[ -f \"\$$so\" ] && patchelf --set-rpath '\$$ORIGIN:\$$ORIGIN/../lib' \"\$$so\" 2>/dev/null || true; \
			done; \
			tar czf cmd/gleann-full-launcher/payload.tar.gz -C $(BUILD_DIR)/stage .; \
			CGO_ENABLED=0 go build -buildvcs=false -ldflags '$(LDFLAGS)' -o $(BINARY_FULL) ./cmd/gleann-full-launcher; \
			tar -czf cmd/gleann-full-launcher/payload.tar.gz --files-from /dev/null; \
			rm -rf $(BUILD_DIR)/stage; \
			chown -R $$(id -u):$$(id -g) /app/$(BUILD_DIR) cmd/gleann-full-launcher /app/internal/server/dist /app/ext/gleann-core-rs/target /app/ext/bitllm.c/build 2>/dev/null || true"
	@echo "✅ Built single standalone $(BINARY_FULL) with FAISS + Tree-sitter + native + EIF"

else

# Host path: platform-specific FAISS check, OpenMP runtime and run-path
# rewriting, mirroring Taskfile.yml so `make full` and `task full` behave the
# same on Linux and Darwin.
$(BINARY_FULL): $(FULL_BUILD_DEPS)
	@if [ ! -f "$(FAISS_LIB_DIR)/libfaiss.a" ] && [ ! -f "$(FAISS_LIB_DIR)/libfaiss.$(SO_EXT)" ]; then \
		echo "❌ FAISS not found in $(FAISS_LIB_DIR). Build it with: scripts/build-faiss.sh (see docs/faiss.md)"; exit 1; \
	fi
	@echo "🔧 Building $(BINARY_FULL) with FAISS + Tree-sitter CGo + native Rust + EIF Runtime (host toolchain, standalone single executable)..."
	@mkdir -p $(BUILD_DIR)/stage
	@if command -v go >/dev/null 2>&1; then \
		set -e; \
		cp -L $(NATIVE_CORE_LIB) $(BUILD_DIR)/stage/; \
		cp -L $(FAISS_LIB_DIR)/libfaiss* $(BUILD_DIR)/stage/ 2>/dev/null || true; \
		CGO_ENABLED=1 \
		CGO_CFLAGS="-w -I$(FAISS_INC_DIR)" \
		CGO_CXXFLAGS="-w -I$(FAISS_INC_DIR)" \
		CGO_LDFLAGS="$(RPATH_FLAGS) $(ZNOW_FLAG) -L$(BUILD_DIR)/stage -L$(FAISS_LIB_DIR) -lgleann_core_rs -lfaiss_c -lfaiss $(BLAS_LDFLAGS) $(OMP_LDFLAGS) $(STDCXX_LDFLAGS) -lm" \
		go build -tags "$(FULL_TAGS)" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/stage/gleann-full-bin $(CMD); \
		if [ "$(UNAME_S)" != "Darwin" ]; then \
			cp -L $(FAISS_LIB_DIR)/libopenblas.so.0 $(BUILD_DIR)/stage/ 2>/dev/null || cp -L /usr/lib/x86_64-linux-gnu/libopenblas.so.0 $(BUILD_DIR)/stage/ 2>/dev/null || true; \
			cp -L /usr/lib/x86_64-linux-gnu/libgomp.so.1 $(BUILD_DIR)/stage/ 2>/dev/null || true; \
			cp -L $(FAISS_LIB_DIR)/libgfortran.so.5 $(BUILD_DIR)/stage/ 2>/dev/null || cp -L /usr/lib/x86_64-linux-gnu/libgfortran.so.5 $(BUILD_DIR)/stage/ 2>/dev/null || true; \
			if command -v patchelf >/dev/null 2>&1; then \
				patchelf --set-rpath '$$ORIGIN:$$ORIGIN/../lib' $(BUILD_DIR)/stage/gleann-full-bin 2>/dev/null || true; \
				for so in $(BUILD_DIR)/stage/*.so*; do \
					if [ -f "$$so" ]; then \
						patchelf --set-rpath '$$ORIGIN:$$ORIGIN/../lib' "$$so" 2>/dev/null || true; \
					fi; \
				done; \
			fi; \
		fi; \
		if [ "$(UNAME_S)" = "Darwin" ]; then \
			if command -v install_name_tool >/dev/null 2>&1; then \
				install_name_tool -add_rpath @executable_path $(BUILD_DIR)/stage/gleann-full-bin 2>/dev/null || true; \
				install_name_tool -add_rpath @loader_path $(BUILD_DIR)/stage/gleann-full-bin 2>/dev/null || true; \
				install_name_tool -add_rpath @loader_path/../lib $(BUILD_DIR)/stage/gleann-full-bin 2>/dev/null || true; \
				for dylib in $(BUILD_DIR)/stage/*.dylib; do \
					if [ -f "$$dylib" ]; then \
						install_name_tool -add_rpath @executable_path "$$dylib" 2>/dev/null || true; \
						install_name_tool -add_rpath @loader_path "$$dylib" 2>/dev/null || true; \
						install_name_tool -add_rpath @loader_path/../lib "$$dylib" 2>/dev/null || true; \
					fi; \
				done; \
			fi; \
		fi; \
		tar czf cmd/gleann-full-launcher/payload.tar.gz -C $(BUILD_DIR)/stage . && \
		CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY_FULL) ./cmd/gleann-full-launcher && \
		tar -czf cmd/gleann-full-launcher/payload.tar.gz --files-from /dev/null && \
		rm -rf $(BUILD_DIR)/stage; \
	else \
		echo "❌ 'go' not found: install Go and Rust, or Docker, to build $(BINARY_FULL)."; exit 1; \
	fi
	@echo "✅ Built single standalone $(BINARY_FULL) with FAISS + Tree-sitter + native + EIF"

endif

# ── Install ─────────────────────────────────────────────────────────────────

# install-user — build full binary and install to ~/.local/bin/gleann
.PHONY: install-user
install-user: $(BINARY_FULL)
	@mkdir -p $(USER_BIN_DIR) $(USER_LIB_DIR)
	install -m 0755 $(BINARY_FULL) $(USER_BIN_DIR)/gleann
	@rm -f $(USER_BIN_DIR)/gleann-full 2>/dev/null || true
	@echo "✅ Installed gleann → $(USER_BIN_DIR)/gleann"
	@echo "   Make sure $(USER_BIN_DIR) is in your PATH."

# install-user-lite — build plain gleann (no CGo) and install to ~/.local/bin/gleann
.PHONY: install-user-lite
install-user-lite: $(BINARY)
	@mkdir -p $(USER_BIN_DIR)
	install -m 0755 $(BINARY) $(USER_BIN_DIR)/gleann
	@echo "✅ Installed gleann → $(USER_BIN_DIR)/gleann"

# install — install to /usr/local/bin (system-wide, needs sudo)
.PHONY: install
install: $(BINARY_FULL)
	install -m 0755 $(BINARY_FULL) $(INSTALL_DIR)/gleann
	@echo "✅ Installed gleann → $(INSTALL_DIR)/gleann"

# ── Tests ────────────────────────────────────────────────────────────────────
ORIGINAL_GOPATH ?= $(shell go env GOPATH 2>/dev/null || echo $(HOME)/go)

.PHONY: test
test:
	@if command -v go >/dev/null 2>&1; then \
		mkdir -p /tmp/gleann-test-home; \
		GLEANN_TEST_MODE=true GOPATH=$(ORIGINAL_GOPATH) HOME=/tmp/gleann-test-home go test -race -timeout 120s $$(go list ./... | grep -v /tests/benchmarks); \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm -v $$(pwd):/app -w /app golang:1.25-bookworm sh -c "go test -v -timeout 120s \$$(go list ./... | grep -v /tests/benchmarks)"; \
	fi

.PHONY: test-integration
test-integration:
	@if command -v go >/dev/null 2>&1; then \
		mkdir -p /tmp/gleann-test-home; \
		GLEANN_TEST_MODE=true GOPATH=$(ORIGINAL_GOPATH) HOME=/tmp/gleann-test-home go test -v -timeout 5m ./tests/integration/...; \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm --network host -v $$(pwd):/app -w /app golang:1.25-alpine sh -c "go test -v -timeout 5m ./tests/integration/..."; \
	fi

.PHONY: test-e2e
test-e2e:
	@if command -v go >/dev/null 2>&1; then \
		mkdir -p /tmp/gleann-test-home; \
		GLEANN_TEST_MODE=true GOPATH=$(ORIGINAL_GOPATH) HOME=/tmp/gleann-test-home go test -v -timeout 5m ./tests/e2e/...; \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm --network host -v $$(pwd):/app -w /app golang:1.25-alpine sh -c "go test -v -timeout 5m ./tests/e2e/..."; \
	fi

# Total coverage of the root module as Codecov computes it (default-tag
# profile merged with the treesitter profiles, codecov.yml ignores applied).
.PHONY: coverage
coverage:
	@scripts/coverage.sh --packages

.PHONY: test-eif
test-eif: build-eif-lib
	@if command -v go >/dev/null 2>&1; then \
		go test -v -tags "eif" ./internal/embedding -run TestEIF; \
	fi

# ── Clean ────────────────────────────────────────────────────────────────────
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR) dist/ gleann-test gleann-test-faiss coverage.out coverage-ts.out coverage-chunking.out
	@echo "🧹 Cleaned"

.PHONY: vet
vet:
	@if command -v go >/dev/null 2>&1; then \
		go vet ./...; \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm -v $$(pwd):/app -w /app golang:1.25-alpine go vet ./...; \
	fi
	@echo "✅ go vet passed"

# ── Lint / Vulnerability Scan ──────────────────────────────────────────────
.PHONY: lint
# staticcheck is pinned (STATICCHECK_VERSION, the same release CI uses); its
# module needs go >= 1.26 to compile, so the install may fetch that toolchain.
# It still analyses this go 1.25 module.
STATICCHECK_VERSION ?= v0.8.1
lint:
	@command -v staticcheck >/dev/null 2>&1 || { echo "installing staticcheck $(STATICCHECK_VERSION)..."; GOTOOLCHAIN=go1.26.0+auto go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION); }
	@command -v govulncheck >/dev/null 2>&1 || { echo "installing govulncheck..."; go install golang.org/x/vuln/cmd/govulncheck@latest; }
	@echo "── staticcheck ──"
	staticcheck ./...
	@echo "── govulncheck ──"
	govulncheck ./...
	@echo "── go mod tidy -diff ──"
	go mod tidy -diff
	@echo "✅ lint passed"

# ── Graph database benchmark (nested module, see bench/graphdb/README.md) ──
# SQLite reference only by default. Candidates that need CGo are compiled in
# with their build tag: make bench-graphdb GRAPHDB_TAGS=kuzu
# Pin the CPUs for comparable numbers: GRAPHDB_PIN="taskset -c 0-3".
GRAPHDB_EDGES ?= 10000
GRAPHDB_TAGS  ?=
GRAPHDB_PIN   ?=
.PHONY: bench-graphdb
bench-graphdb:
	cd bench/graphdb && $(GRAPHDB_PIN) go run $(if $(GRAPHDB_TAGS),-tags "$(GRAPHDB_TAGS)",) ./cmd/graphbench -edges $(GRAPHDB_EDGES)

# ── Vector database benchmark (nested module, see bench/vectordb/README.md) ──
VECTORDB_SIZES ?= 10000
VECTORDB_DIMS  ?= 384
VECTORDB_TAGS  ?=
VECTORDB_PIN   ?=
# Recall gate: fails when recall@10 of the vector backends or of filtered
# search drops below the thresholds in bench/vectordb/gate (also run in CI).
.PHONY: recall-gate
recall-gate:
	cd bench/vectordb && go test -count=1 -v -timeout 20m -run TestRecallGate ./gate/

.PHONY: bench-vectordb
bench-vectordb:
	cd bench/vectordb && $(VECTORDB_PIN) go run $(if $(VECTORDB_TAGS),-tags "$(VECTORDB_TAGS)",) ./cmd/vectorbench -sizes $(VECTORDB_SIZES) -dims $(VECTORDB_DIMS)

# Exporter of v1.9 Kùzu memory stores (own module, CGo; see docs/memory_engine.md, "Upgrading entities from v1.9").
.PHONY: kuzu-memory-export
kuzu-memory-export:
	cd tools/kuzu-memory-export && CGO_ENABLED=1 go build -o ../../bin/kuzu-memory-export .
