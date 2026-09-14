import { useState, useEffect } from 'react';
import { Settings, Terminal, Activity, Sparkles, Database, AlertCircle } from 'lucide-react';

export function System() {
  const [tasks, setTasks] = useState<any[]>([]);
  const [stats, setStats] = useState<any>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [plugins, setPlugins] = useState<any[]>([]);
  const [serverVersion, setServerVersion] = useState<string>('Unknown');
  const [embeddingModels, setEmbeddingModels] = useState<string[]>([]);
  const [llmModels, setLlmModels] = useState<string[]>([]);
  const [localModels, setLocalModels] = useState<string[]>([]);

  // Config edit state
  const [editConfig, setEditConfig] = useState<any>(null);
  const [savingConfig, setSavingConfig] = useState(false);

  useEffect(() => {
    const fetchSystemInfo = () => {
      fetch('/api/tasks')
        .then(res => res.json())
        .then(data => setTasks(data.tasks || []))
        .catch(console.error);

      fetch('/api/blocks/stats')
        .then(res => res.json())
        .then(data => setStats(data))
        .catch(console.error);

      fetch('/api/config')
        .then(res => res.json())
        .then(data => {
          setEditConfig((prev: any) => prev ? prev : data);
        })
        .catch(console.error);

      fetch('/api/logs')
        .then(res => res.json())
        .then(data => setLogs(data.logs || []))
        .catch(console.error);

      fetch('/api/plugins')
        .then(res => res.json())
        .then(data => setPlugins(data.plugins || []))
        .catch(console.error);

      fetch('/health')
        .then(res => res.json())
        .then(data => setServerVersion(data.version || 'Unknown'))
        .catch(console.error);

      fetch('/api/models/local')
        .then(res => res.json())
        .then(data => setLocalModels(data.models || []))
        .catch(console.error);
    };

    fetchSystemInfo();
    const interval = setInterval(fetchSystemInfo, 5000);
    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    if (!editConfig) return;
    const fetchModels = async (provider: string, host: string, apikey: string, setter: any) => {
      if (provider !== 'ollama' && provider !== 'openai') {
        setter([]);
        return;
      }
      try {
        const u = new URL('/api/proxy/models', window.location.href);
        u.searchParams.set('provider', provider);
        u.searchParams.set('host', host || '');
        u.searchParams.set('apikey', apikey || '');
        const r = await fetch(u.toString());
        const d = await r.json();
        setter(d.models || []);
      } catch (err) {
        console.error(err);
      }
    };
    
    fetchModels(
      editConfig.embedding_provider, 
      editConfig.embedding_provider === 'ollama' ? editConfig.ollama_host : editConfig.openai_base_url,
      editConfig.openai_api_key,
      setEmbeddingModels
    );
  }, [editConfig?.embedding_provider, editConfig?.ollama_host, editConfig?.openai_base_url, editConfig?.openai_api_key]);

  useEffect(() => {
    if (!editConfig) return;
    const fetchModels = async (provider: string, host: string, apikey: string, setter: any) => {
      if (provider !== 'ollama' && provider !== 'openai') {
        setter([]);
        return;
      }
      try {
        const u = new URL('/api/proxy/models', window.location.href);
        u.searchParams.set('provider', provider);
        u.searchParams.set('host', host || '');
        u.searchParams.set('apikey', apikey || '');
        const r = await fetch(u.toString());
        const d = await r.json();
        setter(d.models || []);
      } catch (err) {
        console.error(err);
      }
    };
    
    fetchModels(
      editConfig.llm_provider, 
      editConfig.llm_provider === 'ollama' ? editConfig.ollama_host : editConfig.openai_base_url,
      editConfig.openai_api_key,
      setLlmModels
    );
  }, [editConfig?.llm_provider, editConfig?.ollama_host, editConfig?.openai_base_url, editConfig?.openai_api_key]);

  const handleInstallPlugin = async (name: string) => {
    try {
      await fetch(`/api/plugins/${name}/install`, { method: 'POST' });
      alert('Installation task started in the background. Check Background Tasks.');
    } catch (err) {
      console.error(err);
      alert('Failed to start installation.');
    }
  };

  const handleDownloadModel = async (url: string, filename: string) => {
    if (!confirm(`Are you sure you want to download ${filename} to the server? This may take a while depending on your network.`)) return;
    try {
      const res = await fetch('/api/models/download', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url, filename })
      });
      if (!res.ok) throw new Error(await res.text());
      alert(`Started downloading ${filename}. You can check progress in the System Tasks section!`);
    } catch (err) {
      alert(`Download failed to start: ${err}`);
    }
  };

  const handleUninstallPlugin = async (name: string) => {
    if (!confirm(`Are you sure you want to uninstall ${name}?`)) return;
    try {
      await fetch(`/api/plugins/${name}`, { method: 'DELETE' });
      alert('Plugin uninstalled successfully.');
    } catch (err) {
      console.error(err);
      alert('Failed to uninstall plugin.');
    }
  };

  const handleConfigurePlugin = (p: any) => {
    if (p.settings_cmd && p.settings_cmd.length > 0) {
      alert(`This plugin requires terminal configuration.\nPlease run the following command in your terminal:\n\n${p.settings_cmd.join(' ')}`);
    } else {
      alert('This plugin does not have a web configuration interface.');
    }
  };

  const handleSaveConfig = async () => {
    setSavingConfig(true);
    try {
      await fetch('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(editConfig)
      });
      alert('Configuration updated successfully! Note: Some changes may require a server restart.');
    } catch (err) {
      console.error(err);
      alert('Failed to update configuration.');
    } finally {
      setSavingConfig(false);
    }
  };

  const handleFormatSystem = async () => {
    const confirmation = prompt('⚠️ DANGER: This will delete ALL indexes, ALL conversations, and ALL memories. This action CANNOT be undone. Type "FORMAT" to confirm:');
    if (confirmation !== 'FORMAT') return;
    
    try {
      // 1. Delete all indexes
      const idxRes = await fetch('/api/indexes');
      const idxData = await idxRes.json();
      for (const idx of (idxData.indexes || [])) {
        await fetch(`/api/indexes/${idx.name}`, { method: 'DELETE' });
      }
      
      // 2. Delete all conversations
      await fetch('/api/conversations', { method: 'DELETE' });
      
      // 3. Delete all memories
      await fetch('/api/blocks', { method: 'DELETE' });
      
      alert('System formatted successfully! The page will now reload.');
      window.location.reload();
    } catch (err) {
      console.error(err);
      alert('Error during system format.');
    }
  };

  return (
    <div className="flex-1 max-w-6xl mx-auto w-full px-4 pt-6 pb-24 z-10 relative">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-white flex items-center gap-2">
          <Settings className="w-6 h-6 text-gray-400" /> System Management
        </h1>
        <button 
          onClick={handleFormatSystem}
          className="bg-red-600/20 hover:bg-red-600/40 text-red-400 border border-red-500/30 px-4 py-2 rounded-lg font-medium transition-colors text-sm flex items-center gap-2"
        >
          <AlertCircle className="w-4 h-4" />
          Factory Reset
        </button>
      </div>
      
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
        <div className="space-y-6">
          <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg">
            <h2 className="text-lg font-semibold text-white flex items-center gap-2 mb-4">
              <Activity className="w-5 h-5 text-green-400" /> Server Status
            </h2>
            <div className="space-y-3">
              <div className="flex justify-between items-center border-b border-white/5 pb-2">
                <span className="text-gray-400 text-sm">Server Mode</span>
                <span className="text-gray-200 font-mono text-sm">Online (Port 8080)</span>
              </div>
              {stats && (
                <>
                  <div className="flex justify-between items-center border-b border-white/5 pb-2">
                    <span className="text-gray-400 text-sm">Total Memories</span>
                    <span className="text-gray-200 font-mono text-sm">{stats.total_count || 0}</span>
                  </div>
                  <div className="flex justify-between items-center border-b border-white/5 pb-2">
                    <span className="text-gray-400 text-sm">DB Size</span>
                    <span className="text-gray-200 font-mono text-sm">{((stats.disk_size_bytes || 0) / 1024).toFixed(2)} KB</span>
                  </div>
                </>
              )}
            </div>
          </div>

          <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg flex-1">
            <h2 className="text-lg font-semibold text-white flex items-center gap-2 mb-4">
              <Terminal className="w-5 h-5 text-yellow-400" /> Runtime Logs
            </h2>
            <div className="bg-black/50 border border-white/5 rounded-lg p-3 font-mono text-[10px] text-gray-400 h-64 overflow-y-auto flex flex-col gap-1" style={{ scrollbarWidth: 'thin' }}>
              {logs.length === 0 ? (
                <div className="text-center py-4">No logs available.</div>
              ) : (
                logs.map((log, i) => (
                  <div key={i} className="whitespace-pre-wrap break-all">{log.trim()}</div>
                ))
              )}
            </div>
          </div>
        </div>

        <div className="space-y-6">
          <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg">
            <div className="flex justify-between items-center mb-4">
              <h2 className="text-lg font-semibold text-white flex items-center gap-2">
                <Database className="w-5 h-5 text-blue-400" /> Active Configuration
              </h2>
              <button 
                onClick={handleSaveConfig}
                disabled={savingConfig}
                className="bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white px-3 py-1.5 rounded text-xs font-medium transition-colors"
              >
                {savingConfig ? 'Saving...' : 'Save Changes'}
              </button>
            </div>
            {editConfig ? (
              <div className="bg-black/30 rounded-lg overflow-hidden border border-white/5 p-4 flex flex-col gap-6">

                {/* Storage & Vector Backend */}
                <div className="space-y-3">
                  <h3 className="text-[10px] font-bold text-gray-400 uppercase tracking-wider mb-2 border-b border-white/5 pb-2">Storage Pipeline</h3>
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    <div className="md:col-span-2">
                      <label className="text-xs text-gray-500 font-medium block mb-1">Index Directory</label>
                      <input 
                        type="text" 
                        value={editConfig.index_dir || ''} 
                        onChange={e => setEditConfig({...editConfig, index_dir: e.target.value})}
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors"
                      />
                    </div>
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">Vector Backend</label>
                      <select 
                        value={editConfig.backend || 'faiss'} 
                        onChange={e => setEditConfig({...editConfig, backend: e.target.value})}
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors"
                      >
                        <option value="hnsw">HNSW (In-Memory)</option>
                        <option value="faiss">FAISS (Flat/IVF)</option>
                        <option value="faiss-hybrid">FAISS Hybrid (Dense+Sparse)</option>
                        <option value="diskann">DiskANN</option>
                      </select>
                    </div>
                  </div>
                </div>

                {/* Embedding Provider */}
                <div className="space-y-3">
                  <h3 className="text-[10px] font-bold text-gray-400 uppercase tracking-wider mb-2 border-b border-white/5 pb-2">Embedding Engine</h3>
                  <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">Provider</label>
                      <select 
                        value={editConfig.embedding_provider || 'native'} 
                        onChange={e => setEditConfig({...editConfig, embedding_provider: e.target.value})}
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors"
                      >
                        <option value="ollama">Ollama</option>
                        <option value="openai">OpenAI</option>
                        <option value="native">Native (Go)</option>
                        <option value="llamacpp">Llama.cpp (Local GGUF)</option>
                      </select>
                    </div>
                  </div>

                  {/* llamacpp model picker for embedding */}
                  {editConfig.embedding_provider === 'llamacpp' && (
                    <div className="mt-3 bg-amber-500/10 border border-amber-500/30 rounded-xl p-4 space-y-3">
                      <div className="flex items-center gap-2">
                        <span className="text-amber-400">⚡</span>
                        <span className="text-xs font-semibold text-amber-300">Local GGUF model required — models are stored in <code className="bg-black/30 px-1 rounded">~/.gleann/models/</code></span>
                      </div>
                      <p className="text-[11px] text-amber-400/70">Click a model below to select it, then click the HuggingFace link to download it. You can also type a custom filename.</p>
                      <div className="flex flex-wrap gap-2">
                        {[
                          { label: 'BGE-Micro v2 (Tiny/Fast)', file: 'bge-micro-v2-q4_k_m.gguf', hf: 'https://huggingface.co/BARTOWSKI/bge-micro-v2-GGUF/resolve/main/bge-micro-v2-q4_k_m.gguf' },
                          { label: 'Nomic Embed Text v1.5', file: 'nomic-embed-text-v1.5.Q4_K_M.gguf', hf: 'https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.Q4_K_M.gguf' },
                        ].map(m => (
                          <div key={m.file} className={`flex items-center gap-1 rounded-lg border text-[11px] overflow-hidden transition-all ${
                            editConfig.embedding_model === m.file
                              ? 'border-amber-400/60 bg-amber-500/20'
                              : 'border-white/10 bg-black/30 hover:border-amber-500/40'
                          }`}>
                            <button
                              type="button"
                              onClick={() => setEditConfig({...editConfig, embedding_model: m.file})}
                              className="px-2.5 py-1.5 text-amber-200 hover:text-white font-mono"
                              title="Click to select this model"
                            >
                              {m.label}
                            </button>
                            <button
                              type="button"
                              onClick={() => handleDownloadModel(m.hf, m.file)}
                              className="px-2 py-1.5 bg-amber-500/20 hover:bg-amber-500/40 text-amber-400 hover:text-white border-l border-amber-500/20 transition-colors"
                              title="Download from HuggingFace to server"
                            >↓</button>
                          </div>
                        ))}
                      </div>
                      <div>
                        <label className="text-[11px] text-amber-400/70 mb-1 block">Select Local Model or Enter Custom Filename:</label>
                        <div className="flex gap-2 mb-2">
                          <select
                            value={localModels.includes(editConfig.embedding_model) ? editConfig.embedding_model : (editConfig.embedding_model ? 'custom' : '')}
                            onChange={e => {
                              if (e.target.value !== 'custom') setEditConfig({...editConfig, embedding_model: e.target.value})
                            }}
                            className="flex-1 bg-black/50 border border-amber-500/30 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-amber-400/60 transition-colors font-mono"
                          >
                            <option value="">-- Select a local model --</option>
                            {localModels.map(m => (
                              <option key={m} value={m}>{m}</option>
                            ))}
                            <option value="custom">-- Custom Filename / Download --</option>
                          </select>
                        </div>
                        {(!localModels.includes(editConfig.embedding_model) || editConfig.embedding_model === '') && (
                          <div className="flex gap-2">
                            <input
                              type="text"
                              value={editConfig.embedding_model || ''}
                              onChange={e => setEditConfig({...editConfig, embedding_model: e.target.value})}
                              placeholder="e.g. my-model-q4_k_m.gguf"
                              className="flex-1 bg-black/50 border border-amber-500/30 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-amber-400/60 transition-colors font-mono"
                            />
                            <button
                              type="button"
                              onClick={() => {
                                if (!editConfig.embedding_model) return alert("Please enter a filename first.");
                                const url = prompt(`Enter HuggingFace or direct download URL for ${editConfig.embedding_model}:`);
                                if (url) handleDownloadModel(url, editConfig.embedding_model);
                              }}
                              className="px-3 py-1.5 bg-amber-600 hover:bg-amber-500 text-white rounded text-xs font-medium transition-colors"
                            >
                              Download
                            </button>
                          </div>
                        )}
                      </div>
                    </div>
                  )}

                  {(editConfig.embedding_provider || 'native') !== 'llamacpp' && (
                    <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mt-3">
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">Endpoint URL</label>
                      <input 
                        type="text" 
                        value={(editConfig.embedding_provider || 'native') === 'ollama' ? (editConfig.ollama_host || 'http://localhost:11434') : (editConfig.embedding_provider || 'native') === 'openai' ? (editConfig.openai_base_url || 'https://api.openai.com/v1') : ''} 
                        onChange={e => {
                          const prov = editConfig.embedding_provider || 'native';
                          if (prov === 'ollama') setEditConfig({...editConfig, ollama_host: e.target.value})
                          else if (prov === 'openai') setEditConfig({...editConfig, openai_base_url: e.target.value})
                        }}
                        disabled={(editConfig.embedding_provider || 'native') === 'native' || (editConfig.embedding_provider || 'native') === 'llamacpp'}
                        placeholder={(editConfig.embedding_provider || 'native') === 'ollama' ? 'http://localhost:11434' : (editConfig.embedding_provider || 'native') === 'llamacpp' ? 'Managed automatically' : 'https://api.openai.com/v1'}
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors disabled:opacity-50"
                      />
                    </div>
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">API Key</label>
                      <input 
                        type="password" 
                        value={(editConfig.embedding_provider || 'native') === 'openai' ? (editConfig.openai_api_key || '') : ''} 
                        onChange={e => {
                          const prov = editConfig.embedding_provider || 'native';
                          if (prov === 'openai') setEditConfig({...editConfig, openai_api_key: e.target.value})
                        }}
                        disabled={(editConfig.embedding_provider || 'native') !== 'openai'}
                        placeholder={(editConfig.embedding_provider || 'native') === 'openai' ? 'sk-...' : 'Not required'}
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors disabled:opacity-50"
                      />
                    </div>
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">Embedding Model</label>
                      <input 
                        type="text" 
                        list="embedding_models_list"
                        value={editConfig.embedding_model || ''} 
                        onChange={e => setEditConfig({...editConfig, embedding_model: e.target.value})}
                        placeholder="e.g. nomic-embed-text"
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors"
                      />
                      {embeddingModels.length > 0 && (
                        <datalist id="embedding_models_list">
                           {embeddingModels.map(m => <option key={m} value={m} />)}
                        </datalist>
                      )}
                    </div>
                  </div>
                  )}
                </div>

                {/* LLM Provider */}
                <div className="space-y-3">
                  <h3 className="text-[10px] font-bold text-gray-400 uppercase tracking-wider mb-2 border-b border-white/5 pb-2">Language Models</h3>
                  <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">LLM Provider</label>
                      <select 
                        value={editConfig.llm_provider || 'ollama'} 
                        onChange={e => setEditConfig({...editConfig, llm_provider: e.target.value})}
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors"
                      >
                        <option value="ollama">Ollama</option>
                        <option value="openai">OpenAI</option>
                        <option value="anthropic">Anthropic</option>
                        <option value="llamacpp">Llama.cpp (Local GGUF)</option>
                      </select>
                    </div>
                  </div>

                  {/* llamacpp model picker for LLM */}
                  {editConfig.llm_provider === 'llamacpp' && (
                    <div className="mt-3 bg-violet-500/10 border border-violet-500/30 rounded-xl p-4 space-y-3">
                      <div className="flex items-center gap-2">
                        <span className="text-violet-400">🧠</span>
                        <span className="text-xs font-semibold text-violet-300">Local GGUF model required — models are stored in <code className="bg-black/30 px-1 rounded">~/.gleann/models/</code></span>
                      </div>
                      <p className="text-[11px] text-violet-400/70">Click a model to select it, then click ↓ to download from HuggingFace. You can also type a custom filename.</p>
                      <div className="flex flex-wrap gap-2">
                        {[
                          { label: 'Qwen2.5 Coder 1.5B', file: 'qwen2.5-coder-1.5b-instruct-q4_k_m.gguf', hf: 'https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF/resolve/main/qwen2.5-coder-1.5b-instruct-q4_k_m.gguf' },
                          { label: 'Qwen2.5 0.5B Instruct', file: 'qwen2.5-0.5b-instruct-q4_k_m.gguf', hf: 'https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf' },
                          { label: 'Llama 3.2 1B Instruct', file: 'Llama-3.2-1B-Instruct-Q4_K_M.gguf', hf: 'https://huggingface.co/bartowski/Llama-3.2-1B-Instruct-GGUF/resolve/main/Llama-3.2-1B-Instruct-Q4_K_M.gguf' },
                        ].map(m => (
                          <div key={m.file} className={`flex items-center gap-1 rounded-lg border text-[11px] overflow-hidden transition-all ${
                            editConfig.llm_model === m.file
                              ? 'border-violet-400/60 bg-violet-500/20'
                              : 'border-white/10 bg-black/30 hover:border-violet-500/40'
                          }`}>
                            <button
                              type="button"
                              onClick={() => setEditConfig({...editConfig, llm_model: m.file})}
                              className="px-2.5 py-1.5 text-violet-200 hover:text-white font-mono"
                              title="Click to select this model"
                            >
                              {m.label}
                            </button>
                            <button
                              type="button"
                              onClick={() => handleDownloadModel(m.hf, m.file)}
                              className="px-2 py-1.5 bg-violet-500/20 hover:bg-violet-500/40 text-violet-400 hover:text-white border-l border-violet-500/20 transition-colors"
                              title="Download from HuggingFace to server"
                            >↓</button>
                          </div>
                        ))}
                      </div>
                      <div>
                        <label className="text-[11px] text-violet-400/70 mb-1 block">Select Local Model or Enter Custom Filename:</label>
                        <div className="flex gap-2 mb-2">
                          <select
                            value={localModels.includes(editConfig.llm_model) ? editConfig.llm_model : (editConfig.llm_model ? 'custom' : '')}
                            onChange={e => {
                              if (e.target.value !== 'custom') setEditConfig({...editConfig, llm_model: e.target.value})
                            }}
                            className="flex-1 bg-black/50 border border-violet-500/30 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-violet-400/60 transition-colors font-mono"
                          >
                            <option value="">-- Select a local model --</option>
                            {localModels.map(m => (
                              <option key={m} value={m}>{m}</option>
                            ))}
                            <option value="custom">-- Custom Filename / Download --</option>
                          </select>
                        </div>
                        {(!localModels.includes(editConfig.llm_model) || editConfig.llm_model === '') && (
                          <div className="flex gap-2">
                            <input
                              type="text"
                              value={editConfig.llm_model || ''}
                              onChange={e => setEditConfig({...editConfig, llm_model: e.target.value})}
                              placeholder="e.g. my-llm-q4_k_m.gguf"
                              className="flex-1 bg-black/50 border border-violet-500/30 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-violet-400/60 transition-colors font-mono"
                            />
                            <button
                              type="button"
                              onClick={() => {
                                if (!editConfig.llm_model) return alert("Please enter a filename first.");
                                const url = prompt(`Enter HuggingFace or direct download URL for ${editConfig.llm_model}:`);
                                if (url) handleDownloadModel(url, editConfig.llm_model);
                              }}
                              className="px-3 py-1.5 bg-violet-600 hover:bg-violet-500 text-white rounded text-xs font-medium transition-colors"
                            >
                              Download
                            </button>
                          </div>
                        )}
                      </div>
                    </div>
                  )}



                  {(editConfig.llm_provider || 'ollama') !== 'llamacpp' && (
                    <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mt-3">
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">Endpoint URL</label>
                      <input 
                        type="text" 
                        value={(editConfig.llm_provider || 'ollama') === 'ollama' ? (editConfig.ollama_host || 'http://localhost:11434') : (editConfig.llm_provider || 'ollama') === 'openai' ? (editConfig.openai_base_url || 'https://api.openai.com/v1') : ''} 
                        onChange={e => {
                          const prov = editConfig.llm_provider || 'ollama';
                          if (prov === 'ollama') setEditConfig({...editConfig, ollama_host: e.target.value})
                          else if (prov === 'openai') setEditConfig({...editConfig, openai_base_url: e.target.value})
                        }}
                        disabled={(editConfig.llm_provider || 'ollama') === 'anthropic' || (editConfig.llm_provider || 'ollama') === 'llamacpp'}
                        placeholder={(editConfig.llm_provider || 'ollama') === 'ollama' ? 'http://localhost:11434' : (editConfig.llm_provider || 'ollama') === 'llamacpp' ? 'Managed automatically' : 'https://api.openai.com/v1'}
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors disabled:opacity-50"
                      />
                    </div>
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">API Key</label>
                      <input 
                        type="password" 
                        value={(editConfig.llm_provider || 'ollama') === 'openai' ? (editConfig.openai_api_key || '') : ''} 
                        onChange={e => {
                          const prov = editConfig.llm_provider || 'ollama';
                          if (prov === 'openai') setEditConfig({...editConfig, openai_api_key: e.target.value})
                        }}
                        disabled={(editConfig.llm_provider || 'ollama') !== 'openai' && (editConfig.llm_provider || 'ollama') !== 'anthropic'}
                        placeholder={(editConfig.llm_provider || 'ollama') === 'ollama' ? 'Not required' : 'sk-...'}
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors disabled:opacity-50"
                      />
                    </div>
                    <div>
                      <label className="text-xs text-gray-500 font-medium block mb-1">LLM Model</label>
                      <input 
                        type="text"
                        list="llm_models_list"
                        value={editConfig.llm_model || ''} 
                        onChange={e => setEditConfig({...editConfig, llm_model: e.target.value})}
                        placeholder="e.g. llama3, gpt-4o"
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors"
                      />
                      {llmModels.length > 0 && (
                        <datalist id="llm_models_list">
                           {llmModels.map(m => <option key={m} value={m} />)}
                        </datalist>
                      )}
                    </div>
                    <div className="md:col-span-2">
                      <label className="text-xs text-gray-500 font-medium block mb-1">Multimodal Model (Optional)</label>
                      <input 
                        type="text" 
                        list="multimodal_models_list"
                        value={editConfig.multimodal_model || ''} 
                        onChange={e => setEditConfig({...editConfig, multimodal_model: e.target.value})}
                        placeholder="e.g. llava, minicpm-v"
                        className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors"
                      />
                      {llmModels.length > 0 && (
                        <datalist id="multimodal_models_list">
                           {llmModels.map(m => <option key={m} value={m} />)}
                        </datalist>
                      )}
                    </div>
                  </div>
                  )}
                </div>

                <div className="space-y-3 mt-6 mb-6 bg-black/20 p-4 rounded-xl border border-white/5">
                  <div className="flex flex-col md:flex-row gap-4 items-center">
                    <label className="flex items-center gap-2 cursor-pointer whitespace-nowrap">
                      <input 
                        type="checkbox" 
                        checked={editConfig.search_config?.use_reranker || false}
                        onChange={e => {
                          const sc = editConfig.search_config || {};
                          setEditConfig({...editConfig, search_config: {...sc, use_reranker: e.target.checked}});
                        }}
                        className="w-4 h-4 rounded border-white/10 bg-black/50 text-blue-500 focus:ring-blue-500/50 focus:ring-offset-0"
                      />
                      <h3 className="text-sm font-bold text-gray-300 uppercase tracking-wider">Enable Reranker</h3>
                    </label>
                    {editConfig.search_config?.use_reranker && (
                      <div className="flex-1 flex gap-2">
                        <select
                          value={editConfig.search_config?.reranker_config?.provider || 'ollama'}
                          onChange={e => {
                            const sc = editConfig.search_config || {};
                            const rc = sc.reranker_config || {};
                            setEditConfig({...editConfig, search_config: {...sc, reranker_config: {...rc, provider: e.target.value}}});
                          }}
                          className="bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50"
                        >
                          <option value="ollama">Ollama</option>
                          <option value="huggingface">HuggingFace TEI</option>
                          <option value="openai">OpenAI Compatible</option>
                          <option value="llamacpp">Llama.cpp</option>
                        </select>
                      </div>
                    )}
                  </div>
                  
                  {editConfig.search_config?.use_reranker && (
                    <>
                      {/* llamacpp model picker for Reranker */}
                      {(editConfig.search_config?.reranker_config?.provider === 'llamacpp') && (
                        <div className="mt-3 bg-pink-500/10 border border-pink-500/30 rounded-xl p-4 space-y-3">
                          <div className="flex items-center gap-2">
                            <span className="text-pink-400">⚡</span>
                            <span className="text-xs font-semibold text-pink-300">Local GGUF model required — models are stored in <code className="bg-black/30 px-1 rounded">~/.gleann/models/</code></span>
                          </div>
                          <p className="text-[11px] text-pink-400/70">Click a model to select it, then click ↓ to download from HuggingFace. You can also type a custom filename.</p>
                          <div className="flex flex-wrap gap-2">
                            {[
                              { label: 'BGE Reranker v2 M3', file: 'bge-reranker-v2-m3-Q4_K_M.gguf', hf: 'https://huggingface.co/lmstudio-ai/bge-reranker-v2-m3-GGUF/resolve/main/bge-reranker-v2-m3-Q4_K_M.gguf' }
                            ].map(m => (
                              <div key={m.file} className={`flex items-center gap-1 rounded-lg border text-[11px] overflow-hidden transition-all ${
                                editConfig.search_config?.reranker_config?.model === m.file
                                  ? 'border-pink-400/60 bg-pink-500/20'
                                  : 'border-white/10 bg-black/30 hover:border-pink-500/40'
                              }`}>
                                <button
                                  type="button"
                                  onClick={() => {
                                    const sc = editConfig.search_config || {};
                                    const rc = sc.reranker_config || {};
                                    setEditConfig({...editConfig, search_config: {...sc, reranker_config: {...rc, model: m.file}}});
                                  }}
                                  className="px-2.5 py-1.5 text-pink-200 hover:text-white font-mono"
                                  title="Click to select this model"
                                >
                                  {m.label}
                                </button>
                                <button
                                  type="button"
                                  onClick={() => handleDownloadModel(m.hf, m.file)}
                                  className="px-2 py-1.5 bg-pink-500/20 hover:bg-pink-500/40 text-pink-400 hover:text-white border-l border-pink-500/20 transition-colors"
                                  title="Download from HuggingFace to server"
                                >↓</button>
                              </div>
                            ))}
                          </div>
                          <div>
                            <label className="text-[11px] text-pink-400/70 mb-1 block">Select Local Model or Enter Custom Filename:</label>
                            <div className="flex gap-2">
                              <select
                                value={localModels.includes(editConfig.search_config?.reranker_config?.model || '') ? (editConfig.search_config?.reranker_config?.model || '') : (editConfig.search_config?.reranker_config?.model ? 'custom' : '')}
                                onChange={e => {
                                  if (e.target.value !== 'custom') {
                                    const sc = editConfig.search_config || {};
                                    const rc = sc.reranker_config || {};
                                    setEditConfig({...editConfig, search_config: {...sc, reranker_config: {...rc, model: e.target.value}}});
                                  }
                                }}
                                className="w-1/2 bg-black/50 border border-pink-500/30 rounded px-3 py-1.5 text-sm text-pink-100 outline-none focus:border-pink-500/70"
                              >
                                <option value="">-- Downloaded Models --</option>
                                {localModels.map(m => <option key={m} value={m}>{m}</option>)}
                                <option value="custom">-- Custom Filename --</option>
                              </select>
                              {(!localModels.includes(editConfig.search_config?.reranker_config?.model || '') || !editConfig.search_config?.reranker_config?.model) && (
                                <input
                                  type="text"
                                  value={editConfig.search_config?.reranker_config?.model || ''}
                                  onChange={e => {
                                    const sc = editConfig.search_config || {};
                                    const rc = sc.reranker_config || {};
                                    setEditConfig({...editConfig, search_config: {...sc, reranker_config: {...rc, model: e.target.value}}});
                                  }}
                                  placeholder="Custom model filename..."
                                  className="flex-1 bg-black/50 border border-pink-500/30 rounded px-3 py-1.5 text-sm text-pink-100 outline-none focus:border-pink-500/70"
                                />
                              )}
                            </div>
                          </div>
                        </div>
                      )}

                      {(editConfig.search_config?.reranker_config?.provider || 'ollama') !== 'llamacpp' && (
                        <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mt-3">
                          <div>
                            <label className="text-xs text-gray-500 font-medium block mb-1">Endpoint URL</label>
                            <input 
                              type="text" 
                              value={(editConfig.search_config?.reranker_config?.provider || 'ollama') === 'ollama' ? (editConfig.search_config?.reranker_config?.base_url || 'http://localhost:11434') : (editConfig.search_config?.reranker_config?.base_url || '')} 
                              onChange={e => {
                                const sc = editConfig.search_config || {};
                                const rc = sc.reranker_config || {};
                                setEditConfig({...editConfig, search_config: {...sc, reranker_config: {...rc, base_url: e.target.value}}});
                              }}
                              placeholder={(editConfig.search_config?.reranker_config?.provider || 'ollama') === 'ollama' ? 'http://localhost:11434' : 'https://api...'}
                              className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors disabled:opacity-50"
                            />
                          </div>
                          <div>
                            <label className="text-xs text-gray-500 font-medium block mb-1">API Key</label>
                            <input 
                              type="password" 
                              value={editConfig.search_config?.reranker_config?.api_key || ''} 
                              onChange={e => {
                                const sc = editConfig.search_config || {};
                                const rc = sc.reranker_config || {};
                                setEditConfig({...editConfig, search_config: {...sc, reranker_config: {...rc, api_key: e.target.value}}});
                              }}
                              disabled={(editConfig.search_config?.reranker_config?.provider || 'ollama') === 'ollama'}
                              placeholder={(editConfig.search_config?.reranker_config?.provider || 'ollama') === 'ollama' ? 'Not required' : 'sk-...'}
                              className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50 transition-colors disabled:opacity-50"
                            />
                          </div>
                          <div className="md:col-span-2">
                            <label className="text-xs text-gray-500 font-medium block mb-1">Model Name</label>
                            <input
                              type="text"
                              value={editConfig.search_config?.reranker_config?.model || ''}
                              onChange={e => {
                                const sc = editConfig.search_config || {};
                                const rc = sc.reranker_config || {};
                                setEditConfig({...editConfig, search_config: {...sc, reranker_config: {...rc, model: e.target.value}}});
                              }}
                              placeholder="Model (e.g. bge-reranker-v2-m3)"
                              className="w-full bg-black/50 border border-white/10 rounded px-3 py-1.5 text-sm text-gray-300 outline-none focus:border-blue-500/50"
                            />
                          </div>
                        </div>
                      )}
                    </>
                  )}
                </div>

                <div className="flex flex-col gap-3 border-t border-white/5 pt-4">
                  <label className="text-sm text-gray-300 font-medium cursor-pointer flex items-center gap-3 hover:text-white transition-colors">
                    <input 
                      type="checkbox" 
                      checked={editConfig.a2a_enabled !== false}
                      onChange={e => setEditConfig({...editConfig, a2a_enabled: e.target.checked})}
                      className="w-4 h-4 rounded bg-black/50 border-white/10 text-blue-500 focus:ring-blue-500/50 focus:ring-offset-0"
                    />
                    Enable Agent-to-Agent (A2A) Protocol
                  </label>
                  <label className="text-sm text-gray-300 font-medium cursor-pointer flex items-center gap-3 hover:text-white transition-colors">
                    <input 
                      type="checkbox" 
                      checked={editConfig.auto_index || false}
                      onChange={e => setEditConfig({...editConfig, auto_index: e.target.checked})}
                      className="w-4 h-4 rounded bg-black/50 border-white/10 text-blue-500 focus:ring-blue-500/50 focus:ring-offset-0"
                    />
                    Enable Auto-Indexing on Startup
                  </label>
                </div>
              </div>
            ) : (
              <div className="text-sm text-gray-500">Loading config...</div>
            )}
          </div>

          <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg">
            <h2 className="text-lg font-semibold text-white flex items-center gap-2 mb-4">
              <Sparkles className="w-5 h-5 text-blue-400" /> Background Tasks
            </h2>
            
            {tasks.length === 0 ? (
              <div className="text-center py-6 text-gray-500 text-sm">No active background tasks.</div>
            ) : (
              <div className="space-y-3 max-h-[300px] overflow-y-auto pr-2" style={{ scrollbarWidth: 'thin' }}>
                {tasks.map(task => (
                  <div key={task.id} className="bg-black/30 border border-white/5 rounded-lg p-3 text-sm flex justify-between items-start">
                    <div>
                      <div className="font-mono text-blue-300 font-semibold">{task.type} - {task.id}</div>
                      <div className="text-gray-300 text-xs mt-1">{task.message}</div>
                      {task.error && <div className="text-red-400 mt-2 text-xs">{task.error}</div>}
                    </div>
                    <div className={`px-2 py-1 rounded text-[10px] font-bold uppercase ${
                      task.status === 'running' ? 'bg-yellow-500/20 text-yellow-300' :
                      task.status === 'completed' ? 'bg-green-500/20 text-green-300' :
                      'bg-red-500/20 text-red-300'
                    }`}>
                      {task.status}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg">
            <div className="flex justify-between items-center mb-4">
              <h2 className="text-lg font-semibold text-white flex items-center gap-2">
                <Sparkles className="w-5 h-5 text-cyan-400" /> Plugin Catalog
              </h2>
              <span className="text-xs bg-cyan-500/20 text-cyan-300 px-2 py-1 rounded-md font-medium">{plugins.length} Plugins</span>
            </div>
            
            <div className="grid gap-3 max-h-[400px] overflow-y-auto pr-2" style={{ scrollbarWidth: 'thin' }}>
              {plugins.length === 0 ? (
                <div className="text-center py-6 text-gray-500 text-sm italic">No plugins available in the catalog.</div>
              ) : (
                plugins.map((p, i) => (
                  <div key={i} className="bg-black/30 border border-white/5 rounded-xl p-4 flex flex-col hover:border-white/10 transition-colors">
                    <div className="flex justify-between items-start mb-2">
                      <div className="flex items-center gap-3">
                        <span className="text-2xl">{p.icon || '🧩'}</span>
                        <div>
                          <h3 className="text-sm font-bold text-gray-200">{p.name}</h3>
                          <div className="text-[10px] text-gray-500 font-mono mt-0.5">{p.language} • {p.version || 'v1.0.0'}</div>
                        </div>
                      </div>
                      <span className={`text-[10px] uppercase font-bold px-2 py-1 rounded-md border ${
                        p.status === 'installed' ? 'bg-green-500/10 text-green-400 border-green-500/20' : 'bg-gray-500/10 text-gray-400 border-gray-500/20'
                      }`}>
                        {p.status === 'installed' ? 'Installed' : 'Available'}
                      </span>
                    </div>
                    <p className="text-xs text-gray-400 mb-3 line-clamp-2 leading-relaxed">{p.description}</p>
                    <div className="flex justify-between items-center mt-auto border-t border-white/5 pt-3">
                      <a href={p.repo_url} target="_blank" rel="noreferrer" className="text-[10px] text-blue-400 hover:text-blue-300 hover:underline flex items-center gap-1">
                        View Repository
                      </a>
                      {p.status !== 'installed' && (
                        <button 
                          onClick={() => handleInstallPlugin(p.name)}
                          className="bg-blue-600/20 hover:bg-blue-600/40 text-blue-300 border border-blue-500/30 px-3 py-1 rounded text-xs font-medium transition-colors"
                        >
                          Install
                        </button>
                      )}
                      {p.status === 'installed' && (
                        <div className="flex gap-2">
                          {p.has_settings && (
                            <button 
                              onClick={() => handleConfigurePlugin(p)}
                              className="bg-white/5 hover:bg-white/10 text-gray-300 border border-white/10 px-3 py-1 rounded text-xs font-medium transition-colors"
                            >
                              Configure
                            </button>
                          )}
                          <button 
                            onClick={() => handleUninstallPlugin(p.name)}
                            className="bg-red-500/10 hover:bg-red-500/20 text-red-400 border border-red-500/20 px-3 py-1 rounded text-xs font-medium transition-colors"
                          >
                            Uninstall
                          </button>
                        </div>
                      )}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
          
          <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg text-sm">
            <h2 className="text-lg font-semibold text-white flex items-center gap-2 mb-4">
              <Activity className="w-5 h-5 text-gray-400" /> About
            </h2>
            <div className="text-gray-400 space-y-2">
              <p><strong>Gleann</strong> {serverVersion}</p>
              <p>Repository: <a href="https://github.com/tevfik/gleann" className="text-blue-400 hover:underline" target="_blank" rel="noreferrer">tevfik/gleann</a></p>
              <p>Memory Engine: <span className="text-blue-400">BBolt & KuzuDB</span></p>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}


