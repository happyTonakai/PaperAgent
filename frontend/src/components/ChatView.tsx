import { useEffect, useRef, useCallback, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkMath from 'remark-math'
import remarkGfm from 'remark-gfm'
import rehypeKatex from 'rehype-katex'
import rehypeHighlight from 'rehype-highlight'
import { RefreshCw, Maximize2, Minimize2, Sun, Moon, Monitor } from 'lucide-react'
import { usePaper } from '../hooks/usePapers'
import { useSSE } from '../hooks/useSSE'
import { useAppStore } from '../stores/appStore'
import { MessageBubble } from './MessageBubble'
import { RoundNav } from './RoundNav'
import { FontSizeButton } from './FontSizeButton'
import { FontFamilyButton } from './FontFamilyButton'
import type { Message, Theme } from '../types'

function getPdfUrl(sourceUrl: string): string {
  return sourceUrl.replace(/arxiv\.org\/abs\//, 'arxiv.org/pdf/')
}

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

export function ChatView() {
  const {
    currentPaperId,
    pendingPaperId, pendingSummary, pendingError, clearPending,
    theme, setTheme, contentWidth, toggleContentWidth,
    connected,
  } = useAppStore()
  const { data: paper, isLoading, refetch } = usePaper(currentPaperId)
  const { streamRequest } = useSSE()
  const containerRef = useRef<HTMLDivElement>(null)
  const [streamingContent, setStreamingContent] = useState('')
  const [isStreamingLocal, setIsStreamingLocal] = useState(false)
  const [streamError, setStreamError] = useState<string | null>(null)
  const [retryingSummary, setRetryingSummary] = useState(false)
  const [retrySummaryContent, setRetrySummaryContent] = useState('')
  const [retryingRound, setRetryingRound] = useState<number | null>(null)
  const [pendingUserQuestion, setPendingUserQuestion] = useState<string | null>(null)
  const [pendingUserRound, setPendingUserRound] = useState<number>(0)
  const answeringRound = useRef<number | null>(null)
  const userScrolledUp = useRef(false)
  const isAutoScrolling = useRef(false)
  const retryCompletedRoundRef = useRef<number | null>(null)

  const isPending = pendingPaperId === currentPaperId && currentPaperId !== null
  const needsSummaryRetry = !isLoading && !isPending && !retryingSummary && paper && !paper.initial_summary

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
      } else if (isStreamingLocal || isPending) {
        userScrolledUp.current = true
      }
    }
    el.addEventListener('scroll', handleScroll, { passive: true })

    // Wheel fires before scroll — catches user intent early, immune to isAutoScrolling
    const handleWheel = (e: WheelEvent) => {
      // deltaY > 0 = scroll down, deltaY < 0 = scroll up
      if (e.deltaY < 0 && (isStreamingLocal || isPending)) {
        userScrolledUp.current = true
      }
    }
    el.addEventListener('wheel', handleWheel, { passive: true })

    return () => {
      el.removeEventListener('scroll', handleScroll)
      el.removeEventListener('wheel', handleWheel)
    }
  }, [isStreamingLocal, isPending])

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

  const handleSendQuestion = useCallback(async (question: string) => {
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
    await streamRequest(`/api/papers/${currentPaperId}/chat`, { question }, {
      onChunk: (content) => setStreamingContent((prev) => prev + content),
      onDone: () => {
        setIsStreamingLocal(false)
        answeringRound.current = null
        refetch()
      },
      onError: (error) => {
        setStreamError(error)
        setIsStreamingLocal(false)
        setPendingUserQuestion(null)
      },
    })
  }, [currentPaperId, isStreamingLocal, streamRequest, refetch, paper?.messages])

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
    userScrolledUp.current = false
    await streamRequest(`/api/papers/${currentPaperId}/retry-summary`, {}, {
      onChunk: (content) => setRetrySummaryContent((prev) => prev + content),
      onDone: () => {
        setRetryingSummary(false)
        setRetrySummaryContent('')
        refetch()
      },
      onError: (error) => {
        setRetryingSummary(false)
        setRetrySummaryContent('')
        setStreamError(error)
      },
    })
  }, [currentPaperId, streamRequest, refetch])

  const handleRetryChat = useCallback(async (round: number) => {
    if (!currentPaperId) return
    setRetryingRound(round)
    setStreamingContent('')
    setStreamError(null)
    userScrolledUp.current = false
    await streamRequest(`/api/papers/${currentPaperId}/chat/${round}/retry`, {}, {
      onChunk: (content) => setStreamingContent((prev) => prev + content),
      onDone: () => {
        setRetryingRound(null)
        retryCompletedRoundRef.current = retryingRound
        refetch()
      },
      onError: (error) => {
        setStreamError(error)
        setRetryingRound(null)
      },
    })
  }, [currentPaperId, streamRequest, refetch])

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
  if (paper?.initial_summary) {
    allMessages.push({
      round_number: 0,
      role: 'assistant',
      content: paper.initial_summary,
      token_count: 0,
      isInitial: true,
    })
  }
  if (paper) {
    for (const msg of paper.messages.filter(m => m.round_number !== 0)) {
      allMessages.push(msg)
    }
  }

  // --- Controls button style ---
  const controlBtnClass = "p-1.5 rounded-md transition-all duration-200 hover:scale-105 active:scale-95"

  return (
    <div className="flex-1 flex flex-col min-h-0 relative">
      {/* Title bar */}
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

        {/* Controls group */}
        <div className="flex items-center gap-0.5" style={{ fontFamily: 'var(--font-ui)' }}>
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
          {paper?.source_url && (
            <a
              href={getPdfUrl(paper.source_url)}
              target="_blank"
              rel="noopener noreferrer"
              className="p-1.5 rounded-md transition-all duration-200 hover:scale-105 active:scale-95 ml-1"
              style={{ color: 'var(--color-text-muted)' }}
              title="打开 PDF"
              aria-label="打开论文 PDF"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                <polyline points="14 2 14 8 20 8" />
                <line x1="8" y1="13" x2="16" y2="13" />
                <line x1="8" y1="17" x2="12" y2="17" />
              </svg>
            </a>
          )}
        </div>
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

        {/* COMPLETED MESSAGES */}
        <div data-msg-start />
        {allMessages.map((msg, idx) => (
          <MessageBubble
            key={`${msg.round_number}-${msg.role}-${idx}`}
            role={msg.role}
            content={msg.content}
            roundNumber={msg.round_number}
          />
        ))}
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
          <MessageBubble role="assistant" content={streamingContent} isStreaming />
        )}
        {(!streamingContent && pendingUserQuestion && isStreamingLocal) && (
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
            <LoadingDots />
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
