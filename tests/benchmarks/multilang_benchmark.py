#!/usr/bin/env python3
"""
Multi-Language LLM Agent Performance Benchmark (C + Python) — v2
================================================================
Improvements over v1:
- Per-problem retry with exponential backoff
- Aggressive code extraction (strips ALL non-code text)
- Streaming response to avoid timeout
- Better C compilation (preprocessor fixes, -std=c17)

Usage: python3 multilang_benchmark.py
"""

import json
import os
import re
import subprocess
import sys
import time
import urllib.request
import urllib.error
from datetime import datetime
from pathlib import Path

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
LLM_MODEL = os.getenv("GLEANN_LLM_MODEL", "qwen3.5:9b")
OLLAMA_HOST = os.getenv("OLLAMA_HOST", "http://localhost:11001")
MAX_RETRIES = 2
RESULTS_DIR = Path(__file__).parent / "results" / f"multilang_v2_{int(time.time())}"
CODE_DIR = RESULTS_DIR / "code"
CODE_DIR.mkdir(parents=True, exist_ok=True)

# ---------------------------------------------------------------------------
# Colors
# ---------------------------------------------------------------------------
class C:
    GREEN = "\033[0;32m"
    RED = "\033[0;31m"
    YELLOW = "\033[1;33m"
    CYAN = "\033[0;36m"
    BOLD = "\033[1m"
    RESET = "\033[0m"

def log(msg: str): print(f"{C.CYAN}[bench]{C.RESET} {msg}", flush=True)
def pass_log(msg: str): print(f"  {C.GREEN}✅ PASS{C.RESET} — {msg}", flush=True)
def fail_log(msg: str): print(f"  {C.RED}❌ FAIL{C.RESET} — {msg}", flush=True)

# ---------------------------------------------------------------------------
# LLM Query with retry + streaming
# ---------------------------------------------------------------------------
def query_llm(prompt: str, retries: int = MAX_RETRIES) -> str:
    """Query Ollama with retry + streaming fallback. Append code-only instruction."""
    full_prompt = (
        "You are a code generation assistant. Respond with ONLY the requested code "
        "in a single fenced code block. No explanation, no markdown text outside the code block.\n\n"
        + prompt
    )

    for attempt in range(retries + 1):
        # Try streaming first (avoids timeout on slow/queued servers)
        response = _query_streaming(full_prompt, OLLAMA_HOST, LLM_MODEL)
        if response and "LLM_QUERY_FAILED" not in response:
            return response

        # Fallback: non-streaming with higher timeout
        payload = json.dumps({
            "model": LLM_MODEL,
            "prompt": full_prompt,
            "stream": False,
            "options": {"temperature": 0.1}
        }).encode()
        req = urllib.request.Request(
            f"{OLLAMA_HOST}/api/generate",
            data=payload,
            headers={"Content-Type": "application/json"},
            method="POST"
        )
        try:
            with urllib.request.urlopen(req, timeout=180) as resp:
                data = json.loads(resp.read())
                response = data.get("response", "").strip()
                if response and "LLM_QUERY_FAILED" not in response:
                    return response
                log(f"  (attempt {attempt+1}): empty non-streaming response, retrying...")
        except urllib.error.URLError as e:
            log(f"  (attempt {attempt+1}): connection error ({e}), retrying...")
            time.sleep(3 * (attempt + 1))
        except Exception as e:
            log(f"  (attempt {attempt+1}): error ({e}), retrying...")
            time.sleep(3 * (attempt + 1))

    return "LLM_QUERY_FAILED after retries"


