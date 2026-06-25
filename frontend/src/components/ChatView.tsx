import { useEffect, useRef, useCallback, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkMath from 'remark-math'
import remarkGfm from 'remark-gfm'
import rehypeKatex from 'rehype-katex'
import rehypeHighlight from 'rehype-highlight'
import { RefreshCw } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { usePaper } from '../hooks/usePapers'
import { useSSE } from '../hooks/useSSE'
import { useAppStore } from '../stores/appStore'
import { MessageBubble } from './MessageBubble'
import { RoundNav } from './RoundNav'
import type { Message } from '../types'

const remarkPlugins = [remarkMath, remarkGfm]
const rehypePlugins = [rehypeKatex, rehypeHighlight]

function StreamRenderer({ content }: { content: string }) {
  return (
    <div className="markdown-body text-sm leading-relaxed">
      <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins}>
        {content}
      </ReactMarkdown>
    </div>
  )
}

function LoadingDots() {
  return (
    <div className="flex items-center gap-1.5 py-1">
      {[0, 150, 300].map((delay) => (
        <span
          key={delay}
          className="w-1.5 h-1.5 rounded-full"
          style={{
            backgroundColor: 'var(--color-accent)',
            opacity: 0.5,
            animation: `cursor-blink 1.2s ${delay}ms infinite`,
          }}
        />
      ))}
      <span className="text-xs ml-1" style={{ color: 'var(--color-text-muted)' }}>
        正在生成...
      </span>
    </div>
  )
}

// Maps a backend tool name to the Chinese label shown while the engine
// executes the tool. The engine emits a "tool_call" SSE event before
// running the handler; without an indicator a slow tool (fetch_arxiv does
// a network fetch) leaves the UI looking frozen for several seconds.
function toolCallLabel(name: string): string {
  switch (name) {
    case 'fetch_arxiv': return '正在获取论文…'
    case 'get_references': return '正在获取参考文献…'
    default: return `正在调用工具 ${name}…`
  }
}

function ToolCallIndicator({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-1.5 py-1">
      <span>🔧</span>
      <span className="text-xs ml-1" style={{ color: 'var(--color-text-muted)' }}>
        {label}
      </span>
    </div>
  )
}

