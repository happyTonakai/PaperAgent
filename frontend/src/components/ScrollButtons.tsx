import { useState, useEffect, type RefObject } from 'react'
import { ChevronUp, ChevronDown } from 'lucide-react'

interface ScrollButtonsProps {
  containerRef: RefObject<HTMLDivElement | null>
}

export function ScrollButtons({ containerRef }: ScrollButtonsProps) {
  const [showButtons, setShowButtons] = useState(false)

  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const check = () => {
      const { scrollTop, scrollHeight, clientHeight } = el
      setShowButtons(scrollHeight - scrollTop - clientHeight > 100)
    }

    el.addEventListener('scroll', check, { passive: true })
    return () => el.removeEventListener('scroll', check)
  }, [containerRef])

  const scrollToTop = () => {
    containerRef.current?.scrollTo({ top: 0, behavior: 'smooth' })
  }

  const scrollToBottom = () => {
    const el = containerRef.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
  }

  if (!showButtons) return null

  const btnClass = "p-1.5 rounded-full border transition-all duration-200 hover:scale-110 active:scale-95"

  return (
    <div
      className="absolute right-4 bottom-20 flex flex-col gap-1.5 animate-fade-in-up"
      style={{ fontFamily: 'var(--font-ui)' }}
    >
      <button
        onClick={scrollToTop}
        className={btnClass}
        style={{
          backgroundColor: 'var(--color-surface)',
          borderColor: 'var(--color-border)',
          color: 'var(--color-text-muted)',
          boxShadow: 'var(--shadow-md)',
        }}
        title="回到顶部"
      >
        <ChevronUp size={16} />
      </button>
      <button
        onClick={scrollToBottom}
        className={btnClass}
        style={{
          backgroundColor: 'var(--color-surface)',
          borderColor: 'var(--color-border)',
          color: 'var(--color-text-muted)',
          boxShadow: 'var(--shadow-md)',
        }}
        title="滚动到底部"
      >
        <ChevronDown size={16} />
      </button>
    </div>
  )
}
