import { useEffect, useRef, useCallback } from 'react'
import { Toaster, toast } from 'sonner'
import { Settings, Sun, Moon, Monitor, Maximize2, Minimize2 } from 'lucide-react'
import { PaperList } from './components/PaperList'
import { ChatView } from './components/ChatView'
import { InputBox } from './components/InputBox'
import { ErrorBoundary } from './components/ErrorBoundary'
import { NewPaperDialog } from './components/NewPaperDialog'
import { SettingsDialog } from './components/SettingsDialog'
import { LogDialog } from './components/LogDialog'
import { RecommendTab } from './components/RecommendTab'
import { FontFamilyButton } from './components/FontFamilyButton'
import { FontSizeButton } from './components/FontSizeButton'
import { useConnection } from './hooks/useConnection'
import { useAppStore, applyTheme } from './stores/appStore'
import { useQueryClient } from '@tanstack/react-query'
import type { Theme } from './types'

// Helper: sync the active paper ID to the server.
export async function setActivePaperOnServer(id: string | null) {
  await fetch('/api/active-paper', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: id ?? '' }),
  }).catch(() => {})
}

export default function App() {
  useConnection()
  const qc = useQueryClient()
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const urlToOpen = params.get('url')
    const paperIdToOpen = params.get('open')

    if (urlToOpen) {
      window.history.replaceState({}, '', '/')
      createPaperFromUrl(urlToOpen)
    } else if (paperIdToOpen) {
      window.history.replaceState({}, '', '/')
      useAppStore.getState().setCurrentPaperId(paperIdToOpen)
      setActivePaperOnServer(paperIdToOpen)
    } else {
      // No URL or paper param — restore the persisted active paper
      fetch('/api/active-paper')
        .then((res) => res.json())
        .then((data: { id: string | null }) => {
          if (data.id) {
            useAppStore.getState().setCurrentPaperId(data.id)
          }
        })
        .catch(() => {})
    }

    async function createPaperFromUrl(url: string) {
      const controller = new AbortController()
      abortRef.current = controller
      const timeoutId = setTimeout(() => controller.abort(), 60000)

      const {
        setCurrentPaperId,
        setPendingPaperId,
        appendPendingSummary,
        setPendingError,
        clearPending,
      } = useAppStore.getState()

      try {
        toast.loading('正在加载论文...')

        const res = await fetch('/api/papers', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ url }),
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
                      toast.dismiss()
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
                    toast.error(evt.error || '摘要生成失败')
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
            toast.dismiss()
            toast.success('论文已加载')
          }
        }
      } catch (err: unknown) {
        if (err instanceof Error && err.name === 'AbortError') return
        const msg = err instanceof Error ? err.message : '加载失败'
        toast.error(msg)
      } finally {
        clearTimeout(timeoutId)
      }
    }

    return () => {
      abortRef.current?.abort()
    }
  }, [])

  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const handler = () => {
      const current = useAppStore.getState().theme
      if (current === 'system') applyTheme('system')
    }
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [])

  // First-run detection: if config.yaml doesn't exist on disk, auto-open
  // the settings dialog so the user is guided through API key setup
  // instead of staring at a blank UI.
  useEffect(() => {
    let cancelled = false
    fetch('/api/config/status')
      .then((r) => (r.ok ? r.json() : null))
      .then((data: { config_exists?: boolean; api_key_configured?: boolean } | null) => {
        if (cancelled || !data) return
        if (data.config_exists === false) {
          toast.info('首次启动，请先在「API 配置」中填写 API 密钥', { duration: 6000 })
          useAppStore.getState().setSettingsOpen(true)
        }
      })
      .catch(() => {
        // Network/server errors are non-fatal; ignore silently.
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Toggle new paper dialog with Cmd+K / Ctrl+K
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        const { isNewPaperOpen, setNewPaperOpen, isStreaming } = useAppStore.getState()
        if (!isStreaming) setNewPaperOpen(!isNewPaperOpen)
      }
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [])

  // Listen for recommend-chat custom event (from RecommendTab)
  // Creates a paper inline instead of full page navigation
  useEffect(() => {
    const handler = async (e: Event) => {
      const detail = (e as CustomEvent).detail as { arxivId: string; title: string }
      if (!detail?.arxivId) return

      useAppStore.getState().setActiveTab('chat')

      const url = `https://arxiv.org/abs/${detail.arxivId}`
      try {
        toast.loading('正在加载论文...')
        const res = await fetch('/api/papers', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ url }),
        })

        if (!res.ok) {
          const errData = (await res.json().catch(() => ({}))) as { error?: string }
          throw new Error(errData.error || `HTTP ${res.status}`)
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

              try {
                const evt = JSON.parse(trimmedLine.slice(6))
                switch (evt.type) {
                  case 'created':
                    if (evt.paper_id) {
                      paperId = evt.paper_id
                      useAppStore.getState().setPendingPaperId(paperId)
                      useAppStore.getState().setCurrentPaperId(paperId)
                      toast.dismiss()
                      qc.invalidateQueries({ queryKey: ['papers'] })
                    }
                    break
                  case 'chunk':
                    if (evt.content) useAppStore.getState().appendPendingSummary(evt.content)
                    break
                  case 'done':
                    useAppStore.getState().clearPending()
                    break
                  case 'error':
                    toast.error(evt.error || '摘要生成失败')
                    break
                }
              } catch { /* skip */ }
            }
          }
        } else {
          toast.dismiss()
          toast.success('论文已加载')
        }
      } catch (err: unknown) {
        toast.error(err instanceof Error ? err.message : '加载失败')
      }
    }
    window.addEventListener('recommend-chat', handler)
    return () => window.removeEventListener('recommend-chat', handler)
  }, [qc])

  const activeTab = useAppStore((s) => s.activeTab)
  const { theme, setTheme, setSettingsOpen, contentWidth, toggleContentWidth } = useAppStore()
  const controlBtnClass = "p-1.5 rounded-md transition-all duration-200 hover:scale-105 active:scale-95"

  return (
    <div
      className="h-screen flex flex-col"
      style={{ backgroundColor: 'var(--color-bg)', color: 'var(--color-text)' }}
    >
      {/* Tab bar + global controls */}
      <div
        className="flex items-center justify-between px-4 pt-1.5 pb-0 border-b border-[var(--color-border)]"
        style={{ backgroundColor: 'var(--color-bg-elevated)' }}
      >
        <div className="flex items-center gap-0">
          <button
            onClick={() => useAppStore.getState().setActiveTab('chat')}
            className={`px-4 py-2 text-sm font-medium rounded-t-lg transition-colors ${activeTab === 'chat' ? 'bg-[var(--color-surface)] border border-[var(--color-border)] border-b-0 text-[var(--color-text)]' : 'text-[var(--color-text-secondary)] hover:text-[var(--color-text)]'}`}
          >
            💬 论文对话
          </button>
          <button
            onClick={() => useAppStore.getState().setActiveTab('recommend')}
            className={`px-4 py-2 text-sm font-medium rounded-t-lg transition-colors ${activeTab === 'recommend' ? 'bg-[var(--color-surface)] border border-[var(--color-border)] border-b-0 text-[var(--color-text)]' : 'text-[var(--color-text-secondary)] hover:text-[var(--color-text)]'}`}
          >
            📅 每日推荐
          </button>
        </div>

        {/* Global controls (shared by both tabs) */}
        <div
          className="flex items-center gap-0.5 pr-1"
          style={{ fontFamily: 'var(--font-ui)' }}
        >
          <button
            onClick={toggleContentWidth}
            className={controlBtnClass}
            style={{ color: 'var(--color-text-muted)' }}
            title={contentWidth === 'full' ? '窄屏阅读' : '宽屏阅读'}
            aria-label={contentWidth === 'full' ? '切换到窄屏' : '切换到宽屏'}
          >
            {contentWidth === 'full' ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
          </button>
          <button
            onClick={() => {
              const cycle: Theme[] = ['light', 'dark', 'system']
              const idx = cycle.indexOf(theme)
              setTheme(cycle[(idx + 1) % cycle.length])
            }}
            className={controlBtnClass}
            style={{ color: 'var(--color-text-muted)' }}
            title={`主题: ${theme === 'light' ? '浅色' : theme === 'dark' ? '深色' : '跟随系统'}`}
            aria-label="切换主题"
          >
            {theme === 'light' ? <Sun size={15} /> : theme === 'dark' ? <Moon size={15} /> : <Monitor size={15} />}
          </button>
          <FontFamilyButton />
          <FontSizeButton />
          <button
            onClick={() => setSettingsOpen(true)}
            className={controlBtnClass}
            style={{ color: 'var(--color-text-muted)' }}
            title="设置"
            aria-label="设置"
          >
            <Settings size={15} />
          </button>
        </div>
      </div>

      {activeTab === 'chat' ? (
        <div className="flex-1 flex min-h-0">
          <PaperList />

          <div className="flex-1 flex flex-col min-w-0">
            <ErrorBoundary>
              <ChatView />
              <InputBox />
            </ErrorBoundary>
          </div>
        </div>
      ) : (
        <div className="flex-1 flex flex-col min-h-0 overflow-hidden">
          <RecommendTab />
        </div>
      )}

      <NewPaperDialog />
      <SettingsDialog />
      <LogDialog />

      <Toaster
        position="top-right"
        toastOptions={{
          style: {
            fontSize: '0.875rem',
            borderRadius: '0.5rem',
            fontFamily: 'var(--font-ui)',
          },
        }}
      />
    </div>
  )
}
