import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import type { Paper, PaperSummary } from '../types'

const BASE = '/api'

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'Request failed' }))
    throw new Error((err as { error?: string }).error || `HTTP ${res.status}`)
  }
  return res.json()
}

export function usePaperList() {
  return useQuery({
    queryKey: ['papers'],
    queryFn: () => fetchJSON<PaperSummary[]>(`${BASE}/papers`),
  })
}

export function usePaper(id: string | null) {
  return useQuery({
    queryKey: ['paper', id],
    queryFn: () => fetchJSON<Paper>(`${BASE}/papers/${id}`),
    enabled: !!id,
  })
}

export function useDeletePaper() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      fetchJSON(`${BASE}/papers/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['papers'] })
    },
  })
}

export function useUpdateRating() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, rating }: { id: string; rating: number }) =>
      fetchJSON<{ status: string; rating: string }>(`${BASE}/papers/${id}/rating`, {
        method: 'PATCH',
        body: JSON.stringify({ rating }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['papers'] })
    },
  })
}

export function useExportPaper() {
  return useMutation({
    mutationFn: (id: string) =>
      fetchJSON<{ status: string; path: string }>(`${BASE}/papers/${id}/export`, {
        method: 'POST',
      }),
  })
}

export function useTogglePin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      fetchJSON<{ status: string; pinned: boolean }>(`${BASE}/papers/${id}/pin`, {
        method: 'PATCH',
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['papers'] })
    },
  })
}

export function useUpdateTitle() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, title }: { id: string; title: string }) =>
      fetchJSON<{ status: string; title: string }>(`${BASE}/papers/${id}/title`, {
        method: 'PATCH',
        body: JSON.stringify({ title }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['papers'] })
    },
  })
}

export function useSummarizeExport() {
  return useMutation({
    mutationFn: (id: string) =>
      fetchJSON<{ status: string; path: string }>(`${BASE}/papers/${id}/summarize-export`, {
        method: 'POST',
      }),
  })
}
