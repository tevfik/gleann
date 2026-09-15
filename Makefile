# Gleann Makefile
# Usage:
#   make               — build pure Go binary (gleann)
#   make full          — build FAISS + tree-sitter binary (gleann-full)
#   make install       — install gleann to /usr/local/bin
#   make install-user  — install gleann-full to ~/.local/bin/gleann
#   make test          — run all tests
#   make test-faiss    — run FAISS backend tests
#   make release       — build all release artifacts into dist/
#   make clean         — remove built binaries

# ── Variables ──────────────────────────────────────────────────────────────
BUILD_DIR   := build
BINARY      := $(BUILD_DIR)/gleann
BINARY_FULL := $(BUILD_DIR)/gleann-full
CMD         := ./cmd/gleann
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -s -w -X main.version=$(VERSION)
INSTALL_DIR ?= /usr/local/bin
REPO_ROOT   := $(CURDIR)
USER_BIN_DIR ?= $(HOME)/.local/bin
USER_LIB_DIR ?= $(HOME)/.local/lib

# FAISS shared lib locations
FAISS_LIB_DIR ?= $(REPO_ROOT)/deps/faiss_install/lib
FAISS_INC_DIR ?= $(REPO_ROOT)/deps/faiss_install/include

# Platform detection
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
    RPATH_FLAGS := -Wl,-rpath,@loader_path -Wl,-rpath,@loader_path/../lib
    ZNOW_FLAG   := 
    SO_EXT      := dylib
    OMP_PREFIX  := $(shell brew --prefix libomp 2>/dev/null)
    OMP_CFLAGS  := $(if $(OMP_PREFIX),-I$(OMP_PREFIX)/include,)
    OMP_LDFLAGS := $(if $(OMP_PREFIX),-L$(OMP_PREFIX)/lib -lomp,)
else
    RPATH_FLAGS := -Wl,-rpath,'$$ORIGIN' -Wl,-rpath,'$$ORIGIN/../lib' -Wl,-rpath,/usr/local/lib -Wl,-rpath,$(USER_LIB_DIR)
    ZNOW_FLAG   := -Wl,-z,now
    SO_EXT      := so
    OMP_CFLAGS  :=
    OMP_LDFLAGS :=
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
		docker run --rm -u $$(id -u):$$(id -g) -v $$(pwd):/app -w /app/ui node:20-alpine sh -c "npm ci && npm run build"; \
	fi
	@if [ ! -f ui/dist/index.html ]; then echo "❌ ui/dist/index.html missing! Build failed."; exit 1; fi

prepare-assets:
	@if [ "$$SKIP_WEB" = "1" ]; then \
		echo "⚠️  SKIP_WEB=1: Creating minimal placeholder for API-only build..."; \
		mkdir -p internal/server/dist; \
		echo '<!DOCTYPE html><html><body><h1>API-Only Mode</h1><p>Web UI not included in this build.</p></body></html>' > internal/server/dist/index.html; \
	elif [ -d ui/dist ]; then \
		echo "📦 Copying existing web assets..."; \
		rm -rf internal/server/dist; \
		cp -r ui/dist internal/server/dist; \
	else \
		$(MAKE) build-web; \
		echo "📦 Copying web assets..."; \
		rm -rf internal/server/dist; \
		cp -r ui/dist internal/server/dist; \
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

.PHONY: build-rust-core
build-rust-core:
	@if [ -d ext/gleann-core-rs ] && command -v cargo >/dev/null 2>&1; then \
		echo "🔧 Building Rust core..."; \
		cd ext/gleann-core-rs && cargo build --release; \
	fi

# ── Full Binary (Tree-sitter + KuzuDB CGo) ───────────────────────────────────
.PHONY: full
full: $(BINARY_FULL)

