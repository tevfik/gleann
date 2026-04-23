# Gleann Makefile
# Usage:
#   make               — build pure Go binary (gleann)
#   make full          — build FAISS + tree-sitter binary (gleann-full)
#   make install       — install gleann to /usr/local/bin
#   make install-full  — install gleann-full to /usr/local/bin
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

# FAISS shared lib locations (set from environment or default)
FAISS_LIB_DIR ?= /usr/local/lib

# Platform detection
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
    RPATH_FLAGS := -Wl,-rpath,@loader_path
    SO_EXT      := dylib
else
    RPATH_FLAGS := -Wl,-rpath,\$$ORIGIN
    SO_EXT      := so
endif

# ── Default target ──────────────────────────────────────────────────────────
.PHONY: all build
all: $(BINARY)
build: $(BINARY)

# ── Pure Go build (respects CGO_ENABLED env var for cross-compilation) ─────────
.PHONY: $(BINARY)
$(BINARY):
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)
	@echo "✅ Built $(BINARY)"

# build-cgo — local dev build with CGo + tree-sitter
.PHONY: build-cgo
build-cgo: $(BUILD_DIR)/gleann-cgo

$(BUILD_DIR)/gleann-cgo:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 CGO_CFLAGS="-w" go build -tags "treesitter" -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)
	@echo "✅ Built $(BINARY) (with CGo and tree-sitter)"

# ── FAISS + Tree-sitter build ──────────────────────────────────────────────
.PHONY: full
full: $(BINARY_FULL)

$(BINARY_FULL):
	@echo "🔧 Building $(BINARY_FULL) with FAISS + tree-sitter..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 \
	CGO_CFLAGS="-w" \
	CGO_LDFLAGS="$(RPATH_FLAGS) -L$(FAISS_LIB_DIR) -lfaiss_c -lfaiss" \
	go build -tags "faiss treesitter" -ldflags "$(LDFLAGS)" -o $(BINARY_FULL) $(CMD)
	@echo "✅ Built $(BINARY_FULL)"

# ── Install ─────────────────────────────────────────────────────────────────
USER_BIN_DIR ?= $(HOME)/.local/bin

# install-user — build gleann-full (FAISS) and install to ~/.local/bin/gleann
.PHONY: install-user
install-user: $(BINARY_FULL)
	@mkdir -p $(USER_BIN_DIR)
	install -m 0755 $(BINARY_FULL) $(USER_BIN_DIR)/gleann
	@echo "✅ Installed gleann-full → $(USER_BIN_DIR)/gleann"
	@echo "   Make sure $(USER_BIN_DIR) is in your PATH."

# install-user-lite — build plain gleann (no FAISS) and install to ~/.local/bin/gleann
.PHONY: install-user-lite
install-user-lite: $(BINARY)
	@mkdir -p $(USER_BIN_DIR)
	install -m 0755 $(BINARY) $(USER_BIN_DIR)/gleann
	@echo "✅ Installed gleann → $(USER_BIN_DIR)/gleann"

# install — install to /usr/local/bin (system-wide, needs sudo)
.PHONY: install
install: $(BINARY)
	install -m 0755 $(BINARY) $(INSTALL_DIR)/$$(basename $(BINARY))
	@echo "✅ Installed $(BINARY) → $(INSTALL_DIR)/$$(basename $(BINARY))"

.PHONY: install-full
install-full: $(BINARY_FULL)
	install -m 0755 $(BINARY_FULL) $(INSTALL_DIR)/$$(basename $(BINARY_FULL))
	@# Copy FAISS shared libs next to the binary so rpath works.
	@for lib in $(FAISS_LIB_DIR)/libfaiss*.$(SO_EXT)*; do \
		if [ -f "$$lib" ]; then \
			install -m 0755 "$$lib" $(INSTALL_DIR)/; \
			echo "  📦 Installed $$(basename $$lib) → $(INSTALL_DIR)/"; \
		fi; \
	done
	@echo "✅ Installed $(BINARY_FULL) → $(INSTALL_DIR)/$$(basename $(BINARY_FULL))"
	@echo "   Run: $(INSTALL_DIR)/$$(basename $(BINARY_FULL)) setup"

# ── Tests ────────────────────────────────────────────────────────────────────
.PHONY: test
test:
	go test -race -timeout 120s $$(go list ./... | grep -v /tests/benchmarks)

.PHONY: test-faiss
test-faiss:
	go test -tags "cgo faiss" -count=1 -timeout 120s ./internal/backend/faiss/...

.PHONY: test-treesitter
test-treesitter:
	go test -tags "cgo treesitter" -count=1 -timeout 60s ./modules/chunking/...

# test-e2e — run Go integration E2E tests. Requires Ollama running.
.PHONY: test-e2e
test-e2e: $(BINARY)
	@echo "🧪 Running E2E tests (requires Ollama + markitdown)..."
	go test ./tests/integration/ -run TestE2E -v -count=1 -timeout 8m
	@echo "✅ E2E tests passed"

