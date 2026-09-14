import { useState, useEffect } from 'react';
import { Database, Trash2, AlertTriangle } from 'lucide-react';

export function Indexes() {
  const [indexes, setIndexes] = useState<any[]>([]);
  const [newIndexName, setNewIndexName] = useState('');
  const [indexPath, setIndexPath] = useState('');
  const [autoWatch, setAutoWatch] = useState(false);
  const [targetIndex, setTargetIndex] = useState('');
  const [currentModel, setCurrentModel] = useState<string>('');
  
  const loadIndexes = () => {
    fetch('/api/indexes')
      .then(res => res.json())
      .then(data => {
        setIndexes(data.indexes || []);
        setCurrentModel(data.current_embedding_model || '');
        if (data.indexes && data.indexes.length > 0 && !targetIndex) {
          setTargetIndex(data.indexes[0].name);
        }
      })
      .catch(console.error);
  };

  const handleToggleWatch = async (idxName: string, enable: boolean, dir: string) => {
    try {
      const response = await fetch(`/api/indexes/${idxName}/watch`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enable, dir: dir || '' })
      });
      if (response.ok) {
        loadIndexes();
      } else {
        alert('Failed to toggle auto-sync.');
      }
    } catch (err) {
      console.error(err);
      alert('Error toggling auto-sync.');
    }
  };

  useEffect(() => {
    loadIndexes();
  }, []);

  const handleDelete = async (name: string) => {
    if (!confirm(`Are you sure you want to delete index ${name}?`)) return;
    try {
      await fetch(`/api/indexes/${name}`, { method: 'DELETE' });
      loadIndexes();
    } catch (err) {
      console.error(err);
    }
  };

  const handleIndexDir = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!targetIndex || !indexPath.trim()) return;
    try {
      const response = await fetch(`/api/indexes/${targetIndex}/index-path`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: indexPath.trim() })
      });
      if (response.ok) {
        if (autoWatch) {
          await fetch(`/api/indexes/${targetIndex}/watch`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enable: true, dir: indexPath.trim() })
          });
        }
        alert(`Background indexing started for directory!${autoWatch ? ' Auto-sync enabled.' : ''}`);
        setIndexPath('');
      } else {
        alert('Failed to start indexing.');
      }
    } catch (err) {
      console.error(err);
      alert('Error indexing directory.');
    }
  };

  const handleCreateIndex = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newIndexName.trim()) return;
    // Build an empty index by passing empty text
    try {
      const response = await fetch(`/api/indexes/${newIndexName}/build`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ texts: ["initialized"], metadata: { source: "init" } })
      });
      if (response.ok) {
        setNewIndexName('');
        loadIndexes();
      } else {
        alert('Failed to create index.');
      }
    } catch (err) {
      console.error(err);
      alert('Error creating index.');
    }
  };

  return (
    <div className="flex-1 max-w-4xl mx-auto w-full px-4 pt-6 pb-24 z-10 relative">
      <h1 className="text-2xl font-bold text-white mb-6 flex items-center gap-2">
        <Database className="w-6 h-6 text-blue-400" /> Index Management
      </h1>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mb-6">
        <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg">
          <h2 className="text-lg font-semibold text-gray-200 mb-4">Create New Index</h2>
          <form onSubmit={handleCreateIndex} className="flex gap-2">
            <input 
              type="text" 
              value={newIndexName} 
              onChange={e => setNewIndexName(e.target.value)}
              placeholder="Index Name (e.g. workspace)" 
              className="flex-1 px-4 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500"
            />
            <button type="submit" className="bg-blue-600 hover:bg-blue-500 text-white px-6 py-2 rounded-lg font-medium transition-colors">
              Create
            </button>
          </form>
        </div>

        <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg">
          <h2 className="text-lg font-semibold text-gray-200 mb-4">Index a Directory</h2>
          <form onSubmit={handleIndexDir} className="flex flex-col gap-2">
            <select
              value={targetIndex}
              onChange={(e) => setTargetIndex(e.target.value)}
              className="px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500"
            >
              <option value="" disabled>Select Target Index</option>
              {indexes.map((idx: any) => {
                const mismatched = idx.embedding_model && currentModel && idx.embedding_model !== currentModel;
                return (
                  <option key={idx.name} value={idx.name} disabled={mismatched}>
                    {idx.name} {mismatched ? `(Model mismatch: ${idx.embedding_model})` : ''}
                  </option>
                );
              })}
            </select>
            <div className="flex gap-2">
              <input 
                type="text" 
                value={indexPath} 
                onChange={e => setIndexPath(e.target.value)}
                placeholder="Absolute path to directory..."
                className="flex-1 px-4 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500"
              />
              <button type="submit" className="bg-blue-600 hover:bg-blue-500 text-white px-6 py-2 rounded-lg font-medium transition-colors">
                Start
              </button>
            </div>
            <label className="flex items-center gap-2 mt-1 cursor-pointer">
              <input type="checkbox" checked={autoWatch} onChange={e => setAutoWatch(e.target.checked)} className="rounded bg-black/40 border-white/10 text-blue-500 focus:ring-blue-500/50" />
              <span className="text-sm text-gray-400">Auto-Sync (watch directory for changes)</span>
            </label>
          </form>
        </div>
      </div>

      <div className="bg-[#1a1b23] border border-white/10 rounded-xl shadow-lg overflow-hidden">
        <table className="w-full text-left text-sm text-gray-300">
          <thead className="bg-black/40 text-xs uppercase text-gray-500 border-b border-white/10">
            <tr>
              <th className="px-6 py-3">Name</th>
              <th className="px-6 py-3">Documents</th>
              <th className="px-6 py-3">Embedding Model</th>
              <th className="px-6 py-3">Auto-Sync</th>
              <th className="px-6 py-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {indexes.length === 0 ? (
              <tr>
                <td colSpan={3} className="px-6 py-8 text-center text-gray-500">
                  No indexes found. Create one.
                </td>
              </tr>
            ) : (
              indexes.map((idx: any) => {
                const mismatched = idx.embedding_model && currentModel && idx.embedding_model !== currentModel;
                return (
                <tr key={idx.name} className="border-b border-white/5 hover:bg-white/5 transition-colors">
                  <td className="px-6 py-4 font-medium text-white">
                    {idx.name}
                    {mismatched && (
                      <span className="ml-2 inline-flex items-center text-xs font-medium text-amber-500" title={`Mismatch: Index uses ${idx.embedding_model}, active is ${currentModel}`}>
                        <AlertTriangle className="w-3 h-3 mr-1" />
                        Mismatch
                      </span>
                    )}
                  </td>
                  <td className="px-6 py-4">{idx.num_passages || 0}</td>
                  <td className="px-6 py-4 text-xs text-gray-400 font-mono">
                    {idx.embedding_model || 'Unknown'}
                  </td>
                  <td className="px-6 py-4">
                    <label className="relative inline-flex items-center cursor-pointer">
                      <input 
                        type="checkbox" 
                        className="sr-only peer" 
                        checked={idx.auto_watch}
                        onChange={(e) => handleToggleWatch(idx.name, e.target.checked, idx.source_dir)}
                      />
                      <div className="w-9 h-5 bg-black/50 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-gray-300 after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600"></div>
                    </label>
                    {idx.source_dir && <span className="ml-2 text-xs text-gray-500 truncate block max-w-[150px]" title={idx.source_dir}>{idx.source_dir}</span>}
                  </td>
                  <td className="px-6 py-4 text-right">
                    <button onClick={() => handleDelete(idx.name)} className="text-red-400 hover:text-red-300 transition-colors">
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </td>
                </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}


