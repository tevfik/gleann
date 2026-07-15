#!/usr/bin/env python3
"""
Multi-Language LLM Agent Performance Benchmark (C + Python)
===========================================================
Tests how well the configured LLM solves real-world problems in C and Python.
For each problem: generate code -> compile/parse -> run -> verify pass/fail

Usage: python3 multilang_benchmark.py
Requires: ollama reachable, gcc (for C), python3
"""

import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request
from datetime import datetime
from pathlib import Path

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
LLM_MODEL = os.getenv("GLEANN_LLM_MODEL", "qwen3.5:9b")
OLLAMA_HOST = os.getenv("OLLAMA_HOST", "http://localhost:11001")
RESULTS_DIR = Path(__file__).parent / "results" / f"multilang_{int(time.time())}"
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

def log(msg: str):
    print(f"{C.CYAN}[multilang-bench]{C.RESET} {msg}", flush=True)

def pass_log(msg: str):
    print(f"  {C.GREEN}✅ PASS{C.RESET} — {msg}", flush=True)

def fail_log(msg: str):
    print(f"  {C.RED}❌ FAIL{C.RESET} — {msg}", flush=True)

# ---------------------------------------------------------------------------
# LLM Query
# ---------------------------------------------------------------------------
def query_llm(prompt: str) -> str:
    """Query Ollama for code generation."""
    payload = json.dumps({
        "model": LLM_MODEL,
        "prompt": prompt,
        "stream": False
    }).encode()
    req = urllib.request.Request(
        f"{OLLAMA_HOST}/api/generate",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    try:
        with urllib.request.urlopen(req, timeout=90) as resp:
            data = json.loads(resp.read())
            return data.get("response", "LLM_QUERY_FAILED")
    except Exception as e:
        # Retry once
        try:
            req2 = urllib.request.Request(
                f"{OLLAMA_HOST}/api/generate",
                data=payload,
                headers={"Content-Type": "application/json"},
                method="POST"
            )
            with urllib.request.urlopen(req2, timeout=90) as resp:
                data = json.loads(resp.read())
                return data.get("response", "LLM_QUERY_FAILED")
        except Exception as e2:
            return f"LLM_QUERY_FAILED: {e2}"

# ---------------------------------------------------------------------------
# Code Extraction from Markdown Fences
# ---------------------------------------------------------------------------
def extract_code(response: str, lang: str) -> str:
    """Extract ONLY the first ```lang ... ``` block; ignore subsequent blocks."""
    lines = response.split("\n")
    in_code = False
    code_lines = []
    found = False
    # Accepted language tags
    if lang == "c":
        accepted_tags = {"", "c"}
    else:
        accepted_tags = {"", "python", "py"}

    for line in lines:
        stripped = line.strip()
        if stripped.startswith("```"):
            if not in_code:
                tag = stripped[3:].strip().lower()
                if tag in accepted_tags:
                    in_code = True
                    found = True
            else:
                # End of first block — stop immediately
                break
            continue
        if in_code and found:
            code_lines.append(line)

    if code_lines:
        return "\n".join(code_lines)
    # Fallback: try any first fence block
    in_code = False
    code_lines = []
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("```"):
            if not in_code:
                in_code = True
            else:
                break
            continue
        if in_code:
            code_lines.append(line)
    if code_lines:
        return "\n".join(code_lines)
    return response

# ---------------------------------------------------------------------------
# C Problem Set
# ---------------------------------------------------------------------------
def run_c_problem(idx: int, name: str, prompt: str, verify_fn) -> bool:
    log(f"C-{idx}: {name}")
    response = query_llm(prompt)
    code = extract_code(response, "c")

    src_path = CODE_DIR / f"c_p{idx}.c"
    bin_path = CODE_DIR / f"c_p{idx}_bin"
    err_path = CODE_DIR / f"c_p{idx}_err.txt"

    src_path.write_text(code)

    # Try compile
    try:
        result = subprocess.run(
            ["gcc", "-std=c11", "-Wall", "-o", str(bin_path), str(src_path)],
            capture_output=True, text=True, timeout=30
        )
        if result.returncode != 0:
            fail_log(f"C-{idx}: Compilation failed — {result.stderr.strip()[:120]}")
            err_path.write_text(result.stderr)
            return False

        # Run compiled binary
        run_result = subprocess.run(
            [str(bin_path)],
            capture_output=True, text=True, timeout=15
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

    # Verify
    if verify_fn(output, response):
        pass_log(f"C-{idx}: {name}")
        return True
    else:
        fail_log(f"C-{idx}: Output didn't match expectations — {(run_result.stdout+run_result.stderr)[:100]}")
        return False

def run_c_problems() -> int:
    print()
    print("====================================================================")
    print(f"\n{C.BOLD}{C.YELLOW}C BENCHMARK (10 problems){C.RESET}")
    print("====================================================================")

    passed = 0

    # C-1: Linked List Reverse
    passed += run_c_problem(1, "Linked List Reverse",
        'Write a complete C program that reverses a singly linked list. Include struct definition, reverse function, test main with 5 nodes (values 1,2,3,4,5), and print before and after. Use only standard libc.',
        lambda out, resp: "reverse" in out or "5" in out)

    # C-2: Buffer Overflow Detection
    passed += run_c_problem(2, "Buffer Overflow Bug — Debugging",
        'This C code has a buffer overflow bug. Find it, explain the bug, and provide the fixed version:\n#include <stdio.h>\n#include <string.h>\nvoid greet(char *name) {\n    char buf[16];\n    snprintf(buf, sizeof(buf), "Hello, %s!", name);\n    printf("%s\\n", buf);\n}\nint main() { greet("AlexanderTheGreat"); return 0; }',
        lambda out, resp: any(w in resp.lower() for w in ["overflow", "buf[16]", "buffer", "truncat", "size"]))

    # C-3: Binary Search
    passed += run_c_problem(3, "Binary Search — Algorithm",
        'Write a complete C program implementing iterative binary search on a sorted array. Test with {2, 5, 8, 12, 16, 23, 38, 56, 72, 91} searching for 23 (should return index 5) and 100 (should return -1). Print both results.',
        lambda out, resp: "5" in out)

    # C-4: Memory Leak Detection
    passed += run_c_problem(4, "Memory Leak — Detection",
        'This C program has a memory leak. Identify it and explain:\n#include <stdlib.h>\n#include <string.h>\nint process() {\n    char *msg = malloc(64);\n    strcpy(msg, "processing...");\n    char *old = msg;\n    msg = malloc(128);\n    strcpy(msg, old);\n    strcat(msg, " done");\n    printf("%s\\n", msg);\n    free(msg);\n    return 0;\n}\nint main() { process(); return 0; }',
        lambda out, resp: any(w in resp.lower() for w in ["free.*old", "leak", "second malloc", "lost", "first malloc"]))

    # C-5: Stack Data Structure
    passed += run_c_problem(5, "Stack Implementation — Data Structure",
        'Write a complete C program implementing a stack (max 100 ints) using an array. Include push, pop, peek, is_empty functions. In main: push 10, 20, 30, pop twice, print remaining elements.',
        lambda out, resp: any(w in out for w in ["10", "stack"]))

    # C-6: In-place String Reverse
    passed += run_c_problem(6, "String Reverse — Pointers",
        'Write a C function void reverse_string(char *s) that reverses a string in-place using pointers. Include main testing with "Hello World". Print before and after.',
        lambda out, resp: "dlrow olleh" in out or "olleh" in out)

    # C-7: File I/O Word Count
    passed += run_c_problem(7, "Word Count — File I/O",
        'Write a complete C program that reads a text file given as command-line argument and counts words, lines, and characters (like wc). Handle errors properly.',
        lambda out, resp: any(w in out for w in ["word", "line", "char"]))

    # C-8: Fibonacci with Memoization
    passed += run_c_problem(8, "Fibonacci — Memoization",
        'Write a C program computing fibonacci(n) using memoization (array cache). Print fibonacci(10) which should be 55.',
        lambda out, resp: "55" in out)

    # C-9: Count Set Bits
    passed += run_c_problem(9, "Count Set Bits — Bit Manipulation",
        'Write a C function int count_set_bits(unsigned int n) using Brian Kernighan algorithm (n &= n-1). Test with 29 (binary 11101, should return 3). Print the result.',
        lambda out, resp: "3" in out)

    # C-10: Struct Sorting
    passed += run_c_problem(10, "Student Sort — qsort",
        'Write a C program with Student struct (name[50], age, grade). Create 5 students and sort by grade using qsort. Print before and after.',
        lambda out, resp: len(out) > 10)

    print(f"\n{C.BOLD}C Results: {passed}/10 passed{C.RESET}")
    return passed

# ---------------------------------------------------------------------------
# Python Problem Set
# ---------------------------------------------------------------------------
def run_py_problem(idx: int, name: str, prompt: str, verify_fn) -> bool:
    log(f"PY-{idx}: {name}")
    response = query_llm(prompt)
    code = extract_code(response, "python")

    src_path = CODE_DIR / f"py_p{idx}.py"
    src_path.write_text(code)

    # Check syntax
    try:
        result = subprocess.run(
            [sys.executable, "-c", f"import ast; ast.parse(open('{src_path}').read())"],
            capture_output=True, text=True, timeout=10
        )
        if result.returncode != 0:
            fail_log(f"PY-{idx}: Syntax error — {result.stderr.strip()[:120]}")
            return False
    except Exception as e:
        fail_log(f"PY-{idx}: {e}")
        return False

    # Run
    try:
        run_result = subprocess.run(
            [sys.executable, str(src_path)],
            capture_output=True, text=True, timeout=15
        )
        output = (run_result.stdout + run_result.stderr).lower()
    except subprocess.TimeoutExpired:
        fail_log(f"PY-{idx}: Execution timed out")
        return False
    except Exception as e:
        fail_log(f"PY-{idx}: {e}")
        return False

    if verify_fn(output, response):
        pass_log(f"PY-{idx}: {name}")
        return True
    else:
        fail_log(f"PY-{idx}: Output didn't match — {(run_result.stdout+run_result.stderr)[:100]}")
        return False

def run_python_problems() -> int:
    print()
    print("====================================================================")
    print(f"\n{C.BOLD}{C.YELLOW}Python BENCHMARK (10 problems){C.RESET}")
    print("====================================================================")

    passed = 0

    # PY-1: Timing Decorator
    passed += run_py_problem(1, "Timing Decorator",
        'Write a Python @timer decorator that prints execution time of decorated functions. Example: decorate a function that sleeps 0.5s, call it.',
        lambda out, resp: any(w in out for w in ["time", "second", "took", "duration"]))

    # PY-2: Async Concurrency
    passed += run_py_problem(2, "Async HTTP Simulation — asyncio",
        'Write Python using asyncio that simulates 3 tasks with delays 0.1s, 0.2s, 0.3s using asyncio.gather concurrently. Print total time (~0.3s).',
        lambda out, resp: any(w in out for w in ["async", "gath", "0."]))

    # PY-3: Context Manager
    passed += run_py_problem(3, "Custom Context Manager",
        'Write a DatabaseConnection class with __enter__ and __exit__. Print "Connecting..." on enter, "Closing" on exit. Demo with with-statement.',
        lambda out, resp: any(w in out for w in ["connect", "close"]))

    # PY-4: Dataclass Validation
    passed += run_py_problem(4, "Dataclass + Validation",
        'Write a Python dataclass Person (name, age, email) with __post_init__ validation: age 0-150, email contains @. Test with valid and invalid data.',
        lambda out, resp: any(w in out for w in ["valueerror", "invalid", "valid"]))

    # PY-5: CSV Processing (stdlib only)
    passed += run_py_problem(5, "CSV Processing — stdlib",
        'Write Python using csv module and io.StringIO. Create inline test data with name,score columns (5 entries). Compute average, max, min of scores.',
        lambda out, resp: any(c in out for c in ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"]))

    # PY-6: Regex Email/Phone Extraction
    passed += run_py_problem(6, "Regex Extraction",
        'Write Python using re module to extract emails and phone numbers from a sample text containing at least 2 of each. Print results.',
        lambda out, resp: "@" in out)

    # PY-7: Protocol & Duck Typing
    passed += run_py_problem(7, "Protocol — Structural Typing",
        'Write Python using typing.Protocol for Drawable with draw() method. Create Circle and Rectangle that implement it without explicit inheritance.',
        lambda out, resp: any(w in out for w in ["draw", "circle", "rectangle", "protocol"]))

    # PY-8: Generator Pipeline
    passed += run_py_problem(8, "Generator — Lazy Evaluation",
        'Write Python generators: one yields squares, another filters even results. Chain to process 1-20. Print final output.',
        lambda out, resp: any(str(n) in out for n in [4, 16, 36, 64, 100]))

    # PY-9: Custom Exception Hierarchy
    passed += run_py_problem(9, "Exception Hierarchy",
        'Define AppError base class, ValidationError and AuthError subclasses. Create login function raising them. Catch specific types differently.',
        lambda out, resp: any(w in out for w in ["error", "validation", "auth", "handled"]))

    # PY-10: Singleton Metaclass
    passed += run_py_problem(10, "Singleton — Metaclass",
        'Write a Python metaclass enforcing singleton pattern. Create Database class using it. Prove two instances are the same object.',
        lambda out, resp: any(w in out for w in ["singleton", "same", "identical", "true"]))

    print(f"\n{C.BOLD}Python Results: {passed}/10 passed{C.RESET}")
    return passed

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
def main():
    start = time.time()

    print()
    print(f"{C.BOLD}{C.YELLOW}{'=' * 60}{C.RESET}")
    print(f"{C.BOLD}  Multi-Language LLM Agent Performance Benchmark{C.RESET}")
    print(f"  Model: {LLM_MODEL}")
    print(f"  Date: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"{C.BOLD}{C.YELLOW}{'=' * 60}{C.RESET}")

    # Check prerequisites
    try:
        req = urllib.request.Request(f"{OLLAMA_HOST}/api/tags")
        with urllib.request.urlopen(req, timeout=10) as r:
            models = json.loads(r.read())
            model_names = [m["name"] for m in models.get("models", [])]
            if LLM_MODEL not in str(model_names):
                print(f"{C.YELLOW}WARNING: {LLM_MODEL} not in loaded models, may fail{C.RESET}")
    except Exception as e:
        print(f"{C.RED}ERROR: Cannot reach Ollama at {OLLAMA_HOST}: {e}{C.RESET}")
        sys.exit(1)

    # Check gcc
    has_gcc = subprocess.run(["which", "gcc"], capture_output=True).returncode == 0
    if not has_gcc:
        print(f"{C.YELLOW}WARNING: gcc not found, skipping C problems{C.RESET}")

    c_passed = 0
    py_passed = 0

    if has_gcc:
        c_passed = run_c_problems()
    else:
        print("\n(Skipping C — no gcc)")

    py_passed = run_python_problems()

    elapsed = time.time() - start
    total = (10 if has_gcc else 0) + 10
    total_pass = c_passed + py_passed

    print()
    print("====================================================================")
    print(f"{C.BOLD}FINAL RESULTS{C.RESET}")
    print("====================================================================")
    print()
    if has_gcc:
        print(f"  {C.BOLD}C:{C.GREEN} {c_passed}/10 passed{C.RESET}")
    print(f"  {C.BOLD}Python:{C.GREEN} {py_passed}/10 passed{C.RESET}")
    print(f"  {C.BOLD}Total:{C.GREEN} {total_pass}/{total} passed{C.RESET}")
    print(f"  {C.BOLD}Time: {elapsed:.0f}s{C.RESET}")
    print()

    # Save summary
    summary = f"""Multi-Language LLM Agent Benchmark
==================================
Model: {LLM_MODEL}
Date: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}

C Problems:      {c_passed}/10 passed (skipped){"*" if not has_gcc else ""}
Python Problems: {py_passed}/10 passed
Total:           {total_pass}/{total} passed
Elapsed Time:    {elapsed:.0f}s

Code artifacts: {CODE_DIR}
"""
    (RESULTS_DIR / "summary.txt").write_text(summary)
    print(summary)

if __name__ == "__main__":
    main()
