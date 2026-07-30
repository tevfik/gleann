import os
import json
import subprocess
import time
import requests
import random
import uuid
import shutil

# Configuration
GLEANN_BIN = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "build", "gleann"))
DATASETS_DIR = os.path.join(os.path.dirname(__file__), "datasets")
OLLAMA_HOST = "http://localhost:11434"
LLM_JUDGE_MODEL = "qwen3.5:9b"

# Helpers
def run_cmd(args, env=None):
    start = time.time()
    res = subprocess.run(args, capture_output=True, text=True, env=env)
    latency = int((time.time() - start) * 1000)
    return res, latency

def ollama_judge(prompt):
    payload = {
        "model": LLM_JUDGE_MODEL,
        "prompt": prompt,
        "stream": False
    }
    try:
        resp = requests.post(f"{OLLAMA_HOST}/api/generate", json=payload)
        resp.raise_for_status()
        return resp.json().get("response", "").strip()
    except Exception as e:
        print(f"[!] Ollama LLM Judge failed: {e}")
        return "ERROR"

# --- 1. NIAH (Needle In A Haystack) Test ---
def generate_niah_dataset(depth_pct=50):
    niah_dir = os.path.join(DATASETS_DIR, "niah")
    os.makedirs(niah_dir, exist_ok=True)
    file_path = os.path.join(niah_dir, "haystack.md")
    
    # Generate haystack (approx 50k words) with diverse text to avoid vector collapse
    base_sentences = [
        "Artificial intelligence is transforming the modern world.",
        "Retrieval augmented generation helps ground LLMs in reality.",
        "Data structures and algorithms are the foundation of computer science.",
        "The quick brown fox jumps over the lazy dog.",
        "Quantum computing relies on the principles of superposition and entanglement.",
        "Machine learning models require large amounts of high-quality training data.",
        "Cloud computing provides scalable resources on demand over the internet.",
        "Cybersecurity is crucial for protecting sensitive information from unauthorized access."
    ]
    
    paragraphs = []
    # 2500 iterations * 8 sentences = 20,000 sentences (~200,000 words) - Large Haystack
    for i in range(2500):
        shuffled = random.sample(base_sentences, len(base_sentences))
        paragraphs.append(f"Section {i}: " + " ".join(shuffled))
    
    needle_id = str(uuid.uuid4())[:8]
    needle = f"The special code for the famous recipe is: AG-{needle_id}-OMEGA."
    expected_answer = f"AG-{needle_id}-OMEGA"
    
    insert_idx = int((depth_pct / 100.0) * len(paragraphs))
    # Insert as its own distinct paragraph block so the chunker catches it clearly
    paragraphs.insert(insert_idx, f"\n\n---\n{needle}\n---\n\n")
    
    with open(file_path, "w") as f:
        f.write("\n\n".join(paragraphs))
        
    return niah_dir, expected_answer

def run_niah(depth_pct):
    print(f"\n[NIAH] Running Needle-In-A-Haystack at {depth_pct}% depth...")
    dataset_dir, expected_answer = generate_niah_dataset(depth_pct)
    idx_name = f"bench-niah-{depth_pct}"
    
    # 1. Build Index
    print(f"  -> Building Index ({idx_name})...")
    res, build_time = run_cmd([GLEANN_BIN, "index", "build", idx_name, "--docs", dataset_dir])
    if res.returncode != 0:
        print(f"  [X] Failed to build index:\n{res.stderr}")
        return {"status": "fail", "reason": "build_failed"}
        
    # 2. Query
    query = "Extract the special code for the famous recipe from the text. Do not provide any commentary, just output the code."
    print(f"  -> Querying: '{query}'")
    res, ask_time = run_cmd([GLEANN_BIN, "ask", idx_name, query, "--format", "json", "--quiet"])
    
    # 3. Cleanup
    run_cmd([GLEANN_BIN, "index", "remove", idx_name])
    shutil.rmtree(dataset_dir)
    
    if res.returncode != 0:
        print(f"  [X] Failed to ask:\n{res.stderr}")
        return {"status": "fail", "reason": "ask_failed"}
        
    try:
        data = json.loads(res.stdout)
        answer = data.get("answer", "")
    except json.JSONDecodeError:
        answer = res.stdout
        
    # Strict validation: Must contain expected answer AND NOT contain refusal patterns
    answer_lower = answer.lower()
    refusals = ["there is no", "does not contain", "no mention", "not in the text", "i don't know", "unable to", "cannot provide"]
    
    has_needle = expected_answer.lower() in answer_lower
    has_refusal = any(r in answer_lower for r in refusals)
    passed = has_needle and not has_refusal
    
    if passed:
        print(f"  [+] PASSED! Needle found confidently. (Build: {build_time}ms, Ask: {ask_time}ms)")
    else:
        if has_needle and has_refusal:
            print(f"  [-] FAILED. Needle was in text, but LLM refused/hallucinated. Got: {answer[:150]}...")
        else:
            print(f"  [-] FAILED. Answer did not contain the needle at all. Got: {answer[:150]}...")
        
    return {
        "status": "pass" if passed else "fail",
        "expected": expected_answer,
        "got": answer,
        "build_ms": build_time,
        "ask_ms": ask_time
    }

