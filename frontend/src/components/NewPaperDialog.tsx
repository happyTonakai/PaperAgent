import { useState, useRef, useEffect } from 'react'
import { X, Link, Loader2 } from 'lucide-react'
import { useAppStore } from '../stores/appStore'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

export function NewPaperDialog() {
  const {
    isNewPaperOpen, setNewPaperOpen,
    setCurrentPaperId,
    setPendingPaperId, appendPendingSummary, setPendingError, clearPending,
  } = useAppStore()
  const qc = useQueryClient()
  const [url, setUrl] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const [visible, setVisible] = useState(false)
  const [closing, setClosing] = useState(false)

  // Animate in/out
  useEffect(() => {
    if (isNewPaperOpen) {
      setVisible(true)
      setClosing(false)
    } else if (visible && !closing) {
      setClosing(true)
    }
  }, [isNewPaperOpen, visible, closing])

  // Delayed unmount after close animation plays
  useEffect(() => {
    if (!closing) return
    const timer = setTimeout(() => setVisible(false), 200)
    return () => clearTimeout(timer)
  }, [closing])

  const close = () => {
    if (loading && abortRef.current) abortRef.current.abort()
    setNewPaperOpen(false)
  }

  // Close on Escape key
  useEffect(() => {
    if (!isNewPaperOpen) return
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') close() }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [isNewPaperOpen, setNewPaperOpen])

  if (!visible) return null

  const handleSubmit = async () => {
    const trimmed = url.trim()
    if (!trimmed || loading) return

    setLoading(true)
    setError(null)

    const controller = new AbortController()
    abortRef.current = controller
    const timeoutId = setTimeout(() => controller.abort(), 60000)

    try {
      const res = await fetch('/api/papers', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: trimmed }),
        signal: controller.signal,
      })

      if (!res.ok) {
        const errData = await res.json().catch(() => ({}))
        throw new Error((errData as { error?: string }).error || `HTTP ${res.status}`)
      }

      const contentType = res.headers.get('content-type') || ''

      if (contentType.includes('text/event-stream')) {
        const reader = res.body?.getReader()
        if (!reader) throw new Error('No response body')

        const decoder = new TextDecoder()
        let buffer = ''
        let paperId = ''

        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''

          for (const line of lines) {
            const trimmedLine = line.trim()
            if (!trimmedLine.startsWith('data: ')) continue

            const jsonStr = trimmedLine.slice(6)
            try {
              const evt = JSON.parse(jsonStr)

              switch (evt.type) {
                case 'created':
                  if (evt.paper_id) {
                    paperId = evt.paper_id
                    setPendingPaperId(paperId)
                    setCurrentPaperId(paperId)
                    setNewPaperOpen(false)
                    setUrl('')
                    qc.invalidateQueries({ queryKey: ['papers'] })
                  }
                  break
                case 'chunk':
                  if (evt.content) appendPendingSummary(evt.content)
                  break
                case 'title':
                  if (paperId) {
                    qc.invalidateQueries({ queryKey: ['papers'] })
                    qc.invalidateQueries({ queryKey: ['paper', paperId] })
                  }
                  break
                case 'done':
                  clearPending()
                  break
                case 'error':
                  setPendingError(evt.error || 'Unknown error')
                  break
              }
            } catch { /* skip */ }
          }
        }
      } else {
        const data = await res.json()
        if (data.id) {
          qc.invalidateQueries({ queryKey: ['papers'] })
          setCurrentPaperId(data.id)
          setNewPaperOpen(false)
          setUrl('')
          toast.success('论文已加载')
        }
      }
    } catch (err: unknown) {
      if (err instanceof Error && err.name === 'AbortError') return
      const msg = err instanceof Error ? err.message : '加载失败'
      setError(msg)
      toast.error(msg)
    } finally {
      clearTimeout(timeoutId)
      setLoading(false)
      abortRef.current = null
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !loading) handleSubmit()
  }

  const btnBase = "px-4 py-2 text-sm rounded-lg transition-all duration-200 font-medium"
  const btnPrimary = `${btnBase} text-white hover:scale-[1.02] active:scale-[0.98] disabled:opacity-40 disabled:hover:scale-100`

  return (
    <div
      className={`fixed inset-0 z-50 flex items-center justify-center ${closing ? 'animate-fade-out' : 'animate-fade-in'}`}
      style={{ backgroundColor: 'rgba(0,0,0,0.35)', backdropFilter: 'blur(2px)' }}
      onClick={(e) => { if (e.target === e.currentTarget) close() }}
    >
      <div
        className={`rounded-2xl shadow-lg w-full max-w-md mx-4 overflow-hidden ${closing ? 'animate-scale-out' : 'animate-scale-in'}`}
        style={{
          backgroundColor: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          boxShadow: 'var(--shadow-lg)',
        }}
      >
        {/* Header */}
        <div
          className="flex items-center justify-between px-5 py-3.5"
          style={{ borderBottom: '1px solid var(--color-border-light)' }}
        >
          <h2
            className="text-sm font-semibold flex items-center gap-2"
            style={{ fontFamily: 'var(--font-display)', color: 'var(--color-text)' }}
          >
            <Link size={15} style={{ color: 'var(--color-accent)' }} />
            新建论文
          </h2>
          <button
            onClick={() => close()}
            className="p-1.5 rounded-md hover:bg-[var(--color-bg-elevated)] transition-colors duration-150"
            style={{ color: 'var(--color-text-muted)' }}
          >
            <X size={15} />
          </button>
        </div>

        {/* Body */}
        <div className="p-5">
          <label
            className="text-xs block mb-2"
            style={{ color: 'var(--color-text-muted)', fontFamily: 'var(--font-ui)' }}
          >
            输入论文 URL（支持 arXiv 链接）
          </label>
          <input
            type="text"
            value={url}
            onChange={(e) => { setUrl(e.target.value); setError(null) }}
            onKeyDown={handleKeyDown}
            placeholder="https://arxiv.org/abs/..."
            aria-label="论文 URL"
            autoFocus
            disabled={loading}
            className="w-full px-4 py-2.5 rounded-xl text-sm outline-none transition-all duration-200"
            style={{
              fontFamily: 'var(--font-ui)',
              backgroundColor: 'var(--color-bg-inset)',
              color: 'var(--color-text)',
              border: '1px solid transparent',
              boxShadow: 'var(--shadow-sm)',
            }}
            onFocus={(e) => {
              e.currentTarget.style.borderColor = 'var(--color-accent-border)'
            }}
            onBlur={(e) => {
              e.currentTarget.style.borderColor = 'transparent'
            }}
          />
          {error && (
            <p
              className="mt-2 text-sm"
              style={{ color: 'var(--color-danger)', fontFamily: 'var(--font-ui)' }}
            >
              {error}
            </p>
          )}
          <div className="mt-5 flex justify-end gap-2.5">
            <button
              onClick={() => close()}
              className={btnBase}
              style={{
                fontFamily: 'var(--font-ui)',
                color: 'var(--color-text-secondary)',
                backgroundColor: 'var(--color-bg-elevated)',
              }}
            >
              取消
            </button>
            <button
              onClick={handleSubmit}
              disabled={loading || !url.trim()}
              className={btnPrimary}
              style={{ fontFamily: 'var(--font-ui)', backgroundColor: 'var(--color-accent)' }}
            >
              {loading && <Loader2 size={14} className="animate-spin inline mr-1.5" />}
              {loading ? '加载中...' : '加载'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
