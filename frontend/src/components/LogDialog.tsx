import { useState, useEffect, useRef } from 'react'
import { X, Terminal, RefreshCw } from 'lucide-react'
import { useAppStore } from '../stores/appStore'

interface LogEntry {
  time: string
  message: string
}

interface LogsResponse {
  logs: LogEntry[]
}

export function LogDialog() {
  const { isLogOpen, setLogOpen } = useAppStore()
  const [visible, setVisible] = useState(false)
  const [closing, setClosing] = useState(false)
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [loading, setLoading] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)

  // Animate in/out
  useEffect(() => {
    if (isLogOpen) {
      setVisible(true)
      setClosing(false)
    } else if (visible && !closing) {
      setClosing(true)
    }
  }, [isLogOpen, visible, closing])

  // Delayed unmount after close animation
  useEffect(() => {
    if (!closing) return
    const timer = setTimeout(() => setVisible(false), 200)
    return () => clearTimeout(timer)
  }, [closing])

  const close = () => setLogOpen(false)

  // Close on Escape key
  useEffect(() => {
    if (!isLogOpen) return
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') close() }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [isLogOpen, setLogOpen])

  const fetchLogs = async () => {
    setLoading(true)
    try {
      const res = await fetch('/api/logs?limit=200')
      if (res.ok) {
        const data: LogsResponse = await res.json()
        setLogs(data.logs || [])
      }
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (isLogOpen) {
      fetchLogs()
      const timer = setInterval(fetchLogs, 2000)
      return () => clearInterval(timer)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isLogOpen])

  // Auto-scroll to bottom when new logs arrive
  useEffect(() => {
    if (visible && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [logs, visible])

  if (!visible) return null

  const btnBase =
    'px-3 py-1.5 text-xs rounded-lg transition-all duration-200 font-medium hover:scale-[1.02] active:scale-[0.98]'

  return (
    <div
      className={`fixed inset-0 z-50 flex items-center justify-center ${closing ? 'animate-fade-out' : 'animate-fade-in'}`}
      style={{ backgroundColor: 'rgba(0,0,0,0.35)', backdropFilter: 'blur(2px)' }}
      onClick={(e) => { if (e.target === e.currentTarget) close() }}
    >
      <div
        className={`rounded-2xl shadow-lg w-full max-w-3xl mx-4 overflow-hidden flex flex-col ${closing ? 'animate-scale-out' : 'animate-scale-in'}`}
        style={{
          backgroundColor: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          boxShadow: 'var(--shadow-lg)',
          maxHeight: '70vh',
        }}
      >
        {/* Header */}
        <div
          className="flex items-center justify-between px-5 py-3 flex-shrink-0"
          style={{ borderBottom: '1px solid var(--color-border-light)' }}
        >
          <h2
            className="text-sm font-semibold flex items-center gap-2"
            style={{ fontFamily: 'var(--font-display)', color: 'var(--color-text)' }}
          >
            <Terminal size={15} style={{ color: 'var(--color-accent)' }} />
            服务器日志
          </h2>
          <div className="flex items-center gap-1.5">
            <button
              onClick={fetchLogs}
              disabled={loading}
              className={btnBase}
              style={{
                fontFamily: 'var(--font-ui)',
                color: 'var(--color-accent)',
                backgroundColor: 'var(--color-accent-subtle)',
              }}
            >
              <RefreshCw
                size={12}
                className={loading ? 'animate-spin' : ''}
                style={{ display: 'inline', marginRight: 4 }}
              />
              刷新
            </button>
            <button
              onClick={close}
              className="p-1.5 rounded-md hover:bg-[var(--color-bg-elevated)] transition-colors duration-150"
              style={{ color: 'var(--color-text-muted)' }}
            >
              <X size={15} />
            </button>
          </div>
        </div>

        {/* Logs */}
        <div
          ref={scrollRef}
          className="flex-1 overflow-y-auto p-4 font-mono text-xs leading-relaxed custom-scrollbar"
          style={{
            backgroundColor: '#1a1a2e',
            color: '#e0e0e0',
            minHeight: 300,
            fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace",
          }}
        >
          {logs.length === 0 && !loading && (
            <div className="flex items-center justify-center h-full" style={{ color: '#888' }}>
              <span>暂无日志</span>
            </div>
          )}
          {logs.length === 0 && loading && (
            <div className="flex items-center justify-center h-full" style={{ color: '#888' }}>
              <span>加载中...</span>
            </div>
          )}
          {logs.map((entry, i) => (
            <div key={i} className="flex gap-3 py-[1px] hover:bg-white/5">
              <span style={{ color: '#6b7280', flexShrink: 0, userSelect: 'none' }}>
                {entry.time}
              </span>
              <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                {entry.message}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
