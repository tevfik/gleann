import { useState, useEffect } from 'react';
import { Settings, Terminal, Activity, Sparkles, Database, AlertCircle } from 'lucide-react';
import { PluginSettings } from './PluginSettings';
import { SettingsForm } from './SettingsForm';

export function System() {
  const [tasks, setTasks] = useState<any[]>([]);
  const [stats, setStats] = useState<any>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [plugins, setPlugins] = useState<any[]>([]);
  const [configuring, setConfiguring] = useState<any>(null);
  const [serverVersion, setServerVersion] = useState<string>('Unknown');
  const [customUrl, setCustomUrl] = useState('');

  useEffect(() => {
    const fetchSystemInfo = () => {
      if (document.visibilityState !== 'visible') {
        return;
      }

      fetch('/api/tasks')
        .then(res => res.json())
        .then(data => setTasks(data.tasks || []))
        .catch(console.error);

      fetch('/api/blocks/stats')
        .then(res => res.json())
        .then(data => setStats(data))
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

    };

    fetchSystemInfo();
    const interval = setInterval(fetchSystemInfo, 10000);
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        fetchSystemInfo();
      }
    };
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      clearInterval(interval);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, []);

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

  // The plugin describes its own settings form; PluginSettings renders it.
  const handleConfigurePlugin = (p: any) => setConfiguring(p);

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
      {configuring && <PluginSettings plugin={configuring} onClose={() => setConfiguring(null)} />}
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
            <h2 className="text-lg font-semibold text-white flex items-center gap-2 mb-1">
              <Database className="w-5 h-5 text-blue-400" /> Settings
            </h2>
            <p className="text-[11px] text-gray-500 mb-4">
              The same settings as the terminal (gleann setup → All Settings), saved to ~/.gleann/config.json.
            </p>
            <SettingsForm url="/api/settings" catalogUrl="/api/models/catalog" idPrefix="gleann" />
            <details className="mt-4 border-t border-white/5 pt-3">
              <summary className="text-xs text-gray-400 cursor-pointer">Download another GGUF from a URL</summary>
              <div className="flex gap-2 mt-2">
                <input value={customUrl} onChange={e => setCustomUrl(e.target.value)} placeholder="https://huggingface.co/…/resolve/main/model.gguf"
                  className="flex-1 bg-black/40 border border-white/10 rounded px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-blue-500/50" />
                <button disabled={!customUrl.trim()} onClick={() => handleDownloadModel(customUrl.trim(), customUrl.trim().split('/').pop()?.split('?')[0] || 'model.gguf')}
                  className="bg-white/5 hover:bg-white/10 disabled:opacity-40 text-gray-300 border border-white/10 px-3 py-1.5 rounded text-xs">Download</button>
              </div>
              <p className="text-[11px] text-gray-500 mt-1">Saved to ~/.gleann/models; it then appears in the model lists above.</p>
            </details>
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


