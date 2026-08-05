function Indexes() {
  const [indexes, setIndexes] = useState<any[]>([]);
  const [newIndexName, setNewIndexName] = useState('');
  const [indexPath, setIndexPath] = useState('');
  const [targetIndex, setTargetIndex] = useState('');
  
  const loadIndexes = () => {
    fetch('/api/indexes')
      .then(res => res.json())
      .then(data => {
        setIndexes(data.indexes || []);
        if (data.indexes && data.indexes.length > 0 && !targetIndex) {
          setTargetIndex(data.indexes[0].name);
        }
      })
      .catch(console.error);
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
        alert('Background indexing started for directory!');
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
    // We can just build an empty index by sending a dummy text and deleting it, or a specific endpoint.
    // For now, let's just trigger a build with empty texts (wait, that might error).
    // Or we can just use the name for the directory index.
    alert('Index will be created when you index a directory into it.');
  };

  return (
    <div className="flex-1 max-w-4xl mx-auto w-full px-4 pt-6 pb-24 z-10 relative">
      <h1 className="text-2xl font-bold text-white mb-6 flex items-center gap-2">
        <Database className="w-6 h-6 text-blue-400" /> Index Management
      </h1>

      <div className="bg-[#1a1b23] border border-white/10 rounded-xl p-5 shadow-lg mb-6">
        <h2 className="text-lg font-semibold text-gray-200 mb-4">Index a Directory</h2>
        <form onSubmit={handleIndexDir} className="flex gap-2">
          <select
            value={targetIndex}
            onChange={(e) => setTargetIndex(e.target.value)}
            className="px-3 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500"
          >
            <option value="" disabled>Select Target Index</option>
            {indexes.map((idx: any) => (
              <option key={idx.name} value={idx.name}>{idx.name}</option>
            ))}
          </select>
          <input 
            type="text" 
            value={indexPath} 
            onChange={e => setIndexPath(e.target.value)}
            placeholder="Absolute path to directory (e.g. /home/user/docs)" 
            className="flex-1 px-4 py-2 bg-black/40 border border-white/10 rounded-lg text-white focus:outline-none focus:border-blue-500"
          />
          <button type="submit" className="bg-blue-600 hover:bg-blue-500 text-white px-6 py-2 rounded-lg font-medium transition-colors">
            Start Indexing
          </button>
        </form>
      </div>

      <div className="bg-[#1a1b23] border border-white/10 rounded-xl shadow-lg overflow-hidden">
        <table className="w-full text-left text-sm text-gray-300">
          <thead className="bg-black/40 text-xs uppercase text-gray-500 border-b border-white/10">
            <tr>
              <th className="px-6 py-3">Name</th>
              <th className="px-6 py-3">Documents</th>
              <th className="px-6 py-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {indexes.length === 0 ? (
              <tr>
                <td colSpan={3} className="px-6 py-8 text-center text-gray-500">
                  No indexes found. Create one by indexing a directory.
                </td>
              </tr>
            ) : (
              indexes.map((idx: any) => (
                <tr key={idx.name} className="border-b border-white/5 hover:bg-white/5 transition-colors">
                  <td className="px-6 py-4 font-medium text-white">{idx.name}</td>
                  <td className="px-6 py-4">{idx.doc_count || 0}</td>
                  <td className="px-6 py-4 text-right">
                    <button onClick={() => handleDelete(idx.name)} className="text-red-400 hover:text-red-300 transition-colors">
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
