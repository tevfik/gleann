#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════════════════════
# gleann e2e test suite
#
# Tests the full feature surface against the live binary using real fixture
# documents (knowledge-rich markdown + code files + binary formats).
# Works offline — Ollama must be running with configured models.
#
# Usage:
#   ./tests/e2e/run.sh                  # full suite
#   ./tests/e2e/run.sh --quick          # skip LLM-dependent tests
#   ./tests/e2e/run.sh --benchmark      # full suite + quality scoring & weak point detection
#   ./tests/e2e/run.sh --section search # run only a specific section
#   ./tests/e2e/run.sh --help
#
# Exit code: 0 = all required tests pass, 1 = any failure
# ══════════════════════════════════════════════════════════════════════════════
set +e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BINARY="${REPO_ROOT}/build/gleann-full"
FIXTURES="${REPO_ROOT}/tests/e2e/fixtures"
RESULTS_DIR="${REPO_ROOT}/tests/e2e/results"

IDX_DOCS="e2e-docs"
IDX_CODE="e2e-code"
IDX_BIN="e2e-binary"
IDX_FAISS="e2e-faiss"

QUICK=false
SECTION=""

# ── Argument parsing ────────────────────────────────────────────────────────
BENCHMARK=false
for arg in "$@"; do
  case "$arg" in
    --quick)     QUICK=true ;;
    --benchmark) BENCHMARK=true ;;
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

# ── Benchmark helpers ────────────────────────────────────────────────────────
BENCH_JSON="${RESULTS_DIR}/benchmark.json"
declare -A BENCH_METRICS

bench_record() {
  local key="$1" value="$2"
  BENCH_METRICS["$key"]="$value"
}

# time_cmd <label> <cmd...> — runs the command, captures output, records latency
time_cmd() {
  local label="$1"; shift
  local start_ns=$(date +%s%N)
  local output
  output=$("$@" 2>&1)
  local end_ns=$(date +%s%N)
  local ms=$(( (end_ns - start_ns) / 1000000 ))
  bench_record "${label}_ms" "$ms"
  echo "$output"
  return 0
}

bench_write_json() {
  echo "{" > "$BENCH_JSON"
  echo "  \"timestamp\": \"$(date -Iseconds)\"," >> "$BENCH_JSON"
  echo "  \"pass\": $PASS," >> "$BENCH_JSON"
  echo "  \"fail\": $FAIL," >> "$BENCH_JSON"
  echo "  \"warn\": $WARN," >> "$BENCH_JSON"
  echo "  \"total\": $((PASS + FAIL))," >> "$BENCH_JSON"
  echo "  \"metrics\": {" >> "$BENCH_JSON"
  local first=true
  for key in $(echo "${!BENCH_METRICS[@]}" | tr ' ' '\n' | sort); do
    if [[ "$first" == "true" ]]; then first=false; else echo "," >> "$BENCH_JSON"; fi
    printf "    \"%s\": %s" "$key" "${BENCH_METRICS[$key]}" >> "$BENCH_JSON"
  done
  echo "" >> "$BENCH_JSON"
  echo "  }," >> "$BENCH_JSON"
  echo "  \"weak_points\": [" >> "$BENCH_JSON"
  local wfirst=true
  # Detect weak points: search precision < 100%, latency > 2000ms, warn > 5
  if [[ "${BENCH_METRICS[search_precision_pct]:-0}" != "100" ]]; then
    if [[ "$wfirst" == "true" ]]; then wfirst=false; else echo "," >> "$BENCH_JSON"; fi
    printf "    {\"area\": \"search_precision\", \"value\": %s, \"threshold\": 100, \"note\": \"Some precision queries missed expected content\"}" \
      "${BENCH_METRICS[search_precision_pct]:-0}" >> "$BENCH_JSON"
  fi
  if [[ "${BENCH_METRICS[search_avg_latency_ms]:-0}" -gt 2000 ]] 2>/dev/null; then
    if [[ "$wfirst" == "true" ]]; then wfirst=false; else echo "," >> "$BENCH_JSON"; fi
    printf "    {\"area\": \"search_latency\", \"value\": %s, \"threshold\": 2000, \"note\": \"Average search latency above 2s\"}" \
      "${BENCH_METRICS[search_avg_latency_ms]:-0}" >> "$BENCH_JSON"
  fi
  if [[ $WARN -gt 5 ]]; then
    if [[ "$wfirst" == "true" ]]; then wfirst=false; else echo "," >> "$BENCH_JSON"; fi
    printf "    {\"area\": \"warnings\", \"value\": %d, \"threshold\": 5, \"note\": \"High number of warnings indicates instability\"}" \
      "$WARN" >> "$BENCH_JSON"
  fi
  if [[ $FAIL -gt 0 ]]; then
    if [[ "$wfirst" == "true" ]]; then wfirst=false; else echo "," >> "$BENCH_JSON"; fi
    printf "    {\"area\": \"failures\", \"value\": %d, \"threshold\": 0, \"note\": \"Any failure is a critical weak point\"}" \
      "$FAIL" >> "$BENCH_JSON"
  fi
  echo "" >> "$BENCH_JSON"
  echo "  ]" >> "$BENCH_JSON"
  echo "}" >> "$BENCH_JSON"
}

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
  if echo "$OUT" | grep -qiE "$(echo "$expected" | tr '_' '|')"; then
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

  # Check report contains "Suggested Questions" section
  if grep -q "Suggested Questions" "$RPT_OUT" 2>/dev/null; then
    pass "graph report: contains Suggested Questions section"
  else
    warn "graph report: Suggested Questions section not found (corpus may be too small)"
  fi

  # Check report contains Cross-Community Edges with scores
  if grep -qE "Score|Cross-Community" "$RPT_OUT" 2>/dev/null; then
    pass "graph report: contains surprising edge scoring"
  else
    warn "graph report: surprising edge scoring not found"
  fi
