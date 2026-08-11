import { useState, useEffect, useRef, useMemo } from 'react';
import { Routes, Route, Link, useLocation } from 'react-router-dom';
import ForceGraph2D from 'react-force-graph-2d';
import { Send, Database, Brain, GitMerge, Sparkles, AlertCircle, MessageSquare, Plus, Search, Trash2, Settings, Sun, Moon, Terminal, Activity, ArrowRight, Paperclip, AlertTriangle } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

interface Source {
  text: string;
  metadata?: any;
  score?: number;
}

interface Message {
  role: 'user' | 'assistant';
  content: string;
  sources?: Source[];
  status?: string;
}

interface ConversationSummary {
  id: string;
  short_id: string;
  title: string;
  model: string;
  indexes: string;
  message_count: number;
  created_at: string;
  updated_at: string;
}

function Chat() {
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

function Memory() {
  const [blocks, setBlocks] = useState<any[]>([]);
  const [newContent, setNewContent] = useState('');
  const [newTier, setNewTier] = useState('long');

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
        <button 
          onClick={handleClearAll}
          className="bg-red-500/10 hover:bg-red-500/20 text-red-400 border border-red-500/20 px-4 py-2 rounded-lg text-sm font-medium transition-colors"
        >
          Clear All
        </button>
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
                      <div className="flex gap-2 items-center">
                        <span className="text-[10px] text-gray-500">{new Date(block.created_at).toLocaleString()}</span>
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
                    <p className="text-sm text-gray-300 whitespace-pre-wrap">{block.content}</p>
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

function Graph() {
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

function System() {
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

function Indexes() {
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

function KnowledgeGraph() {
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



function App() {
  const location = useLocation();

  const getNavClass = (path: string) => {
    const isActive = location.pathname === path;
    return `flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm font-medium transition-all ${
      isActive ? 'text-white bg-white/10' : 'text-gray-400 hover:text-white hover:bg-white/5'
    }`;
  };

  return (
    <div className="h-screen bg-[#0b0c10] text-gray-200 font-sans selection:bg-blue-500/30 flex flex-col overflow-hidden">
      <div className="fixed top-0 left-1/2 -translate-x-1/2 w-[1000px] h-[600px] bg-blue-900/10 rounded-full blur-[120px] pointer-events-none z-0" />

      <nav className="relative z-20 flex items-center justify-between px-6 py-3 border-b border-white/5 bg-black/40 backdrop-blur-xl h-[65px] flex-shrink-0">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-blue-600 to-blue-500 flex items-center justify-center shadow-[0_0_15px_rgba(168,85,247,0.3)]">
            <Sparkles className="w-4 h-4 text-white" />
          </div>
          <span className="font-bold text-lg tracking-wide text-white">Gleann</span>
        </div>
        <div className="flex gap-2">
          <Link to="/" className={getNavClass('/')}>
            <MessageSquare className="w-4 h-4" /> Ask/Chat
          </Link>
          <Link to="/memory" className={getNavClass('/memory')}>
            <Brain className="w-4 h-4" /> Memory
          </Link>
          <Link to="/knowledge" className={getNavClass('/knowledge')}>
            <GitMerge className="w-4 h-4" /> Knowledge Graph
          </Link>
          <Link to="/indexes" className={getNavClass('/indexes')}>
            <Database className="w-4 h-4" /> Indexes
          </Link>
          <Link to="/graph" className={getNavClass('/graph')}>
            <GitMerge className="w-4 h-4" /> AST Graph
          </Link>
          <Link to="/system" className={getNavClass('/system')}>
            <Settings className="w-4 h-4" /> System
          </Link>
        </div>
      </nav>

      <div className="flex-1 overflow-y-auto relative z-10">
        <Routes>
          <Route path="/" element={<Chat />} />
          <Route path="/memory" element={<Memory />} />
          <Route path="/knowledge" element={<KnowledgeGraph />} />
          <Route path="/indexes" element={<Indexes />} />
          <Route path="/graph" element={<Graph />} />
          <Route path="/system" element={<System />} />
        </Routes>
      </div>
    </div>
  );
}

export default App;
