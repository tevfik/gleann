import { Routes, Route, Link, useLocation } from 'react-router-dom';
import { Database, Brain, GitMerge, Sparkles, MessageSquare, Settings } from 'lucide-react';

import { Chat } from './components/Chat';
import { Memory } from './components/Memory';
import { KnowledgeGraph } from './components/KnowledgeGraph';
import { Indexes } from './components/Indexes';
import { Graph } from './components/Graph';
import { System } from './components/System';

function App() {
  const location = useLocation();
  const isChat = location.pathname === '/';

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

      <div className={`flex-1 relative z-10 ${isChat ? 'overflow-hidden' : 'overflow-y-auto'}`}>
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