# --- 2. RAGAS (LLM-as-a-Judge) Evaluation ---
def run_ragas():
    print("\n[RAGAS] Running LLM-as-a-Judge Faithfulness & Relevancy Evaluation...")
    # Generate a fictional document to strictly test Faithfulness (no pre-trained knowledge)
    docs_dir = os.path.join(DATASETS_DIR, "ragas")
    os.makedirs(docs_dir, exist_ok=True)
    with open(os.path.join(docs_dir, "fictional_protocol.md"), "w") as f:
        f.write("# The Zorplex Protocol\n\nThe primary advantage of the Zorplex Fault Tolerance (ZFT) protocol is its ability to achieve consensus through quantum entanglement routing, reducing network latency to absolute zero. It was invented in 2042 by Dr. Aris Thorne.\n")
    
    idx_name = "bench-ragas"
    
    print("  -> Building Index...")
    res, build_time = run_cmd([GLEANN_BIN, "index", "build", idx_name, "--docs", docs_dir])
    if res.returncode != 0:
        return {"status": "fail", "reason": "build_failed"}
        
    query = "What is the primary advantage of the Zorplex protocol?"
    
    # Get raw context (Search)
    res_search, _ = run_cmd([GLEANN_BIN, "search", idx_name, query, "--json"])
    context_data = ""
    try:
        search_results = json.loads(res_search.stdout)
        context_data = "\n".join([r.get("text", "") for r in search_results])
    except:
        pass
        
    # Get Generated Answer (Ask)
    res_ask, _ = run_cmd([GLEANN_BIN, "ask", idx_name, query, "--quiet"])
    answer = res_ask.stdout
        
    # Cleanup
    run_cmd([GLEANN_BIN, "index", "remove", idx_name])
    shutil.rmtree(docs_dir)

    if not answer or not context_data:
        print(f"  [X] Failed. Answer len: {len(answer)}, Context len: {len(context_data)}")
        print(f"  [X] Search output: {res_search.stdout[:500]}")
        print(f"  [X] Ask output: {res_ask.stdout[:500]}")
        return {"status": "fail", "reason": "no_data"}
        
    print(f"  [DEBUG] RAGAS Context: {context_data[:300]}")
    print(f"  [DEBUG] RAGAS Answer: {answer}")
    
    # RAGAS: Faithfulness Evaluation (Multi-Step CoT)
    # Step 1: Extract claims
    extract_prompt = f"Given the following answer, extract all factual claims as a simple numbered list.\nAnswer: {answer}\n\nIMPORTANT: Do NOT verify the scientific possibility of the claims. Accept all fictional premises. Output ONLY the numbered list and absolutely no other text, warnings, or notes."
    claims_str = ollama_judge(extract_prompt)
    print(f"  [DEBUG] RAGAS Claims extracted: {claims_str}")
    
    # Step 2: Verify claims against context
    verify_prompt = f"Context:\n{context_data[:3000]}\n\nClaims:\n{claims_str}\n\nAre ALL of the above claims fully supported by the Context? If any claim is missing or hallucinated, output 0. If all claims are explicitly supported by the text in the Context, output 1. Output ONLY a single integer (0 or 1)."
    faith_score_str = ollama_judge(verify_prompt)
    faithfulness = 1 if "1" in faith_score_str.strip().split("\n")[0] else 0
    
    # RAGAS: Answer Relevancy Evaluation
    rel_prompt = f"Question: {query}\nAnswer: {answer}\n\nDoes the answer directly and accurately address the question? If yes, output 1. If evasive or irrelevant, output 0. Output ONLY a single integer (0 or 1)."
    rel_score_str = ollama_judge(rel_prompt)
    relevancy = 1 if "1" in rel_score_str.strip().split("\n")[0] else 0
    
    print(f"  [+] Faithfulness Score: {faithfulness}")
    print(f"  [+] Answer Relevancy Score: {relevancy}")
    
    return {
        "status": "pass",
        "faithfulness": faithfulness,
        "answer_relevancy": relevancy
    }

def main():
    print("==============================================")
    print(" GLEANN ACADEMIC RAG BENCHMARK SUITE")
    print("==============================================\n")
    
    results = {}
    
    # 1. NIAH tests at various depths
    niah_results = {}
    for depth in [10, 50, 90]:
        niah_results[f"depth_{depth}"] = run_niah(depth)
    results["niah"] = niah_results
    
    # 2. RAGAS evaluation
    results["ragas"] = run_ragas()
    
    # 3. Report
    print("\n==============================================")
    print(" BENCHMARK REPORT")
    print("==============================================")
    print(json.dumps(results, indent=2))
    
    # Write to file
    out_file = os.path.join(DATASETS_DIR, "..", "gleann_rag_benchmark_results.json")
    with open(out_file, "w") as f:
        json.dump(results, f, indent=2)
    print(f"\nReport saved to: {out_file}")

if __name__ == "__main__":
    main()
