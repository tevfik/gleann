import { useState, useEffect } from 'react';
import { 
  Database, Trash2, AlertTriangle, Shield, ShieldOff, Tag, Edit3, 
  Plus, Check, X, FolderOpen 
} from 'lucide-react';
import type { IndexInfo } from '../types';

export function Indexes() {
  const [indexes, setIndexes] = useState<IndexInfo[]>([]);
  const [currentModel, setCurrentModel] = useState<string>('');
  const [tagFilter, setTagFilter] = useState<string>('all');

  // Form: Create New Index
  const [newIndexName, setNewIndexName] = useState('');
  const [newIndexDesc, setNewIndexDesc] = useState('');
  const [newIndexTags, setNewIndexTags] = useState('');
  const [newIndexMCP, setNewIndexMCP] = useState(true);

  // Form: Index a Directory
  const [targetIndex, setTargetIndex] = useState('');
  const [isCustomTarget, setIsCustomTarget] = useState(false);
  const [customIndexName, setCustomIndexName] = useState('');
  const [indexPath, setIndexPath] = useState('');
  const [dirTags, setDirTags] = useState('');
  const [dirDesc, setDirDesc] = useState('');
  const [autoWatch, setAutoWatch] = useState(false);

  // Inline Table Editors
  const [editingTagFor, setEditingTagFor] = useState<string | null>(null);
  const [inlineTagInput, setInlineTagInput] = useState('');
  const [editingDescFor, setEditingDescFor] = useState<string | null>(null);
  const [inlineDescInput, setInlineDescInput] = useState('');

  const loadIndexes = () => {
    fetch('/api/indexes')
      .then(res => res.json())
      .then(data => {
        const idxs: IndexInfo[] = data.indexes || [];
        setIndexes(idxs);
        setCurrentModel(data.current_embedding_model || '');
        if (idxs.length > 0 && !targetIndex) {
          setTargetIndex(idxs[0].name);
        }
      })
      .catch(console.error);
  };

  useEffect(() => {
    loadIndexes();
  }, []);

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
    const nextVal = !currentExposed;
    // Optimistic UI update
    setIndexes(prev => prev.map(idx => idx.name === name ? { ...idx, mcp_exposed: nextVal } : idx));
    try {
      const response = await fetch(`/api/indexes/${name}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mcp_exposed: nextVal })
      });
      if (!response.ok) {
        loadIndexes();
        alert('Failed to update MCP exposure status.');
      }
    } catch (err) {
      console.error(err);
      loadIndexes();
    }
  };

  const handleAddTagInline = async (indexName: string) => {
    const cleaned = inlineTagInput.trim().replace(/^@/, '');
    if (!cleaned) {
      setEditingTagFor(null);
      return;
    }
    const target = indexes.find(i => i.name === indexName);
    const existing = target?.tags || [];
    if (existing.map(t => t.toLowerCase()).includes(cleaned.toLowerCase())) {
      setEditingTagFor(null);
      setInlineTagInput('');
      return;
    }
    const updatedTags = [...existing, cleaned];
    // Optimistic update
    setIndexes(prev => prev.map(idx => idx.name === indexName ? { ...idx, tags: updatedTags } : idx));
    setEditingTagFor(null);
    setInlineTagInput('');

    try {
      await fetch(`/api/indexes/${indexName}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tags: updatedTags })
      });
    } catch (err) {
      console.error(err);
      loadIndexes();
    }
  };

  const handleRemoveTag = async (indexName: string, tagToRemove: string) => {
    const target = indexes.find(i => i.name === indexName);
    const updatedTags = (target?.tags || []).filter(t => t.toLowerCase() !== tagToRemove.toLowerCase());
    // Optimistic update
    setIndexes(prev => prev.map(idx => idx.name === indexName ? { ...idx, tags: updatedTags } : idx));

    try {
      await fetch(`/api/indexes/${indexName}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tags: updatedTags })
      });
    } catch (err) {
      console.error(err);
      loadIndexes();
    }
  };

  const handleSaveDescInline = async (indexName: string) => {
    const newDesc = inlineDescInput.trim();
    // Optimistic update
    setIndexes(prev => prev.map(idx => idx.name === indexName ? { ...idx, description: newDesc } : idx));
    setEditingDescFor(null);

    try {
      await fetch(`/api/indexes/${indexName}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ description: newDesc })
      });
    } catch (err) {
      console.error(err);
      loadIndexes();
    }
  };

  const handleDelete = async (name: string) => {
    if (!confirm(`Are you sure you want to delete index "${name}"?`)) return;
    try {
      await fetch(`/api/indexes/${name}`, { method: 'DELETE' });
      loadIndexes();
    } catch (err) {
      console.error(err);
    }
  };

  const handleCreateIndex = async (e: React.FormEvent) => {
    e.preventDefault();
    const name = newIndexName.trim();
    if (!name) return;

    const parsedTags = newIndexTags
      .split(',')
      .map(t => t.trim().replace(/^@/, ''))
      .filter(t => t.length > 0);

    try {
      const response = await fetch(`/api/indexes/${name}/build`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ 
          texts: ["initialized"], 
          metadata: { source: "init" },
          tags: parsedTags,
          description: newIndexDesc.trim(),
          mcp_exposed: newIndexMCP
        })
      });
      if (response.ok) {
        setNewIndexName('');
        setNewIndexDesc('');
        setNewIndexTags('');
        setNewIndexMCP(true);
        loadIndexes();
      } else {
        const err = await response.json().catch(() => ({}));
        alert(`Failed to create index: ${err.error || 'Unknown error'}`);
      }
    } catch (err) {
      console.error(err);
      alert('Error creating index.');
    }
  };

  const handleIndexDir = async (e: React.FormEvent) => {
    e.preventDefault();
    const finalIndexName = isCustomTarget ? customIndexName.trim() : targetIndex;
    if (!finalIndexName || !indexPath.trim()) {
      alert('Please provide an index name and directory path.');
      return;
    }

    const parsedTags = dirTags
      .split(',')
      .map(t => t.trim().replace(/^@/, ''))
      .filter(t => t.length > 0);

    try {
      const response = await fetch(`/api/indexes/${finalIndexName}/index-path`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ 
          path: indexPath.trim(),
          tags: parsedTags.length > 0 ? parsedTags : undefined,
          description: dirDesc.trim() || undefined
        })
      });
      if (response.ok) {
        if (autoWatch) {
          await fetch(`/api/indexes/${finalIndexName}/watch`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enable: true, dir: indexPath.trim() })
          });
        }
        alert(`Indexing started for "${finalIndexName}"!${autoWatch ? ' Auto-sync enabled.' : ''}`);
        setIndexPath('');
        setDirTags('');
        setDirDesc('');
        loadIndexes();
      } else {
        alert('Failed to start indexing.');
      }
    } catch (err) {
      console.error(err);
      alert('Error indexing directory.');
    }
  };

  // Collect unique tags for filter tabs
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
        <div>
          <h1 className="text-2xl font-bold text-white flex items-center gap-2">
            <Database className="w-6 h-6 text-blue-400" /> Index Governance & Scoping
          </h1>
          <p className="text-xs text-gray-400 mt-1">
            Manage tags, descriptions, and MCP visibility for your AI assistants and coding agents.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mb-8">
        {/* Create New Index Card */}
        <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg flex flex-col justify-between">
          <div>
            <h2 className="text-base font-semibold text-gray-200 mb-3 flex items-center gap-2">
              <Plus className="w-4 h-4 text-blue-400" /> Create New Index
            </h2>
            <form onSubmit={handleCreateIndex} className="flex flex-col gap-3">
              <div>
                <label className="text-xs text-gray-400 block mb-1">Index Name *</label>
                <input 
                  type="text" 
                  value={newIndexName} 
                  onChange={e => setNewIndexName(e.target.value)}
                  placeholder="e.g. backend-api" 
                  className="w-full px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 text-sm"
                  required
                />
              </div>

              <div>
                <label className="text-xs text-gray-400 block mb-1">Description</label>
                <input 
                  type="text" 
                  value={newIndexDesc} 
                  onChange={e => setNewIndexDesc(e.target.value)}
                  placeholder="e.g. Core microservice and auth backend" 
                  className="w-full px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 text-sm"
                />
              </div>

              <div>
                <label className="text-xs text-gray-400 block mb-1">Tags (comma-separated)</label>
                <input 
                  type="text" 
                  value={newIndexTags} 
                  onChange={e => setNewIndexTags(e.target.value)}
                  placeholder="e.g. work, backend, golang" 
                  className="w-full px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 text-sm"
                />
                {newIndexTags.trim() && (
                  <div className="flex flex-wrap gap-1 mt-1.5">
                    {newIndexTags.split(',').map(t => t.trim().replace(/^@/, '')).filter(Boolean).map(tag => (
                      <span key={tag} className="px-2 py-0.5 rounded text-[11px] font-medium bg-purple-500/20 text-purple-300 border border-purple-500/30">
                        @{tag}
                      </span>
                    ))}
                  </div>
                )}
              </div>

              <div className="flex items-center justify-between pt-1">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input 
                    type="checkbox" 
                    checked={newIndexMCP} 
                    onChange={e => setNewIndexMCP(e.target.checked)} 
                    className="rounded bg-black/40 border-white/10 text-emerald-500 focus:ring-emerald-500/50" 
                  />
                  <span className="text-xs text-gray-300">Expose to MCP AI Agents</span>
                </label>
                <button 
                  type="submit" 
                  className="bg-blue-600 hover:bg-blue-500 text-white px-5 py-2 rounded-lg font-medium transition-colors text-sm"
                >
                  Create Index
                </button>
              </div>
            </form>
          </div>
        </div>

        {/* Index a Directory Card */}
        <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg flex flex-col justify-between">
          <div>
            <h2 className="text-base font-semibold text-gray-200 mb-3 flex items-center gap-2">
              <FolderOpen className="w-4 h-4 text-emerald-400" /> Index a Directory
            </h2>
            <form onSubmit={handleIndexDir} className="flex flex-col gap-3">
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label className="text-xs text-gray-400">Target Index *</label>
                  <button 
                    type="button" 
                    onClick={() => setIsCustomTarget(!isCustomTarget)}
                    className="text-[11px] text-blue-400 hover:text-blue-300"
                  >
                    {isCustomTarget ? "← Choose existing" : "+ Create new target"}
                  </button>
                </div>
                {isCustomTarget ? (
                  <input 
                    type="text" 
                    value={customIndexName} 
                    onChange={e => setCustomIndexName(e.target.value)}
                    placeholder="New index name (e.g. project-docs)" 
                    className="w-full px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 text-sm"
                    required
                  />
                ) : (
                  <select
                    value={targetIndex}
                    onChange={(e) => setTargetIndex(e.target.value)}
                    className="w-full px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500 text-sm"
                  >
                    <option value="" disabled>Select Target Index</option>
                    {indexes.map((idx) => {
                      const mismatched = idx.embedding_model && currentModel && idx.embedding_model !== currentModel;
                      return (
                        <option key={idx.name} value={idx.name} disabled={!!mismatched}>
                          {idx.name} {mismatched ? `(Mismatch: ${idx.embedding_model})` : ''}
                        </option>
                      );
                    })}
                  </select>
                )}
              </div>

              <div>
                <label className="text-xs text-gray-400 block mb-1">Directory Path *</label>
                <input 
                  type="text" 
                  value={indexPath} 
                  onChange={e => setIndexPath(e.target.value)}
                  placeholder="Absolute path e.g. /home/user/workspace/src" 
                  className="w-full px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 text-sm font-mono"
                  required
                />
              </div>

              <div className="grid grid-cols-2 gap-2">
                <div>
                  <label className="text-xs text-gray-400 block mb-1">Tags (optional)</label>
                  <input 
                    type="text" 
                    value={dirTags} 
                    onChange={e => setDirTags(e.target.value)}
                    placeholder="work, docs" 
                    className="w-full px-3 py-1.5 bg-black/40 border border-white/10 rounded-lg text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 text-xs"
                  />
                </div>
                <div>
                  <label className="text-xs text-gray-400 block mb-1">Description (optional)</label>
                  <input 
                    type="text" 
                    value={dirDesc} 
                    onChange={e => setDirDesc(e.target.value)}
                    placeholder="Brief summary" 
                    className="w-full px-3 py-1.5 bg-black/40 border border-white/10 rounded-lg text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 text-xs"
                  />
                </div>
              </div>

              <div className="flex items-center justify-between pt-1">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input 
                    type="checkbox" 
                    checked={autoWatch} 
                    onChange={e => setAutoWatch(e.target.checked)} 
                    className="rounded bg-black/40 border-white/10 text-blue-500 focus:ring-blue-500/50" 
                  />
                  <span className="text-xs text-gray-300">Auto-Sync on changes</span>
                </label>
                <button 
                  type="submit" 
                  className="bg-emerald-600 hover:bg-emerald-500 text-white px-5 py-2 rounded-lg font-medium transition-colors text-sm"
                >
                  Start Indexing
                </button>
              </div>
            </form>
          </div>
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

      {/* Main Table */}
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
                const isEditingTags = editingTagFor === idx.name;
                const isEditingDesc = editingDescFor === idx.name;

                return (
                <tr key={idx.name} className="border-b border-white/5 hover:bg-white/5 transition-colors">
                  {/* Name and Description */}
                  <td className="px-5 py-4 max-w-[240px]">
                    <div className="font-medium text-white flex items-center">
                      {idx.name}
                      {mismatched && (
                        <span className="ml-2 inline-flex items-center text-xs font-medium text-amber-500" title={`Mismatch: Index uses ${idx.embedding_model}, active is ${currentModel}`}>
                          <AlertTriangle className="w-3 h-3 mr-1" />
                          Mismatch
                        </span>
                      )}
                    </div>

                    {isEditingDesc ? (
                      <div className="flex items-center gap-1 mt-1.5">
                        <input
                          type="text"
                          autoFocus
                          value={inlineDescInput}
                          onChange={e => setInlineDescInput(e.target.value)}
                          onKeyDown={e => {
                            if (e.key === 'Enter') handleSaveDescInline(idx.name);
                            if (e.key === 'Escape') setEditingDescFor(null);
                          }}
                          placeholder="Index description..."
                          className="px-2 py-0.5 text-xs bg-black/60 border border-blue-500 rounded text-white focus:outline-none w-full"
                        />
                        <button 
                          onClick={() => handleSaveDescInline(idx.name)}
                          className="p-1 text-emerald-400 hover:text-emerald-300" 
                          title="Save"
                        >
                          <Check className="w-3.5 h-3.5" />
                        </button>
                        <button 
                          onClick={() => setEditingDescFor(null)}
                          className="p-1 text-gray-500 hover:text-gray-300" 
                          title="Cancel"
                        >
                          <X className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    ) : idx.description ? (
                      <p 
                        onClick={() => {
                          setEditingDescFor(idx.name);
                          setInlineDescInput(idx.description || '');
                        }}
                        className="text-xs text-gray-400 mt-1 flex items-center gap-1 group cursor-pointer hover:text-gray-200"
                        title="Click to edit description"
                      >
                        <span className="truncate">{idx.description}</span>
                        <Edit3 className="w-3 h-3 opacity-0 group-hover:opacity-100 text-gray-500 transition-opacity shrink-0" />
                      </p>
                    ) : (
                      <button 
                        onClick={() => {
                          setEditingDescFor(idx.name);
                          setInlineDescInput('');
                        }}
                        className="text-[11px] text-gray-500 hover:text-gray-300 mt-0.5 inline-flex items-center gap-1 transition-colors"
                      >
                        + Add description
                      </button>
                    )}

                    <span className="text-[11px] text-gray-500 font-mono block mt-0.5">
                      {idx.embedding_model || 'Unknown model'}
                    </span>
                  </td>

                  {/* Interactive Tags */}
                  <td className="px-5 py-4">
                    <div className="flex flex-wrap items-center gap-1.5 max-w-[220px]">
                      {(idx.tags || []).map(tag => (
                        <span 
                          key={tag} 
                          className="group inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-medium bg-purple-500/20 text-purple-300 border border-purple-500/30"
                        >
                          <span>@{tag}</span>
                          <button
                            onClick={() => handleRemoveTag(idx.name, tag)}
                            className="text-purple-400/60 hover:text-red-400 transition-colors p-0.5 -mr-1"
                            title={`Remove tag @${tag}`}
                          >
                            <X className="w-2.5 h-2.5" />
                          </button>
                        </span>
                      ))}

                      {isEditingTags ? (
                        <div className="inline-flex items-center gap-1 bg-black/60 border border-purple-500 rounded px-1.5 py-0.5">
                          <input
                            type="text"
                            autoFocus
                            value={inlineTagInput}
                            onChange={e => setInlineTagInput(e.target.value)}
                            onKeyDown={e => {
                              if (e.key === 'Enter') handleAddTagInline(idx.name);
                              if (e.key === 'Escape') setEditingTagFor(null);
                            }}
                            placeholder="tag..."
                            className="w-16 text-[11px] bg-transparent text-white focus:outline-none"
                          />
                          <button 
                            onClick={() => handleAddTagInline(idx.name)}
                            className="text-emerald-400 hover:text-emerald-300"
                            title="Add"
                          >
                            <Check className="w-3 h-3" />
                          </button>
                          <button 
                            onClick={() => setEditingTagFor(null)}
                            className="text-gray-500 hover:text-gray-300"
                            title="Cancel"
                          >
                            <X className="w-3 h-3" />
                          </button>
                        </div>
                      ) : (
                        <button
                          onClick={() => {
                            setEditingTagFor(idx.name);
                            setInlineTagInput('');
                          }}
                          className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] text-gray-500 hover:text-purple-300 hover:bg-white/5 border border-dashed border-white/10 transition-colors"
                          title="Add tag"
                        >
                          <Tag className="w-2.5 h-2.5" />
                          <span>+tag</span>
                        </button>
                      )}
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
                      title={isExposed ? "Exposed to AI Agents via MCP (Click to make Private)" : "Private / Hidden from AI Agents (Click to Expose)"}
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
                    {idx.source_dir && (
                      <span className="text-[11px] text-gray-500 truncate block max-w-[120px]" title={idx.source_dir}>
                        {idx.source_dir}
                      </span>
                    )}
                  </td>

                  <td className="px-5 py-4 text-right">
                    <button 
                      onClick={() => handleDelete(idx.name)} 
                      className="text-red-400 hover:text-red-300 p-1.5 rounded hover:bg-red-500/10 transition-colors"
                      title="Delete index"
                    >
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
