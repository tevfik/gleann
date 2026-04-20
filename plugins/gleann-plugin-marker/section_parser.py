"""Markdown section parser — extracts graph-ready nodes and edges from markdown.

Produces a structure mirroring the AST code indexer pattern:
  - Nodes: Document (1) + Section (N)
  - Edges: HAS_SECTION (Document→root sections), HAS_SUBSECTION (Section→Section)

The /convert endpoint returns this directly so gleann can ingest it into KuzuDB
exactly like code symbols/calls.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

# Heading pattern: # Title, ## Title, ### Title, etc.
_HEADING_RE = re.compile(r"^(#{1,6})\s+(.+)$", re.MULTILINE)

# Numbered heading pattern: "3.14.1 Title" — dots indicate hierarchy depth.
_NUMBERED_RE = re.compile(r"^(\d+(?:\.\d+)*)\s+")

# Figure/Table heading pattern: "Figure 4. Multi-AHB matrix"
_FIGURE_TABLE_RE = re.compile(r"^(Figure|Table)\s+\d+", re.IGNORECASE)


@dataclass
class Node:
    """A graph node to be stored in KuzuDB."""

    type: str  # "Document" or "Section"
    data: dict

    def to_dict(self) -> dict:
        return {"_type": self.type, **self.data}


@dataclass
class Edge:
    """A graph edge to be stored in KuzuDB."""

    type: str  # "HAS_SECTION" or "HAS_SUBSECTION"
    from_id: str
    to_id: str

    def to_dict(self) -> dict:
        return {"_type": self.type, "from": self.from_id, "to": self.to_id}


@dataclass
class PluginResult:
    """Full graph-ready response from the plugin."""

    nodes: list[Node]
    edges: list[Edge]

    def to_dict(self) -> dict:
        return {
            "nodes": [n.to_dict() for n in self.nodes],
            "edges": [e.to_dict() for e in self.edges],
        }


def parse_document(
    markdown: str,
    source_path: str,
    doc_format: str = "unknown",
    page_count: int | None = None,
) -> PluginResult:
    """Parse markdown into graph-ready nodes and edges.

    Returns a PluginResult with:
      - 1 Document node (keyed by source_path)
      - N Section nodes (keyed by "doc:<path>:s<id>")
      - Edges: HAS_SECTION (Document→root), HAS_SUBSECTION (parent→child)

    Each Section node carries its raw ``content`` text — gleann handles chunking.

    Hierarchy detection:
      1. Uses markdown heading levels (#, ##, ###) as primary signal
      2. If all headings are the same level (e.g. all ##), falls back to
         numbered heading patterns (1, 2.1, 3.14.1) to infer hierarchy
      3. Figure/Table headings are attached to their nearest preceding section
    """
    lines = markdown.split("\n")
    nodes: list[Node] = []
    edges: list[Edge] = []

    # --- Extract headings ---
    headings: list[tuple[int, int, str]] = []  # (line_idx, level, title)
    for i, line in enumerate(lines):
        m = _HEADING_RE.match(line)
        if m:
            headings.append((i, len(m.group(1)), m.group(2).strip()))

    # --- Detect flat heading syndrome (all same level from docling) ---
    if headings:
        levels = set(h[1] for h in headings)
        if len(levels) == 1:
            # All headings are the same markdown level — use numbering to infer hierarchy
            headings = _infer_levels_from_numbering(headings)

    # --- Document node ---
    doc_title = _infer_title(markdown, headings)
    doc_summary = _extract_summary(markdown)

    doc_node = Node(
        type="Document",
        data={
            "path": source_path,
            "title": doc_title,
            "format": doc_format,
            "summary": doc_summary,
            "word_count": len(markdown.split()),
            "page_count": page_count or 0,
        },
    )
    nodes.append(doc_node)

    if not headings:
        # No headings — single implicit section with all content.
        sec_id = _sec_id(source_path, "s0")
        sec_node = Node(
            type="Section",
            data={
                "id": sec_id,
                "heading": doc_title,
                "level": 0,
                "content": markdown,
                "summary": doc_summary,
                "doc_path": source_path,
            },
        )
        nodes.append(sec_node)
        edges.append(Edge(type="HAS_SECTION", from_id=source_path, to_id=sec_id))
        return PluginResult(nodes=nodes, edges=edges)

    # --- Build sections ---
    parent_stack: list[tuple[str, int]] = []  # (local_id, level)
    child_counters: dict[str | None, int] = {}  # parent_local_id → child count

    for idx, (line_idx, level, title) in enumerate(headings):
        # Content range: heading+1 … next heading (or EOF).
        content_start = line_idx + 1
        content_end = headings[idx + 1][0] if idx + 1 < len(headings) else len(lines)
        content = "\n".join(lines[content_start:content_end]).strip()

        # Find parent: walk stack backwards.
        while parent_stack and parent_stack[-1][1] >= level:
            parent_stack.pop()

        parent_local_id: str | None = None
        if parent_stack:
            parent_local_id = parent_stack[-1][0]

        # Generate local section ID (s0, s0.0, s0.0.1, ...).
        order = child_counters.get(parent_local_id, 0)
        child_counters[parent_local_id] = order + 1

        if parent_local_id:
            local_id = f"{parent_local_id}.{order}"
        else:
            local_id = f"s{order}"

        sec_id = _sec_id(source_path, local_id)

        sec_node = Node(
            type="Section",
            data={
                "id": sec_id,
                "heading": title,
                "level": level,
                "content": content,
                "summary": _extract_summary(content),
                "doc_path": source_path,
            },
        )
        nodes.append(sec_node)

        # Edge: Document → root section, or parent Section → child Section.
        if parent_local_id is None:
            edges.append(Edge(
                type="HAS_SECTION",
                from_id=source_path,
                to_id=sec_id,
            ))
        else:
            edges.append(Edge(
                type="HAS_SUBSECTION",
                from_id=_sec_id(source_path, parent_local_id),
                to_id=sec_id,
            ))

        parent_stack.append((local_id, level))

    return PluginResult(nodes=nodes, edges=edges)


# --- Helpers ---

def _sec_id(doc_path: str, local_id: str) -> str:
    """Build a globally unique section ID: doc:<path>:<local_id>."""
    return f"doc:{doc_path}:{local_id}"


def _infer_title(markdown: str, headings: list[tuple[int, int, str]]) -> str:
    """Pick first H1, or first heading, or first non-empty line."""
    if headings:
        for _, level, title in headings:
            if level == 1:
                return title
        return headings[0][2]
    for line in markdown.split("\n"):
        line = line.strip()
        if line:
            return line[:100]
    return "Untitled"


def _extract_summary(text: str, max_chars: int = 200) -> str:
    """First non-empty, non-heading paragraph."""
    for para in text.split("\n\n"):
        para = para.strip()
        if para and not para.startswith("#") and not para.startswith("<!--"):
            if len(para) > max_chars:
                cut = para[:max_chars].rsplit(" ", 1)[0]
                return cut + "..."
            return para
    return ""


def _infer_levels_from_numbering(
    headings: list[tuple[int, int, str]],
) -> list[tuple[int, int, str]]:
    """Re-assign heading levels based on numbering patterns.

    When docling outputs all headings as ## (level 2), this function detects
    numbered headings like "3.14.1 Title" and assigns level = dot count + 1:
      - "1 Introduction"        → level 1
      - "2.1 Compatibility"     → level 2
      - "3.14.1 Internal reset" → level 3
      - "Figure 4. ..."         → level of previous section + 1
      - Unnumbered headings     → level 1 (top-level)
    """
    result = []
    prev_level = 1

    for line_idx, _orig_level, title in headings:
        m = _NUMBERED_RE.match(title)
        if m:
            num_str = m.group(1)  # e.g. "3.14.1"
            level = num_str.count(".") + 1
            prev_level = level
            result.append((line_idx, level, title))
        elif _FIGURE_TABLE_RE.match(title):
            # Figures/Tables belong to the current section
            result.append((line_idx, prev_level + 1, title))
        else:
            # Unnumbered heading (e.g. "Contents", "Features") — treat as top-level
            result.append((line_idx, 1, title))
            prev_level = 1

    return result