$(BINARY_FULL): prepare-assets
	@echo "🔧 Building $(BINARY_FULL) with Tree-sitter + KuzuDB CGo..."
	@mkdir -p $(BUILD_DIR)
	@if command -v go >/dev/null 2>&1; then \
		CGO_ENABLED=1 CGO_CFLAGS="-w" go build -tags "treesitter" -ldflags "$(LDFLAGS) -extldflags '$(RPATH_FLAGS)'" -o $(BINARY_FULL) $(CMD); \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm -v gleann-go-cache:/go/pkg/mod -v gleann-build-cache:/root/.cache/go-build -v $$(pwd):/app -w /app golang:1.25 sh -c "CGO_ENABLED=1 CGO_CFLAGS='-w' go build -buildvcs=false -tags 'treesitter' -ldflags '$(LDFLAGS) -extldflags \"$(RPATH_FLAGS)\"' -o $(BINARY_FULL) $(CMD) && go mod download && cp /go/pkg/mod/github.com/kuzudb/go-kuzu@v0.11.3/lib/dynamic/linux-amd64/libkuzu.so /app/$(BUILD_DIR)/ 2>/dev/null || true && chown -R $$(id -u):$$(id -g) /app/$(BUILD_DIR)"; \
	fi
	@if command -v patchelf >/dev/null 2>&1 && [ -f $(BINARY_FULL) ]; then \
		patchelf --set-rpath '$$ORIGIN:$$ORIGIN/../lib:/usr/local/lib:$(USER_LIB_DIR)' $(BINARY_FULL) 2>/dev/null || true; \
	fi
	@echo "✅ Built $(BINARY_FULL)"

# ── Install ─────────────────────────────────────────────────────────────────

# install-user — build gleann-full and install to ~/.local/bin/gleann
.PHONY: install-user
install-user: $(BINARY_FULL)
	@mkdir -p $(USER_BIN_DIR) $(USER_LIB_DIR)
	install -m 0755 $(BINARY_FULL) $(USER_BIN_DIR)/gleann
	install -m 0755 $(BINARY_FULL) $(USER_BIN_DIR)/gleann-full
	@if [ -f $(BUILD_DIR)/libkuzu.so ]; then \
		install -m 0755 $(BUILD_DIR)/libkuzu.so $(USER_LIB_DIR)/; \
		install -m 0755 $(BUILD_DIR)/libkuzu.so $(USER_BIN_DIR)/; \
	fi
	@echo "✅ Installed gleann-full → $(USER_BIN_DIR)/gleann"
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
	@if [ -f $(BUILD_DIR)/libkuzu.so ]; then \
		install -m 0755 $(BUILD_DIR)/libkuzu.so /usr/local/lib/ 2>/dev/null || true; \
	fi
	@echo "✅ Installed $(BINARY_FULL) → $(INSTALL_DIR)/gleann"

# ── Tests ────────────────────────────────────────────────────────────────────
.PHONY: test
test:
	@if command -v go >/dev/null 2>&1; then \
		mkdir -p build/test-home; \
		GLEANN_TEST_MODE=true HOME=$(shell pwd)/build/test-home go test -race -timeout 120s $$(go list ./... | grep -v /tests/benchmarks); \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm -v $$(pwd):/app -w /app golang:1.25-bookworm sh -c "go test -v -timeout 120s \$$(go list ./... | grep -v /tests/benchmarks)"; \
	fi

.PHONY: test-e2e
test-e2e:
	@if command -v docker >/dev/null 2>&1; then \
		docker run --rm --network host -v $$(pwd):/app -w /app golang:1.25-alpine sh -c "go test -v -timeout 5m ./tests/integration/..."; \
	fi

# ── Clean ────────────────────────────────────────────────────────────────────
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR) dist/ gleann-test gleann-test-faiss coverage.out coverage-ts.out
	@echo "🧹 Cleaned"

.PHONY: vet
vet:
	@if command -v go >/dev/null 2>&1; then \
		go vet ./...; \
	elif command -v docker >/dev/null 2>&1; then \
		docker run --rm -v $$(pwd):/app -w /app golang:1.25-alpine go vet ./...; \
	fi
	@echo "✅ go vet passed"
