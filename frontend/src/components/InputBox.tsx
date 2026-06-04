import { useState, useRef, useEffect, useCallback } from 'react'
import { Send, Command } from 'lucide-react'
import { useAppStore } from '../stores/appStore'
import { useExportPaper } from '../hooks/usePapers'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

interface CmdEntry {
  name: string
  description: string
  action: () => void
}

// arXiv URL/ID patterns for client-side detection.
const ARXIV_URL_RE = /^(https?:\/\/)?(www\.)?arxiv\.org\/(abs|pdf|html)\/\S+$/i
const ARXIV_ID_RE = /^\d{4}\.\d{4,5}(v\d+)?$/
const ARXIV_OLD_ID_RE = /^[a-z-]+(\.[a-z]{2})?\/\d{7}(v\d+)?$/i

function isArxivInput(s: string): boolean {
  const trimmed = s.trim()
  if (!trimmed) return false
  return ARXIV_URL_RE.test(trimmed) || ARXIV_ID_RE.test(trimmed) || ARXIV_OLD_ID_RE.test(trimmed)
}

export function InputBox() {
  const { currentPaperId, isStreaming, setSettingsOpen, sendQuestion, contentWidth } = useAppStore()
  const exportPaper = useExportPaper()
  const qc = useQueryClient()
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const isComposingRef = useRef(false)
  const [showCommands, setShowCommands] = useState(false)
  const [selectedCmdIdx, setSelectedCmdIdx] = useState(0)
  const [creatingPaper, setCreatingPaper] = useState(false)
  // Local state for command autocomplete UI only — textarea itself is uncontrolled via ref
  const [localValue, setLocalValue] = useState('')

  const commands: CmdEntry[] = [
    {
      name: '/export',
      description: '导出到 Obsidian',
      action: async () => {
        if (!currentPaperId) return
        try {
          const result = await exportPaper.mutateAsync(currentPaperId)
          toast.success(`已导出到 ${result.path}`)
        } catch (err) {
          toast.error(err instanceof Error ? err.message : '导出失败')
        }
      },
    },
    {
      name: '/config',
      description: '打开设置',
      action: () => setSettingsOpen(true),
    },
    {
      name: '/help',
      description: '显示帮助',
      action: () => toast('可用命令: /export /config /help /btw', { duration: 5000 }),
    },
    {
      name: '/btw',
      description: '提问但不记入上下文',
      action: () => toast('请使用 /btw <问题> 格式直接在后面输入问题', { duration: 3000 }),
    },
  ]

  const filteredCommands = localValue.startsWith('/')
    ? commands.filter((c) => c.name.startsWith(localValue.trim()))
    : []

  useEffect(() => {
    if (localValue.startsWith('/') && filteredCommands.length > 0) {
      setShowCommands(true)
      setSelectedCmdIdx(0)
    } else {
      setShowCommands(false)
    }
  }, [localValue, filteredCommands.length])

  // Ref to keep latest commands accessible from stable callbacks
  const commandsRef = useRef(commands)
  commandsRef.current = commands

  // Create a new paper from arXiv URL, handling SSE stream and duplicates.
  const createPaperFromArxiv = useCallback(async (url: string) => {
    const {
      setCurrentPaperId,
      setPendingPaperId,
      appendPendingSummary,
      setPendingError,
      clearPending,
    } = useAppStore.getState()

    setCreatingPaper(true)

    try {
      const res = await fetch('/api/papers', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url }),
      })

      if (!res.ok) {
        const errData = await res.json().catch(() => ({}))
        throw new Error((errData as { error?: string }).error || `HTTP ${res.status}`)
      }

      const contentType = res.headers.get('content-type') || ''

      if (contentType.includes('text/event-stream')) {
        // New paper: handle SSE stream
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
            } catch { /* skip parse error */ }
          }
        }
      } else {
        // JSON response: either duplicate (existing: true) or non-arxiv result
        const data = await res.json() as { existing?: boolean; id?: string; title?: string; error?: string }
        if (data.existing && data.id) {
          // Paper already exists — switch to it
          setCurrentPaperId(data.id)
          qc.invalidateQueries({ queryKey: ['papers'] })
          qc.invalidateQueries({ queryKey: ['paper', data.id] })
          toast.info(`论文「${data.title || '已存在'}」已存在，已切换到该论文`)
        } else if (data.id) {
          qc.invalidateQueries({ queryKey: ['papers'] })
          setCurrentPaperId(data.id)
          toast.success('论文已加载')
        } else if (data.error) {
          toast.error(data.error)
        }
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '加载失败'
      toast.error(msg)
    } finally {
      setCreatingPaper(false)
    }
  }, [qc])

  const handleSend = useCallback(() => {
    const el = inputRef.current
    if (!el) return
    const trimmed = el.value.trim()
    if (!trimmed || isStreaming || creatingPaper) return

    // If input is an arXiv URL/ID, auto-create new paper.
    if (isArxivInput(trimmed)) {
      el.value = ''
      setLocalValue('')
      createPaperFromArxiv(trimmed)
      return
    }

    // If no active paper and input is not an arXiv link, show guidance.
    if (!currentPaperId) {
      toast('请先输入 arXiv 链接创建一篇论文', { duration: 3000 })
      return
    }

    if (trimmed.startsWith('/')) {
      // Check for /btw prefix: /btw <question>
      const btwPrefix = '/btw '
      if (trimmed.startsWith(btwPrefix)) {
        const btwQuestion = trimmed.slice(btwPrefix.length).trim()
        if (btwQuestion && sendQuestion) {
          sendQuestion(btwQuestion, { skipContext: true })
          el.value = ''
          setLocalValue('')
          return
        }
      }

      const cmd = commandsRef.current.find((c) => c.name === trimmed)
      if (cmd) {
        cmd.action()
        el.value = ''
        setLocalValue('')
        return
      }
    }

    if (sendQuestion) {
      sendQuestion(trimmed)
      el.value = ''
      setLocalValue('')
    }
  }, [isStreaming, creatingPaper, currentPaperId, sendQuestion, createPaperFromArxiv])

  // Auto-resize function — called from onInput, avoids useEffect layout thrashing
  const autoResize = useCallback((el: HTMLTextAreaElement) => {
    el.style.height = 'auto'
    el.style.height = Math.min(Math.max(el.scrollHeight, 5 * 24), 10 * 24) + 'px'
  }, [])

  const handleInput = useCallback((e: React.FormEvent<HTMLTextAreaElement>) => {
    const el = e.currentTarget
    autoResize(el)
    // Only trigger React re-render when command autocomplete may be active.
    // Normal text input is entirely native — uncontrolled textarea handles it.
    const val = el.value
    if (val.startsWith('/') || localValue.startsWith('/')) {
      setLocalValue(val)
    }
  }, [autoResize, localValue])

  const handleCompositionStart = useCallback(() => {
    isComposingRef.current = true
  }, [])

  const handleCompositionEnd = useCallback(() => {
    // Defer reset so that a subsequent keydown Enter (same event tick)
    // still sees isComposingRef.current === true.
    setTimeout(() => { isComposingRef.current = false }, 0)
  }, [])

  const filteredCommandsRef = useRef(filteredCommands)
  filteredCommandsRef.current = filteredCommands

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (showCommands) {
      const cmds = filteredCommandsRef.current
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSelectedCmdIdx((i) => (i + 1) % cmds.length)
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSelectedCmdIdx((i) => (i - 1 + cmds.length) % cmds.length)
      } else if (e.key === 'Tab' || e.key === 'Enter') {
        e.preventDefault()
        const cmd = cmds[selectedCmdIdx]
        if (cmd) {
          const el = inputRef.current
          if (el) {
            el.value = cmd.name
            setLocalValue(cmd.name)
            autoResize(el)
          }
          setShowCommands(false)
        }
      } else if (e.key === 'Escape') {
        setShowCommands(false)
      }
      return
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      // Don't submit during IME composition (e.g. Chinese Pinyin Enter to commit raw input).
      // isComposingRef is kept true via setTimeout(0) in onCompositionEnd to cover the
      // case where compositionend fires before keydown (Chrome, Firefox).
      if (isComposingRef.current || e.nativeEvent.isComposing) return
      e.preventDefault()
      handleSend()
    }
  }, [showCommands, selectedCmdIdx, handleSend, autoResize])

  return (
    <div
      className="flex-shrink-0 p-4 relative"
      style={{
        backgroundColor: 'var(--color-surface)',
        borderTop: '1px solid var(--color-border)',
      }}
    >
      <div className={contentWidth === 'narrow' && currentPaperId ? 'max-w-[55%] mx-auto' : ''}>
        {/* Command autocomplete */}
        {showCommands && (
          <div
            className="absolute bottom-full left-4 right-4 mb-2 rounded-lg shadow-lg overflow-hidden z-50 animate-scale-in"
            role="listbox"
            aria-label="可用命令"
            aria-expanded={showCommands}
            style={{
              backgroundColor: 'var(--color-surface)',
              border: '1px solid var(--color-border)',
              boxShadow: 'var(--shadow-lg)',
            }}
          >
            {filteredCommands.map((cmd, idx) => (
              <div
                key={cmd.name}
                role="option"
                aria-selected={idx === selectedCmdIdx}
                className="px-3 py-2.5 text-sm cursor-pointer flex items-center gap-2.5 transition-colors duration-100"
                style={{
                  fontFamily: 'var(--font-ui)',
                  backgroundColor: idx === selectedCmdIdx
                    ? 'var(--color-accent-subtle)'
                    : 'transparent',
                  color: idx === selectedCmdIdx
                    ? 'var(--color-accent)'
                    : 'var(--color-text)',
                }}
                onClick={() => {
                  const el = inputRef.current
                  if (el) {
                    el.value = cmd.name
                    setLocalValue(cmd.name)
                    autoResize(el)
                  }
                  setShowCommands(false)
                  inputRef.current?.focus()
                }}
              >
                <Command size={13} style={{ color: 'var(--color-text-muted)' }} />
                <span className="font-medium">{cmd.name}</span>
                <span style={{ color: 'var(--color-text-muted)' }} className="text-xs">
                  {cmd.description}
                </span>
              </div>
            ))}
          </div>
        )}

        {/* Input area */}
        <div className="flex items-end gap-2.5">
          <textarea
            ref={inputRef}
            defaultValue=""
            onInput={handleInput}
            onCompositionStart={handleCompositionStart}
            onCompositionEnd={handleCompositionEnd}
            onKeyDown={handleKeyDown}
            placeholder={
              isStreaming
                ? '正在生成回复...'
                : creatingPaper
                  ? '正在加载论文...'
                  : currentPaperId
                    ? '输入问题，Shift+Enter 换行。输入 / 查看命令...'
                    : '粘贴 arXiv 链接开始阅读论文'
            }
            disabled={isStreaming || creatingPaper}
            className="flex-1 resize-none rounded-xl px-4 py-2.5 outline-none transition-all duration-200 overflow-y-auto disabled:opacity-50"
            style={{
              minHeight: 5 * 24,
              maxHeight: 10 * 24,
              fontFamily: 'var(--font-body)',
              fontSize: 'var(--paper-font-size)',
              backgroundColor: 'var(--color-bg-inset)',
              color: 'var(--color-text)',
              border: '1px solid transparent',
              boxShadow: 'var(--shadow-sm)',
            }}
            onFocus={(e) => {
              e.currentTarget.style.borderColor = 'var(--color-accent-border)'
              e.currentTarget.style.boxShadow = 'var(--shadow-md)'
            }}
            onBlur={(e) => {
              e.currentTarget.style.borderColor = 'transparent'
              e.currentTarget.style.boxShadow = 'var(--shadow-sm)'
            }}
          />
          <button
            onClick={handleSend}
            disabled={isStreaming}
            className="flex-shrink-0 p-2.5 rounded-xl transition-all duration-200 hover:scale-105 active:scale-95 disabled:opacity-40 disabled:hover:scale-100"
            style={{
              backgroundColor: 'var(--color-accent)',
              color: '#fff',
            }}
            aria-label="发送"
          >
            <Send size={15} />
          </button>
        </div>
      </div>
    </div>
  )
}
