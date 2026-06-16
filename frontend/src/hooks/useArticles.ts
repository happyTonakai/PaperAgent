import { useCallback, useEffect, useRef, useState } from 'react'
import type { RecommendArticle, RecommendStats } from '../types'

const BASE = '/api/recommend'
const PAGE_SIZE = 100

// ── Fetch helpers ──

async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path)
  if (!res.ok) {
    let msg = `GET ${path}: ${res.status}`
    try {
      const errBody = await res.json() as { error?: string }
      if (errBody.error) msg += ` - ${errBody.error}`
    } catch { /* ignore parse errors */ }
    throw new Error(msg)
  }
  return res.json()
}

async function apiPost<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    let msg = `POST ${path}: ${res.status}`
    try {
      const errBody = await res.json() as { error?: string }
      if (errBody.error) msg += ` - ${errBody.error}`
    } catch { /* ignore parse errors */ }
    throw new Error(msg)
  }
  return res.json()
}

async function apiPut<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    let msg = `PUT ${path}: ${res.status}`
    try {
      const errBody = await res.json() as { error?: string }
      if (errBody.error) msg += ` - ${errBody.error}`
    } catch { /* ignore parse errors */ }
    throw new Error(msg)
  }
  return res.json()
}

// ── Hooks ──

export function useArticles(status?: number | null) {
  const [articles, setArticles] = useState<RecommendArticle[]>([])
  const [loading, setLoading] = useState(false)
  const [hasMore, setHasMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const offsetRef = useRef(0)
  // Monotonic request id; out-of-order responses (from rapid tab switches)
  // are discarded so they don't clobber the latest view.
  const requestIdRef = useRef(0)

  const fetchPage = useCallback(async (reset: boolean) => {
    const myId = ++requestIdRef.current
    setLoading(true)
    setError(null)
    try {
      const currentOffset = reset ? 0 : offsetRef.current
      const params = new URLSearchParams()
      if (status != null) params.set('status', String(status))
      params.set('limit', String(PAGE_SIZE))
      params.set('offset', String(currentOffset))
      const data = await apiGet<RecommendArticle[]>('/articles?' + params.toString())
      if (myId !== requestIdRef.current) return // stale
      if (reset) {
        setArticles(data)
        offsetRef.current = data.length
      } else {
        setArticles(prev => [...prev, ...data])
        offsetRef.current += data.length
      }
      setHasMore(data.length === PAGE_SIZE)
    } catch (e) {
      if (myId !== requestIdRef.current) return // stale
      setError(String(e))
    } finally {
      if (myId === requestIdRef.current) setLoading(false)
    }
  }, [status])

  useEffect(() => {
    fetchPage(true)
  }, [status, fetchPage])

  const loadMore = useCallback(() => fetchPage(false), [fetchPage])
  const refetch = useCallback(() => fetchPage(true), [fetchPage])

  return { articles, loading, hasMore, error, refetch, loadMore }
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

// Polls /api/recommend/scheduler-status on a timer. Used by the RecommendTab
// status indicator ("上次推荐 HH:MM"); the same endpoint is also shown in
// the settings dialog. Default cadence is 60s — frequent enough to feel
// live, light enough that the UI doesn't churn.
export function useSchedulerStatus(refreshIntervalMs = 60000) {
  const [status, setStatus] = useState<SchedulerStatus | null>(null)

  const fetch = useCallback(async () => {
    try {
      const data = await apiGet<SchedulerStatus>('/scheduler-status')
      setStatus(data)
    } catch {
      // silently fail
    }
  }, [])

  useEffect(() => {
    fetch()
    if (refreshIntervalMs > 0) {
      const t = setInterval(fetch, refreshIntervalMs)
      return () => clearInterval(t)
    }
  }, [fetch, refreshIntervalMs])

  return { status, refetch: fetch }
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

// batchUpdateArticleStatus marks many articles at once. Empty ids is a no-op.
export async function batchUpdateArticleStatus(ids: string[], status: number): Promise<void> {
  if (ids.length === 0) return
  await apiPut('/articles/status', { ids, status })
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
