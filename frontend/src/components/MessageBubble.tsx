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
}

export function MessageBubble({ role, content, roundNumber, isStreaming }: MessageBubbleProps) {
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
      </div>
    </div>
  )
}
