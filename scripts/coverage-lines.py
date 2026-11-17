#!/usr/bin/env python3
"""coverage-lines.py — line coverage of Go profiles, computed the way Codecov does.

Usage: scripts/coverage-lines.py [--packages] [--files PKG] [--misses FILE]
                                 PROFILE [PROFILE...]

`go tool cover` and scripts/coverage.sh count statements; Codecov counts
lines. For every file:line it collects all coverage blocks whose range
includes the line (after merging the profiles: a block is covered when any
profile covers it). The line is a hit when every such block ran, a miss when
none ran, and a partial when only some ran. Partials count as not covered:
coverage = hits / (hits + misses + partials).

Like the Codecov uploader's "fixes" for Go, lines that are blank, hold only
a comment, or hold only a closing brace `}` are not counted (the source is
read from the repository; this reproduces the per-package Codecov figures).

  --packages      print the per-package table (lowest first) and the total
  --files PKG     print per-file figures for the package path suffix PKG
  --misses FILE   list the uncovered (miss/partial) lines of files whose path
                  ends with FILE

The paths ignored in codecov.yml are dropped (keep IGNORE in sync).
"""
import os
import re
import sys
from collections import defaultdict

IGNORE = re.compile(r"/(tests/|internal/graph/analysisgolden/|internal/testutil/)")
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BLOCK = re.compile(r"^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$")


def load(paths):
    blocks = {}
    for path in paths:
        with open(path, encoding="utf-8") as f:
            for raw in f:
                m = BLOCK.match(raw.strip())
                if not m:
                    continue
                name = m.group(1)
                if IGNORE.search(name):
                    continue
                key = (name, int(m.group(2)), int(m.group(3)), int(m.group(4)), int(m.group(5)))
                hit = int(m.group(7)) > 0
                blocks[key] = blocks.get(key, False) or hit
    return blocks


def module_path():
    with open(os.path.join(ROOT, "go.mod"), encoding="utf-8") as f:
        for raw in f:
            if raw.startswith("module "):
                return raw.split()[1].strip()
    return ""


def skipped_lines(name, module):
    """Line numbers Codecov drops: blank, comment-only and `}`-only lines."""
    rel = name[len(module) + 1:] if name.startswith(module + "/") else name
    try:
        with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
            src = f.read().split("\n")
    except OSError:
        return set()
    skip = set()
    for i, text in enumerate(src, 1):
        t = text.strip()
        if t == "" or t.startswith("//") or t == "}":
            skip.add(i)
    return skip


def lines_of(blocks):
    """Return {file: {line: "hit"|"miss"|"partial"}}."""
    seen = defaultdict(lambda: defaultdict(list))
    for (name, sl, _sc, el, _ec), hit in blocks.items():
        for line in range(sl, el + 1):
            seen[name][line].append(hit)
    module = module_path()
    out = {}
    for name, per_line in seen.items():
        skip = skipped_lines(name, module)
        states = {}
        for line, hits in per_line.items():
            if line in skip:
                continue
            if all(hits):
                states[line] = "hit"
            elif not any(hits):
                states[line] = "miss"
            else:
                states[line] = "partial"
        out[name] = states
    return out


def summarise(states):
    hits = sum(1 for s in states.values() if s == "hit")
    return hits, len(states)


def main(argv):
    packages = False
    files_pkg = None
    misses_file = None
    paths = []
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--packages":
            packages = True
        elif a == "--files":
            i += 1
            files_pkg = argv[i]
        elif a == "--misses":
            i += 1
            misses_file = argv[i]
        elif a in ("-h", "--help"):
            print(__doc__)
            return 0
        else:
            paths.append(a)
        i += 1
    if not paths:
        print(__doc__, file=sys.stderr)
        return 2

    per_file = lines_of(load(paths))

    if misses_file:
        for name in sorted(per_file):
            if not name.endswith(misses_file):
                continue
            for line in sorted(per_file[name]):
                state = per_file[name][line]
                if state != "hit":
                    print(f"{name}:{line} {state}")
        return 0

    if files_pkg:
        rows = []
        for name, states in per_file.items():
            pkg = name.rsplit("/", 1)[0]
            if not pkg.endswith(files_pkg):
                continue
            hits, total = summarise(states)
            rows.append((100.0 * hits / total if total else 100.0, total - hits, name))
        for pct, unc, name in sorted(rows):
            print(f"{pct:6.1f}%  {unc:5d} uncovered  {name}")
        return 0

    pkg_hits = defaultdict(int)
    pkg_total = defaultdict(int)
    for name, states in per_file.items():
        pkg = name.rsplit("/", 1)[0]
        hits, total = summarise(states)
        pkg_hits[pkg] += hits
        pkg_total[pkg] += total
    if packages:
        rows = sorted(
            (100.0 * pkg_hits[p] / pkg_total[p], pkg_total[p] - pkg_hits[p], p)
            for p in pkg_total
            if pkg_total[p]
        )
        for pct, unc, p in rows:
            print(f"{pct:6.1f}%  {unc:6d} uncovered  {p}")
    hits = sum(pkg_hits.values())
    total = sum(pkg_total.values())
    if total == 0:
        print("no lines")
        return 1
    print(f"total: {hits}/{total} lines, {100.0 * hits / total:.2f}%")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
