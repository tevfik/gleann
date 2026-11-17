# Agent-level task scenarios (simulated, not measured)

> **This page is not an empirical evaluation.** The agent suite behind
> `gleann bench --agent` (`pkg/benchmark/agent_eval.go`) is a scripted scenario.
> Tool-call counts, context-token counts, recall and precision for the "baseline"
> agent and for the "gleann" agent are constants written into the code. No agent,
> model or index is run. The suite therefore cannot show that gleann saves tokens
> or raises task success, and no percentage it prints should be quoted as a result.

## Where to find measured results

- [Retrieval experiments](retrieval-experiments.md): what was tried to improve
  code retrieval, with corpus, task sets and numbers from real runs.
- [Benchmarks](benchmarks.md): retrieval benchmark of BM25, vector, hybrid and
  GraphRAG strategies on real task sets.

## Status

A real agent-level evaluation is planned: the same tasks run by an actual agent
with and without gleann's tools, scoring task completion, tool calls and context
tokens from recorded sessions. Until it exists, the claim "gleann reduces agent
tool calls and context size" is a hypothesis, not a finding.

## The simulated scenarios

The suite models four tasks. For each, the "without gleann" and "with gleann"
behaviour below is what the scenario *assumes*.

1. **Bug locating (`BugLocate`):** find a bug in embedding-dimension mismatch
   validation.
   - *Assumed without gleann:* grep queries match common strings across many
     files and several candidate files are loaded into context.
   - *Assumed with gleann:* `gleann_search` combines BM25 (with camelCase
     sub-word splitting) and dense retrieval and ranks the exact symbol first.

2. **Function inspection and modification (`FunctionModify`):** inspect and
   modify the camelCase tokenizer.
   - *Assumed without gleann:* whole source files are read into context.
   - *Assumed with gleann:* `gleann_read` (modes `map`, `signatures`, `lines`)
     returns a targeted structural overview.

3. **Impact analysis (`ImpactAnalysis`):** blast radius of refactoring the
   `OpenStore` symbol.
   - *Assumed without gleann:* grepping the name returns false positives from
     tests, mocks and identically named methods.
   - *Assumed with gleann:* `gleann_impact` walks the code graph with exact
     fully-qualified names and separates production callers from test callers.

4. **Cross-session recall (`CrossSessionRecall`):** recall an architecture
   decision made in a prior session.
   - *Assumed without gleann:* nothing survives between sessions.
   - *Assumed with gleann:* tiered memory (`memory_context`, `memory_remember`)
     persists decisions across sessions.

These descriptions say what each tool is designed to do. Whether an agent
benefits in practice is what the planned evaluation has to measure.

## Running the simulation

```bash
gleann bench --agent
```

The command prints a notice that the output is simulated, and the generated
report carries the same notice. Do not overwrite this page with that output.
