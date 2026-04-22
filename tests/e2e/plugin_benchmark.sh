#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════════════════════
# gleann Plugin Benchmark — Extraction Backend Comparison
#
# Compares gleann-plugin-docs (markitdown + docling) and gleann-plugin-marker
# across speed and quality metrics using the same test documents.
#
# Backends tested:
#   1. markitdown CLI (Layer 0 — Go-native via MarkItDownExtractor)
#   2. gleann-plugin-docs/convert (markitdown or docling via HTTP)
#   3. gleann-plugin-marker/convert (marker-pdf via HTTP)
#
# Metrics:
#   Speed:   latency_ms, throughput (docs/sec)
#   Quality: section_count, heading_detection_rate, hierarchy_depth,
#            word_count, content_fidelity (keyword overlap), markdown_bytes
#
# Usage:
#   ./tests/e2e/plugin_benchmark.sh                 # full benchmark
#   ./tests/e2e/plugin_benchmark.sh --quick          # PDF only, 1 iteration
#   ./tests/e2e/plugin_benchmark.sh --format pdf     # specific format
#   ./tests/e2e/plugin_benchmark.sh --json           # JSON output only
#
# Prerequisites:
#   - gleann binary built (build/gleann-full)
#   - Both plugins installed: gleann-docs, gleann-marker
#   - markitdown CLI installed (for Layer 0 comparison)
#
# Output:
#   tests/e2e/results/plugin_benchmark.json
#   tests/e2e/results/plugin_benchmark.md (Markdown table)
# ══════════════════════════════════════════════════════════════════════════════
set +e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FIXTURES="${REPO_ROOT}/tests/e2e/fixtures/binary"
RESULTS_DIR="${REPO_ROOT}/tests/e2e/results"
BINARY="${REPO_ROOT}/build/gleann-full"

PLUGIN_DOCS_PORT=8765
PLUGIN_MARKER_PORT=8766
PLUGIN_DOCS_URL="http://localhost:${PLUGIN_DOCS_PORT}"
PLUGIN_MARKER_URL="http://localhost:${PLUGIN_MARKER_PORT}"

ITERATIONS=3
QUICK=false
JSON_ONLY=false
FORMAT_FILTER=""

# ── Argument parsing ────────────────────────────────────────────────────────
for arg in "$@"; do
  case "$arg" in
    --quick)     QUICK=true; ITERATIONS=1 ;;
    --json)      JSON_ONLY=true ;;
  esac
