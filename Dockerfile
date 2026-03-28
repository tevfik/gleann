# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.24-bookworm AS builder

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
COPY modules/hnsw/go.mod    modules/hnsw/go.sum    modules/hnsw/
COPY modules/bm25/go.mod    modules/bm25/go.sum    modules/bm25/
COPY modules/chunking/go.mod modules/chunking/go.sum modules/chunking/

RUN go mod download

# Copy full source.
COPY . .

# Build pure-Go binary (no CGo, no tree-sitter/FAISS).
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/gleann ./cmd/gleann

# ── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/gleann /usr/local/bin/gleann

# Default index directory — mount a volume here for persistence.
ENV GLEANN_INDEX_DIR=/data/indexes

# Expose default server port.
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/gleann", "version"]

ENTRYPOINT ["gleann"]
CMD ["serve", "--port", "8080"]
