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

# ── Default target ──────────────────────────────────────────────────────────
.PHONY: all build
all: $(BINARY)
build: $(BINARY)

# ── Pure Go build (no CGo) ──────────────────────────────────────────────────
.PHONY: $(BINARY)
$(BINARY):
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "treesitter" -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)
	@echo "✅ Built $(BINARY) (with CGo and tree-sitter)"

# ── FAISS + Tree-sitter build ──────────────────────────────────────────────
.PHONY: full
full: $(BINARY_FULL)

$(BINARY_FULL):
	@echo "🔧 Building $(BINARY_FULL) with FAISS + tree-sitter..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 \
	CGO_LDFLAGS="-Wl,-rpath=\$$ORIGIN -L$(FAISS_LIB_DIR) -lfaiss_c -lfaiss" \
	go build -tags "faiss treesitter" -ldflags "$(LDFLAGS)" -o $(BINARY_FULL) $(CMD)
	@echo "✅ Built $(BINARY_FULL)"

# ── Install ─────────────────────────────────────────────────────────────────
.PHONY: install
install: $(BINARY)
	install -m 0755 $(BINARY) $(INSTALL_DIR)/$$(basename $(BINARY))
	@echo "✅ Installed $(BINARY) → $(INSTALL_DIR)/$$(basename $(BINARY))"

.PHONY: install-full
install-full: $(BINARY_FULL)
	install -m 0755 $(BINARY_FULL) $(INSTALL_DIR)/$$(basename $(BINARY_FULL))
	@# Copy FAISS shared libs next to the binary so \$$ORIGIN rpath works.
	@for lib in $(FAISS_LIB_DIR)/libfaiss*.so*; do \
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
	go test -race -timeout 120s $$(go list ./... | grep -v /benchmarks)

.PHONY: test-faiss
test-faiss:
	go test -tags "cgo faiss" -count=1 -timeout 120s ./internal/backend/faiss/...

.PHONY: test-treesitter
test-treesitter:
	go test -tags "cgo treesitter" -count=1 -timeout 60s ./internal/chunking/...

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
	cp $(FAISS_LIB_DIR)/libfaiss_c.so $(BUILD_DIR)/ 2>/dev/null || true
	cp $(FAISS_LIB_DIR)/libfaiss.so   $(BUILD_DIR)/ 2>/dev/null || true
	cd $(BUILD_DIR) && tar czf ../$@ $$(basename $(BINARY_FULL)) libfaiss*.so 2>/dev/null; true
	rm -f $(BUILD_DIR)/libfaiss*.so
	@echo "  → $@"

# ── Clean ────────────────────────────────────────────────────────────────────
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR) dist/ gleann-test gleann-test-faiss
	@echo "🧹 Cleaned"

# ── Quick dev targets ─────────────────────────────────────────────────────────
.PHONY: dev
dev:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "treesitter" -race -o $(BINARY) $(CMD)

.PHONY: bench-faiss
bench-faiss:
	go test -tags "cgo faiss" -run=^$$ -bench=. -benchmem -timeout 120s ./internal/backend/faiss/

.PHONY: vet
vet:
	go vet ./...
	@echo "✅ go vet passed"
