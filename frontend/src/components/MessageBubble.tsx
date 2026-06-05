import { useState } from 'react'
import { Copy, Check, RefreshCw, Trash2 } from 'lucide-react'
import { InlineConfirm } from './InlineConfirm'
import ReactMarkdown from 'react-markdown'
import remarkMath from 'remark-math'
import remarkGfm from 'remark-gfm'
import rehypeKatex from 'rehype-katex'
import rehypeHighlight from 'rehype-highlight'

const remarkPlugins = [remarkMath, remarkGfm]
const rehypePlugins = [rehypeKatex, rehypeHighlight]

interface MessageBubbleProps {
  role: 'user' | 'assistant'
  content: string
  roundNumber?: number
  isStreaming?: boolean
  skipContext?: boolean
  promptTokens?: number
  completionTokens?: number
  cachedTokens?: number
  cumulativePromptTokens?: number
  cumulativeCompletionTokens?: number
  cumulativeCachedTokens?: number
  onDeleteRound?: (round: number) => void
  onRetryRound?: (round: number) => void
}

function CopyBtn({ content }: { content: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      onClick={async () => {
        await navigator.clipboard.writeText(content)
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      }}
      className="p-1 rounded transition-all duration-200 hover:scale-105 active:scale-95 flex-shrink-0"
      style={{ color: copied ? 'var(--color-accent)' : 'var(--color-text-muted)' }}
      title="复制原文"
      aria-label="复制原文"
    >
      {copied ? <Check size={13} /> : <Copy size={13} />}
    </button>
  )
}

export function MessageBubble({ role, content, roundNumber, isStreaming, skipContext, promptTokens, completionTokens, cachedTokens, cumulativePromptTokens, cumulativeCompletionTokens, cumulativeCachedTokens, onDeleteRound, onRetryRound }: MessageBubbleProps) {
  const hasTokens = role === 'assistant' && (promptTokens !== undefined || completionTokens !== undefined) &&
    (promptTokens !== 0 || completionTokens !== 0) && !isStreaming
  const hasCumulative = hasTokens && cumulativePromptTokens !== undefined && (cumulativePromptTokens > 0 || cumulativeCompletionTokens! > 0)

  // Actions only for completed Q&A assistant messages (round > 0)
  const showActions = role === 'assistant' && !isStreaming && roundNumber !== undefined && roundNumber > 0
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [retryConfirmOpen, setRetryConfirmOpen] = useState(false)

  return (
    <div
      className="flex gap-3 px-5 py-4 animate-fade-in-up"
      {...(roundNumber !== undefined ? { 'data-round': roundNumber } : {})}
      style={{
        backgroundColor: role === 'user' ? 'var(--color-bg-elevated)' : 'transparent',
        borderBottom: '1px solid var(--color-border-light)',
      }}
    >
      {/* Avatar */}
      <div
        className="flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center text-xs font-medium mt-0.5 select-none"
        style={role === 'user'
          ? {
              backgroundColor: 'var(--color-accent-subtle)',
              color: 'var(--color-accent)',
              fontFamily: 'var(--font-display)',
              fontSize: '0.8rem',
            }
          : {
              backgroundColor: 'var(--color-bg-inset)',
              color: 'var(--color-text-secondary)',
              fontFamily: 'var(--font-display)',
              fontSize: '0.8rem',
            }
        }
      >
        {role === 'user' ? 'Q' : 'A'}
      </div>

      {/* BTW badge */}
      {skipContext && (
        <span
          className="flex-shrink-0 text-xs px-1.5 py-0.5 rounded mt-1 select-none"
          style={{
            fontFamily: 'var(--font-ui)',
            color: role === 'user' ? 'var(--color-accent)' : 'var(--color-text-muted)',
            backgroundColor: role === 'user' ? 'var(--color-accent-subtle)' : 'var(--color-bg-inset)',
            opacity: 0.7,
          }}
        >
          BTW
        </span>
      )}

      {/* Content */}
      <div className="flex-1 min-w-0">
        <div className="markdown-body leading-relaxed">
          <ReactMarkdown
            remarkPlugins={remarkPlugins}
            rehypePlugins={rehypePlugins}
          >
            {content}
          </ReactMarkdown>
        </div>
        {isStreaming && (
          <span
            className="inline-block w-2 h-4 ml-0.5 align-middle"
            style={{
              backgroundColor: 'var(--color-accent)',
              animation: 'cursor-blink 0.7s step-end infinite',
            }}
          />
        )}
        {(hasTokens || showActions) && (
          <div
            className="mt-2 flex items-center gap-2"
            style={{ fontFamily: 'var(--font-ui)' }}
          >
            {hasTokens && (
              <div className="text-xs leading-relaxed" style={{ color: 'var(--color-text-muted)' }}>
                <span style={{ opacity: 0.6 }}>本轮</span> 输入 {((promptTokens ?? 0) - (cachedTokens ?? 0)).toLocaleString()} · 输出 {(completionTokens ?? 0).toLocaleString()}
                {(cachedTokens ?? 0) > 0 && (
                  <> · 缓存命中 {(cachedTokens ?? 0).toLocaleString()}</>
                )}
                {hasCumulative && (
                  <>
                    <span className="mx-2" style={{ opacity: 0.3 }}>|</span>
                    <span style={{ opacity: 0.6 }}>累计</span> 输入 {((cumulativePromptTokens ?? 0) - (cumulativeCachedTokens ?? 0)).toLocaleString()} · 输出 {(cumulativeCompletionTokens ?? 0).toLocaleString()}
                    {(cumulativeCachedTokens ?? 0) > 0 && (
                      <> · 缓存命中 {(cumulativeCachedTokens ?? 0).toLocaleString()}</>
                    )}
                  </>
                )}
              </div>
            )}
            <CopyBtn content={content} />
            {showActions && onRetryRound && (
              <div className="relative">
                <button
                  onClick={() => { setRetryConfirmOpen(true); setDeleteConfirmOpen(false) }}
                  className="p-1 rounded transition-all duration-200 hover:scale-105 active:scale-95 flex-shrink-0"
                  style={{ color: 'var(--color-text-muted)' }}
                  title="重新生成"
                  aria-label="重新生成回答"
                >
                  <RefreshCw size={13} />
                </button>
                <InlineConfirm
                  open={retryConfirmOpen}
                  message="重新生成此条回答？当前回答将被替换。"
                  confirmLabel="确认重试"
                  onConfirm={() => { setRetryConfirmOpen(false); onRetryRound(roundNumber) }}
                  onCancel={() => setRetryConfirmOpen(false)}
                />
              </div>
            )}
            {showActions && onDeleteRound && (
              <div className="relative">
                <button
                  onClick={() => { setDeleteConfirmOpen(true); setRetryConfirmOpen(false) }}
                  className="p-1 rounded transition-all duration-200 hover:scale-105 active:scale-95 flex-shrink-0"
                  style={{ color: 'var(--color-text-muted)' }}
                  title="删除此条问答"
                  aria-label="删除此条问答"
                >
                  <Trash2 size={13} />
                </button>
                <InlineConfirm
                  open={deleteConfirmOpen}
                  message="确认删除此条问答？删除后不可恢复。"
                  confirmLabel="确认删除"
                  danger
                  onConfirm={() => { setDeleteConfirmOpen(false); onDeleteRound(roundNumber) }}
                  onCancel={() => setDeleteConfirmOpen(false)}
                />
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