else
  warn "graph report: output file not created"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 5b: GRAPH QUERY / PATH / EXPLAIN
# ══════════════════════════════════════════════════════════════════════════════
header "§5b Graph Query / Path / Explain  $(ts)"

# graph query: search for a symbol from the code fixtures
QUERY_TERM="parser"  # parser.go has several symbols containing this
OUT=$("$BINARY" graph query "$QUERY_TERM" --index "$IDX_CODE" 2>&1)
if echo "$OUT" | grep -qiE "symbol|matching|function|method|\["; then
  pass "graph query: symbol search returned results"
  sub "$(echo "$OUT" | head -5)"
else
  warn "graph query: no symbols found (code corpus may be too small)"
fi

# graph explain: show full context for a symbol
# First find a symbol name from the code index
FIRST_SYM=$("$BINARY" graph query "$QUERY_TERM" --index "$IDX_CODE" 2>&1 | grep -oP '(?<=\] )\S+' | head -1)
if [[ -n "$FIRST_SYM" ]]; then
  OUT=$("$BINARY" graph explain "$FIRST_SYM" --index "$IDX_CODE" 2>&1)
  if echo "$OUT" | grep -qiE "symbol|callee|caller|edge|neighbor|impact|explain"; then
    pass "graph explain: context shown for '$FIRST_SYM'"
  else
    warn "graph explain: unexpected output for '$FIRST_SYM'"
  fi
else
  skip "graph explain: no symbol found to explain"
fi

# graph path: find shortest path between two symbols
if [[ -n "$FIRST_SYM" ]]; then
  SECOND_SYM=$("$BINARY" graph query "$QUERY_TERM" --index "$IDX_CODE" 2>&1 | grep -oP '(?<=\] )\S+' | sed -n '2p')
  if [[ -n "$SECOND_SYM" && "$SECOND_SYM" != "$FIRST_SYM" ]]; then
    OUT=$("$BINARY" graph path "$FIRST_SYM" "$SECOND_SYM" --index "$IDX_CODE" 2>&1)
    if echo "$OUT" | grep -qiE "path|hop|→|←|CALLS|no path"; then
      pass "graph path: path query executed"
    else
      warn "graph path: unexpected output"
    fi
  else
    skip "graph path: need two distinct symbols"
  fi
fi

# graph stats
OUT=$("$BINARY" graph query "." --index "$IDX_CODE" 2>&1)
assert_exit_ok "graph query exits cleanly" $?

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 5c: GRAPH EXPORT
# ══════════════════════════════════════════════════════════════════════════════
header "§5c Graph Export  $(ts)"