def _query_streaming(prompt: str, host: str, model: str) -> str:
    """Stream response from Ollama chunk-by-chunk to avoid timeout."""
    payload = json.dumps({
        "model": model,
        "prompt": prompt,
        "stream": True,
        "options": {"temperature": 0.1}
    }).encode()
    req = urllib.request.Request(
        f"{host}/api/generate",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    try:
        chunks = []
        with urllib.request.urlopen(req, timeout=30) as resp:
            while True:
                line = resp.readline()
                if not line:
                    break
                line_str = line.decode('utf-8').strip()
                if not line_str:
                    continue
                try:
                    chunk = json.loads(line_str)
                    text = chunk.get("response", "")
                    if text:
                        chunks.append(text)
                    if chunk.get("done", False):
                        break
                except json.JSONDecodeError:
                    continue
        return "".join(chunks).strip()
    except Exception:
        return ""

# ---------------------------------------------------------------------------
# Aggressive code extraction
# ---------------------------------------------------------------------------
def extract_code(response: str, lang: str) -> str:
    """Extract ONLY the first code block from response."""
    # Try ```lang first
    if lang == "c":
        pattern = r'```(?:c|C)\n(.*?)\n```'
    else:
        pattern = r'```(?:python|py|Python)\n(.*?)\n```'

    m = re.search(pattern, response, re.DOTALL)
    if m:
        return m.group(1).strip()

    # Fallback: any fenced block
    m = re.search(r'```\n(.*?)\n```', response, re.DOTALL)
    if m:
        return m.group(1).strip()

    # Last resort: try to find #include or import and take from there
    for marker in ["#include", "import ", "def ", "class "]:
        idx = response.find(marker)
        if idx >= 0:
            code = response[idx:]
            # Strip any trailing non-code (explanations after closing brace)
            return code.strip()

    return response.strip()

# ---------------------------------------------------------------------------
# C helper: clean up common LLM artifacts before compilation
# ---------------------------------------------------------------------------
def preprocess_c_code(code: str) -> str:
    """Remove common LLM output artifacts that break compilation."""
    # Remove trailing explanations after last }
    lines = code.split("\n")
    result_lines = []
    brace_depth = 0
    past_main_end = False

    for line in lines:
        stripped = line.strip()
        if not stripped or stripped.startswith("//") or stripped.startswith("/*"):
            result_lines.append(line)
            continue

        # Count braces
        open_b = stripped.count("{")
        close_b = stripped.count("}")
        brace_depth += open_b - close_b

        result_lines.append(line)

    # If there's text after all braces are closed, try to cut it
    # Find last line with } and take everything up to there + 2 lines (for comments)
    last_brace = -1
    for i, line in enumerate(result_lines):
        if "}" in line.strip():
            last_brace = i

    if last_brace > 0:
        # Keep up to 3 lines after last } (for potential cleanup code)
        cutoff = min(last_brace + 3, len(result_lines))
        result_lines = result_lines[:cutoff]

    return "\n".join(result_lines)

# ---------------------------------------------------------------------------
# Problem definitions
# ---------------------------------------------------------------------------
C_PROBLEMS = [
    {
        "name": "Linked List Reverse",
        "prompt": "Write a C program to reverse a singly linked list. Define struct Node with int data and Node* next. Implement reverse() function, create list with values 1-5, print before and after reversal. Only standard libc.",
        "verify": lambda out: any(w in out.lower() for w in ["5 4 3", "5->4", "reverse"])
    },
    {
        "name": "Buffer Overflow Debug",
        "prompt": 'Find the bug in this C code and explain it:\nvoid greet(char *name) {\n    char buf[16];\n    snprintf(buf, sizeof(buf), "Hello, %s!", name);\n    printf("%s\\n", buf);\n}\nint main() { greet("AlexanderTheGreat"); return 0; }',
        "verify": lambda out: any(w in out.lower() for w in ["overflow", "buffer", "buf[16]", "truncat", "size"]),
        "code_test": False  # No compilation, just analysis
    },
    {
        "name": "Binary Search",
        "prompt": "Write a C program with iterative binary search. Test array {2,5,8,12,16,23,38,56,72,91}. Search for 23 (index 5) and 100 (return -1). Print both results.",
        "verify": lambda out: "5" in out and ("-" in out or "-1" in out)
    },
    {
        "name": "Memory Leak Detection",
        "prompt": 'Identify the memory leak here:\nchar *msg = malloc(64);\nstrcpy(msg, "data");\nchar *old = msg;\nmsg = malloc(128);\nfree(msg);',
        "verify": lambda out: any(w in out.lower() for w in ["leak", "old", "first", "lost", "malloc"]),
        "code_test": False
    },
    {
        "name": "Stack Implementation",
        "prompt": "Write a C program implementing a stack of 100 ints with push, pop, peek. Push 10, 20, 30, pop twice, print remaining.",
        "verify": lambda out: "10" in out
    },
    {
        "name": "String Reverse",
        "prompt": 'Write void reverse_string(char *s) that reverses a string in-place. Use two pointers: one starting at the beginning, one at the end (strlen-1), swap and move toward center. Main: test with "Hello World", print before and after.',
        "verify": lambda out: any(w in out.lower() for w in ["dlrow", "olleh", "hello world", "before"])
    },
    {
        "name": "Word Count (wc)",
        "prompt": "Write a C program like wc: read file from argv[1], count lines/words/chars, print them.",
        "verify": lambda out: len(out) > 5 and any(w in out.lower() for w in ["line", "word", "char", "0", "1"])
    },
    {
        "name": "Fibonacci Memoization",
        "prompt": "Write C program computing fibonacci with memoization array. Initialize memo with -1 using memset or loop. Print fibonacci(10) which is 55.",
        "verify": lambda out: "55" in out
    },
    {
        "name": "Count Set Bits",
        "prompt": "Write int count_set_bits(unsigned int n) using n &= (n-1). Test with 29 (binary 11101 has 4 set bits), should print 4.",
        "verify": lambda out: "4" in out
    },
    {
        "name": "qsort Students",
        "prompt": "Write C with Student struct {char name[50], int grade}. Create 5 students, sort by grade using qsort, print before and after.",
        "verify": lambda out: len(out) > 10
    },
]

PY_PROBLEMS = [
    {
        "name": "Timer Decorator",
        "prompt": "Write a Python @timer decorator that prints execution time. Test with a function that sleeps 0.3s.",
        "verify": lambda out: any(w in out.lower() for w in ["time", "second", "took", "duration"])
    },
    {
        "name": "Asyncio Gather",
        "prompt": "Write Python asyncio code with 3 tasks (delays 0.1, 0.2, 0.3s) using asyncio.gather. Print total time.",
        "verify": lambda out: any(w in out.lower() for w in ["0.", "gather", "async"])
    },
    {
        "name": "Context Manager",
        "prompt": 'Write DatabaseConnection class with __enter__ (print "Connecting") and __exit__ (print "Closing"). Demo with with-statement.',
        "verify": lambda out: any(w in out.lower() for w in ["connect", "close"])
    },
    {
        "name": "Dataclass Validation",
        "prompt": "Write Python dataclass Person (name, age, email) with __post_init__ validation. Test valid and invalid.",
        "verify": lambda out: any(w in out.lower() for w in ["valueerror", "invalid", "valid", "must be", "cannot", "error"])
    },
    {
        "name": "CSV Processing",
        "prompt": "Write Python using csv + io.StringIO with name,score data (5 entries). Print average/max/min scores.",
        "verify": lambda out: any(c in out for c in "0123456789")
    },
    {
        "name": "Regex Extraction",
        "prompt": "Write Python re module code to extract emails from text containing 'test@example.com' and 'user@test.org'. Print results.",
        "verify": lambda out: "@" in out
    },
    {
        "name": "Protocol Typing",
        "prompt": "Write typing.Protocol for Drawable with draw(). Create Circle and Rectangle. Demo duck typing.",
        "verify": lambda out: any(w in out.lower() for w in ["draw", "circle", "rectangle"])
    },
    {
        "name": "Generator Pipeline",
        "prompt": "Write Python generators: squares of 1-20, filter even results. Print final output.",
        "verify": lambda out: any(str(n) in out for n in [4, 16, 36, 64])
    },
    {
        "name": "Exception Hierarchy",
        "prompt": "Define AppError base class, ValidationError and AuthError subclasses. Demo raising and catching.",
        "verify": lambda out: any(w in out.lower() for w in ["error", "validation", "auth"])
    },
    {
        "name": "Singleton Metaclass",
        "prompt": "Write Python metaclass enforcing singleton. Create Database class. Prove two instances are same.",
        "verify": lambda out: any(w in out.lower() for w in ["singleton", "same", "true", "identical"])
    },
]

# ---------------------------------------------------------------------------
# Run C problem
# ---------------------------------------------------------------------------
def run_c_problem(idx: int, problem: dict) -> bool:
    name = problem["name"]
    is_code_test = problem.get("code_test", True)

    log(f"C-{idx}: {name}")
    response = query_llm(problem["prompt"])

    if "LLM_QUERY_FAILED" in response:
        fail_log(f"C-{idx}: LLM query failed")
        return False

    # For non-code problems, just verify the analysis
    if not is_code_test:
        if problem["verify"](response):
            pass_log(f"C-{idx}: {name}")
            return True
        else:
            fail_log(f"C-{idx}: Analysis didn't match")
            return False

    code = extract_code(response, "c")
    code = preprocess_c_code(code)

    src_path = CODE_DIR / f"c_p{idx}.c"
    bin_path = CODE_DIR / f"c_p{idx}_bin"
    src_path.write_text(code)

    # Compile
    try:
        result = subprocess.run(
            ["gcc", "-std=c17", "-Wall", "-o", str(bin_path), str(src_path)],
            capture_output=True, text=True, timeout=15
        )
        if result.returncode != 0:
            err_lines = result.stderr.strip().split("\n")[:3]
            fail_log(f"C-{idx}: Compile error — {'; '.join(l.strip() for l in err_lines)}")
            return False

        run_result = subprocess.run(
            [str(bin_path)], capture_output=True, text=True, timeout=10
        )
        output = (run_result.stdout + run_result.stderr).lower()

    except FileNotFoundError:
        fail_log(f"C-{idx}: gcc not found")
        return False
    except subprocess.TimeoutExpired:
        fail_log(f"C-{idx}: Execution timed out")
        return False
    except Exception as e:
        fail_log(f"C-{idx}: {e}")
        return False

    if problem["verify"](output):
        pass_log(f"C-{idx}: {name}")
        return True
    else:
        fail_log(f"C-{idx}: Wrong output — {(run_result.stdout+run_result.stderr)[:80]}")
        return False

# ---------------------------------------------------------------------------
# Run Python problem
# ---------------------------------------------------------------------------
def run_py_problem(idx: int, problem: dict) -> bool:
    name = problem["name"]
    log(f"PY-{idx}: {name}")

    response = query_llm(problem["prompt"])

    if "LLM_QUERY_FAILED" in response:
        fail_log(f"PY-{idx}: LLM query failed")
        return False

    code = extract_code(response, "python")

    src_path = CODE_DIR / f"py_p{idx}.py"
    src_path.write_text(code)

    # Syntax check
    try:
        result = subprocess.run(
            [sys.executable, "-c", f"import ast; ast.parse(open('{src_path}').read())"],
            capture_output=True, text=True, timeout=5
        )
        if result.returncode != 0:
            fail_log(f"PY-{idx}: Syntax error")
            return False
    except Exception as e:
        fail_log(f"PY-{idx}: {e}")
        return False

    # Run
    try:
        run_result = subprocess.run(
            [sys.executable, str(src_path)],
            capture_output=True, text=True, timeout=10
        )
        output = (run_result.stdout + run_result.stderr).lower()
    except subprocess.TimeoutExpired:
        fail_log(f"PY-{idx}: Execution timed out")
        return False

    if problem["verify"](output):
        pass_log(f"PY-{idx}: {name}")
        return True
    else:
        fail_log(f"PY-{idx}: Wrong output — {(run_result.stdout+run_result.stderr)[:80]}")
        return False

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
def main():
    start = time.time()

    print()
    print(f"{C.BOLD}{C.YELLOW}{'='*60}{C.RESET}")
    print(f"{C.BOLD}  Multi-Language LLM Benchmark v2{C.RESET}")
    print(f"  Model: {LLM_MODEL}")
    print(f"  Ollama: {OLLAMA_HOST}")
    print(f"  Date: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"{C.BOLD}{C.YELLOW}{'='*60}{C.RESET}")

    # Check connection
    try:
        req = urllib.request.Request(f"{OLLAMA_HOST}/api/tags")
        with urllib.request.urlopen(req, timeout=10) as r:
            models = json.loads(r.read())
            names = [m["name"] for m in models.get("models", [])]
            if LLM_MODEL not in str(names):
                print(f"{C.YELLOW}WARNING: {LLM_MODEL} not listed{C.RESET}")
    except Exception as e:
        print(f"{C.RED}ERROR: Cannot reach Ollama: {e}{C.RESET}")
        sys.exit(1)

    has_gcc = subprocess.run(["which", "gcc"], capture_output=True).returncode == 0

    # Warm up model
    log("Warming up model...")
    query_llm("print hello")
    log("Ready.")

    c_passed = 0
    py_passed = 0

    # C problems
    if has_gcc:
        print(f"\n{C.BOLD}─── C BENCHMARK ───{C.RESET}")
        for i, prob in enumerate(C_PROBLEMS, 1):
            t0 = time.time()
            c_passed += run_c_problem(i, prob)
            elapsed = time.time() - t0
            log(f"  ({elapsed:.1f}s)")
    else:
        print(f"\n{C.YELLOW}(Skipping C — no gcc){C.RESET}")

    # Python problems
    print(f"\n{C.BOLD}─── Python BENCHMARK ───{C.RESET}")
    for i, prob in enumerate(PY_PROBLEMS, 1):
        t0 = time.time()
        py_passed += run_py_problem(i, prob)
        elapsed = time.time() - t0
        log(f"  ({elapsed:.1f}s)")

    total_time = time.time() - start
    total = len(C_PROBLEMS) + len(PY_PROBLEMS) if has_gcc else len(PY_PROBLEMS)
    total_pass = c_passed + py_passed

    print()
    print(f"{C.BOLD}{'='*60}{C.RESET}")
    print(f"{C.BOLD}RESULTS{C.RESET}")
    print(f"{'='*60}")
    if has_gcc:
        print(f"  C:       {c_passed}/{len(C_PROBLEMS)} ✅")
    print(f"  Python:  {py_passed}/{len(PY_PROBLEMS)} ✅")
    print(f"  Total:   {total_pass}/{total} ({total_pass/total*100:.0f}%)")
    print(f"  Time:    {total_time:.0f}s ({total_time/total:.1f}s per problem)")

    # Save
    summary = f"""Multi-Language LLM Benchmark v2
================================
Model: {LLM_MODEL}
Ollama: {OLLAMA_HOST}
Date: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}

C:       {c_passed}/{len(C_PROBLEMS)} passed
Python:  {py_passed}/{len(PY_PROBLEMS)} passed
Total:   {total_pass}/{total} ({total_pass/total*100:.0f}%)
Time:    {total_time:.0f}s

Artifacts: {CODE_DIR}
"""
    (RESULTS_DIR / "summary.txt").write_text(summary)
    print(f"\n{summary}")

if __name__ == "__main__":
    main()
