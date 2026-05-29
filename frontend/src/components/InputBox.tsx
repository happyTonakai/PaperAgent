import { useState, useRef, useEffect, useCallback } from 'react'
import { Send, Command } from 'lucide-react'
import { useAppStore } from '../stores/appStore'
import { useExportPaper } from '../hooks/usePapers'
import { toast } from 'sonner'

interface CmdEntry {
  name: string
  description: string
  action: () => void
}

export function InputBox() {
  const { currentPaperId, isStreaming, inputValue, setInputValue, setSettingsOpen, sendQuestion, contentWidth } = useAppStore()
  const exportPaper = useExportPaper()
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const [showCommands, setShowCommands] = useState(false)
  const [selectedCmdIdx, setSelectedCmdIdx] = useState(0)

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
      action: () => toast('可用命令: /export /config /help', { duration: 5000 }),
    },
  ]

  const filteredCommands = inputValue.startsWith('/')
    ? commands.filter((c) => c.name.startsWith(inputValue.trim()))
    : []

  useEffect(() => {
    if (inputValue.startsWith('/') && filteredCommands.length > 0) {
      setShowCommands(true)
      setSelectedCmdIdx(0)
    } else {
      setShowCommands(false)
    }
  }, [inputValue, filteredCommands.length])

  useEffect(() => {
    const el = inputRef.current
    if (el) {
      el.style.height = 'auto'
      el.style.height = Math.min(Math.max(el.scrollHeight, 5 * 24), 10 * 24) + 'px'
    }
  }, [inputValue])

  const handleSend = useCallback(() => {
    const trimmed = inputValue.trim()
    if (!trimmed || isStreaming || !currentPaperId) return

    if (trimmed.startsWith('/')) {
      const cmd = commands.find((c) => c.name === trimmed)
      if (cmd) {
        cmd.action()
        setInputValue('')
        return
      }
    }

    if (sendQuestion) {
      sendQuestion(trimmed)
      setInputValue('')
    }
  }, [inputValue, isStreaming, currentPaperId, setInputValue, sendQuestion])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (showCommands) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSelectedCmdIdx((i) => (i + 1) % filteredCommands.length)
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSelectedCmdIdx((i) => (i - 1 + filteredCommands.length) % filteredCommands.length)
      } else if (e.key === 'Tab' || e.key === 'Enter') {
        e.preventDefault()
        const cmd = filteredCommands[selectedCmdIdx]
        if (cmd) {
          setInputValue(cmd.name)
          setShowCommands(false)
        }
      } else if (e.key === 'Escape') {
        setShowCommands(false)
      }
      return
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  if (!currentPaperId) return null

  return (
    <div
      className="flex-shrink-0 p-4 relative"
      style={{
        backgroundColor: 'var(--color-surface)',
        borderTop: '1px solid var(--color-border)',
      }}
    >
      <div className={contentWidth === 'narrow' ? 'max-w-[55%] mx-auto' : ''}>
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
                  setInputValue(cmd.name)
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
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={isStreaming ? '正在生成回复...' : '输入问题，Shift+Enter 换行。输入 / 查看命令...'}
            disabled={isStreaming}
            className="flex-1 resize-none rounded-xl px-4 py-2.5 text-sm outline-none transition-all duration-200 overflow-y-auto disabled:opacity-50"
            style={{
              minHeight: 5 * 24,
              maxHeight: 10 * 24,
              fontFamily: 'var(--font-body)',
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
            disabled={isStreaming || !inputValue.trim()}
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