done
for i in $(seq 1 $#); do
  arg="${!i}"
  if [[ "$arg" == "--format" ]]; then
    next=$((i+1))
    FORMAT_FILTER="${!next}"
  fi
done

# ── Colours ─────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

log()    { [[ "$JSON_ONLY" == "false" ]] && echo -e "$@"; }
header() { log "\n${CYAN}${BOLD}══ $1 ══${NC}"; }
info()   { log "  ${BLUE}▸${NC} $1"; }
pass()   { log "  ${GREEN}✓${NC} $1"; }
warn()   { log "  ${YELLOW}⚠${NC} $1"; }
fail()   { log "  ${RED}✗${NC} $1"; }

# ── Ground truth definitions ────────────────────────────────────────────────
# Each test document has expected sections and keywords for quality scoring.
# Format: FILE|EXPECTED_SECTIONS|EXPECTED_KEYWORDS (pipe-separated)
#
# EXPECTED_SECTIONS: comma-separated heading strings the backend should detect.
# EXPECTED_KEYWORDS: comma-separated words that must appear in extracted text.

declare -A GROUND_TRUTH_SECTIONS
declare -A GROUND_TRUTH_KEYWORDS
declare -A GROUND_TRUTH_DEPTH

# benchmark_report.pdf — structured 2-page PDF with headings + table
GROUND_TRUTH_SECTIONS["benchmark_report.pdf"]="Introduction,Methodology,Test Corpus,Metrics Definition,Results,PDF Extraction,DOCX Extraction,Image-based Documents,Conclusion,Future Work"
GROUND_TRUTH_KEYWORDS["benchmark_report.pdf"]="extraction,knowledge,MarkItDown,Docling,Marker,OCR,semantic,accuracy,latency,ensemble"
GROUND_TRUTH_DEPTH["benchmark_report.pdf"]=3  # H1 → H2 → H3

# technical_report.docx — Word doc with headings
GROUND_TRUTH_SECTIONS["technical_report.docx"]=""
GROUND_TRUTH_KEYWORDS["technical_report.docx"]=""
GROUND_TRUTH_DEPTH["technical_report.docx"]=0

# benchmark_data.xlsx — spreadsheet
GROUND_TRUTH_SECTIONS["benchmark_data.xlsx"]=""
GROUND_TRUTH_KEYWORDS["benchmark_data.xlsx"]="data,benchmark"
GROUND_TRUTH_DEPTH["benchmark_data.xlsx"]=0

# research_slides.pptx — presentation
GROUND_TRUTH_SECTIONS["research_slides.pptx"]=""
GROUND_TRUTH_KEYWORDS["research_slides.pptx"]="research,slide"
GROUND_TRUTH_DEPTH["research_slides.pptx"]=0

# api_reference.html — HTML page
GROUND_TRUTH_SECTIONS["api_reference.html"]="Base URL,Index Operations,Search,Memory API,OpenAI-Compatible Proxy"
GROUND_TRUTH_KEYWORDS["api_reference.html"]="api,endpoint,request,search,memory,index,query,semantic"
GROUND_TRUTH_DEPTH["api_reference.html"]=3

# ── Helper functions ────────────────────────────────────────────────────────

# time_ms: run command, echo elapsed ms. Output goes to $BENCH_OUTPUT_FILE.
BENCH_OUTPUT_FILE=$(mktemp)
BENCH_METRICS_FILE=$(mktemp)
trap "rm -f $BENCH_OUTPUT_FILE $BENCH_METRICS_FILE" EXIT

time_ms() {
  local start_ns end_ns ms
  start_ns=$(date +%s%N)
  "$@" > "$BENCH_OUTPUT_FILE" 2>/dev/null
  local exit_code=$?
  end_ns=$(date +%s%N)
  ms=$(( (end_ns - start_ns) / 1000000 ))
  echo "$ms"
  return $exit_code
}

# analyze_output: compute all quality metrics from JSON in $BENCH_OUTPUT_FILE.
# Writes a key=value file to $BENCH_METRICS_FILE.
analyze_output() {
  local expected_sections="$1"
  local expected_keywords="$2"
  local expected_depth="$3"

  python3 << 'PYEOF' - "$expected_sections" "$expected_keywords" "$expected_depth" "$BENCH_OUTPUT_FILE" "$BENCH_METRICS_FILE"
import json, sys, re, os

expected_sections = sys.argv[1]
expected_keywords = sys.argv[2]
expected_depth = int(sys.argv[3]) if sys.argv[3] else 0
input_file = sys.argv[4]
output_file = sys.argv[5]

try:
    with open(input_file) as f:
        data = json.load(f)
except Exception:
    data = {}

nodes = data.get('nodes', [])
edges = data.get('edges', [])
markdown = data.get('markdown', '')
backend = data.get('backend', 'unknown')

# Section count
sections = [n for n in nodes if n.get('_type') == 'Section']
section_count = len(sections)
edge_count = len(edges)

# Word count
word_count = len(markdown.split())

# Markdown bytes
md_bytes = len(markdown.encode('utf-8'))

# Max heading depth
max_depth = 0
for line in markdown.split('\n'):
    m = re.match(r'^(#{1,6})\s', line)
    if m:
        d = len(m.group(1))
        if d > max_depth:
            max_depth = d

# Heading detection rate
if expected_sections:
    exp_list = [h.strip() for h in expected_sections.split(',') if h.strip()]
    found = sum(1 for h in exp_list if h.lower() in markdown.lower())
    heading_pct = int(found * 100 / len(exp_list)) if exp_list else 100
else:
    heading_pct = 100

# Keyword fidelity
if expected_keywords:
    kw_list = [k.strip() for k in expected_keywords.split(',') if k.strip()]
    found_kw = sum(1 for k in kw_list if k.lower() in markdown.lower())
    keyword_pct = int(found_kw * 100 / len(kw_list)) if kw_list else 100
else:
    keyword_pct = 100

with open(output_file, 'w') as f:
    f.write(f"backend={backend}\n")
    f.write(f"sections={section_count}\n")
    f.write(f"edges={edge_count}\n")
    f.write(f"heading_pct={heading_pct}\n")
    f.write(f"keyword_pct={keyword_pct}\n")
    f.write(f"max_depth={max_depth}\n")
    f.write(f"word_count={word_count}\n")
    f.write(f"md_bytes={md_bytes}\n")
PYEOF
}

# read_metric: read a metric from $BENCH_METRICS_FILE.
read_metric() {
  grep "^${1}=" "$BENCH_METRICS_FILE" | cut -d= -f2
}

# ── Plugin health check + auto-start ────────────────────────────────────────

start_plugin() {
  local name="$1" url="$2" port="$3"

  # Check if already running.
  if curl -s "${url}/health" >/dev/null 2>&1; then
    pass "${name} already running on port ${port}"
    return 0
  fi

  info "Starting ${name}..."

  # Use gleann's plugins.json to find the command.
  local cmd
  cmd=$(python3 -c "
import json
with open('$HOME/.gleann/plugins.json') as f:
    reg = json.load(f)
for p in reg['plugins']:
    if p['name'] == '${name}':
        print(' '.join(p['command']))
        break
" 2>/dev/null)

  if [[ -z "$cmd" ]]; then
    fail "${name} not found in plugins.json"
    return 1
  fi

  # Start in background.
  eval "$cmd" &>/dev/null &
  local pid=$!

  # Wait for health.
  for i in $(seq 1 20); do
    if curl -s "${url}/health" >/dev/null 2>&1; then
      pass "${name} started (PID ${pid}, port ${port})"
      return 0
    fi
    sleep 0.5
  done

  fail "${name} failed to start within 10s"
  return 1
}

stop_plugin() {
  local port="$1"
  local pid
  pid=$(lsof -ti ":${port}" 2>/dev/null | head -1)
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null
    info "Stopped plugin on port ${port} (PID ${pid})"
  fi
}

# ── Convert via plugin HTTP endpoint ────────────────────────────────────────

convert_via_plugin() {
  local url="$1" filepath="$2"
  curl -s -X POST "${url}/convert" \
    -F "file=@${filepath}" \
    -H "Accept: application/json" \
    --max-time 120 2>/dev/null
}

# ── Convert via markitdown CLI (Layer 0) ────────────────────────────────────

convert_via_markitdown() {
  local filepath="$1"
  local md_tmp
  md_tmp=$(mktemp)
  markitdown "$filepath" > "$md_tmp" 2>/dev/null

  if [[ ! -s "$md_tmp" ]]; then
    echo '{"nodes":[],"edges":[],"markdown":"","backend":"markitdown-cli"}'
    rm -f "$md_tmp"
    return
  fi

  # Build a PluginResult-compatible JSON with section parsing.
  python3 << 'PYEOF' - "$filepath" "$md_tmp"
import json, re, sys, os

filepath = sys.argv[1]
md_file = sys.argv[2]
with open(md_file) as f:
    md = f.read()

fname = os.path.basename(filepath)
ext = os.path.splitext(fname)[1].lstrip('.').lower()
doc_id = f'doc:{fname}'

nodes = []
edges = []

# Document node
nodes.append({'_type': 'Document', 'title': fname, 'format': ext})

# Parse headings
sections = []
stack = []  # (level, section_id)
for line in md.split('\n'):
    m = re.match(r'^(#{1,6})\s+(.*)', line)
    if m:
        level = len(m.group(1))
        heading = m.group(2).strip().strip('*')
        sid = f'{doc_id}:s{len(sections)}'
        sections.append({'id': sid, 'heading': heading, 'level': level})
        nodes.append({'_type': 'Section', 'id': sid, 'heading': heading, 'level': level})

        # Find parent
        while stack and stack[-1][0] >= level:
            stack.pop()

        if stack:
            parent_id = stack[-1][1]
            edges.append({'_type': 'HAS_SUBSECTION', 'from': parent_id, 'to': sid})
        else:
            edges.append({'_type': 'HAS_SECTION', 'from': doc_id, 'to': sid})

        stack.append((level, sid))

print(json.dumps({
    'nodes': nodes,
    'edges': edges,
    'markdown': md,
    'backend': 'markitdown-cli'
}))
PYEOF
  rm -f "$md_tmp"
}

# ══════════════════════════════════════════════════════════════════════════════
# MAIN
# ══════════════════════════════════════════════════════════════════════════════

mkdir -p "$RESULTS_DIR"

header "Gleann Plugin Benchmark"
log "  Iterations: ${ITERATIONS}"
log "  Fixtures:   ${FIXTURES}"
log "  Results:    ${RESULTS_DIR}"

# ── 1. Prerequisite checks ──────────────────────────────────────────────────
header "Prerequisites"

MARKITDOWN_OK=false
if command -v markitdown &>/dev/null; then
  pass "markitdown CLI found: $(which markitdown)"
  MARKITDOWN_OK=true
else
  warn "markitdown CLI not found — Layer 0 tests will be skipped"
fi

DOCS_OK=false
if start_plugin "gleann-plugin-docs" "$PLUGIN_DOCS_URL" "$PLUGIN_DOCS_PORT"; then
  DOCS_OK=true
fi

MARKER_OK=false
if start_plugin "gleann-plugin-marker" "$PLUGIN_MARKER_URL" "$PLUGIN_MARKER_PORT"; then
  MARKER_OK=true
fi

# Identify active backends from plugin-docs.
DOCS_BACKEND="unknown"
if [[ "$DOCS_OK" == "true" ]]; then
  DOCS_HEALTH=$(curl -s "${PLUGIN_DOCS_URL}/health" 2>/dev/null)
  DOCS_BACKEND=$(echo "$DOCS_HEALTH" | python3 -c "
import sys, json
try:
    h = json.load(sys.stdin)
    backends = h.get('backends', {})
    if backends.get('docling'):
        print('docling')
    elif backends.get('markitdown'):
        print('markitdown')
    else:
        print('unknown')
except: print('unknown')
" 2>/dev/null)
  info "gleann-plugin-docs active backend: ${DOCS_BACKEND}"
fi

# ── 2. Define test files ────────────────────────────────────────────────────
header "Test Documents"

declare -a TEST_FILES
for f in "$FIXTURES"/*; do
  fname=$(basename "$f")
  ext="${fname##*.}"

  # Skip .gitkeep and non-document files.
  [[ "$fname" == ".gitkeep" ]] && continue
  [[ "$ext" == "csv" ]] && continue  # CSV has no sections, not useful for quality comparison

  # Format filter.
  if [[ -n "$FORMAT_FILTER" ]] && [[ "$ext" != "$FORMAT_FILTER" ]]; then
    continue
  fi

  TEST_FILES+=("$f")
  info "$fname (${ext}, $(du -h "$f" | cut -f1))"
done

if [[ ${#TEST_FILES[@]} -eq 0 ]]; then
  fail "No test files found in ${FIXTURES}"
  exit 1
fi

# ── 3. Run benchmarks ──────────────────────────────────────────────────────
header "Running Benchmarks"

# Results accumulator: JSON lines in a temp file.
RESULTS_TMPFILE=$(mktemp)
echo "[" > "$RESULTS_TMPFILE"
FIRST_RESULT=true

# emit_result: append a JSON result line using metrics from $BENCH_METRICS_FILE.
emit_result() {
  local fname="$1" ext="$2" backend_label="$3" avg_ms="$4" expected_depth="$5"

  local backend sections edges heading_pct keyword_pct max_depth word_count md_bytes
  backend=$(read_metric backend)
  sections=$(read_metric sections)
  edges=$(read_metric edges)
  heading_pct=$(read_metric heading_pct)
  keyword_pct=$(read_metric keyword_pct)
  max_depth=$(read_metric max_depth)
  word_count=$(read_metric word_count)
  md_bytes=$(read_metric md_bytes)

  [[ "$FIRST_RESULT" == "false" ]] && echo "," >> "$RESULTS_TMPFILE"
  FIRST_RESULT=false

  python3 -c "
import json
print(json.dumps({
    'file': '$fname',
    'format': '$ext',
    'backend': '$backend_label',
    'latency_ms': $avg_ms,
    'iterations': $ITERATIONS,
    'sections': ${sections:-0},
    'edges': ${edges:-0},
    'heading_detection_pct': ${heading_pct:-0},
    'keyword_fidelity_pct': ${keyword_pct:-0},
    'max_heading_depth': ${max_depth:-0},
    'expected_depth': ${expected_depth:-0},
    'word_count': ${word_count:-0},
    'markdown_bytes': ${md_bytes:-0}
}))
" >> "$RESULTS_TMPFILE"
}

for filepath in "${TEST_FILES[@]}"; do
  fname=$(basename "$filepath")
  ext="${fname##*.}"

  log "\n  ${BOLD}${fname}${NC}"

  expected_sections="${GROUND_TRUTH_SECTIONS[$fname]:-}"
  expected_keywords="${GROUND_TRUTH_KEYWORDS[$fname]:-}"
  expected_depth="${GROUND_TRUTH_DEPTH[$fname]:-0}"

  # ── Backend 1: markitdown CLI ──
  if [[ "$MARKITDOWN_OK" == "true" ]]; then
    total_ms=0
    for i in $(seq 1 "$ITERATIONS"); do
      ms=$(time_ms convert_via_markitdown "$filepath")
      total_ms=$((total_ms + ms))
    done
    avg_ms=$((total_ms / ITERATIONS))

    analyze_output "$expected_sections" "$expected_keywords" "$expected_depth"
    sections=$(read_metric sections)
    heading_pct=$(read_metric heading_pct)
    keyword_pct=$(read_metric keyword_pct)

    pass "markitdown-cli: ${avg_ms}ms, ${sections} sections, ${heading_pct}% headings, ${keyword_pct}% keywords"
    emit_result "$fname" "$ext" "markitdown-cli" "$avg_ms" "$expected_depth"
  fi

  # ── Backend 2: gleann-plugin-docs ──
  if [[ "$DOCS_OK" == "true" ]]; then
    total_ms=0
    for i in $(seq 1 "$ITERATIONS"); do
      ms=$(time_ms convert_via_plugin "$PLUGIN_DOCS_URL" "$filepath")
      total_ms=$((total_ms + ms))
    done
    avg_ms=$((total_ms / ITERATIONS))

    analyze_output "$expected_sections" "$expected_keywords" "$expected_depth"
    backend=$(read_metric backend)
    sections=$(read_metric sections)
    heading_pct=$(read_metric heading_pct)
    keyword_pct=$(read_metric keyword_pct)

    pass "plugin-docs (${backend}): ${avg_ms}ms, ${sections} sections, ${heading_pct}% headings, ${keyword_pct}% keywords"
    emit_result "$fname" "$ext" "plugin-docs/${backend}" "$avg_ms" "$expected_depth"
  fi

  # ── Backend 3: gleann-plugin-marker ──
  if [[ "$MARKER_OK" == "true" ]]; then
    marker_supported=true
    case "$ext" in
      xlsx|xls|csv) marker_supported=false ;;
    esac

    if [[ "$marker_supported" == "true" ]]; then
      total_ms=0
      for i in $(seq 1 "$ITERATIONS"); do
        ms=$(time_ms convert_via_plugin "$PLUGIN_MARKER_URL" "$filepath")
        total_ms=$((total_ms + ms))
      done
      avg_ms=$((total_ms / ITERATIONS))

      analyze_output "$expected_sections" "$expected_keywords" "$expected_depth"
      sections=$(read_metric sections)
      heading_pct=$(read_metric heading_pct)
      keyword_pct=$(read_metric keyword_pct)

      pass "plugin-marker: ${avg_ms}ms, ${sections} sections, ${heading_pct}% headings, ${keyword_pct}% keywords"
      emit_result "$fname" "$ext" "plugin-marker" "$avg_ms" "$expected_depth"
    else
      warn "plugin-marker: skipped ${fname} (unsupported format)"
    fi
  fi
done

echo "]" >> "$RESULTS_TMPFILE"

# ── 4. Write results JSON ──────────────────────────────────────────────────
header "Writing Results"

cat "$RESULTS_TMPFILE" | python3 -c "
import json, sys
from datetime import datetime

results = json.load(sys.stdin)

output = {
    'timestamp': datetime.now().isoformat(),
    'iterations': $ITERATIONS,
    'results': results,
    'summary': {}
}

# Aggregate by backend.
backends = {}
for r in results:
    b = r['backend']
    if b not in backends:
        backends[b] = {'latency': [], 'sections': [], 'headings': [], 'keywords': [], 'files': 0}
    backends[b]['latency'].append(r['latency_ms'])
    backends[b]['sections'].append(r['sections'])
    backends[b]['headings'].append(r['heading_detection_pct'])
    backends[b]['keywords'].append(r['keyword_fidelity_pct'])
    backends[b]['files'] += 1

for b, data in backends.items():
    output['summary'][b] = {
        'avg_latency_ms': int(sum(data['latency']) / len(data['latency'])),
        'avg_sections': round(sum(data['sections']) / len(data['sections']), 1),
        'avg_heading_detection_pct': round(sum(data['headings']) / len(data['headings']), 1),
        'avg_keyword_fidelity_pct': round(sum(data['keywords']) / len(data['keywords']), 1),
        'files_tested': data['files']
    }

print(json.dumps(output, indent=2))
" > "${RESULTS_DIR}/plugin_benchmark.json"

pass "JSON results: ${RESULTS_DIR}/plugin_benchmark.json"

# ── 5. Generate Markdown table ──────────────────────────────────────────────

python3 -c "
import json, sys

with open('${RESULTS_DIR}/plugin_benchmark.json') as f:
    data = json.load(f)

results = data['results']
summary = data['summary']
ts = data['timestamp']

print('# Plugin Extraction Benchmark Results')
print()
print(f'> Generated: {ts}  ')
print(f'> Iterations per test: {data[\"iterations\"]}')
print()

# Metrics explanation
print('## Metrics')
print()
print('| Metric | Description | Measurement |')
print('|--------|-------------|-------------|')
print('| **Latency** | Wall-clock time for document conversion | Averaged over N iterations (ms) |')
print('| **Sections** | Number of Section nodes in output graph | Count of \`_type=Section\` nodes |')
print('| **Heading Detection** | % of known headings found in output | Case-insensitive text match against ground truth |')
print('| **Keyword Fidelity** | % of expected keywords present in text | Case-insensitive match against ground-truth keywords |')
print('| **Max Depth** | Deepest heading level detected | Count of \`#\` in markdown headings (1-6) |')
print('| **Word Count** | Total words in extracted markdown | \`wc -w\` on markdown output |')
print('| **Markdown Size** | Raw markdown output size | Byte length |')
print()

# Per-file detail table
print('## Detailed Results')
print()
print('| File | Backend | Latency (ms) | Sections | Edges | Heading Det. | Keyword Fid. | Depth | Words | MD Size |')
print('|------|---------|:--------:|:--------:|:-----:|:------------:|:------------:|:-----:|:-----:|:-------:|')

for r in sorted(results, key=lambda x: (x['file'], x['backend'])):
    print(f'| {r[\"file\"]} | {r[\"backend\"]} | {r[\"latency_ms\"]} | {r[\"sections\"]} | {r[\"edges\"]} | {r[\"heading_detection_pct\"]}% | {r[\"keyword_fidelity_pct\"]}% | {r[\"max_heading_depth\"]}/{r[\"expected_depth\"]} | {r[\"word_count\"]} | {r[\"markdown_bytes\"]} |')

print()

# Summary table
print('## Summary by Backend')
print()
print('| Backend | Avg Latency | Avg Sections | Heading Det. | Keyword Fid. | Files |')
print('|---------|:-----------:|:------------:|:------------:|:------------:|:-----:|')

for b, s in sorted(summary.items()):
    print(f'| {b} | {s[\"avg_latency_ms\"]}ms | {s[\"avg_sections\"]} | {s[\"avg_heading_detection_pct\"]}% | {s[\"avg_keyword_fidelity_pct\"]}% | {s[\"files_tested\"]} |')

print()

# Analysis
print('## Analysis')
print()

# Find best backend per metric
if len(summary) >= 2:
    fastest = min(summary.items(), key=lambda x: x[1]['avg_latency_ms'])
    most_sections = max(summary.items(), key=lambda x: x[1]['avg_sections'])
    best_headings = max(summary.items(), key=lambda x: x[1]['avg_heading_detection_pct'])
    best_keywords = max(summary.items(), key=lambda x: x[1]['avg_keyword_fidelity_pct'])

    print(f'- **Fastest**: {fastest[0]} ({fastest[1][\"avg_latency_ms\"]}ms avg)')
    print(f'- **Most Sections Detected**: {most_sections[0]} ({most_sections[1][\"avg_sections\"]} avg)')
    print(f'- **Best Heading Detection**: {best_headings[0]} ({best_headings[1][\"avg_heading_detection_pct\"]}%)')
    print(f'- **Best Content Fidelity**: {best_keywords[0]} ({best_keywords[1][\"avg_keyword_fidelity_pct\"]}%)')
    print()

    # Recommendations
    print('### Recommendations')
    print()
    print('| Use Case | Recommended Backend | Reason |')
    print('|----------|-------------------|--------|')
    print(f'| Speed-critical | {fastest[0]} | Lowest latency |')
    print(f'| Structure-rich | {most_sections[0]} | Best section detection |')
    print(f'| High fidelity | {best_keywords[0]} | Best content preservation |')

    # PDF-specific comparison
    pdf_results = [r for r in results if r['format'] == 'pdf']
    if len(pdf_results) >= 2:
        print()
        print('### PDF-Specific Comparison')
        print()
        for r in sorted(pdf_results, key=lambda x: x['backend']):
            print(f'- **{r[\"backend\"]}**: {r[\"latency_ms\"]}ms, {r[\"sections\"]} sections, {r[\"heading_detection_pct\"]}% headings, {r[\"word_count\"]} words')
" > "${RESULTS_DIR}/plugin_benchmark.md"

pass "Markdown report: ${RESULTS_DIR}/plugin_benchmark.md"

# ── 6. Print summary ───────────────────────────────────────────────────────
if [[ "$JSON_ONLY" == "false" ]]; then
  header "Summary"
  python3 -c "
import json
with open('${RESULTS_DIR}/plugin_benchmark.json') as f:
    data = json.load(f)
for b, s in sorted(data['summary'].items()):
    lat = s['avg_latency_ms']
    sec = s['avg_sections']
    hdr = s['avg_heading_detection_pct']
    kwd = s['avg_keyword_fidelity_pct']
    print(f'  {b:30s}  {lat:>6d}ms  {sec:>5.1f} secs  {hdr:>5.1f}% head  {kwd:>5.1f}% kwd')
" 2>/dev/null

  echo ""
  log "  ${GREEN}Done.${NC} Results at: ${RESULTS_DIR}/plugin_benchmark.{json,md}"
fi
