// ---- API Response Types ----

export interface Message {
  round_number: number
  role: 'user' | 'assistant'
  content: string
  token_count: number
  prompt_tokens?: number
  completion_tokens?: number
  cached_tokens?: number
  skip_context?: boolean
}

export interface Paper {
  id: string
  title: string
  source_url: string
  initial_summary: string
  model_used: string
  total_tokens_used?: number
  total_prompt_tokens?: number
  total_completion_tokens?: number
  total_cached_tokens?: number
  rating?: number
  created_at: string
  updated_at: string
  messages: Message[]
}

export interface PaperSummary {
  id: string
  title: string
  rating?: number
  updated_at: string
}

// ---- SSE Event Types ----

export interface SSEEvent {
  type: 'chunk' | 'done' | 'error' | 'title' | 'created'
  content?: string
  error?: string
  paper_id?: string
  title?: string
  round_id?: number
  prompt_tokens?: number
  completion_tokens?: number
  cached_tokens?: number
}

// ---- UI State Types ----

export type Theme = 'light' | 'dark' | 'system'

export type FontSize = 'small' | 'medium' | 'large'

export type FontFamily = 'serif' | 'sans'
