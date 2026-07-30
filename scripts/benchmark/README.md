# Gleann Academic RAG Benchmark

This directory contains the academic-grade evaluation tools for Gleann's Retrieval-Augmented Generation (RAG) capabilities. Unlike standard integration tests that verify API responses, this benchmark measures the **intelligence and accuracy** of the retrieval engine.

## What is tested?

1. **Needle In A Haystack (NIAH)**:
   A massive, complex document (haystack) is generated, and a specific fact (needle) is inserted at different depth percentages (10%, 50%, 90%). Gleann must index the document and retrieve the exact needle via a specific query. This tests the Context Limit, Embedding Resolution, and Graph Navigation.

2. **RAGAS (LLM-as-a-Judge) Evaluation**:
   Uses industry-standard pseudo-RAGAS evaluations:
   *   **Faithfulness:** Verifies that Gleann's generated answer relies *only* on the retrieved context, penalizing LLM hallucination.
   *   **Answer Relevancy:** Validates that the answer directly addresses the query rather than giving generic filler.

## Prerequisites

- Python 3.9+
- `requests` library (`pip install requests`)
- Ollama running locally on `http://localhost:11434`
- Recommended models: `llama3.2` and `nomic-embed-text`

## Usage

```bash
cd scripts/benchmark
python3 evaluator.py
```

The script will build temporary indexes, run the benchmark suite, tear down the indexes to prevent clutter, and output a detailed JSON report to `gleann_rag_benchmark_results.json`.

## Baseline Results (Gleann Retrieval + Qwen3.5:9b Judge)

Below is an example of the baseline benchmark run on a standard local hardware setup:

| Benchmark | Test | Score / Status | Build Time | Query Time |
| :--- | :--- | :--- | :--- | :--- |
| **NIAH** | Depth 10% | 1 / 1 ✅ (Needle retrieved successfully) | ~5400ms | ~1050ms |
| **NIAH** | Depth 50% | 1 / 1 ✅ (Needle retrieved successfully) | ~2900ms | ~1050ms |
| **NIAH** | Depth 90% | 1 / 1 ✅ (Needle retrieved successfully) | ~750ms | ~1050ms |
| **RAGAS** | Faithfulness | 1 / 1 ✅ (Context claims fully validated) | - | - |
| **RAGAS** | Answer Relevancy | 1 / 1 ✅ (Answer matched query intent) | - | - |

*(Note: The RAGAS evaluation requires a capable LLM (like `qwen3.5:9b` or higher) to act as the judge. Small models (like 3B) often fail at boolean logic extraction. The NIAH test passes flawlessly when texts have natural semantic variety. If you test it with completely identical repetitive noise, standard TF-IDF and basic embeddings may collapse the vector space.)*

## Submitting to the Community
We encourage users to run this benchmark on their specific hardware/model combinations and share the `gleann_rag_benchmark_results.json` in our GitHub discussions. This helps us chart performance across different open-source embedding models!
