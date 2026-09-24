# Gleann vs Baseline: Agent-Level Task Evaluation (T25)

Empirical evaluation comparing an AI coding agent with native tools (file reading, grep, zero cross-session state) against an agent empowered by **Gleann** (hybrid dense+BM25 search, mode-aware file read, AST code graph blast radius, and tiered persistent BBolt memory).

## Executive Summary

| Metric | Baseline (Without Gleann) | Gleann-Augmented | Gain / Improvement |
|---|---|---|---|
| **Task Success Rate** | 75.0% | **100.0%** | **+25.0%** |
| **Average Tool Calls / Task** | 6.00 calls | **1.00 calls** | **-83.3%** calls |
| **Average Context Tokens / Task** | 10788 tokens | **550 tokens** | **-94.9%** token savings |
| **Recall Accuracy** | 60.2% | **100.0%** | **+39.8%** |
| **Precision Rate** | 42.5% | **100.0%** | **+57.5%** |

## Detailed Task Breakdown

| Task ID | Type | Task Description | Baseline Calls | Gleann Calls | Baseline Tokens | Gleann Tokens | Token Savings | Recall Gain |
|---|---|---|---|---|---|---|---|---|
| `agent-01-bug` | BugLocate | Find Bug in Embedding Dimension Mismatch Validation | 7 | **1** | 17200 | **660** | **96.2%** | **1.3x** |
| `agent-02-modify` | FunctionModify | Inspect and Modify CamelCase Tokenizer Implementation | 3 | **1** | 4950 | **510** | **89.7%** | **1.0x** |
| `agent-03-impact` | ImpactAnalysis | Analyze Blast Radius of OpenStore Symbol Refactor | 8 | **1** | 13000 | **690** | **94.7%** | **1.5x** |
| `agent-04-recall` | CrossSessionRecall | Recall Architecture Decision from Prior Session | 6 | **1** | 8000 | **340** | **95.8%** | **99.0x** |

## Key Agent Capabilities Evaluated

1. **Bug Locating (`BugLocate`):**
   - *Without Gleann:* Agent relies on grep queries, matches common strings across dozens of files, loads multiple candidate files into context.
   - *With Gleann:* `gleann_search` combines Okapi BM25 (with camelCase sub-word splitting) and dense vector retrieval, boosting exact symbol matches to rank #1 in a single tool call.

2. **Function Inspection & Modification (`FunctionModify`):**
   - *Without Gleann:* Reading full source files blows up agent context windows (4,000–8,000+ tokens per file).
   - *With Gleann:* `gleann_read` (modes: `map`, `signatures`, `lines`) delivers targeted structural overviews, reducing tokens by **85–94%**.

3. **Impact Analysis & Blast Radius (`ImpactAnalysis`):**
   - *Without Gleann:* Grepping symbol names returns false positives from test assertions, mocks, and identically named methods across different structs.
   - *With Gleann:* `gleann_impact` traverses the KùzuDB AST graph with exact FQN resolution, strictly isolating production callers from test callers with 100% precision.

4. **Cross-Session Memory Recall (`CrossSessionRecall`):**
   - *Without Gleann:* Zero memory survives across sessions. The agent repeats mistakes or re-queries previously answered architectural questions.
   - *With Gleann:* Tiered BBolt memory (`memory_context`, `memory_remember`) automatically persists decisions across sessions with Git repo scope isolation, deduplication, and code-change staleness invalidation.
