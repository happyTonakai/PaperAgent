import { useCallback, useEffect, useState } from 'react'
import type { RecommendArticle, RecommendStats } from '../types'

const BASE = '/api/recommend'

// ── Fetch helpers ──

async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path)
  if (!res.ok) throw new Error(`GET ${path}: ${res.status}`)
  return res.json()
}

async function apiPost<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) throw new Error(`POST ${path}: ${res.status}`)
  return res.json()
}

async function apiPut<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`PUT ${path}: ${res.status}`)
  return res.json()
}

// ── Hooks ──

export function useArticles(status?: number | null) {
  const [articles, setArticles] = useState<RecommendArticle[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const fetch = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const params = new URLSearchParams()
      if (status != null) params.set('status', String(status))
      params.set('limit', '100')
      const data = await apiGet<RecommendArticle[]>('/articles?' + params.toString())
      setArticles(data)
    } catch (e) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }, [status])

  useEffect(() => { fetch() }, [fetch])

  return { articles, loading, error, refetch: fetch }
}

export function useTodayRecommendations(date?: string) {
  const [articles, setArticles] = useState<RecommendArticle[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const fetch = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const params = date ? `?date=${date}` : ''
      const data = await apiGet<RecommendArticle[]>('/today' + params)
      setArticles(data)
    } catch (e) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }, [date])

  useEffect(() => { fetch() }, [fetch])

  return { articles, loading, error, refetch: fetch }
}

export function useStats() {
  const [stats, setStats] = useState<RecommendStats | null>(null)

  const fetch = useCallback(async () => {
    try {
      const data = await apiGet<RecommendStats>('/stats')
      setStats(data)
    } catch {
      // silently fail
    }
  }, [])

  useEffect(() => { fetch() }, [fetch])

  return { stats, refetch: fetch }
}

// ── Actions ──

export async function fetchNewArticles(): Promise<number> {
  const data = await apiPost<{ fetched: number }>('/fetch')
  return data.fetched
}

export async function generateRecommendations(): Promise<number> {
  const data = await apiPost<{ recommended: number }>('/generate')
  return data.recommended
}

export async function updateArticleStatus(id: string, status: number): Promise<void> {
  await apiPut(`/articles/${encodeURIComponent(id)}/status`, { status })
}

export async function updateArticleComment(id: string, comment: string): Promise<void> {
  await apiPut(`/articles/${encodeURIComponent(id)}/comment`, { comment })
}

export async function getRecommendationDates(): Promise<string[]> {
  return apiGet<string[]>('/dates')
}

export async function getArticlesByDate(date: string): Promise<RecommendArticle[]> {
  return apiGet<RecommendArticle[]>(`/dates/${encodeURIComponent(date)}`)
}

export async function getPreferences(): Promise<string> {
  const data = await apiGet<{ content: string }>('/preferences')
  return data.content
}

export async function savePreferences(content: string): Promise<void> {
  await apiPut('/preferences', { content })
}

export async function getRecommendConfig(): Promise<{
  recommend: { daily_papers: number; scoring_batch_size: number; scheduled_time: string; push_to_feishu: boolean; diversity_ratio: number }
  arxiv_categories: string[]
  api: { scoring: { base_url: string; api_key: string; model: string } | null; translation: { base_url: string; api_key: string; model: string } | null }
}> {
  return apiGet('/config')
}

export async function updateRecommendConfig(config: Record<string, unknown>): Promise<void> {
  await apiPut('/config', config)
}

export interface SchedulerStatus {
  is_running: boolean
  last_run: string
  last_error: string
  next_run: string
  scheduled: string
  daily_count: number
  push_to_feishu: boolean
}

export async function triggerFullPipeline(): Promise<{ status: string }> {
  return apiPost<{ status: string }>('/trigger')
}

export async function pushToFeishu(): Promise<{ status: string; count?: number; message?: string }> {
  return apiPost<{ status: string; count?: number; message?: string }>('/push-to-feishu')
}

export async function getSchedulerStatus(): Promise<SchedulerStatus> {
  return apiGet<SchedulerStatus>('/scheduler-status')
}
