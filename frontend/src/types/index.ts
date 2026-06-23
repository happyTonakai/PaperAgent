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
  arxiv_id?: string
  // Primary GitHub repo URL extracted from the paper's abstract (when present).
  // Empty/undefined when the abstract does not mention a GitHub repo — the WebUI
  // hides the GitHub icon button in that case.
  github_url?: string
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
  pinned?: boolean
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

// ---- Recommend System Types ----

export type ArticleStatus = 0 | 1 | 2 | -1 | 3

export interface RecommendArticle {
  id: string
  title: string
  link: string
  abstract: string | null
  status: number
  score: number
  author: string | null
  category: string | null
  hf_upvotes: number | null
  ax_net_votes: number | null
  votes_updated_at: string | null
  comment: string | null
  recommend_date: string | null
  batch_order: number | null
  created_at: string
  // Translated fields (present when translation API is configured)
  translated_title?: string
  translated_abstract?: string
}

export interface RecommendStats {
  unread: number
  clicked: number
  liked: number
  disliked: number
  read: number
  total: number
}

export interface RecommendConfig {
  recommend: {
    daily_papers: number
    scoring_batch_size: number
    auto_refresh: boolean
  }
  arxiv_categories: string[]
  api: {
    scoring: { base_url: string; api_key: string; model: string } | null
  }
}
