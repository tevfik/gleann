import { useState, useEffect, useMemo } from 'react';
import ForceGraph2D from 'react-force-graph-2d';
import { GitMerge } from 'lucide-react';

export function Graph() {
  const [indexes, setIndexes] = useState<string[]>([]);
  const [selectedIndex, setSelectedIndex] = useState<string>('');
  const [queryType, setQueryType] = useState<string>('callees');
  const [inputVal, setInputVal] = useState<string>('');
  const [results, setResults] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [viewMode, setViewMode] = useState<'list'|'graph'>('graph');

  const graphData = useMemo(() => {
    if (!results || !results.results) return null;
    const nodes: any[] = [];
    const links: any[] = [];
    
    // Root node
    const rootId = inputVal;
    nodes.push({ id: rootId, name: rootId, kind: 'query', val: 5, color: '#06b6d4' }); // cyan-500
    
    results.results.forEach((node: any) => {
      nodes.push({
        id: node.fqn,
        name: node.name,
        kind: node.kind,
        val: 2,
        color: node.kind === 'struct' || node.kind === 'class' ? '#3b82f6' : (node.kind === 'interface' ? '#10b981' : '#8b5cf6')
      });
      
      // Directed edges based on query type
      const isCaller = queryType === 'callers';
      links.push({
        source: isCaller ? node.fqn : rootId,
        target: isCaller ? rootId : node.fqn,
      });
    });
    
    return { nodes, links };
  }, [results, inputVal, queryType]);

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
    if (!selectedIndex || !inputVal.trim()) return;

    setLoading(true);
    setError('');
    setResults(null);

    const body: any = { query: queryType };
    if (queryType === 'cypher') {
      body.cypher = inputVal;
    } else if (queryType === 'symbols_in_file') {
      body.file = inputVal;
    } else {
      body.symbol = inputVal;
    }

    try {
      const res = await fetch(`/api/graph/${selectedIndex}/query`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || 'Query failed');
      }
      setResults(data);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex-1 max-w-5xl mx-auto w-full px-4 pt-6 pb-24 z-10 relative flex flex-col h-full">
      <h1 className="text-2xl font-bold text-white mb-6 flex items-center gap-2">
        <GitMerge className="w-6 h-6 text-blue-400" /> AST Graph Explorer
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
          
          <div className="w-full md:w-auto">
            <label className="text-xs font-semibold text-gray-500 uppercase block mb-1">Query Type</label>
            <select 
              value={queryType} 
              onChange={e => setQueryType(e.target.value)}
              className="w-full bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-3 py-2 outline-none"
            >
              <option value="callees">Callees</option>
              <option value="callers">Callers</option>
              <option value="impact">Impact Analysis</option>
              <option value="symbols_in_file">Symbols in File</option>
              <option value="cypher">Raw Cypher</option>
            </select>
          </div>

          <div className="flex-1 w-full">
            <label className="text-xs font-semibold text-gray-500 uppercase block mb-1">
              {queryType === 'cypher' ? 'Cypher Query' : queryType === 'symbols_in_file' ? 'File Path' : 'Symbol FQN'}
            </label>
            <input 
              type="text"
              value={inputVal}
              onChange={e => setInputVal(e.target.value)}
              placeholder={queryType === 'cypher' ? "MATCH (n) RETURN n LIMIT 10" : "e.g., pkg/api.Server.Start"}
              className="w-full bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-3 py-2 outline-none"
            />
          </div>

          <button 
            type="submit"
            disabled={loading || !inputVal.trim() || !selectedIndex}
            className="w-full md:w-auto bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white px-6 py-2 rounded-lg font-medium transition-colors"
          >
            {loading ? 'Running...' : 'Execute'}
          </button>
        </form>
      </div>

      {error && (
        <div className="bg-red-500/10 border border-red-500/20 text-red-300 p-4 rounded-xl mb-6 text-sm">
          {error}
        </div>
      )}

      {results && (
        <div className="flex-1 bg-black/40 border border-white/5 rounded-xl p-4 overflow-y-auto" style={{ scrollbarWidth: 'thin' }}>
          <div className="flex justify-between items-center mb-4 pb-2 border-b border-white/5">
            <h3 className="font-semibold text-gray-300">Results</h3>
            <div className="flex items-center gap-4">
              {graphData && (
                <div className="flex bg-black/40 border border-white/10 rounded-lg overflow-hidden">
                  <button 
                    onClick={() => setViewMode('list')}
                    className={`px-3 py-1 text-xs font-medium ${viewMode === 'list' ? 'bg-blue-600 text-white' : 'text-gray-400 hover:text-gray-200'}`}
                  >
                    List
                  </button>
                  <button 
                    onClick={() => setViewMode('graph')}
                    className={`px-3 py-1 text-xs font-medium ${viewMode === 'graph' ? 'bg-blue-600 text-white' : 'text-gray-400 hover:text-gray-200'}`}
                  >
                    Graph
                  </button>
                </div>
              )}
              <span className="text-xs text-gray-500">Took {results.query_ms}ms</span>
            </div>
          </div>
          
          {queryType === 'impact' ? (
            <div className="space-y-4 text-sm text-gray-300">
              <p><strong>Total Affected:</strong> {results.total_affected}</p>
              <div>
                <strong>Direct Callers:</strong>
                <ul className="list-disc list-inside ml-2 mt-1 opacity-80">
                  {results.direct_callers?.map((c: string, i: number) => <li key={i}>{c}</li>)}
                  {!results.direct_callers?.length && <li>None</li>}
                </ul>
              </div>
              <div>
                <strong>Affected Files:</strong>
                <ul className="list-disc list-inside ml-2 mt-1 opacity-80">
                  {results.affected_files?.map((c: string, i: number) => <li key={i}>{c}</li>)}
                  {!results.affected_files?.length && <li>None</li>}
                </ul>
              </div>
            </div>
          ) : queryType === 'cypher' ? (
            <pre className="text-xs font-mono text-gray-400 whitespace-pre-wrap">{JSON.stringify(results.rows, null, 2)}</pre>
          ) : (
            viewMode === 'graph' && graphData ? (
              <div className="w-full h-[500px] bg-[#0b0c10] border border-white/5 rounded-xl overflow-hidden relative">
                <ForceGraph2D
                  graphData={graphData}
                  nodeLabel="name"
                  nodeColor="color"
                  nodeRelSize={4}
                  linkDirectionalArrowLength={3.5}
                  linkDirectionalArrowRelPos={1}
                  linkColor={() => 'rgba(255,255,255,0.2)'}
                  backgroundColor="#0b0c10"
                  width={800}
                  height={500}
                  onNodeClick={(node: any) => setInputVal(node.id)}
                />
                <div className="absolute top-2 left-2 flex flex-col gap-1 pointer-events-none">
                  <div className="flex items-center gap-2"><span className="w-2 h-2 rounded-full bg-cyan-500"></span><span className="text-[10px] text-gray-400 uppercase">Search Root</span></div>
                  <div className="flex items-center gap-2"><span className="w-2 h-2 rounded-full bg-blue-500"></span><span className="text-[10px] text-gray-400 uppercase">Struct/Class</span></div>
                  <div className="flex items-center gap-2"><span className="w-2 h-2 rounded-full bg-green-500"></span><span className="text-[10px] text-gray-400 uppercase">Interface</span></div>
                  <div className="flex items-center gap-2"><span className="w-2 h-2 rounded-full bg-blue-500"></span><span className="text-[10px] text-gray-400 uppercase">Method/Function</span></div>
                </div>
              </div>
            ) : (
              <div className="space-y-2">
                {results.results?.map((node: any, i: number) => (
                  <div key={i} className="flex items-center gap-3 bg-white/5 p-2 rounded">
                    <span className="bg-blue-500/20 text-blue-300 text-[10px] uppercase px-1.5 py-0.5 rounded font-bold w-20 text-center">
                      {node.kind}
                    </span>
                    <div className="flex flex-col">
                      <span className="text-sm font-mono text-gray-200">{node.fqn}</span>
                      <span className="text-xs text-gray-500">{node.name}</span>
                    </div>
                  </div>
                ))}
                {!results.results?.length && <div className="text-sm text-gray-500 text-center py-8">No results found.</div>}
              </div>
            )
          )}
        </div>
      )}
    </div>
  );
}


