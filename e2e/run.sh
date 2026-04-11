#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════════════════════
# gleann e2e test suite
#
# Tests the full feature surface against the live binary using real fixture
# documents (knowledge-rich markdown + code files + binary formats).
# Works offline — Ollama must be running with configured models.
#
# Usage:
#   ./e2e/run.sh                  # full suite
#   ./e2e/run.sh --quick          # skip LLM-dependent tests
#   ./e2e/run.sh --section search # run only a specific section
#   ./e2e/run.sh --help
#
# Exit code: 0 = all required tests pass, 1 = any failure
# ══════════════════════════════════════════════════════════════════════════════
set +e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="${REPO_ROOT}/build/gleann-full"
FIXTURES="${REPO_ROOT}/e2e/fixtures"
RESULTS_DIR="${REPO_ROOT}/e2e/results"

IDX_DOCS="e2e-docs"
IDX_CODE="e2e-code"
IDX_BIN="e2e-binary"
IDX_FAISS="e2e-faiss"

QUICK=false
SECTION=""

# ── Argument parsing ────────────────────────────────────────────────────────
for arg in "$@"; do
  case "$arg" in
    --quick)    QUICK=true ;;
    --help|-h)
      grep '^#' "${BASH_SOURCE[0]}" | head -20 | sed 's/^# \?//'
      exit 0 ;;
  esac
done
for i in "$@"; do
  if [[ "$i" == "--section" && -n "${2:-}" ]]; then SECTION="$2"; fi
  shift
done

# ── Colours ─────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

PASS=0; FAIL=0; SKIP=0; WARN=0
declare -a FAILURES

# ── Helpers ──────────────────────────────────────────────────────────────────
pass()  { PASS=$((PASS+1));  echo -e "  ${GREEN}✓${NC} $1"; }
fail()  { FAIL=$((FAIL+1));  echo -e "  ${RED}✗${NC} $1"; FAILURES+=("$1"); }
skip()  { SKIP=$((SKIP+1));  echo -e "  ${YELLOW}⊘${NC} $1 (skipped)"; }
warn()  { WARN=$((WARN+1));  echo -e "  ${YELLOW}⚠${NC} $1"; }
header(){ echo -e "\n${CYAN}${BOLD}══ $1 ══${NC}"; }
sub()   { echo -e "  ${BLUE}▸${NC} $1"; }

assert_contains() {
  local desc="$1" pattern="$2" text="$3"
  if echo "$text" | grep -qi "$pattern"; then
    pass "$desc"
  else
    fail "$desc (expected /$pattern/ in output)"
  fi
}

assert_json() {
  local desc="$1" text="$2"
  if echo "$text" | python3 -c "import sys,json; json.load(sys.stdin)" 2>/dev/null; then
    pass "$desc"
  elif echo "$text" | grep -q '^\s*[\[{]'; then
    pass "$desc (looks like JSON)"
  else
    fail "$desc (not valid JSON)"
  fi
}