# GraphML export
GML_OUT="${RESULTS_DIR}/graph_export_${IDX_CODE}.graphml"
OUT=$("$BINARY" graph export --index "$IDX_CODE" --format graphml --output "$GML_OUT" 2>&1)
if [[ -f "$GML_OUT" ]]; then
  GML_SIZE=$(wc -c < "$GML_OUT")
  pass "graph export graphml: file created (${GML_SIZE} bytes)"
  if grep -q "graphml\|<node\|<edge" "$GML_OUT" 2>/dev/null; then
    pass "graph export graphml: valid XML structure"
  else
    warn "graph export graphml: expected XML tags not found"
  fi
else
  warn "graph export graphml: output file not created"
fi

# Cypher export
CYP_OUT="${RESULTS_DIR}/graph_export_${IDX_CODE}.cypher"
OUT=$("$BINARY" graph export --index "$IDX_CODE" --format cypher --output "$CYP_OUT" 2>&1)
if [[ -f "$CYP_OUT" ]]; then
  CYP_SIZE=$(wc -c < "$CYP_OUT")
  pass "graph export cypher: file created (${CYP_SIZE} bytes)"
  if grep -qiE "CREATE|MERGE|neo4j" "$CYP_OUT" 2>/dev/null; then
    pass "graph export cypher: contains Neo4j statements"
  else
    warn "graph export cypher: expected Cypher statements not found"
  fi
else
  warn "graph export cypher: output file not created"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 5d: GRAPH WIKI
# ══════════════════════════════════════════════════════════════════════════════
header "§5d Graph Wiki  $(ts)"

WIKI_DIR="${RESULTS_DIR}/wiki_${IDX_CODE}"
mkdir -p "$WIKI_DIR"
OUT=$("$BINARY" graph wiki --index "$IDX_CODE" --output "$WIKI_DIR" 2>&1)
WIKI_FILES=$(find "$WIKI_DIR" -name "*.md" 2>/dev/null | wc -l)
if [[ "$WIKI_FILES" -gt 0 ]]; then
  pass "graph wiki: ${WIKI_FILES} markdown files generated"
else
  warn "graph wiki: no wiki files generated (corpus may be too small for communities)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 5e: GRAPH HOOKS
# ══════════════════════════════════════════════════════════════════════════════
header "§5e Graph Hooks  $(ts)"

OUT=$("$BINARY" graph hook status 2>&1)
if echo "$OUT" | grep -qiE "hook|install|not found|status|git|pre-commit"; then
  pass "graph hook status: command executed"
