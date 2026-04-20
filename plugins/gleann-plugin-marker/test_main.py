"""Tests for gleann-plugin-marker."""

import json
import os
import tempfile
import pytest

# Ensure section_parser is importable
import sys
sys.path.insert(0, os.path.dirname(__file__))

import section_parser


class TestSectionParser:
    """Test section_parser compatibility with marker output."""

    def test_basic_markdown(self):
        md = "# Title\n\nSome content.\n\n## Section 1\n\nText here.\n"
        result = section_parser.parse_document(md, "test.pdf", "pdf")
        d = result.to_dict()

        assert len(d["nodes"]) >= 3  # Document + 2 sections
        assert d["nodes"][0]["_type"] == "Document"
        assert d["nodes"][0]["title"] == "Title"

    def test_empty_markdown(self):
        result = section_parser.parse_document("", "empty.pdf", "pdf")
        d = result.to_dict()
        # Should still produce a Document node
        assert any(n["_type"] == "Document" for n in d["nodes"])

    def test_marker_style_output(self):
        """marker outputs clean markdown with proper heading hierarchy."""
        md = """# Switch Transformers

## Abstract

We present Switch Transformers, a simplified mixture-of-experts architecture.

## 1 Introduction

Mixture of Experts (MoE) models have shown promise.

### 1.1 Background

The concept of conditional computation was introduced by Bengio et al.

## 2 Method

Our method simplifies the MoE routing mechanism.

### 2.1 Simplified Routing

We use a simplified top-1 routing strategy.

### 2.2 Load Balancing

To ensure balanced expert utilization we introduce an auxiliary loss.
"""
        result = section_parser.parse_document(md, "paper.pdf", "pdf", page_count=12)
        d = result.to_dict()

        doc_nodes = [n for n in d["nodes"] if n["_type"] == "Document"]
        sec_nodes = [n for n in d["nodes"] if n["_type"] == "Section"]

        assert len(doc_nodes) == 1
        assert doc_nodes[0]["title"] == "Switch Transformers"
        assert doc_nodes[0]["page_count"] == 12
        assert len(sec_nodes) >= 6

        # Check edge structure
        has_section = [e for e in d["edges"] if e["_type"] == "HAS_SECTION"]
        has_subsection = [e for e in d["edges"] if e["_type"] == "HAS_SUBSECTION"]
        assert len(has_section) >= 1  # Document → root sections
        assert len(has_subsection) >= 2  # Section → subsections

    def test_node_ids_are_unique(self):
        md = "# A\n\n## B\n\n## C\n\n### D\n\n# E\n\n"
        result = section_parser.parse_document(md, "test.pdf", "pdf")
        d = result.to_dict()

        ids = [n.get("id", n.get("path", "")) for n in d["nodes"]]
        assert len(ids) == len(set(ids)), f"Duplicate IDs found: {ids}"

    def test_page_count_propagation(self):
        md = "# Title\n\nContent.\n"
        result = section_parser.parse_document(md, "doc.pdf", "pdf", page_count=42)
        d = result.to_dict()
        doc = next(n for n in d["nodes"] if n["_type"] == "Document")
        assert doc["page_count"] == 42


class TestPluginProtocol:
    """Test that the plugin response format matches gleann expectations."""

    def test_response_structure(self):
        md = "# Test\n\nContent.\n"
        result = section_parser.parse_document(md, "test.pdf", "pdf")
        d = result.to_dict()

        # Must have nodes and edges
        assert "nodes" in d
        assert "edges" in d

        # Every node must have _type
        for node in d["nodes"]:
            assert "_type" in node
            assert node["_type"] in ("Document", "Section")

        # Every edge must have _type, from, to
        for edge in d["edges"]:
            assert "_type" in edge
            assert "from" in edge
            assert "to" in edge
            assert edge["_type"] in ("HAS_SECTION", "HAS_SUBSECTION")

    def test_json_serializable(self):
        md = "# Test\n\n## Sub\n\nContent.\n"
        result = section_parser.parse_document(md, "test.pdf", "pdf")
        # Must be JSON-serializable
        json_str = json.dumps(result.to_dict())
        parsed = json.loads(json_str)
        assert len(parsed["nodes"]) > 0


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
