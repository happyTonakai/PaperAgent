import { useEffect, useRef } from 'react'
import { Toaster, toast } from 'sonner'
import { PaperList } from './components/PaperList'
import { ChatView } from './components/ChatView'
import { InputBox } from './components/InputBox'
import { ErrorBoundary } from './components/ErrorBoundary'
import { NewPaperDialog } from './components/NewPaperDialog'
import { SettingsDialog } from './components/SettingsDialog'
import { LogDialog } from './components/LogDialog'
import { useConnection } from './hooks/useConnection'
import { useAppStore, applyTheme } from './stores/appStore'
import { useQueryClient } from '@tanstack/react-query'

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

  return (
    <div
      className="h-screen flex flex-col"
      style={{ backgroundColor: 'var(--color-bg)', color: 'var(--color-text)' }}
    >
      <div className="flex-1 flex min-h-0">
        <PaperList />

        <div className="flex-1 flex flex-col min-w-0">
          <ErrorBoundary>
            <ChatView />
            <InputBox />
          </ErrorBoundary>
        </div>
      </div>

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
