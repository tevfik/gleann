import { useState, useEffect, useMemo } from 'react';
import ForceGraph2D from 'react-force-graph-2d';
import { Brain } from 'lucide-react';

export function KnowledgeGraph() {
  const [indexes, setIndexes] = useState<string[]>([]);
  const [selectedIndex, setSelectedIndex] = useState<string>('');
  const [startId, setStartId] = useState<string>('');
  const [depth, setDepth] = useState<number>(2);
  const [results, setResults] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const graphData = useMemo(() => {
    if (!results || !results.nodes) return null;
    const nodes = results.nodes.map((n: any) => ({
      id: n.id,
      name: n.label,
      val: 3,
      color: n.node_type === 'Entity' ? '#3b82f6' : '#10b981'
    }));
    
    const links = results.edges?.map((e: any) => ({
      source: e.from,
      target: e.to,
      label: e.relation_type,
      color: 'rgba(255,255,255,0.4)'
    })) || [];
    
    return { nodes, links };
  }, [results]);

  useEffect(() => {
    fetch('/api/indexes')
      .then(res => res.json())
      .then(data => {
        const idxs = data.indexes ? data.indexes.map((i: any) => i.name) : [];
        setIndexes(idxs);
        if (idxs.length > 0) setSelectedIndex(idxs[0]);
      })
      .catch(console.error);
  }, []);

  const handleQuery = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedIndex || !startId.trim()) return;

    setLoading(true);
    setError('');
    setResults(null);

    try {
      const res = await fetch(`/api/memory/${selectedIndex}/traverse`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ start_id: startId, depth: depth })
      });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || 'Traverse failed');
      }
      setResults(data);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex-1 max-w-6xl mx-auto w-full px-4 pt-6 pb-24 z-10 relative flex flex-col h-full">
      <h1 className="text-2xl font-bold text-white mb-6 flex items-center gap-2">
        <Brain className="w-6 h-6 text-purple-400" /> Knowledge Graph Explorer
      </h1>
      
      <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-4 shadow-lg mb-6">
        <form onSubmit={handleQuery} className="flex flex-col md:flex-row gap-3 items-end">
          <div className="w-full md:w-auto">
            <label className="text-xs font-semibold text-gray-500 uppercase block mb-1">Index</label>
            <select 
              value={selectedIndex} 
              onChange={e => setSelectedIndex(e.target.value)}
              className="w-full bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-3 py-2 outline-none"
            >
              {indexes.map(idx => <option key={idx} value={idx}>{idx}</option>)}
            </select>
          </div>
          
          <div className="flex-1 w-full">
            <label className="text-xs font-semibold text-gray-500 uppercase block mb-1">Start Node ID</label>
            <input 
              type="text"
              value={startId}
              onChange={e => setStartId(e.target.value)}
              placeholder="e.g., entity_123 or * for root..."
              className="w-full bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-3 py-2 outline-none"
            />
          </div>

          <div className="w-full md:w-24">
            <label className="text-xs font-semibold text-gray-500 uppercase block mb-1">Depth</label>
            <input 
              type="number"
              min="1"
              max="10"
              value={depth}
              onChange={e => setDepth(parseInt(e.target.value))}
              className="w-full bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-3 py-2 outline-none text-center"
            />
          </div>

          <button 
            type="submit"
            disabled={loading || !startId.trim() || !selectedIndex}
            className="w-full md:w-auto bg-purple-600 hover:bg-purple-500 disabled:opacity-50 text-white px-6 py-2 rounded-lg font-medium transition-colors"
          >
            {loading ? 'Traversing...' : 'Explore'}
          </button>
        </form>
      </div>

      {error && (
        <div className="bg-red-500/10 border border-red-500/20 text-red-300 p-4 rounded-xl mb-6 text-sm">
          {error}
        </div>
      )}

      {results && (
        <div className="flex-1 bg-black/40 border border-white/5 rounded-xl p-4 overflow-hidden flex flex-col">
          <div className="flex justify-between items-center mb-4 pb-2 border-b border-white/5">
            <h3 className="font-semibold text-gray-300">Knowledge Network</h3>
            <span className="text-xs text-gray-500">{results.count} nodes found</span>
          </div>
          
          <div className="flex-1 w-full h-[600px] bg-[#0b0c10] border border-white/5 rounded-xl overflow-hidden relative">
            {graphData && graphData.nodes.length > 0 ? (
              <ForceGraph2D
                graphData={graphData}
                nodeLabel="name"
                nodeColor="color"
                nodeRelSize={4}
                linkDirectionalArrowLength={3.5}
                linkDirectionalArrowRelPos={1}
                linkLabel="label"
                backgroundColor="#0b0c10"
              />
            ) : (
              <div className="flex items-center justify-center h-full text-gray-500">No connections found from this node.</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

