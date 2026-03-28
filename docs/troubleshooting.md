# Troubleshooting

Common issues and their solutions. Run `gleann doctor` for automated diagnostics.

## Ollama Connection Issues

### "Cannot connect to Ollama"

**Symptoms:** Embedding or ask commands fail with connection errors.

**Quick fix:**
```bash
# Check if Ollama is running
curl http://localhost:11434/api/tags

# Start Ollama
ollama serve

# Or via Docker
docker run -d -p 11434:11434 ollama/ollama
```

**If Ollama is on a different host:**
```bash
# Set in config
gleann config edit
# Change "ollama_host" to "http://your-server:11434"

# Or via environment variable
export OLLAMA_HOST=http://your-server:11434
```

### "Model not found"

**Symptoms:** `Error: model "bge-m3" not found`

**Fix:** Pull the required models:
```bash
# Embedding model (required)
ollama pull bge-m3

# Chat/ask model
ollama pull llama3.2:3b-instruct

# List available models
ollama list
```

## Performance Issues

### "First embedding is very slow"

**Why:** The first embedding call triggers model loading into GPU/RAM. This is normal — subsequent calls are much faster.

| Model | First call | Subsequent calls |
|-------|-----------|-----------------|
| bge-m3 | 5-15s | ~100ms per batch |
| nomic-embed | 3-10s | ~80ms per batch |

**Tips:**
- Use `--batch-size 64` for larger batches (faster throughput)
- Use `--concurrency 2` to parallelize (if you have enough VRAM)
- Consider [FAISS backend](faiss.md) for larger datasets

### "GPU not detected / Running on CPU"

**Check GPU status:**
```bash
# NVIDIA
nvidia-smi

# Check Ollama GPU usage
curl http://localhost:11434/api/tags | python3 -m json.tool
```

**Ollama GPU troubleshooting:**
- Ensure NVIDIA drivers are installed: `nvidia-smi` should show your GPU
- Ollama auto-detects NVIDIA GPUs — no config needed
- For AMD GPUs, install ROCm: see [Ollama GPU docs](https://github.com/ollama/ollama/blob/main/docs/gpu.md)
- Apple Silicon (M1/M2/M3): Ollama uses Metal automatically

### "Index build takes too long"

For large codebases (>10,000 files):

```bash
# Use .gleannignore to exclude irrelevant files
echo "vendor/" >> .gleannignore
echo "node_modules/" >> .gleannignore
echo "*.min.js" >> .gleannignore
echo ".git/" >> .gleannignore

# Rebuild with pruning (skip unchanged files)
gleann index rebuild my-code --docs ./src --prune
```

## Search Quality Issues

### "Search results are not relevant"

1. **Enable reranking** for better precision:
   ```bash
   gleann search my-docs "query" --rerank
   ```

2. **Try hybrid search** (vector + keyword):
   ```bash
   gleann search my-docs "query" --hybrid
   ```

3. **Increase result count** and review:
   ```bash
   gleann search my-docs "query" --top-k 20
   ```

4. **Rebuild with smaller chunks** for more precise matching:
   ```bash
   gleann index rebuild my-docs --docs ./docs --chunk-size 256 --chunk-overlap 25
   ```

### "AST graph queries return no results"

- Ensure the index was built with `--graph`:
  ```bash
  gleann index build my-code --docs ./src --graph
  ```
- The full build (with tree-sitter) is required for AST parsing:
  ```bash
  make build-full   # CGo build with tree-sitter support
  ```

## Plugin Issues

### "PDF files not indexed"

Gleann requires the docs plugin for PDF/DOCX/XLSX files:

```bash
# Install via TUI
gleann tui
# Navigate to Plugins → Install gleann-plugin-docs

# Or download manually
# See: docs/plugin-install-guide.md
```

### "Plugin fails to start"

```bash
# Check plugin health
curl http://localhost:<port>/health

# Check plugin registration
cat ~/.gleann/plugins.json

# Restart gleann to reload plugins
```

## Configuration Issues

### "Config changes not taking effect"

**Precedence** (highest to lowest):
1. CLI flags (`--model`, `--host`, etc.)
2. Environment variables (`OLLAMA_HOST`, etc.)
3. Config file (`~/.gleann/config.json`)
4. Built-in defaults

```bash
# Verify current config
gleann config show

# Validate config file
gleann config validate

# Check effective settings
gleann doctor
```

### "Reset to default config"

```bash
# Remove config and reconfigure
rm ~/.gleann/config.json
gleann setup --bootstrap
```

## Docker Issues

### "Ollama not reachable from container"

In Docker, use the service name instead of `localhost`:

```yaml
# docker-compose.yml
environment:
  OLLAMA_HOST: http://ollama:11434  # NOT localhost
```

### "Out of memory in container"

Add resource limits to `docker-compose.yml`:

```yaml
services:
  gleann:
    deploy:
      resources:
        limits:
          memory: 2G
```

## Getting More Help

```bash
# Automated diagnostics
gleann doctor

# Show version
gleann version

# Check system status
gleann setup --check
```

If the issue persists, please [open an issue](https://github.com/tevfik/gleann/issues) with the output of `gleann doctor`.