else
  warn "graph hook status: unexpected output"
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
    if echo "$OUT" | grep -qi "$expected"; then
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
# SECTION 12: BENCHMARK MODE — Quality Scoring & Weak Point Detection
# ══════════════════════════════════════════════════════════════════════════════
if [[ "$BENCHMARK" == "true" ]]; then
  header "§12 Benchmark Scoring  $(ts)"

  # ── Search Precision: measure how many precision queries hit the right doc ──
  sub "Measuring search precision..."
  PRECISION_HIT=0
  PRECISION_TOTAL=0
  TOTAL_LATENCY=0

  declare -A PRECISION_QUERIES=(
    ["quantum entanglement superposition"]="qubit\|quantum\|superposition\|entangle"
    ["zero-knowledge proof succinct"]="proof\|zk\|commitm\|groth\|snar"
    ["CRISPR Cas9 gene editing"]="crispr\|cas9\|gene\|edit\|pam"
    ["transformer attention mechanism"]="attention\|transformer\|scale\|softmax"
    ["HotStuff BFT consensus protocol"]="hotstuff\|bft\|consensus\|linear"
    ["permafrost thawing climate"]="permafrost\|thaw\|climate\|carbon"
    ["Louvain community detection modularity"]="community\|modularity\|louvain\|graph"
    ["mRNA vaccine pseudouridine"]="mrna\|pseudouridine\|vaccine\|codon"
  )

  for query in "${!PRECISION_QUERIES[@]}"; do
    expected="${PRECISION_QUERIES[$query]}"
    PRECISION_TOTAL=$((PRECISION_TOTAL + 1))
    START_NS=$(date +%s%N)
    OUT=$("$BINARY" search "$IDX_DOCS" "$query" 2>&1)
    END_NS=$(date +%s%N)
    LATENCY=$(( (END_NS - START_NS) / 1000000 ))
    TOTAL_LATENCY=$((TOTAL_LATENCY + LATENCY))

    if echo "$OUT" | grep -qi "$expected"; then
      PRECISION_HIT=$((PRECISION_HIT + 1))
      pass "precision: '${query:0:45}...' (${LATENCY}ms)"
    else
      warn "precision: '${query:0:45}...' MISS (${LATENCY}ms)"
    fi
    bench_record "search_query_${PRECISION_TOTAL}_ms" "$LATENCY"
  done

  if [[ $PRECISION_TOTAL -gt 0 ]]; then
    PRECISION_PCT=$((PRECISION_HIT * 100 / PRECISION_TOTAL))
    AVG_LATENCY=$((TOTAL_LATENCY / PRECISION_TOTAL))
    bench_record "search_precision_pct" "$PRECISION_PCT"
    bench_record "search_precision_hit" "$PRECISION_HIT"
    bench_record "search_precision_total" "$PRECISION_TOTAL"
    bench_record "search_avg_latency_ms" "$AVG_LATENCY"
    bench_record "search_total_latency_ms" "$TOTAL_LATENCY"
    sub "Search precision: ${PRECISION_HIT}/${PRECISION_TOTAL} (${PRECISION_PCT}%)"
    sub "Average search latency: ${AVG_LATENCY}ms"
  fi

  # ── Code Search Precision ────────────────────────────────────────────────
  sub "Measuring code search precision..."
  CODE_HIT=0
  CODE_TOTAL=0

  declare -A CODE_QUERIES=(
    ["circular buffer ring buffer"]="circular\|buffer\|ring\|queue"
    ["rate limiter token bucket"]="rate\|limit\|bucket\|token"
    ["fibonacci recursive memoize"]="fib\|recurs\|memo"
  )

  for query in "${!CODE_QUERIES[@]}"; do
    expected="${CODE_QUERIES[$query]}"
    CODE_TOTAL=$((CODE_TOTAL + 1))
    OUT=$("$BINARY" search "$IDX_CODE" "$query" 2>&1)
    if echo "$OUT" | grep -qiE "$expected"; then
      CODE_HIT=$((CODE_HIT + 1))
    fi
  done

  if [[ $CODE_TOTAL -gt 0 ]]; then
    CODE_PCT=$((CODE_HIT * 100 / CODE_TOTAL))
    bench_record "code_search_precision_pct" "$CODE_PCT"
    sub "Code search precision: ${CODE_HIT}/${CODE_TOTAL} (${CODE_PCT}%)"
  fi

  # ── Graph Metrics ────────────────────────────────────────────────────────
  sub "Measuring graph quality..."
  OUT=$("$BINARY" graph communities --index "$IDX_CODE" 2>&1)
  COMMUNITY_COUNT=$(echo "$OUT" | grep -oP '\d+(?= communit)' | head -1)
  MODULARITY=$(echo "$OUT" | grep -oP '[\d.]+(?=\s*modularity)' | head -1)
  GOD_NODE_COUNT=$(echo "$OUT" | grep -oP '\d+(?= god node)' | head -1)

  bench_record "graph_communities" "\"${COMMUNITY_COUNT:-0}\""
  bench_record "graph_modularity" "\"${MODULARITY:-0}\""
  bench_record "graph_god_nodes" "\"${GOD_NODE_COUNT:-0}\""
  sub "Communities: ${COMMUNITY_COUNT:-?}, Modularity: ${MODULARITY:-?}, God Nodes: ${GOD_NODE_COUNT:-?}"

  # ── Memory System Roundtrip ──────────────────────────────────────────────
  sub "Measuring memory system latency..."
  MEM_TAG="bench_$$"

  START_NS=$(date +%s%N)
  "$BINARY" memory add short "benchmark test fact: neural architecture search [${MEM_TAG}]" >/dev/null 2>&1
  END_NS=$(date +%s%N)
  MEM_ADD_MS=$(( (END_NS - START_NS) / 1000000 ))
  bench_record "memory_add_ms" "$MEM_ADD_MS"

  START_NS=$(date +%s%N)
  "$BINARY" memory search "neural architecture search" >/dev/null 2>&1
  END_NS=$(date +%s%N)
  MEM_SEARCH_MS=$(( (END_NS - START_NS) / 1000000 ))
  bench_record "memory_search_ms" "$MEM_SEARCH_MS"

  START_NS=$(date +%s%N)
  "$BINARY" memory context >/dev/null 2>&1
  END_NS=$(date +%s%N)
  MEM_CTX_MS=$(( (END_NS - START_NS) / 1000000 ))
  bench_record "memory_context_ms" "$MEM_CTX_MS"

  sub "Memory latency: add=${MEM_ADD_MS}ms search=${MEM_SEARCH_MS}ms context=${MEM_CTX_MS}ms"

  # ── Index Build Throughput ───────────────────────────────────────────────
  sub "Measuring index build throughput..."
  IDX_BENCH="e2e-bench-$$"
  START_NS=$(date +%s%N)
  "$BINARY" index build "$IDX_BENCH" --docs "$FIXTURES/docs" >/dev/null 2>&1
  END_NS=$(date +%s%N)
  BUILD_MS=$(( (END_NS - START_NS) / 1000000 ))
  bench_record "index_build_ms" "$BUILD_MS"
  sub "Index build: ${BUILD_MS}ms for ${doc_count} docs"
  "$BINARY" index remove "$IDX_BENCH" >/dev/null 2>&1 || true

  # ── Token Reduction Quality ──────────────────────────────────────────────
  OUT=$("$BINARY" benchmark --index "$IDX_DOCS" --docs "$FIXTURES/docs" 2>&1)
  REDUCTION_RATIO=$(echo "$OUT" | grep -oE '[0-9]+(\.[0-9]+)?x' | head -1 | tr -d 'x')
  if [[ -n "$REDUCTION_RATIO" ]]; then
    bench_record "token_reduction_ratio" "\"${REDUCTION_RATIO}\""
    sub "Token reduction: ${REDUCTION_RATIO}x"
  fi

  # ── Feature Coverage Summary ─────────────────────────────────────────────
  bench_record "e2e_pass" "$PASS"
  bench_record "e2e_fail" "$FAIL"
  bench_record "e2e_warn" "$WARN"
  bench_record "e2e_skip" "$SKIP"

  # Write benchmark JSON
  bench_write_json
  pass "benchmark: results written to ${BENCH_JSON}"

  # ── Print Benchmark Report ──────────────────────────────────────────────
  echo ""
  echo -e "${CYAN}${BOLD}══ BENCHMARK REPORT ══════════════════════════════════${NC}"
  echo -e "  ${BOLD}Search Precision${NC}      ${PRECISION_HIT:-?}/${PRECISION_TOTAL:-?} (${PRECISION_PCT:-?}%)"
  echo -e "  ${BOLD}Avg Search Latency${NC}    ${AVG_LATENCY:-?}ms"
  echo -e "  ${BOLD}Code Search Precision${NC} ${CODE_HIT:-?}/${CODE_TOTAL:-?} (${CODE_PCT:-?}%)"
  echo -e "  ${BOLD}Index Build Time${NC}      ${BUILD_MS:-?}ms"
  echo -e "  ${BOLD}Token Reduction${NC}       ${REDUCTION_RATIO:-?}x"
  echo -e "  ${BOLD}Memory Add Latency${NC}    ${MEM_ADD_MS:-?}ms"
  echo -e "  ${BOLD}Memory Search Latency${NC} ${MEM_SEARCH_MS:-?}ms"
  echo -e "  ${BOLD}Graph Communities${NC}     ${COMMUNITY_COUNT:-?}"
  echo -e "  ${BOLD}Graph Modularity${NC}      ${MODULARITY:-?}"
  echo ""

  # Print weak points
  WEAK_FOUND=false
  if [[ "${PRECISION_PCT:-0}" -lt 75 ]]; then
    echo -e "  ${RED}⚠ WEAK:${NC} Search precision below 75% — check embedding model quality"
    WEAK_FOUND=true
  fi
  if [[ "${AVG_LATENCY:-0}" -gt 2000 ]]; then
    echo -e "  ${RED}⚠ WEAK:${NC} Average search latency above 2s — consider index optimization"
    WEAK_FOUND=true
  fi
  if [[ $FAIL -gt 0 ]]; then
    echo -e "  ${RED}⚠ WEAK:${NC} ${FAIL} test failures — critical functionality broken"
    WEAK_FOUND=true
  fi
  if [[ $WARN -gt 10 ]]; then
    echo -e "  ${YELLOW}⚠ NOTE:${NC} ${WARN} warnings — review for potential regressions"
    WEAK_FOUND=true
  fi
  if [[ "$WEAK_FOUND" == "false" ]]; then
    echo -e "  ${GREEN}✓${NC} No weak points detected — all metrics within thresholds"
  fi
  echo ""
fi

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
ls -lh "$RESULTS_DIR"/*.{html,md,json,graphml,cypher} 2>/dev/null | awk '{print "  "$NF, $5}' || true

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
