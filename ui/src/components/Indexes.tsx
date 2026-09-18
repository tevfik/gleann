import { useState, useEffect } from 'react';
import { Database, Trash2, AlertTriangle, Shield, ShieldOff, Tag, Edit3 } from 'lucide-react';
import type { IndexInfo } from '../types';


export function Indexes() {
  const [indexes, setIndexes] = useState<IndexInfo[]>([]);
  const [newIndexName, setNewIndexName] = useState('');
  const [indexPath, setIndexPath] = useState('');
  const [autoWatch, setAutoWatch] = useState(false);
  const [targetIndex, setTargetIndex] = useState('');
  const [currentModel, setCurrentModel] = useState<string>('');
  const [tagFilter, setTagFilter] = useState<string>('all');
  
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

  const handleToggleWatch = async (idxName: string, enable: boolean, dir?: string) => {
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

  const handleToggleMCP = async (name: string, currentExposed: boolean = true) => {
    try {
      const response = await fetch(`/api/indexes/${name}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mcp_exposed: !currentExposed })
      });
      if (response.ok) {
        loadIndexes();
      } else {
        alert('Failed to toggle MCP exposure status.');
      }
    } catch (err) {
      console.error(err);
      alert('Error updating MCP exposure.');
    }
  };

  const handleEditTags = async (idx: IndexInfo) => {
    const current = (idx.tags || []).join(', ');
    const input = prompt(`Edit tags for index "${idx.name}" (comma-separated):`, current);
    if (input === null) return;
    const parsedTags = input
      .split(',')
      .map(t => t.trim().replace(/^@/, ''))
      .filter(t => t.length > 0);

    try {
      const response = await fetch(`/api/indexes/${idx.name}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tags: parsedTags })
      });
      if (response.ok) {
        loadIndexes();
      } else {
        alert('Failed to update tags.');
      }
    } catch (err) {
      console.error(err);
      alert('Error updating tags.');
    }
  };

  const handleEditDesc = async (idx: IndexInfo) => {
    const input = prompt(`Edit description for index "${idx.name}":`, idx.description || '');
    if (input === null) return;

    try {
      const response = await fetch(`/api/indexes/${idx.name}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ description: input.trim() })
      });
      if (response.ok) {
        loadIndexes();
      } else {
        alert('Failed to update description.');
      }
    } catch (err) {
      console.error(err);
      alert('Error updating description.');
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

  // Collect unique tags
  const allTags = Array.from(
    new Set(indexes.flatMap(idx => idx.tags || []))
  ).sort();

  const filteredIndexes = indexes.filter(idx => {
    if (tagFilter === 'all') return true;
    if (tagFilter === '__mcp__') return idx.mcp_exposed !== false;
    if (tagFilter === '__private__') return idx.mcp_exposed === false;
    return (idx.tags || []).map(t => t.toLowerCase()).includes(tagFilter.toLowerCase());
  });

  return (
    <div className="flex-1 max-w-5xl mx-auto w-full px-4 pt-6 pb-24 z-10 relative">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-white flex items-center gap-2">
          <Database className="w-6 h-6 text-blue-400" /> Index Governance & Scoping
        </h1>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mb-6">
        <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg">
          <h2 className="text-lg font-semibold text-gray-200 mb-4">Create New Index</h2>
          <form onSubmit={handleCreateIndex} className="flex gap-2">
            <input 
              type="text" 
              value={newIndexName} 
              onChange={e => setNewIndexName(e.target.value)}
              placeholder="Index Name (e.g. workspace)" 
              className="flex-1 px-4 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500 text-sm"
            />
            <button type="submit" className="bg-blue-600 hover:bg-blue-500 text-white px-6 py-2 rounded-lg font-medium transition-colors text-sm">
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
              className="px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500 text-sm"
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
                className="flex-1 px-4 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500 text-sm"
              />
              <button type="submit" className="bg-blue-600 hover:bg-blue-500 text-white px-6 py-2 rounded-lg font-medium transition-colors text-sm">
                Start
              </button>
            </div>
            <label className="flex items-center gap-2 mt-1 cursor-pointer">
              <input type="checkbox" checked={autoWatch} onChange={e => setAutoWatch(e.target.checked)} className="rounded bg-black/40 border-white/10 text-blue-500 focus:ring-blue-500/50" />
              <span className="text-xs text-gray-400">Auto-Sync (watch directory for changes)</span>
            </label>
          </form>
        </div>
      </div>

      {/* Filter Tabs */}
      <div className="flex flex-wrap items-center gap-2 mb-4">
        <span className="text-xs text-gray-400 font-medium mr-1">Filter:</span>
        <button
          onClick={() => setTagFilter('all')}
          className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
            tagFilter === 'all'
              ? 'bg-blue-600 text-white'
              : 'bg-white/5 text-gray-400 hover:bg-white/10 hover:text-white'
          }`}
        >
          All ({indexes.length})
        </button>
        <button
          onClick={() => setTagFilter('__mcp__')}
          className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
            tagFilter === '__mcp__'
              ? 'bg-emerald-600 text-white'
              : 'bg-white/5 text-gray-400 hover:bg-white/10 hover:text-white'
          }`}
        >
          🟢 MCP Active
        </button>
        <button
          onClick={() => setTagFilter('__private__')}
          className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
            tagFilter === '__private__'
              ? 'bg-amber-600 text-white'
              : 'bg-white/5 text-gray-400 hover:bg-white/10 hover:text-white'
          }`}
        >
          🔒 Private
        </button>
        {allTags.map(tag => (
          <button
            key={tag}
            onClick={() => setTagFilter(tag)}
            className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
              tagFilter === tag
                ? 'bg-purple-600 text-white'
                : 'bg-white/5 text-purple-300 hover:bg-white/10'
            }`}
          >
            @{tag}
          </button>
        ))}
      </div>

      <div className="bg-[#1a1b23] border border-white/10 rounded-xl shadow-lg overflow-hidden">
        <table className="w-full text-left text-sm text-gray-300">
          <thead className="bg-black/40 text-xs uppercase text-gray-500 border-b border-white/10">
            <tr>
              <th className="px-5 py-3">Index & Info</th>
              <th className="px-5 py-3">Tags & Scope</th>
              <th className="px-5 py-3">MCP Access</th>
              <th className="px-5 py-3">Passages</th>
              <th className="px-5 py-3">Auto-Sync</th>
              <th className="px-5 py-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {filteredIndexes.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-6 py-8 text-center text-gray-500">
                  No indexes found matching current filter.
                </td>
              </tr>
            ) : (
              filteredIndexes.map((idx: IndexInfo) => {
                const mismatched = idx.embedding_model && currentModel && idx.embedding_model !== currentModel;
                const isExposed = idx.mcp_exposed !== false;
                return (
                <tr key={idx.name} className="border-b border-white/5 hover:bg-white/5 transition-colors">
                  <td className="px-5 py-4">
                    <div className="font-medium text-white flex items-center">
                      {idx.name}
                      {mismatched && (
                        <span className="ml-2 inline-flex items-center text-xs font-medium text-amber-500" title={`Mismatch: Index uses ${idx.embedding_model}, active is ${currentModel}`}>
                          <AlertTriangle className="w-3 h-3 mr-1" />
                          Mismatch
                        </span>
                      )}
                    </div>
                    {idx.description ? (
                      <p className="text-xs text-gray-400 mt-1 flex items-center gap-1 group">
                        <span>{idx.description}</span>
                        <button 
                          onClick={() => handleEditDesc(idx)} 
                          className="opacity-0 group-hover:opacity-100 text-gray-500 hover:text-white transition-opacity"
                          title="Edit description"
                        >
                          <Edit3 className="w-3 h-3" />
                        </button>
                      </p>
                    ) : (
                      <button 
                        onClick={() => handleEditDesc(idx)}
                        className="text-[11px] text-gray-600 hover:text-gray-400 mt-0.5 inline-flex items-center gap-1"
                      >
                        + Add description
                      </button>
                    )}
                    <span className="text-[11px] text-gray-500 font-mono block mt-0.5">
                      {idx.embedding_model || 'Unknown model'}
                    </span>
                  </td>

                  {/* Tags */}
                  <td className="px-5 py-4">
                    <div className="flex flex-wrap items-center gap-1.5 max-w-[200px]">
                      {(idx.tags || []).map(tag => (
                        <span 
                          key={tag} 
                          className="px-2 py-0.5 rounded text-[11px] font-medium bg-purple-500/20 text-purple-300 border border-purple-500/30"
                        >
                          @{tag}
                        </span>
                      ))}
                      <button
                        onClick={() => handleEditTags(idx)}
                        className="p-1 rounded text-gray-500 hover:text-purple-300 hover:bg-white/5 transition-colors"
                        title="Edit tags"
                      >
                        <Tag className="w-3 h-3" />
                      </button>
                    </div>
                  </td>

                  {/* MCP Toggle */}
                  <td className="px-5 py-4">
                    <button
                      onClick={() => handleToggleMCP(idx.name, isExposed)}
                      className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border transition-colors ${
                        isExposed
                          ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30 hover:bg-emerald-500/20'
                          : 'bg-zinc-800 text-zinc-400 border-zinc-700 hover:bg-zinc-700'
                      }`}
                      title={isExposed ? "Exposed to AI Agents via MCP" : "Private (Hidden from AI Agents)"}
                    >
                      {isExposed ? (
                        <>
                          <Shield className="w-3 h-3" />
                          <span>Exposed</span>
                        </>
                      ) : (
                        <>
                          <ShieldOff className="w-3 h-3" />
                          <span>Private</span>
                        </>
                      )}
                    </button>
                  </td>

                  <td className="px-5 py-4 font-mono text-xs text-gray-400">
                    {idx.num_passages ?? idx.count ?? 0}
                  </td>

                  <td className="px-5 py-4">
                    <label className="relative inline-flex items-center cursor-pointer">
                      <input 
                        type="checkbox" 
                        className="sr-only peer" 
                        checked={idx.auto_watch}
                        onChange={(e) => handleToggleWatch(idx.name, e.target.checked, idx.source_dir)}
                      />
                      <div className="w-9 h-5 bg-black/50 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-gray-300 after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600"></div>
                    </label>
                    {idx.source_dir && <span className="text-[11px] text-gray-500 truncate block max-w-[120px]" title={idx.source_dir}>{idx.source_dir}</span>}
                  </td>

                  <td className="px-5 py-4 text-right">
                    <button onClick={() => handleDelete(idx.name)} className="text-red-400 hover:text-red-300 p-1 rounded hover:bg-red-500/10 transition-colors">
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