.PHONY: test-e2e-plugins
test-e2e-plugins: $(BINARY)
	@echo "🧪 Running E2E plugin bash tests..."
	bash tests/e2e/plugin_test.sh
	@echo "✅ E2E plugin tests passed"

# test-e2e-full — run the full bash E2E suite against gleann-full binary.
.PHONY: test-e2e-full
test-e2e-full: $(BINARY_FULL)
	@echo "🧪 Running full E2E suite..."
	bash tests/e2e/run.sh
	@echo "✅ Full E2E suite passed"

# test-benchmark — run E2E suite with benchmark scoring and weak point detection.
.PHONY: test-benchmark
test-benchmark: $(BINARY_FULL)
	@echo "🧪 Running E2E + benchmark..."
	bash tests/e2e/run.sh --benchmark
	@echo "✅ Benchmark complete — results in tests/e2e/results/"

# test-bench — run Go performance benchmarks.
.PHONY: test-bench
test-bench:
	go test -run=^$$ -bench=. -benchmem -timeout 120s ./tests/benchmarks/

# pre-commit — run all checks locally before committing
.PHONY: pre-commit
pre-commit: vet test test-e2e
	@echo "✅ All pre-commit checks passed"

# ── Release (local dist/ directory) ─────────────────────────────────────────
.PHONY: release
release: dist/gleann-$(VERSION)-linux-amd64.tar.gz dist/gleann-full-$(VERSION)-linux-amd64.tar.gz
	@echo "📦 Release artifacts in dist/"

DIST_DIR := dist

$(DIST_DIR):
	mkdir -p $(DIST_DIR)

dist/gleann-$(VERSION)-linux-amd64.tar.gz: $(BINARY) | $(DIST_DIR)
	tar czf $@ -C $(BUILD_DIR) $$(basename $(BINARY))
	@echo "  → $@"

dist/gleann-full-$(VERSION)-linux-amd64.tar.gz: $(BINARY_FULL) | $(DIST_DIR)
	@# Bundle the binary + FAISS shared libs in a single tarball.
	cp $(FAISS_LIB_DIR)/libfaiss_c.$(SO_EXT) $(BUILD_DIR)/ 2>/dev/null || true
	cp $(FAISS_LIB_DIR)/libfaiss.$(SO_EXT)   $(BUILD_DIR)/ 2>/dev/null || true
	cd $(BUILD_DIR) && tar czf ../$@ $$(basename $(BINARY_FULL)) libfaiss*.$(SO_EXT) 2>/dev/null; true
	rm -f $(BUILD_DIR)/libfaiss*.$(SO_EXT)
	@echo "  → $@"

# ── Clean ────────────────────────────────────────────────────────────────────
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR) dist/ gleann-test gleann-test-faiss
	@echo "🧹 Cleaned"

# ── Docker ──────────────────────────────────────────────────────────────────
DOCKER_IMAGE   ?= gleann
DOCKER_TAG     ?= $(VERSION)

.PHONY: docker
docker:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) --build-arg VERSION=$(VERSION) .
	@echo "✅ Built $(DOCKER_IMAGE):$(DOCKER_TAG)"

.PHONY: docker-full
docker-full:
	docker build -t $(DOCKER_IMAGE)-full:$(DOCKER_TAG) --build-arg VERSION=$(VERSION) -f Dockerfile.full .
	@echo "✅ Built $(DOCKER_IMAGE)-full:$(DOCKER_TAG)"

.PHONY: docker-run
docker-run:
	docker run --rm -p 8080:8080 -v gleann-data:/data/indexes $(DOCKER_IMAGE):$(DOCKER_TAG)

# ── Quick dev targets ─────────────────────────────────────────────────────────
.PHONY: dev
dev:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "treesitter" -race -o $(BINARY) $(CMD)

.PHONY: bench-faiss
bench-faiss:
	go test -tags "cgo faiss" -run=^$$ -bench=. -benchmem -timeout 120s ./tests/benchmarks/vector_db/

.PHONY: vet
vet:
	go vet ./...
	@echo "✅ go vet passed"

# ── ParseBench CI targets ────────────────────────────────────────────────────
.PHONY: bench-parse
bench-parse:
	@echo "📊 Running ParseBench (requires gleann-plugin-marker on :8766)..."
	go test ./tests/benchmarks/ -run TestParseBench -v -timeout 600s -count=1
	@echo "📄 Results: tests/e2e/results/parsebench_results.md"

.PHONY: bench-plugins
bench-plugins:
	@echo "📊 Running Plugin Benchmark..."
	go test ./tests/benchmarks/ -run TestPluginBenchmark -v -timeout 300s -count=1
	@echo "📄 Results: tests/e2e/results/plugin_benchmark.md"

.PHONY: bench-all
bench-all: bench-faiss bench-plugins bench-parse
	@echo "✅ All benchmarks complete"