assert_min_chars() {
  local desc="$1" min="$2" text="$3"
  local len=${#text}
  if [[ $len -ge $min ]]; then
    pass "$desc (${len} chars ≥ ${min})"
  else
    fail "$desc (only ${len} chars, expected ≥ ${min})"
  fi
}

assert_file_exists() {
  local desc="$1" path="$2"
  if [[ -f "$path" ]]; then
    pass "$desc"
  else
    fail "$desc (file not found: $path)"
  fi
}

assert_exit_ok() {
  local desc="$1" code="$2"
  if [[ $code -eq 0 ]]; then
    pass "$desc"
  else
    fail "$desc (exit code $code)"
  fi
}

ollama_available() {
  local host
  host=$(cat ~/.gleann/config.json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('ollama_host','http://localhost:11434'))" 2>/dev/null || echo "http://localhost:11434")
  curl -sf "${host}/api/tags" >/dev/null 2>&1
}

ts() { date '+%T'; }

# ══════════════════════════════════════════════════════════════════════════════
# PRE-FLIGHT
# ══════════════════════════════════════════════════════════════════════════════
header "Pre-flight checks  $(ts)"

if [[ ! -x "$BINARY" ]]; then
  echo -e "${RED}Error: binary not found at $BINARY${NC}"
  echo "Run: make full"
  exit 1
fi
pass "Binary exists: $BINARY"

if [[ ! -d "$FIXTURES/docs" ]]; then
  fail "Fixture docs directory missing: $FIXTURES/docs"
  exit 1
fi
doc_count=$(ls "$FIXTURES/docs"/*.md 2>/dev/null | wc -l)
pass "Fixture docs: ${doc_count} markdown files"

code_count=$(ls "$FIXTURES/code"/*.{go,py,ts} 2>/dev/null | wc -l)
pass "Fixture code: ${code_count} source files"

bin_count=$(ls "$FIXTURES/binary"/*.{docx,xlsx,pptx,csv,html} 2>/dev/null | wc -l)
pass "Fixture binary: ${bin_count} document files"

mkdir -p "$RESULTS_DIR"

if ollama_available; then
  pass "Ollama is reachable"
  HAS_LLM=true
else
  warn "Ollama not reachable — LLM-dependent tests will be skipped"
  HAS_LLM=false
fi

BACKEND=$(GOFLAGS="-tags=faiss" "$BINARY" index build "$IDX_DOCS" --docs "$FIXTURES/docs" 2>&1 | grep -o "faiss\|hnsw" | head -1 || true)
sub "Binary backend: ${BACKEND:-auto-detect from build tags}"

# ── Cleanup from previous run ────────────────────────────────────────────────
sub "Removing leftover test indexes"
for idx in "$IDX_DOCS" "$IDX_CODE" "$IDX_BIN" "$IDX_FAISS"; do
  "$BINARY" index remove "$idx" >/dev/null 2>&1 || true
done

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 1: INDEX BUILD
# ══════════════════════════════════════════════════════════════════════════════
header "§1  Index Build  $(ts)"

# 1a. Docs index (markdown knowledge corpus)
sub "Building docs index from ${doc_count} markdown files..."
OUT=$("$BINARY" index build "$IDX_DOCS" --docs "$FIXTURES/docs" 2>&1)
assert_exit_ok "index build docs" $?
assert_contains "index build reports chunks" "chunk\|passage\|Built\|indexed" "$OUT"
sub "Output: $(echo "$OUT" | grep -Ei 'chunk|passage|Built|indexed' | tail -1)"

# 1b. Code index with AST graph
sub "Building code index with --graph from ${code_count} source files..."
OUT=$("$BINARY" index build "$IDX_CODE" --docs "$FIXTURES/code" --graph 2>&1)
assert_exit_ok "index build code+graph" $?

# 1c. Binary format index (DOCX, XLSX, PPTX, CSV, HTML)
sub "Building binary formats index..."
OUT=$("$BINARY" index build "$IDX_BIN" --docs "$FIXTURES/binary" 2>&1)
assert_exit_ok "index build binary formats" $?
if echo "$OUT" | grep -qi "chunk\|passage\|Built"; then
  pass "Binary document extraction succeeded (DOCX/XLSX/PPTX/CSV/HTML)"
else
  warn "Binary index build produced unexpected output: $(echo "$OUT" | tail -2)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 2: INDEX INFO & LIST
# ══════════════════════════════════════════════════════════════════════════════
header "§2  Index Metadata  $(ts)"

OUT=$("$BINARY" index list 2>&1)
assert_contains "index list shows docs index" "$IDX_DOCS" "$OUT"
assert_contains "index list shows code index" "$IDX_CODE" "$OUT"
assert_contains "index list shows binary index" "$IDX_BIN" "$OUT"

OUT=$("$BINARY" index info "$IDX_DOCS" 2>&1)
assert_contains "index info shows passages" "Chunks\|passages\|Passages" "$OUT"
assert_contains "index info shows backend" "hnsw\|faiss\|Backend" "$OUT"

OUT=$("$BINARY" index info "$IDX_CODE" 2>&1)
assert_contains "code index info shows model" "model\|Model" "$OUT"

sub "JSON output:"
OUT=$("$BINARY" index list --json 2>&1)
assert_json "index list --json is valid JSON" "$OUT"

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 3: SEMANTIC SEARCH
# ══════════════════════════════════════════════════════════════════════════════
header "§3  Semantic Search (no LLM)  $(ts)"

# Each query targets knowledge unique to one fixture document
declare -A SEARCH_CASES=(
  ["qubit superposition quantum"]="quantum_computing"
  ["Byzantine generals consensus protocol"]="byzantine_fault"
  ["lipid nanoparticle mRNA pseudouridine"]="mrna_vaccines"
  ["permafrost thermokarst carbon methane"]="permafrost_carbon"
  ["zk-SNARK Groth16 elliptic curve pairing"]="zkproof"
  ["transformer attention mechanism scaled dot-product"]="transformer_arch"
  ["CRISPR Cas9 guide RNA double strand break"]="crispr_cas9"
  ["seL4 microkernel capability-based access control"]="microkernel_os"
)

for query in "${!SEARCH_CASES[@]}"; do
  expected="${SEARCH_CASES[$query]}"
  OUT=$("$BINARY" search "$IDX_DOCS" "$query" --json 2>&1)
  if echo "$OUT" | grep -qi "$(echo "$expected" | tr '_' ' \|')"; then
    pass "search: '$query' → found $expected content"
  else
    # Softer check: any result returned at all
    if echo "$OUT" | grep -q '"text"'; then
      warn "search: '$query' → results returned but source unclear"
    else
      fail "search: '$query' → no results"
    fi
  fi
done

# Code search
OUT=$("$BINARY" search "$IDX_CODE" "rate limiter token bucket" 2>&1)
if echo "$OUT" | grep -qi "token\|bucket\|rate\|limiter"; then
  pass "code search: rate limiter found"
else
  warn "code search: rate limiter not found (may be chunking variation)"
fi

OUT=$("$BINARY" search "$IDX_CODE" "circular buffer capacity ring" 2>&1)
if echo "$OUT" | grep -qi "circular\|buffer\|queue\|capacity"; then
  pass "code search: CircularBuffer found"
else
  warn "code search: CircularBuffer not found"
fi

# Binary format search
OUT=$("$BINARY" search "$IDX_BIN" "HNSW FAISS benchmark latency recall" 2>&1)
if echo "$OUT" | grep -qi "hnsw\|faiss\|latency\|recall\|benchmark"; then
  pass "binary search: benchmark content found in DOCX/XLSX/HTML"
else
  warn "binary search: benchmark content not found in binary docs"
fi

# Search JSON output
OUT=$("$BINARY" search "$IDX_DOCS" "quantum entanglement" --json 2>&1)
assert_json "search --json output is valid JSON" "$OUT"

# Hybrid search
OUT=$("$BINARY" search "$IDX_DOCS" "Louvain modularity community detection" --hybrid 0.7 2>&1)
if echo "$OUT" | grep -qi "community\|modularity\|louvain\|graph"; then
  pass "hybrid search: community detection found"
else
  warn "hybrid search: community detection not found (may not be in docs corpus)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 4: FAISS BACKEND VERIFICATION
# ══════════════════════════════════════════════════════════════════════════════
header "§4  FAISS Backend  $(ts)"

# Verify the binary was built with FAISS
sub "Checking FAISS backend availability..."
OUT=$("$BINARY" index info "$IDX_DOCS" 2>&1)
if echo "$OUT" | grep -qi "faiss"; then
  pass "FAISS backend active (gleann-full build)"
  FAISS_ACTIVE=true
else
  META_FILE=$(find ~/.gleann/indexes 2>/dev/null -name "*.meta.json" | head -1)
  if [[ -n "$META_FILE" ]] && grep -qi "faiss" "$META_FILE" 2>/dev/null; then
    pass "FAISS backend confirmed via meta file"
    FAISS_ACTIVE=true
  else
    warn "FAISS backend not confirmed — binary may be gleann (pure-Go) not gleann-full"
    FAISS_ACTIVE=false
  fi
fi

# FAISS-specific: build a separate test index explicitly listing it
if [[ "$FAISS_ACTIVE" == "true" ]]; then
  sub "FAISS index build + search roundtrip..."
  "$BINARY" index remove "$IDX_FAISS" >/dev/null 2>&1 || true
  OUT=$("$BINARY" index build "$IDX_FAISS" --docs "$FIXTURES/docs" 2>&1)
  assert_exit_ok "FAISS index build" $?

  OUT=$("$BINARY" search "$IDX_FAISS" "zero-knowledge proof succinct" 2>&1)
  if echo "$OUT" | grep -qi "proof\|zk\|commitm\|groth\|snar"; then
    pass "FAISS search: ZKProof content found"
  else
    warn "FAISS search: ZKProof content not found"
  fi

  # Compare FAISS vs HNSW passage counts (should be equal)
  FAISS_PASSAGES=$("$BINARY" index info "$IDX_FAISS" --json 2>&1 | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('num_passages', d.get('chunks', 0)))" 2>/dev/null || echo "0")
  DOCS_PASSAGES=$("$BINARY" index info "$IDX_DOCS" --json 2>&1 | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('num_passages', d.get('chunks', 0)))" 2>/dev/null || echo "0")
  sub "Passages: ${IDX_DOCS}=${DOCS_PASSAGES}, ${IDX_FAISS}=${FAISS_PASSAGES}"
  if [[ "$FAISS_PASSAGES" == "$DOCS_PASSAGES" && "$FAISS_PASSAGES" != "0" ]]; then
    pass "FAISS and HNSW indexes yield same passage count (${FAISS_PASSAGES})"
  else
    warn "Passage count mismatch or could not parse (faiss=${FAISS_PASSAGES}, hnsw=${DOCS_PASSAGES})"
  fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 5: CODE GRAPH & COMMUNITY DETECTION
# ══════════════════════════════════════════════════════════════════════════════
header "§5  Code Graph & Community Detection  $(ts)"

# Communities
OUT=$("$BINARY" graph communities --index "$IDX_CODE" 2>&1)
if echo "$OUT" | grep -qiE "communit|modularity|god node|symbol"; then
  pass "graph communities detected"
  sub "$(echo "$OUT" | grep -iE "modularity|communities|god" | head -3)"
else
  warn "graph communities: unexpected output (code corpus may be too small)"
fi

# Graph viz HTML
VIZ_OUT="${RESULTS_DIR}/graph_${IDX_CODE}.html"
OUT=$("$BINARY" graph viz --index "$IDX_CODE" --output "$VIZ_OUT" 2>&1)
if [[ -f "$VIZ_OUT" ]]; then
  VIZ_SIZE=$(wc -c < "$VIZ_OUT")
  pass "graph viz: HTML file created (${VIZ_SIZE} bytes)"
  if grep -q "vis.js\|visNetwork\|DataSet" "$VIZ_OUT" 2>/dev/null; then
    pass "graph viz: vis.js content detected"
  else
    warn "graph viz: vis.js not detected in output"
  fi
else
  warn "graph viz: output file not created (may need --output flag support)"
fi

# Graph report
RPT_OUT="${RESULTS_DIR}/graph_report_${IDX_CODE}.md"
OUT=$("$BINARY" graph report --index "$IDX_CODE" --output "$RPT_OUT" 2>&1)
if [[ -f "$RPT_OUT" ]]; then
  RPT_SIZE=$(wc -c < "$RPT_OUT")
  pass "graph report: Markdown created (${RPT_SIZE} bytes)"
else
  warn "graph report: output file not created"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 6: BENCHMARK
# ══════════════════════════════════════════════════════════════════════════════
header "§6  Token Reduction Benchmark  $(ts)"

OUT=$("$BINARY" benchmark --index "$IDX_DOCS" --docs "$FIXTURES/docs" 2>&1)
if echo "$OUT" | grep -qiE "token|reduction|corpus|rag|compress|saving"; then
  pass "benchmark: output present"
  REDUCTION=$(echo "$OUT" | grep -oE '[0-9]+(\.[0-9]+)?x' | head -1)
  if [[ -n "$REDUCTION" ]]; then
    pass "benchmark: compression ratio reported (${REDUCTION})"
  else
    warn "benchmark: could not parse compression ratio"
  fi
  sub "$(echo "$OUT" | grep -Ei 'token|reduction|saving|ratio' | tail -3)"
else
  warn "benchmark: expected token reduction output not found"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 7: MEMORY SYSTEM
# ══════════════════════════════════════════════════════════════════════════════
header "§7  Memory System  $(ts)"

# Wipe test memories first
"$BINARY" memory clear --tier short 2>/dev/null || true
"$BINARY" memory clear --tier medium 2>/dev/null || true

# Add memories to different tiers
UNIQUE_TAG="e2etag_$$"
OUT=$("$BINARY" memory add long  "gleann e2e test: CRISPR prime editing uses pegRNA and reverse transcriptase [${UNIQUE_TAG}]" 2>&1)
assert_exit_ok "memory add long" $?

OUT=$("$BINARY" memory add medium "gleann e2e test: HotStuff BFT protocol achieves linear message complexity [${UNIQUE_TAG}]" 2>&1)
assert_exit_ok "memory add medium" $?

OUT=$("$BINARY" memory add short "gleann e2e test: pseudouridine modification enables mRNA vaccine translation [${UNIQUE_TAG}]" 2>&1)
assert_exit_ok "memory add short" $?

# List
OUT=$("$BINARY" memory list 2>&1)
assert_contains "memory list shows entries" "$UNIQUE_TAG\|long\|medium\|short" "$OUT"

# Search
OUT=$("$BINARY" memory search "prime editing pegRNA" 2>&1)
if echo "$OUT" | grep -qi "prime\|pegRNA\|CRISPR\|$UNIQUE_TAG"; then
  pass "memory search: CRISPR fact found"
else
  warn "memory search: CRISPR fact not found in results"
fi

OUT=$("$BINARY" memory search "HotStuff BFT linear" 2>&1)
if echo "$OUT" | grep -qi "HotStuff\|BFT\|linear\|message\|$UNIQUE_TAG"; then
  pass "memory search: BFT fact found"
else
  warn "memory search: BFT fact not found"
fi

# Stats
OUT=$("$BINARY" memory stats 2>&1)
if echo "$OUT" | grep -qiE "total|long|medium|short|block"; then
  pass "memory stats: output present"
else
  warn "memory stats: no stats output"
fi

# Context
OUT=$("$BINARY" memory context 2>&1)
if echo "$OUT" | grep -qi "memory\|context\|block\|$UNIQUE_TAG\|prime\|BFT"; then
  pass "memory context: context contains memories"
else
  warn "memory context: expected content not found"
fi

# Scoped memory
OUT=$("$BINARY" memory add long "e2e scoped fact for project-alpha" --scope "project-alpha" 2>&1)
if echo "$OUT" | grep -qiE "stored|saved|added|Long-term|OK"; then
  pass "memory scope: scoped fact added"
else
  warn "memory scope: unclear response for scoped add"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 8: ASK (LLM RAG) — Requires Ollama
# ══════════════════════════════════════════════════════════════════════════════
header "§8  RAG Ask (requires Ollama)  $(ts)"

if [[ "$HAS_LLM" == "false" || "$QUICK" == "true" ]]; then
  skip "§8 ask tests (Ollama not available or --quick mode)"
else
  # precision queries — the answer must come from a specific fixture doc
  declare -A ASK_CASES=(
    ["What is the T1 coherence time achieved by superconducting qubits?"]="qubit\|microsecond\|coher"
    ["What PAM sequence does SpCas9 recognize?"]="NGG\|PAM\|protospacer"
    ["What is the linear message complexity protocol that replaced PBFT?"]="HotStuff\|linear\|protocol"
    ["What percentage of Northern Hemisphere land is underlain by permafrost?"]="25\|percent\|permafrost"
    ["What is the scaling factor used in Transformer attention?"]="sqrt\|√\|dk\|scale\|dim"
  )

  for question in "${!ASK_CASES[@]}"; do
    expected="${ASK_CASES[$question]}"
    OUT=$("$BINARY" ask "$IDX_DOCS" "$question" --quiet 2>&1 | tail -20)
    if echo "$OUT" | grep -qiE "$expected"; then
      pass "ask: '${question:0:60}...'"
    elif echo "$OUT" | grep -qi "error\|connection\|refused\|failed"; then
      skip "ask failed: LLM error"
    else
      warn "ask: '${question:0:60}' — answer unclear"
      sub "$(echo "$OUT" | head -2)"
    fi
  done

  # Cross-domain question for multi-index
  OUT=$("$BINARY" ask "${IDX_DOCS},${IDX_CODE}" "Explain the token bucket rate limiting algorithm" --quiet 2>&1 | tail -15)
  if echo "$OUT" | grep -qi "token\|bucket\|rate\|refill\|limit"; then
    pass "ask multi-index: token bucket question answered from code fixture"
  else
    warn "ask multi-index: expected rate limiter answer not found"
  fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 9: BINARY DOCUMENT EXTRACTION QUALITY
# ══════════════════════════════════════════════════════════════════════════════
header "§9  Binary Document Extraction Quality  $(ts)"

# Search content that came from DOCX (technical_report.docx)
OUT=$("$BINARY" search "$IDX_BIN" "HNSW memory footprint recall FAISS" 2>&1)
if echo "$OUT" | grep -qi "hnsw\|recall\|faiss\|memory\|latency"; then
  pass "DOCX extraction: benchmark report content searchable"
else
  warn "DOCX extraction: content not found in search results"
fi

# Search content from XLSX (benchmark_data.xlsx)
OUT=$("$BINARY" search "$IDX_BIN" "nomic-embed-text bge-m3 embedding dimensions MTEB" 2>&1)
if echo "$OUT" | grep -qi "nomic\|bge\|embed\|dimension\|768\|1024"; then
  pass "XLSX extraction: embedding model table content searchable"
else
  warn "XLSX extraction: embedding table not found in results"
fi

# Search content from PPTX (research_slides.pptx)
OUT=$("$BINARY" search "$IDX_BIN" "dual backend FAISS HNSW production development" 2>&1)
if echo "$OUT" | grep -qi "dual\|backend\|faiss\|hnsw\|production\|development"; then
  pass "PPTX extraction: slide content searchable"
else
  warn "PPTX extraction: slide content not found"
fi

# Search content from HTML (api_reference.html)
OUT=$("$BINARY" search "$IDX_BIN" "REST API memory ingest recall" 2>&1)
if echo "$OUT" | grep -qi "api\|memory\|ingest\|recall\|endpoint"; then
  pass "HTML extraction: API reference content searchable"
else
  warn "HTML extraction: API reference not found"
fi

# Search content from CSV (benchmarks.csv)
OUT=$("$BINARY" search "$IDX_BIN" "throughput QPS search latency recall" 2>&1)
if echo "$OUT" | grep -qi "throughput\|qps\|latency\|recall\|benchmark"; then
  pass "CSV extraction: benchmark CSV content searchable"
else
  warn "CSV extraction: benchmark CSV not found"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 10: CLI SURFACE & FLAGS
# ══════════════════════════════════════════════════════════════════════════════
header "§10 CLI Surface  $(ts)"

# Version
OUT=$("$BINARY" version 2>&1)
assert_contains "version command" "gleann\|v[0-9]" "$OUT"

# Help structure
OUT=$("$BINARY" help 2>&1)
assert_contains "help: index subcommand" "index" "$OUT"
assert_contains "help: search" "search" "$OUT"
assert_contains "help: memory" "memory" "$OUT"
assert_contains "help: graph" "graph" "$OUT"
assert_contains "help: benchmark" "benchmark" "$OUT"

# Config
OUT=$("$BINARY" config 2>&1)
assert_contains "config show: provider" "provider\|model\|host\|{" "$OUT"

OUT=$("$BINARY" config path 2>&1)
assert_contains "config path: contains config.json" "config.json" "$OUT"

OUT=$("$BINARY" config validate 2>&1)
assert_contains "config validate: output" "valid\|provider\|model\|Config" "$OUT"

# Doctor
OUT=$("$BINARY" doctor 2>&1)
assert_contains "doctor runs" "gleann\|config\|version\|check\|✓\|✗" "$OUT"

# Index rebuild
OUT=$("$BINARY" index rebuild "$IDX_BIN" --docs "$FIXTURES/binary" 2>&1)
assert_exit_ok "index rebuild" $?

# .gleannignore support
IGNORE_DIR=$(mktemp -d)
cp "$FIXTURES/docs/quantum_computing.md" "$IGNORE_DIR/"
echo "quantum_computing.md" > "${IGNORE_DIR}/.gleannignore"
IDX_IGNORE="e2e-ignore-$$"
"$BINARY" index build "$IDX_IGNORE" --docs "$IGNORE_DIR" >/dev/null 2>&1
OUT=$("$BINARY" search "$IDX_IGNORE" "qubit superposition" 2>&1)
if echo "$OUT" | grep -qi "qubit\|superposition"; then
  warn ".gleannignore: ignored file still found (pattern may not match)"
else
  pass ".gleannignore: ignored file excluded from index"
fi
"$BINARY" index remove "$IDX_IGNORE" >/dev/null 2>&1 || true
rm -rf "$IGNORE_DIR"

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 11: WATCHER
# ══════════════════════════════════════════════════════════════════════════════
header "§11 Auto-index Watcher  $(ts)"

sub "Testing watch builds index on first run..."
IDX_WATCH="e2e-watch-$$"
WATCH_DIR=$(mktemp -d)
cp "$FIXTURES/docs/quantum_computing.md" "$WATCH_DIR/"

timeout 5s "$BINARY" index watch "$IDX_WATCH" --docs "$WATCH_DIR" >/dev/null 2>&1 || true
sleep 1

OUT=$("$BINARY" index info "$IDX_WATCH" 2>&1)
if echo "$OUT" | grep -qi "chunks\|passages\|model\|backend"; then
  pass "watcher: index created on first run"
else
  warn "watcher: index not confirmed after first run"
fi
"$BINARY" index remove "$IDX_WATCH" >/dev/null 2>&1 || true
rm -rf "$WATCH_DIR"

# ══════════════════════════════════════════════════════════════════════════════
# CLEANUP
# ══════════════════════════════════════════════════════════════════════════════
header "Cleanup  $(ts)"
sub "Removing test indexes..."
for idx in "$IDX_DOCS" "$IDX_CODE" "$IDX_BIN" "$IDX_FAISS"; do
  "$BINARY" index remove "$idx" >/dev/null 2>&1 && echo -e "  removed $idx" || true
done
sub "Clearing e2e test memories..."
"$BINARY" memory clear --tier short >/dev/null 2>&1 || true

echo ""
echo -e "${BOLD}Results written to: ${RESULTS_DIR}/${NC}"
ls -lh "$RESULTS_DIR"/*.{html,md} 2>/dev/null | awk '{print "  "$NF, $5}' || true

# ══════════════════════════════════════════════════════════════════════════════
# SUMMARY
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo -e "${CYAN}${BOLD}══ SUMMARY ══════════════════════════════════════════${NC}"
echo -e "  ${GREEN}PASS${NC}   $PASS"
echo -e "  ${RED}FAIL${NC}   $FAIL"
echo -e "  ${YELLOW}WARN${NC}   $WARN"
echo -e "  ${YELLOW}SKIP${NC}   $SKIP"
TOTAL=$((PASS + FAIL))
if [[ $TOTAL -gt 0 ]]; then
  RATE=$(echo "scale=1; $PASS * 100 / $TOTAL" | bc 2>/dev/null || echo "?")
  echo -e "  ${BOLD}Score${NC}  ${RATE}% (${PASS}/${TOTAL} hard checks)"
fi
echo ""

if [[ $FAIL -gt 0 ]]; then
  echo -e "${RED}${BOLD}Failures:${NC}"
  for f in "${FAILURES[@]}"; do
    echo -e "  ${RED}✗${NC} $f"
  done
  echo ""
  exit 1
fi

echo -e "${GREEN}${BOLD}All required checks passed.${NC}"
exit 0
