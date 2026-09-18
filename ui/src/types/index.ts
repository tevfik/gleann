export interface Source {
  text: string;
  metadata?: any;
  score?: number;
}

export interface Message {
  role: 'user' | 'assistant';
  content: string;
  sources?: Source[];
  status?: string;
  images?: string[];
}

export interface ConversationSummary {
  id: string;
  short_id: string;
  title: string;
  model: string;
  indexes: string;
  message_count: number;
  created_at: string;
  updated_at: string;
}

export interface MemoryBlock {
  id: string;
  content: string;
  tier: 'short' | 'medium' | 'long';
  scope?: string;
  created_at: string;
  expires_at?: string;
}

export interface IndexInfo {
  name: string;
  backend?: string;
  dimension?: number;
  count?: number;
  num_passages?: number;
  docs_dir?: string;
  source_dir?: string;
  watching?: boolean;
  auto_watch?: boolean;
  embedding_model?: string;
  tags?: string[];
  description?: string;
  mcp_exposed?: boolean;
}

