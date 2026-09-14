import { useState, useEffect, useRef } from 'react';
import { Send, MessageSquare, Plus, Search, Trash2, Sun, Moon, Paperclip, Brain, AlertCircle, Activity, Database } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { Source, Message, ConversationSummary } from '../types';

export function Chat() {
  const [query, setQuery] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  
  const [indexes, setIndexes] = useState<string[]>([]);
  const [selectedIndex, setSelectedIndex] = useState<string>('');
  
  const [mode, setMode] = useState<'ask' | 'search'>('ask');
  const [visionRAG, setVisionRAG] = useState(false);
  
  const [conversations, setConversations] = useState<ConversationSummary[]>([]);
  const [activeConvId, setActiveConvId] = useState<string>('');
  const [activeConvTitle, setActiveConvTitle] = useState<string>('New Conversation');
  const [isEditingTitle, setIsEditingTitle] = useState(false);
  const [viewingSource, setViewingSource] = useState<Source | null>(null);
  const [isLightMode, setIsLightMode] = useState(false);
  const [pastedImages, setPastedImages] = useState<{url: string, file: File}[]>([]);
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (isLightMode) {
      document.body.classList.add('light-theme');
    } else {
      document.body.classList.remove('light-theme');
    }
  }, [isLightMode]);
  
  const messagesEndRef = useRef<HTMLDivElement>(null);

  // Fetch initial data
  useEffect(() => {
    fetch('/api/indexes')
      .then(res => res.json())
      .then(data => {
        const idxs = data.indexes ? data.indexes.map((i: any) => i.name) : [];
        setIndexes(idxs);
        if (idxs.length > 0) setSelectedIndex(idxs[0]);
      })
      .catch(err => console.error("Failed to fetch indexes:", err));

    loadConversations();
  }, []);

  const loadConversations = () => {
    fetch('/api/conversations')
      .then(res => res.json())
      .then(data => {
        setConversations(data.conversations || []);
      })
      .catch(err => console.error("Failed to fetch conversations:", err));
  };

  const deleteConversation = (id: string, e: React.MouseEvent) => {
    e.stopPropagation();
    if (!confirm('Are you sure you want to delete this chat?')) return;
    fetch(`/api/conversations/${id}`, { method: 'DELETE' })
      .then(res => res.json())
      .then(() => {
        if (activeConvId === id) {
          setActiveConvId('');
          setMessages([]);
        }
        loadConversations();
      })
      .catch(err => console.error("Failed to delete conversation:", err));
  };

  const saveEditedTitle = (newTitle: string) => {
    if (!activeConvId || !newTitle.trim()) {
      setIsEditingTitle(false);
      return;
    }
    fetch(`/api/conversations/${activeConvId}`, { 
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: newTitle })
    })
      .then(res => res.json())
      .then(() => {
        setActiveConvTitle(newTitle);
        loadConversations();
        setIsEditingTitle(false);
      })
      .catch(err => console.error("Failed to rename conversation:", err));
  };

  const loadConversation = (id: string) => {
    fetch(`/api/conversations/${id}`)
      .then(res => res.json())
      .then(data => {
        if (data.indexes && data.indexes.length > 0) {
          setSelectedIndex(data.indexes[0]);
        }
        setMessages(data.messages || data.history || []);
        setActiveConvId(id);
        setActiveConvTitle(data.title || 'Untitled');
      })
      .catch(console.error);
  };

  const startNewConversation = () => {
    setActiveConvId('');
    setActiveConvTitle('New Conversation');
    setMessages([]);
  };

  const clearAllHistory = () => {
    if (!confirm('Are you sure you want to clear all history?')) return;
    fetch('/api/conversations', { method: 'DELETE' })
      .then(res => res.json())
      .then(() => {
        setActiveConvId('');
        setMessages([]);
        loadConversations();
      })
      .catch(err => console.error("Failed to clear history:", err));
  };

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const handleAsk = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!query.trim() || !selectedIndex || isLoading) return;

    const userQuery = query;
    setQuery('');
    setMessages(prev => [...prev, { role: 'user', content: userQuery }]);
    setIsLoading(true);

    try {
      if (userQuery.trim().startsWith('/index ')) {
        const path = userQuery.trim().substring(7).trim();
        const response = await fetch(`/api/indexes/${selectedIndex}/index-path`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: path })
        });
        if (!response.ok) throw new Error('Failed to index path');
        setMessages(prev => [...prev, { 
          role: 'assistant', 
          content: `✅ Indexing started in background for \`${path}\` into \`${selectedIndex}\`!`
        }]);
        setIsLoading(false);
        return;
      }

      if (mode === 'search') {
        const response = await fetch(`/api/indexes/${selectedIndex}/search`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ query: userQuery, top_k: 10 })
        });
        
        if (!response.ok) throw new Error(`Search failed: ${response.statusText}`);
        
        const data = await response.json();
        
        setMessages(prev => [...prev, { 
          role: 'assistant', 
          content: `**Semantic Search Results:** Found ${data.count || 0} chunks.`,
          sources: data.results || []
        }]);
        
      } else {
        // ASK (RAG) Mode - Streaming
        setMessages(prev => [...prev, { role: 'assistant', content: '' }]);
  
        const body: any = { question: userQuery };
        if (activeConvId) body.conversation_id = activeConvId;
        if (visionRAG) body.vision_rag = true;
        if (pastedImages.length > 0) {
          body.images = pastedImages.map(img => img.url.split(',')[1]);
        }
        setPastedImages([]);

        const response = await fetch(`/api/indexes/${selectedIndex}/ask?stream=true`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body)
        });
  
        if (!response.ok) {
          throw new Error(`Error: ${response.statusText}`);
        }
  
        const reader = response.body?.getReader();
        const decoder = new TextDecoder();
        if (!reader) throw new Error("No reader available");
  
        let buffer = '';
        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n\n');
          buffer = lines.pop() || ''; 
          
          for (const line of lines) {
            if (line.startsWith('data: ')) {
              const dataStr = line.slice(6);
              if (dataStr === '[DONE]') continue;
              try {
                const data = JSON.parse(dataStr);
                if (data.error) {
                  setMessages(prev => {
                    const newMsgs = [...prev];
                    const lastMsg = newMsgs[newMsgs.length - 1];
                    lastMsg.content += `\n❌ **Error:** ${data.error}`;
                    return newMsgs;
                  });
                } else if (data.sources) {
                  setMessages(prev => {
                    const newMsgs = [...prev];
                    const lastMsg = newMsgs[newMsgs.length - 1];
                    lastMsg.sources = data.sources;
                    return newMsgs;
                  });
                } else if (data.status) {
                  setMessages(prev => {
                    const newMsgs = [...prev];
                    const lastMsg = newMsgs[newMsgs.length - 1];
                    lastMsg.status = data.status;
                    return newMsgs;
                  });
                } else if (data.token) {
                  setMessages(prev => {
                    const newMsgs = [...prev];
                    const lastMsg = newMsgs[newMsgs.length - 1];
                    lastMsg.content += data.token;
                    lastMsg.status = undefined; // Clear status when tokens arrive
                    return newMsgs;
                  });
                } else if (data.conversation_id) {
                  setActiveConvId(data.conversation_id);
                  if (!activeConvTitle || activeConvTitle === 'New Conversation') {
                    setActiveConvTitle(userQuery.length > 50 ? userQuery.substring(0, 47) + '...' : userQuery);
                  }
                  setTimeout(() => {
                    loadConversations();
                  }, 500);
                }
              } catch (e) {
                // ignore parse errors
              }
            }
          }
        }
        
        // Refresh conversations to get updated title or new convo ID
        loadConversations();
      }
    } catch (error) {
      setMessages(prev => {
        const newMsgs = [...prev];
        const lastMsg = newMsgs[newMsgs.length - 1];
        lastMsg.content = `❌ Connection Error: Failed to reach the Gleann backend. Please ensure the index is built and the server is running.`;
        return newMsgs;
      });
    } finally {
      setIsLoading(false);
    }
  };

  const handleFileUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file || !selectedIndex) return;
    
    if (fileInputRef.current) fileInputRef.current.value = '';

    setIsLoading(true);
    setMessages(prev => [...prev, { role: 'user', content: `[Uploaded File: ${file.name}]` }]);

    try {
      const formData = new FormData();
      formData.append('file', file);

      const response = await fetch(`/api/indexes/${selectedIndex}/upload`, {
        method: 'POST',
        body: formData
      });

      if (!response.ok) throw new Error('Failed to index file');
      
      setMessages(prev => [...prev, { 
        role: 'assistant', 
        content: `✅ File \`${file.name}\` successfully indexed into \`${selectedIndex}\` and ready to be queried!`
      }]);
    } catch (err) {
      console.error(err);
      setMessages(prev => [...prev, { role: 'assistant', content: '❌ Failed to index file.' }]);
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="flex flex-1 h-[calc(100vh-65px)] overflow-hidden">
      {/* Sidebar - Conversation History */}
      <aside className="w-64 bg-[#111218] border-r border-white/5 flex flex-col z-10 hidden md:flex">
        <div className="p-4 border-b border-white/5">
          <button 
            onClick={startNewConversation}
            className="w-full flex items-center justify-center gap-2 bg-blue-600 hover:bg-blue-500 text-white px-4 py-2 rounded-lg transition-colors text-sm font-medium"
          >
            <Plus className="w-4 h-4" /> New Chat
          </button>
        </div>
        <div className="flex-1 overflow-y-auto p-2" style={{ scrollbarWidth: 'thin' }}>
          <div className="flex items-center justify-between px-2 mt-2 mb-2">
            <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider">History</div>
            {conversations.length > 0 && (
              <button onClick={clearAllHistory} className="text-[10px] text-gray-500 hover:text-red-400">Clear All</button>
            )}
          </div>
          {conversations.length === 0 ? (
            <div className="text-gray-600 text-xs text-center py-4">No history found.</div>
          ) : (
            conversations.map(conv => (
              <button 
                key={conv.id}
                onClick={() => loadConversation(conv.id)}
                className={`w-full text-left flex items-start gap-2 p-2 rounded-lg transition-colors mb-1 group ${
                  activeConvId === conv.id ? 'bg-white/10 text-white' : 'text-gray-400 hover:bg-white/5 hover:text-gray-200'
                }`}
              >
                <MessageSquare className="w-4 h-4 flex-shrink-0 mt-0.5 opacity-70" />
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium truncate">{conv.title}</div>
                  <div className="text-[10px] text-gray-500 mt-0.5">{new Date(conv.created_at).toLocaleDateString()}</div>
                </div>
                <div className="flex flex-col opacity-0 group-hover:opacity-100 gap-1">
                  <div 
                    className="p-1 hover:bg-red-500/20 text-red-400 rounded transition-all"
                    onClick={(e) => deleteConversation(conv.id, e)}
                    title="Delete Conversation"
                  >
                    <Trash2 className="w-3 h-3" />
                  </div>
                </div>
              </button>
            ))
          )}
        </div>
        <div className="p-4 border-t border-white/5">
          <button 
            onClick={() => setIsLightMode(!isLightMode)}
            className="flex items-center gap-2 text-sm text-gray-400 hover:text-white transition-colors w-full p-2 rounded-lg hover:bg-white/5"
          >
            {isLightMode ? <Moon className="w-4 h-4" /> : <Sun className="w-4 h-4" />}
            {isLightMode ? 'Dark Mode' : 'Light Mode'}
          </button>
        </div>
      </aside>

      {/* Main Chat Area */}
      <div className="flex-1 flex flex-col relative z-10">
        <main className="flex-1 flex flex-col max-w-4xl mx-auto w-full px-4 pt-6 pb-28 overflow-hidden">
          {messages.length === 0 ? (
            <div className="flex-1 flex flex-col items-center justify-center opacity-80 mt-10">
              <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-blue-500/20 to-blue-500/20 flex items-center justify-center border border-white/10 mb-6">
                <Brain className="w-8 h-8 text-blue-400" />
              </div>
              <h1 className="text-4xl font-bold tracking-tight text-center mb-4 text-transparent bg-clip-text bg-gradient-to-br from-white to-gray-400">
                Workspace Intelligence
              </h1>
              <p className="text-gray-400 text-center max-w-lg mb-8">
                I have access to your indexed code, documentation, and long-term memory. Ask me to explain the architecture, find bugs, or trace dependencies.
              </p>
              
              {indexes.length === 0 && (
                <div className="flex items-start gap-3 bg-red-500/10 border border-red-500/20 p-4 rounded-xl text-red-300 max-w-md">
                  <AlertCircle className="w-5 h-5 flex-shrink-0 mt-0.5" />
                  <div className="text-sm">
                    <strong className="font-semibold block mb-1">No indexes found</strong>
                    Run <code className="bg-black/30 px-1 py-0.5 rounded text-red-200">gleann build my-index --dir .</code> in your terminal to create your first knowledge base.
                  </div>
                </div>
              )}
            </div>
          ) : (
            <div className="flex-1 overflow-y-auto pr-2 flex flex-col gap-6" style={{ scrollbarWidth: 'thin' }}>
              {activeConvId && (
                <div className="flex items-center justify-center py-2 border-b border-white/5 mb-2 sticky top-0 bg-[#0b0c10]/90 backdrop-blur z-10">
                  {isEditingTitle ? (
                    <input 
                      type="text" 
                      defaultValue={activeConvTitle}
                      autoFocus
                      onBlur={(e) => saveEditedTitle(e.target.value)}
                      onKeyDown={(e) => e.key === 'Enter' && saveEditedTitle(e.currentTarget.value)}
                      className="bg-black/50 border border-blue-500/50 rounded px-3 py-1 text-sm text-center text-white focus:outline-none w-64"
                    />
                  ) : (
                    <h2 onClick={() => setIsEditingTitle(true)} className="text-sm font-semibold text-gray-400 hover:text-white cursor-pointer transition-colors flex items-center gap-2">
                      {activeConvTitle} <span className="text-[10px] bg-white/10 px-1.5 py-0.5 rounded">Edit</span>
                    </h2>
                  )}
                </div>
              )}
              {messages.map((msg, i) => (
                <div key={i} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  <div className={`max-w-[85%] rounded-2xl p-4 ${
                    msg.role === 'user' 
                      ? 'bg-blue-600 text-white shadow-lg shadow-blue-900/20 rounded-tr-sm' 
                      : 'bg-[#1a1b23] border border-white/5 text-gray-300 shadow-md rounded-tl-sm'
                  }`}>
                    {msg.role === 'assistant' && msg.content === '' ? (
                      msg.status ? (
                        <div className="flex items-center text-blue-400 gap-2 px-2 h-6">
                          <Activity className="h-4 w-4 animate-spin" />
                          <span className="text-sm italic">{msg.status}</span>
                        </div>
                      ) : (
                        <div className="flex gap-1.5 h-6 items-center px-2">
                          <div className="w-2 h-2 bg-blue-400 rounded-full animate-pulse" />
                          <div className="w-2 h-2 bg-blue-400 rounded-full animate-pulse delay-75" />
                          <div className="w-2 h-2 bg-blue-400 rounded-full animate-pulse delay-150" />
                        </div>
                      )
                    ) : (
                      <div className="prose prose-invert prose-p:leading-relaxed prose-pre:bg-black/50 prose-pre:border prose-pre:border-white/10 max-w-none">
                        <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.content}</ReactMarkdown>
                      </div>
                    )}
                    {msg.sources && msg.sources.length > 0 && (
                      <div className="mt-4 pt-3 border-t border-white/5">
                        <details className="group cursor-pointer">
                          <summary className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2 flex items-center gap-1 list-none outline-none">
                            <Database className="w-3 h-3 group-open:text-blue-400 transition-colors" />
                            <span>Sources ({msg.sources.length})</span>
                            <span className="ml-auto transition-transform group-open:rotate-180 opacity-50">▼</span>
                          </summary>
                          <div className="flex flex-col gap-2 mt-2">
                            {msg.sources.map((src, idx) => (
                              <div 
                                key={idx} 
                                onClick={() => setViewingSource(src)}
                                className="bg-black/30 border border-white/5 rounded-md p-2 text-[11px] font-mono text-gray-400 break-words cursor-pointer hover:border-blue-500/50 hover:bg-blue-500/5 transition-colors"
                              >
                                <div className="flex gap-2 mb-1 items-center">
                                  <span className="text-blue-400 font-bold">[{idx + 1}]</span>
                                  {src.metadata?.source && (
                                    <span className="text-blue-300/80 bg-blue-500/10 px-1 rounded truncate flex-1">{src.metadata.source}</span>
                                  )}
                                  {src.score !== undefined && (
                                    <span className="text-gray-600">score: {src.score.toFixed(3)}</span>
                                  )}
                                </div>
                                <span className="opacity-70 line-clamp-3 leading-relaxed">{src.text}</span>
                              </div>
                            ))}
                          </div>
                        </details>
                      </div>
                    )}
                  </div>
                </div>
              ))}
              <div ref={messagesEndRef} />
            </div>
          )}
        </main>

        {viewingSource && (
          <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center p-6" onClick={() => setViewingSource(null)}>
            <div className="bg-[#111218] border border-blue-500/30 rounded-xl max-w-4xl w-full max-h-[85vh] flex flex-col shadow-2xl" onClick={e => e.stopPropagation()}>
              <div className="p-4 border-b border-white/10 flex justify-between items-center bg-black/40 rounded-t-xl">
                <div className="flex items-center gap-2">
                  <Database className="w-5 h-5 text-blue-400" />
                  <h3 className="font-semibold text-gray-200">
                    {viewingSource.metadata?.source || 'Source Content'}
                  </h3>
                </div>
                <button onClick={() => setViewingSource(null)} className="text-gray-400 hover:text-white p-1">✕</button>
              </div>
              <div className="p-5 overflow-y-auto flex-1 font-mono text-sm text-gray-300 whitespace-pre-wrap leading-relaxed" style={{ scrollbarWidth: 'thin' }}>
                {viewingSource.text}
              </div>
              <div className="p-3 border-t border-white/10 bg-black/20 text-xs text-gray-500 rounded-b-xl flex justify-between">
                <span>Semantic Search Result</span>
                {viewingSource.score !== undefined && <span>Score: {viewingSource.score.toFixed(4)}</span>}
              </div>
            </div>
          </div>
        )}

        <div className="absolute bottom-0 left-0 right-0 bg-gradient-to-t from-[#0b0c10] via-[#0b0c10] to-transparent pt-10 pb-6 pointer-events-none z-20">
          <div className="max-w-4xl mx-auto px-6 pointer-events-auto relative">
            <form onSubmit={handleAsk} className="relative group">
              <div className="absolute -inset-1 bg-gradient-to-r from-blue-600 to-blue-500 rounded-2xl blur opacity-20 group-hover:opacity-30 transition duration-500"></div>
              <div className="relative flex flex-col bg-[#13151a] border border-white/10 rounded-2xl shadow-2xl p-2 transition-all">
                
                <div className="flex items-center gap-4 border-b border-white/5 pb-2 mb-2 px-2 pt-1">
                  <div className="flex items-center">
                    <span className="text-xs font-semibold text-gray-500 uppercase tracking-wider mr-2">Target Index:</span>
                    {indexes.length > 0 ? (
                      <select 
                        value={selectedIndex} 
                        onChange={e => setSelectedIndex(e.target.value)}
                        className="bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-2 py-1 outline-none hover:bg-white/5 cursor-pointer"
                      >
                        {indexes.map(idx => (
                          <option key={idx} value={idx}>{idx}</option>
                        ))}
                      </select>
                    ) : (
                      <span className="text-xs text-red-400">No indexes available</span>
                    )}
                  </div>

                  <div className="flex items-center">
                    <span className="text-xs font-semibold text-gray-500 uppercase tracking-wider mr-2">Mode:</span>
                    <select 
                      value={mode} 
                      onChange={e => setMode(e.target.value as 'ask' | 'search')}
                      className="bg-black/30 border border-white/10 rounded-lg text-sm text-gray-300 px-2 py-1 outline-none hover:bg-white/5 cursor-pointer"
                    >
                      <option value="ask">Ask (RAG)</option>
                      <option value="search">Semantic Search</option>
                    </select>
                  </div>
                  
                  {mode === 'ask' && (
                    <div className="flex items-center ml-auto">
                      <button
                        type="button"
                        onClick={() => setVisionRAG(!visionRAG)}
                        className={`flex items-center gap-1.5 px-3 py-1 rounded border text-[11px] font-semibold transition-all ${
                          visionRAG 
                            ? 'bg-blue-500/20 border-blue-400 text-blue-400 shadow-[0_0_10px_rgba(59,130,246,0.2)]' 
                            : 'bg-black/30 border-white/10 text-gray-400 hover:text-gray-300'
                        }`}
                        title="Late-Binding Multimodal RAG (Deep Vision RAG)"
                      >
                        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>
                        Deep Vision RAG
                      </button>
                    </div>
                  )}
                </div>

                <div className="flex flex-col bg-black/20 rounded-xl mt-1 border border-white/5">
                  {pastedImages.length > 0 && (
                    <div className="flex flex-wrap gap-2 p-3 border-b border-white/5">
                      {pastedImages.map((img, i) => (
                        <div key={i} className="relative group">
                          <img src={img.url} alt="pasted" className="h-16 rounded object-cover border border-white/10" />
                          <button
                            type="button"
                            onClick={() => setPastedImages(prev => prev.filter((_, idx) => idx !== i))}
                            className="absolute -top-2 -right-2 bg-red-500 rounded-full p-0.5 text-white opacity-0 group-hover:opacity-100 transition-opacity"
                          >
                            <Trash2 className="w-3 h-3" />
                          </button>
                        </div>
                      ))}
                    </div>
                  )}
                  <div className="flex items-end">
                  <input 
                    type="file" 
                    ref={fileInputRef} 
                    className="hidden" 
                    onChange={handleFileUpload} 
                  />
                  <button 
                    type="button"
                    onClick={() => fileInputRef.current?.click()}
                    disabled={indexes.length === 0 || isLoading}
                    className="p-3 text-gray-500 hover:text-blue-400 disabled:opacity-50 transition-colors"
                    title="Upload & Index File"
                  >
                    <Paperclip className="w-5 h-5" />
                  </button>
                  <textarea
                    className="flex-1 bg-transparent text-white py-3 outline-none placeholder-gray-500 text-[15px] resize-none max-h-48 min-h-[44px]"
                    placeholder={indexes.length > 0 ? (mode === 'ask' ? "Ask Gleann or /index <path>..." : "Search index...") : "Please create an index first..."}
                    rows={1}
                    value={query}
                    disabled={indexes.length === 0 || isLoading}
                    onChange={(e) => {
                      setQuery(e.target.value);
                      e.target.style.height = 'auto';
                      e.target.style.height = `${Math.min(e.target.scrollHeight, 200)}px`;
                    }}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && !e.shiftKey) {
                        e.preventDefault();
                        handleAsk();
                      }
                    }}
                    onPaste={(e) => {
                      const items = e.clipboardData?.items;
                      if (items) {
                        for (let i = 0; i < items.length; i++) {
                          if (items[i].type.indexOf('image/') !== -1) {
                            const file = items[i].getAsFile();
                            if (file) {
                              const reader = new FileReader();
                              reader.onload = (e) => {
                                if (e.target?.result) {
                                  setPastedImages(prev => [...prev, { url: e.target!.result as string, file }]);
                                }
                              };
                              reader.readAsDataURL(file);
                            }
                          }
                        }
                      }
                    }}
                  />
                  <button 
                    disabled={!query.trim() || indexes.length === 0 || isLoading}
                    className="bg-transparent disabled:opacity-50 disabled:cursor-not-allowed hover:bg-white/5 text-white p-3 rounded-xl transition-all"
                  >
                    {mode === 'ask' ? <Send className="w-5 h-5 text-blue-400" /> : <Search className="w-5 h-5 text-blue-400" />}
                  </button>
                  </div>
                </div>
              </div>
            </form>
            
            <div className="text-center mt-3">
              <span className="text-[11px] text-gray-600">Gleann injects your Long-Term Memory context into every request.</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