export function ChatView() {
  const {
    currentPaperId,
    pendingPaperId, pendingSummary, pendingError, clearPending,
    contentWidth,
    connected,
  } = useAppStore()
  const { data: paper, isLoading, refetch } = usePaper(currentPaperId)
  const qc = useQueryClient()
  const { streamRequest } = useSSE()
  const containerRef = useRef<HTMLDivElement>(null)
  const [streamingContent, setStreamingContent] = useState('')
  const pendingSummaryRef = useRef('')
  const [isStreamingLocal, setIsStreamingLocal] = useState(false)
  const [streamError, setStreamError] = useState<string | null>(null)
  const [retryingSummary, setRetryingSummary] = useState(false)
  const [retrySummaryContent, setRetrySummaryContent] = useState('')
  const [retryingRound, setRetryingRound] = useState<number | null>(null)
  const [pendingUserQuestion, setPendingUserQuestion] = useState<string | null>(null)
  // Name of the tool the engine is currently executing (null when idle). Set
  // from the "tool_call" SSE event so the UI can show a fetching indicator;
  // cleared on the first follow-up chunk / done / error.
  const [toolCallName, setToolCallName] = useState<string | null>(null)
  const [pendingUserRound, setPendingUserRound] = useState<number>(0)
  const answeringRound = useRef<number | null>(null)
  const userScrolledUp = useRef(false)
  const isAutoScrolling = useRef(false)
  const retryCompletedRoundRef = useRef<number | null>(null)

  const isPending = pendingPaperId === currentPaperId && currentPaperId !== null
  const needsSummaryRetry = !isLoading && !isPending && !retryingSummary && paper && !paper.initial_summary && !pendingSummaryRef.current

  const scrollToBottom = useCallback(() => {
    if (containerRef.current) {
      isAutoScrolling.current = true
      containerRef.current.scrollTop = containerRef.current.scrollHeight
      requestAnimationFrame(() => { isAutoScrolling.current = false })
    }
  }, [])

  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const handleScroll = () => {
      if (isAutoScrolling.current) return
      const isNearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 5
      if (isNearBottom) {
        userScrolledUp.current = false
      } else if (isStreamingLocal || isPending || retryingSummary) {
        userScrolledUp.current = true
      }
    }
    el.addEventListener('scroll', handleScroll, { passive: true })

    // Wheel fires before scroll — catches user intent early, immune to isAutoScrolling
    const handleWheel = (e: WheelEvent) => {
      // deltaY > 0 = scroll down, deltaY < 0 = scroll up
      if (e.deltaY < 0 && (isStreamingLocal || isPending || retryingSummary)) {
        userScrolledUp.current = true
      }
    }
    el.addEventListener('wheel', handleWheel, { passive: true })

    return () => {
      el.removeEventListener('scroll', handleScroll)
      el.removeEventListener('wheel', handleWheel)
    }
  }, [isStreamingLocal, isPending, retryingSummary])

  useEffect(() => {
    if ((isStreamingLocal || isPending) && !userScrolledUp.current) {
      scrollToBottom()
    }
  }, [streamingContent, pendingSummary, scrollToBottom, isStreamingLocal, isPending])

  useEffect(() => {
    if (retryingSummary && !userScrolledUp.current) {
      scrollToBottom()
    }
  }, [retrySummaryContent, scrollToBottom, retryingSummary])

  const refetchPaperRef = useRef(refetch)
  refetchPaperRef.current = refetch
  useEffect(() => {
    if (pendingPaperId === null && currentPaperId && paper && !paper.initial_summary) {
      refetchPaperRef.current()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pendingPaperId])

  const handleSendQuestion = useCallback(async (question: string, opts?: { skipContext?: boolean }) => {
    if (!currentPaperId || isStreamingLocal) return
    setStreamingContent('')
    setPendingUserQuestion(question)
    setStreamError(null)
    userScrolledUp.current = false
    const nextRound = (paper?.messages?.length ?? 0) > 0
      ? Math.max(...paper!.messages.map(m => m.round_number), 0) + 1
      : 1
    setPendingUserRound(nextRound)
    answeringRound.current = nextRound
    setIsStreamingLocal(true)
    setToolCallName(null)
    const body: Record<string, unknown> = { question }
    if (opts?.skipContext) {
      body.skip_context = true
    }
    await streamRequest(`/api/papers/${currentPaperId}/chat`, body, {
      onChunk: (content) => {
        setToolCallName(null)
        setStreamingContent((prev) => prev + content)
      },
      onDone: () => {
        setToolCallName(null)
        setIsStreamingLocal(false)
        answeringRound.current = null
        refetch()
        qc.invalidateQueries({ queryKey: ['papers'] })
      },
      onError: (error) => {
        setToolCallName(null)
        setStreamError(error)
        setIsStreamingLocal(false)
        setPendingUserQuestion(null)
      },
      onToolCall: (name) => setToolCallName(name),
    })
  }, [currentPaperId, isStreamingLocal, streamRequest, refetch, paper?.messages])

  // Track last pending summary so it survives clearPending
  useEffect(() => {
    if (pendingSummary) {
      pendingSummaryRef.current = pendingSummary
    }
  }, [pendingSummary])

  // Clean up local state when refetched data catches up after streaming/retry ends
  useEffect(() => {
    if (!paper) return
    // Chat: clean up when the user's question appears in persisted messages
    if (!isStreamingLocal && pendingUserQuestion) {
      const found = paper.messages.some(
        m => m.round_number === pendingUserRound && m.role === 'user'
      )
      if (found) {
        setStreamingContent('')
        setPendingUserQuestion(null)
      }
    }
    // Retry: clean up when refetched data contains the retried round
    if (!isStreamingLocal && retryingRound === null && retryCompletedRoundRef.current !== null && streamingContent) {
      const found = paper.messages.some(
        m => m.round_number === retryCompletedRoundRef.current
      )
      if (found) {
        setStreamingContent('')
        retryCompletedRoundRef.current = null
      }
    }
  }, [paper?.messages, isStreamingLocal, pendingUserQuestion, pendingUserRound, retryingRound, streamingContent])

  const handleRetrySummary = useCallback(async () => {
    if (!currentPaperId) return
    setRetryingSummary(true)
    setRetrySummaryContent('')
    setToolCallName(null)
    userScrolledUp.current = false
    await streamRequest(`/api/papers/${currentPaperId}/retry-summary`, {}, {
      onChunk: (content) => {
        setToolCallName(null)
        setRetrySummaryContent((prev) => prev + content)
      },
      onDone: () => {
        setToolCallName(null)
        setRetryingSummary(false)
        setRetrySummaryContent('')
        refetch()
        qc.invalidateQueries({ queryKey: ['papers'] })
      },
      onError: (error) => {
        setToolCallName(null)
        setRetryingSummary(false)
        setRetrySummaryContent('')
        setStreamError(error)
      },
      onToolCall: (name) => setToolCallName(name),
    })
  }, [currentPaperId, streamRequest, refetch])

  const handleRetryChat = useCallback(async (round: number) => {
    if (!currentPaperId) return
    setRetryingRound(round)
    setStreamingContent('')
    setStreamError(null)
    setToolCallName(null)
    userScrolledUp.current = false
    await streamRequest(`/api/papers/${currentPaperId}/chat/${round}/retry`, {}, {
      onChunk: (content) => {
        setToolCallName(null)
        setStreamingContent((prev) => prev + content)
      },
      onDone: () => {
        setToolCallName(null)
        setRetryingRound(null)
        retryCompletedRoundRef.current = round
        refetch()
        qc.invalidateQueries({ queryKey: ['papers'] })
      },
      onError: (error) => {
        setToolCallName(null)
        setStreamError(error)
        setRetryingRound(null)
      },
      onToolCall: (name) => setToolCallName(name),
    })
  }, [currentPaperId, streamRequest, refetch])

  const handleDeleteRound = useCallback(async (round: number) => {
    if (!currentPaperId) return
    try {
      const res = await fetch(`/api/papers/${currentPaperId}/rounds/${round}`, { method: 'DELETE' })
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: 'Delete failed' }))
        throw new Error((err as { error?: string }).error || `HTTP ${res.status}`)
      }
      refetch()
      qc.invalidateQueries({ queryKey: ['papers'] })
    } catch (err) {
      setStreamError(err instanceof Error ? err.message : 'Delete failed')
    }
  }, [currentPaperId, refetch])

  const setSendQuestion = useAppStore((s) => s.setSendQuestion)
  useEffect(() => {
    setSendQuestion(handleSendQuestion)
    return () => setSendQuestion(null)
  }, [handleSendQuestion, setSendQuestion])

  const lastUnansweredRound = useRef<number | null>(null)
  if (paper && !isStreamingLocal && !retryingSummary && retryingRound === null) {
    const msgs = paper.messages
    if (msgs.length > 0 && msgs[msgs.length - 1].role === 'user') {
      lastUnansweredRound.current = msgs[msgs.length - 1].round_number
    } else {
      lastUnansweredRound.current = null
    }
  }

  // --- Empty state ---
  if (!currentPaperId) {
    return (
      <div
        className="flex-1 flex items-center justify-center"
        style={{ color: 'var(--color-text-muted)' }}
      >
        <div className="text-center animate-fade-in-up">
          <div
            className="text-6xl mb-5 opacity-40"
            style={{ fontFamily: 'var(--font-display)' }}
          >
            &para;
          </div>
          <p
            className="text-lg"
            style={{ fontFamily: 'var(--font-display)', color: 'var(--color-text-secondary)' }}
          >
            选择一篇论文开始阅读
          </p>
          <p
            className="text-sm mt-2"
            style={{ fontFamily: 'var(--font-ui)', color: 'var(--color-text-muted)' }}
          >
            点击左侧论文列表，或创建新论文
          </p>
        </div>
      </div>
    )
  }

  // --- Loading skeleton ---
  if (isLoading && !isPending) {
    return (
      <div className="flex-1 flex flex-col gap-4 p-6">
        {[1, 2, 3].map((i) => (
          <div key={i} className="flex gap-3 animate-pulse">
            <div
              className="w-8 h-8 rounded-full flex-shrink-0"
              style={{ backgroundColor: 'var(--color-bg-inset)' }}
            />
            <div className="flex-1 space-y-2.5">
              <div
                className="h-4 rounded w-3/4"
                style={{
                  background: 'var(--color-bg-inset)',
                  backgroundImage: 'linear-gradient(90deg, transparent, var(--color-border-light), transparent)',
                  backgroundSize: '200% 100%',
                  animation: 'shimmer 1.5s infinite',
                }}
              />
              <div
                className="h-4 rounded w-1/2"
                style={{
                  background: 'var(--color-bg-inset)',
                  backgroundImage: 'linear-gradient(90deg, transparent, var(--color-border-light), transparent)',
                  backgroundSize: '200% 100%',
                  animation: 'shimmer 1.5s infinite',
                }}
              />
            </div>
          </div>
        ))}
      </div>
    )
  }

  // --- Build message list ---
  const allMessages: (Message & { isInitial?: boolean })[] = []
  const summaryMsg = paper?.messages?.find(m => m.round_number === 0 && m.role === 'assistant')
  if (paper?.initial_summary) {
    allMessages.push({
      round_number: 0,
      role: 'assistant',
      content: paper.initial_summary,
      token_count: 0,
      prompt_tokens: summaryMsg?.prompt_tokens,
      completion_tokens: summaryMsg?.completion_tokens,
      cached_tokens: summaryMsg?.cached_tokens,
      isInitial: true,
    })
  }
  if (paper) {
    for (const msg of paper.messages) {
      if (msg.round_number === 0) continue
      // During retry streaming, hide the old assistant answer for that round
      // so the streaming bubble at the bottom acts as the visual replacement
      if (retryingRound !== null && msg.round_number === retryingRound && msg.role === 'assistant') continue
      allMessages.push(msg)
    }
  }

  return (
    <div className="flex-1 flex flex-col min-h-0 relative">
      {/* Title bar — only the paper title. Global controls (width / theme /
          font / size / settings) live in the App-level tab bar; the per-paper
          PDF link lives next to the send button in InputBox. */}
      <div
        className="flex-shrink-0 px-5 py-3 flex items-center gap-3"
        style={{
          backgroundColor: 'var(--color-surface)',
          borderBottom: '1px solid var(--color-border)',
        }}
      >
        <h2
          className="text-sm font-semibold truncate flex-1 flex items-center gap-2"
          style={{
            fontFamily: 'var(--font-display)',
            color: 'var(--color-text)',
            letterSpacing: '-0.01em',
          }}
        >
          {!connected && (
            <span
              className="inline-block rounded-full flex-shrink-0 animate-pulse"
              style={{
                width: 7,
                height: 7,
                backgroundColor: 'var(--color-danger)',
              }}
              title="与服务器断开连接"
            />
          )}
          {paper?.title || '加载中...'}
        </h2>
      </div>

      {/* Messages container */}
      <div ref={containerRef} className="flex-1 overflow-y-auto custom-scrollbar">
        <div className={contentWidth === 'narrow' ? 'max-w-[55%] mx-auto' : ''}>

        {/* PENDING SUMMARY STREAM */}
        {isPending && (
          <div
            className="flex gap-3 px-5 py-4"
            style={{ borderBottom: '1px solid var(--color-border-light)' }}
          >
            <div
              className="flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center text-xs font-medium mt-0.5 select-none"
              style={{
                backgroundColor: 'var(--color-bg-inset)',
                color: 'var(--color-text-secondary)',
                fontFamily: 'var(--font-display)',
              }}
            >
              A
            </div>
            <div className="flex-1 min-w-0">
              {pendingSummary ? (
                <StreamRenderer content={pendingSummary} />
              ) : (
                <LoadingDots />
              )}
              {pendingSummary && (
                <span
                  className="inline-block w-2 h-4 ml-0.5 align-middle"
                  style={{
                    backgroundColor: 'var(--color-accent)',
                    animation: 'cursor-blink 0.7s step-end infinite',
                  }}
                />
              )}
            </div>
          </div>
        )}

        {/* RETRY SUMMARY STREAM */}
        {retryingSummary && (
          <div
            className="flex gap-3 px-5 py-4"
            style={{ borderBottom: '1px solid var(--color-border-light)' }}
          >
            <div
              className="flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center text-xs font-medium mt-0.5 select-none"
              style={{
                backgroundColor: 'var(--color-bg-inset)',
                color: 'var(--color-text-secondary)',
                fontFamily: 'var(--font-display)',
              }}
            >
              A
            </div>
            <div className="flex-1 min-w-0">
              {retrySummaryContent ? (
                <StreamRenderer content={retrySummaryContent} />
              ) : toolCallName ? (
                <ToolCallIndicator label={toolCallLabel(toolCallName)} />
              ) : (
                <LoadingDots />
              )}
              {retrySummaryContent && (
                <span
                  className="inline-block w-2 h-4 ml-0.5 align-middle"
                  style={{
                    backgroundColor: 'var(--color-accent)',
                    animation: 'cursor-blink 0.7s step-end infinite',
                  }}
                />
              )}
            </div>
          </div>
        )}

        {/* TRANSITION: keep pending content visible until paper data catches up */}
        {!isPending && !paper?.initial_summary && pendingSummaryRef.current && (
          <div
            className="flex gap-3 px-5 py-4"
            style={{ borderBottom: '1px solid var(--color-border-light)' }}
          >
            <div
              className="flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center text-xs font-medium mt-0.5 select-none"
              style={{
                backgroundColor: 'var(--color-bg-inset)',
                color: 'var(--color-text-secondary)',
                fontFamily: 'var(--font-display)',
              }}
            >
              A
            </div>
            <div className="flex-1 min-w-0">
              <StreamRenderer content={pendingSummaryRef.current} />
            </div>
          </div>
        )}

        {/* COMPLETED MESSAGES */}
        <div data-msg-start />
        {(() => {
          let cumP = 0, cumC = 0, cumCache = 0
          return allMessages.map((msg, idx) => {
            if (msg.prompt_tokens) cumP += msg.prompt_tokens
            if (msg.completion_tokens) cumC += msg.completion_tokens
            if (msg.cached_tokens) cumCache += msg.cached_tokens
            return (
              <MessageBubble
                key={`${msg.round_number}-${msg.role}-${idx}`}
                role={msg.role}
                content={msg.content}
                roundNumber={msg.round_number}
                promptTokens={msg.prompt_tokens}
                completionTokens={msg.completion_tokens}
                cachedTokens={msg.cached_tokens}
                cumulativePromptTokens={cumP}
                cumulativeCompletionTokens={cumC}
                cumulativeCachedTokens={cumCache}
                skipContext={msg.skip_context}
                onDeleteRound={handleDeleteRound}
                onRetryRound={handleRetryChat}
              />
            )
          })
        })()}
        <div data-msg-end />

        {/* PENDING USER QUESTION — shown during streaming and until refetch catches up */}
        {pendingUserQuestion && (
          <MessageBubble
            role="user"
            content={pendingUserQuestion}
            roundNumber={pendingUserRound}
          />
        )}

        {/* CHAT STREAM — shown during streaming and until refetch catches up */}
        {streamingContent && (
          <MessageBubble
            role="assistant"
            content={streamingContent}
            isStreaming
            roundNumber={retryingRound ?? undefined}
          />
        )}
        {(!streamingContent && ((pendingUserQuestion && isStreamingLocal) || (toolCallName && (isStreamingLocal || retryingRound !== null)))) && (
          <div
            className="flex gap-3 px-5 py-4"
            style={{ borderBottom: '1px solid var(--color-border-light)' }}
          >
            <div
              className="w-8 h-8 rounded-full flex items-center justify-center text-xs font-medium"
              style={{
                backgroundColor: 'var(--color-bg-inset)',
                color: 'var(--color-text-secondary)',
                fontFamily: 'var(--font-display)',
              }}
            >
              A
            </div>
            {toolCallName ? <ToolCallIndicator label={toolCallLabel(toolCallName)} /> : <LoadingDots />}
          </div>
        )}

        {/* NEEDS SUMMARY RETRY */}
        {needsSummaryRetry && !retryingSummary && (
          <div className="px-5 py-10 flex flex-col items-center gap-3 animate-fade-in-up">
            <p className="text-sm" style={{ color: 'var(--color-text-muted)', fontFamily: 'var(--font-body)' }}>
              摘要生成未完成，点击重新生成
            </p>
            <button
              onClick={handleRetrySummary}
              className="px-5 py-2 text-sm rounded-lg flex items-center gap-2 transition-all duration-200 hover:scale-[1.02] active:scale-[0.98]"
              style={{
                backgroundColor: 'var(--color-accent)',
                color: '#fff',
                fontFamily: 'var(--font-ui)',
              }}
            >
              <RefreshCw size={14} /> 重新生成摘要
            </button>
          </div>
        )}

        {/* ERRORS */}
        {(streamError || pendingError) && (
          <div className="px-5 py-4 animate-fade-in-up">
            <div
              className="text-sm rounded-lg p-3 flex items-center gap-2 flex-wrap"
              style={{
                color: 'var(--color-danger)',
                backgroundColor: 'var(--color-danger-subtle)',
                fontFamily: 'var(--font-ui)',
              }}
            >
              <span>{(streamError || pendingError)}</span>
              <div className="flex gap-2 ml-auto">
                {lastUnansweredRound.current !== null && !retryingSummary && (
                  <button
                    onClick={() => handleRetryChat(lastUnansweredRound.current!)}
                    className="underline hover:no-underline flex items-center gap-1 transition-colors duration-150"
                    style={{ color: 'var(--color-accent)' }}
                  >
                    <RefreshCw size={12} /> 重试
                  </button>
                )}
                <button
                  onClick={() => { setStreamError(null); clearPending() }}
                  className="underline hover:no-underline transition-colors duration-150"
                  style={{ color: 'var(--color-accent)' }}
                >
                  关闭
                </button>
              </div>
            </div>
          </div>
        )}

        <div className="h-6" />
        </div>
      </div>

      <RoundNav messages={paper?.messages ?? []} containerRef={containerRef} narrow={contentWidth === 'narrow'} />
    </div>
  )
}
