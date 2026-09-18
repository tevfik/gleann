import { useState, useEffect } from 'react';
import { Brain, Trash2, ArrowRight, Sparkles, Tag } from 'lucide-react';

export function Memory() {
  const [blocks, setBlocks] = useState<any[]>([]);
  const [newContent, setNewContent] = useState('');
  const [newTier, setNewTier] = useState('long');
  const [compactMsg, setCompactMsg] = useState<string | null>(null);

  const loadBlocks = () => {
    fetch(`/api/blocks?t=${Date.now()}`)
      .then(res => res.json())
      .then(data => {
        setBlocks(data.blocks || []);
      })
      .catch(console.error);
  };

  useEffect(() => {
    loadBlocks();
  }, []);

  const handleCompact = async () => {
    try {
      const res = await fetch('/api/blocks/compact', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ min_validity: 0.2 })
      });
      const data = await res.json();
      setCompactMsg(`Compacted: ${data.pruned || 0} stale/unreliable block(s) pruned`);
      setTimeout(() => setCompactMsg(null), 4000);
      loadBlocks();
    } catch (err) {
      console.error(err);
      setCompactMsg('Compaction failed');
      setTimeout(() => setCompactMsg(null), 4000);
    }
  };

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newContent.trim()) return;
    try {
      await fetch('/api/blocks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: newContent, tier: newTier })
      });
      setNewContent('');
      loadBlocks();
    } catch (err) {
      console.error(err);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm('Are you sure you want to delete this memory block?')) return;
    try {
      await fetch(`/api/blocks/${id}`, { method: 'DELETE' });
      loadBlocks();
    } catch (err) {
      console.error(err);
    }
  };

  const handleClearAll = async () => {
    if (!confirm('Are you sure you want to clear ALL memory blocks?')) return;
    try {
      await fetch('/api/blocks', { method: 'DELETE' });
      loadBlocks();
    } catch (err) {
      console.error(err);
    }
  };

  return (
    <div className="flex-1 max-w-5xl mx-auto w-full px-4 pt-6 pb-24 z-10 relative">
      <div className="flex justify-between items-center mb-2">
        <h1 className="text-2xl font-bold text-white flex items-center gap-2">
          <Brain className="w-6 h-6 text-blue-400" /> Long-Term Memory
        </h1>
        <div className="flex items-center gap-2">
          {compactMsg && (
            <span className="text-xs text-indigo-400 font-medium bg-indigo-500/10 border border-indigo-500/20 px-2.5 py-1 rounded-md animate-fade-in">
              {compactMsg}
            </span>
          )}
          <button
            onClick={handleCompact}
            title="Compact memory by pruning stale and unreliable memories (<0.2 Bayesian validity)"
            className="bg-indigo-500/10 hover:bg-indigo-500/20 text-indigo-400 border border-indigo-500/20 px-3 py-1.5 rounded-lg text-sm font-medium flex items-center gap-1.5 transition-colors"
          >
            <Sparkles className="w-4 h-4" />
            Compact Memory
          </button>
          <button 
            onClick={handleClearAll}
            className="bg-red-500/10 hover:bg-red-500/20 text-red-400 border border-red-500/20 px-4 py-2 rounded-lg text-sm font-medium transition-colors"
          >
            Clear All
          </button>
        </div>
      </div>
      <p className="text-sm text-gray-400 mb-6 max-w-3xl">
        Memory blocks are automatically injected into the LLM context based on semantic relevance. 
        <strong> Short Tier</strong> holds recent conversation states, <strong>Medium Tier</strong> holds project-level context, and <strong>Long Tier</strong> holds core facts and permanent knowledge. Gleann dynamically retrieves these during chat!
      </p>

      {/* Memory Flow Dashboard */}
      <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-6 shadow-lg mb-8 relative overflow-hidden">
        <div className="absolute top-0 left-0 w-full h-1 bg-gradient-to-r from-green-500 via-blue-500 to-blue-500"></div>
        <h2 className="text-sm font-semibold text-gray-300 uppercase tracking-wider mb-4">Memory Flow & Status</h2>
        <div className="flex flex-col md:flex-row justify-between items-center gap-4 relative z-10">
          
          {/* Short Tier */}
          <div className="flex-1 w-full bg-black/40 border border-white/5 rounded-lg p-4 flex flex-col items-center justify-center relative group hover:border-green-500/30 transition-colors">
            <div className="absolute inset-0 bg-green-500/5 blur-xl rounded-full opacity-0 group-hover:opacity-100 transition-opacity"></div>
            <div className="text-green-400 font-mono text-3xl font-bold mb-1 relative z-10">{blocks.filter(b => b.tier === 'short').length}</div>
            <div className="text-xs text-gray-400 uppercase font-semibold tracking-wide relative z-10">Short Tier</div>
            <div className="text-[10px] text-gray-500 text-center mt-2 relative z-10">Volatile state & recent chat</div>
          </div>

          <div className="hidden md:flex flex-col items-center justify-center text-gray-600">
            <ArrowRight className="w-6 h-6 animate-pulse text-green-500/50" />
            <span className="text-[10px] mt-1 font-mono">Promote</span>
          </div>

          {/* Medium Tier */}
          <div className="flex-1 w-full bg-black/40 border border-white/5 rounded-lg p-4 flex flex-col items-center justify-center relative group hover:border-blue-500/30 transition-colors">
            <div className="absolute inset-0 bg-blue-500/5 blur-xl rounded-full opacity-0 group-hover:opacity-100 transition-opacity"></div>
            <div className="text-blue-400 font-mono text-3xl font-bold mb-1 relative z-10">{blocks.filter(b => b.tier === 'medium').length}</div>
            <div className="text-xs text-gray-400 uppercase font-semibold tracking-wide relative z-10">Medium Tier</div>
            <div className="text-[10px] text-gray-500 text-center mt-2 relative z-10">Project context & scoped facts</div>
          </div>

          <div className="hidden md:flex flex-col items-center justify-center text-gray-600">
            <ArrowRight className="w-6 h-6 animate-pulse text-blue-500/50" />
            <span className="text-[10px] mt-1 font-mono">Synthesize</span>
          </div>

          {/* Long Tier */}
          <div className="flex-1 w-full bg-black/40 border border-white/5 rounded-lg p-4 flex flex-col items-center justify-center relative group hover:border-blue-500/30 transition-colors">
            <div className="absolute inset-0 bg-blue-500/5 blur-xl rounded-full opacity-0 group-hover:opacity-100 transition-opacity"></div>
            <div className="text-blue-400 font-mono text-3xl font-bold mb-1 relative z-10">{blocks.filter(b => b.tier === 'long').length}</div>
            <div className="text-xs text-gray-400 uppercase font-semibold tracking-wide relative z-10">Long Tier</div>
            <div className="text-[10px] text-gray-500 text-center mt-2 relative z-10">Permanent knowledge base</div>
          </div>

        </div>
      </div>
      
      <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-4 shadow-lg mb-8">
        <form onSubmit={handleAdd} className="flex flex-col gap-3">
          <textarea
            value={newContent}
            onChange={e => setNewContent(e.target.value)}
            placeholder="Add a new memory block manually..."
            className="w-full bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-3 py-2 outline-none min-h-[80px]"
          />
          <div className="flex justify-between items-center">
            <select 
              value={newTier} 
              onChange={e => setNewTier(e.target.value)}
              className="bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-3 py-1 outline-none"
            >
              <option value="short">Short Tier</option>
              <option value="medium">Medium Tier</option>
              <option value="long">Long Tier</option>
            </select>
            <button 
              type="submit"
              disabled={!newContent.trim()}
              className="bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white px-6 py-2 rounded-lg text-sm font-medium transition-colors"
            >
              Add Memory
            </button>
          </div>
        </form>
      </div>

      <div className="space-y-8">
        {['short', 'medium', 'long'].map(tierGroup => {
          const tierBlocks = blocks.filter(b => b.tier === tierGroup);
          if (tierBlocks.length === 0) return null;
          return (
            <div key={tierGroup}>
              <h3 className="text-lg font-semibold text-gray-300 capitalize mb-3 border-b border-white/5 pb-2">
                {tierGroup} Term Memory <span className="text-xs text-gray-500 ml-2">({tierBlocks.length})</span>
              </h3>
              <div className="grid gap-4">
                {tierBlocks.map((block, i) => (
                  <div key={i} className="bg-[#1a1b23] border border-white/10 rounded-xl p-4 shadow-lg group hover:border-blue-500/30 transition-colors">
                    <div className="flex justify-between items-start mb-2">
                      <div className="flex flex-wrap gap-2 items-center">
                        <span className="text-[10px] text-gray-500">{new Date(block.created_at).toLocaleString()}</span>
                        {block.label && <span className="text-[10px] font-mono bg-white/5 text-gray-400 px-1.5 py-0.5 rounded uppercase">{block.label}</span>}
                        {block.scope && <span className="text-[10px] bg-blue-500/10 text-blue-300 px-1.5 py-0.5 rounded">Scope: {block.scope}</span>}
                      </div>
                      <button 
                        onClick={() => handleDelete(block.id)}
                        className="opacity-0 group-hover:opacity-100 text-gray-500 hover:text-red-400 transition-all p-1"
                        title="Delete memory"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                    <p className="text-sm text-gray-300 whitespace-pre-wrap mb-2">{block.content}</p>
                    {block.tags && block.tags.length > 0 && (
                      <div className="flex flex-wrap gap-1 mt-2 pt-2 border-t border-white/5">
                        {block.tags.map((t: string, idx: number) => (
                          <span key={idx} className="text-[10px] text-gray-400 bg-white/5 px-2 py-0.5 rounded-full flex items-center gap-1">
                            <Tag className="w-2.5 h-2.5 opacity-60" />
                            {t}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          );
        })}
        {blocks.length === 0 && (
          <div className="text-gray-500 text-center py-10">No memory blocks found. Add one above!</div>
        )}
      </div>
    </div>
  );
}


