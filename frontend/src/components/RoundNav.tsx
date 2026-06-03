import { useRef, useState } from 'react'
import { ChevronUp, ChevronDown } from 'lucide-react'
import type { Message } from '../types'

interface RoundNavProps {
  messages: Message[]
  containerRef: React.RefObject<HTMLDivElement | null>
  narrow?: boolean
}

function truncate(s: string, max: number) {
  if (s.length <= max) return s
  return s.slice(0, max) + '…'
}

const COL_WIDTH = 30
const ROW_HEIGHT = 14

export function RoundNav({ messages, containerRef, narrow }: RoundNavProps) {
  const [hovered, setHovered] = useState(false)
  const [hoveredIdx, setHoveredIdx] = useState<number | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout>>(null)

  const handleEnter = () => {
    if (timerRef.current) clearTimeout(timerRef.current)
    setHovered(true)
  }
  const handleLeave = () => {
    timerRef.current = setTimeout(() => { setHovered(false); setHoveredIdx(null) }, 300)
  }

  const rounds: { round: number; label: string }[] = []
  const seen = new Set<number>()
  for (const msg of messages) {
    if (msg.role === 'user' && msg.round_number > 0 && !seen.has(msg.round_number)) {
      seen.add(msg.round_number)
      const firstLine = msg.content.split('\n')[0]
      rounds.push({ round: msg.round_number, label: firstLine.slice(0, 50) })
    }
  }

  const hasRounds = rounds.length > 0

  const scrollTo = (selector: string) => {
    const el = containerRef.current
    if (!el) return
    const target = el.querySelector(selector)
    if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  return (
    <div
      className="absolute top-0 bottom-0 z-20 flex flex-col items-center justify-center"
      style={{ right: narrow ? 'calc((100% - 55%) / 2 - 40px)' : 4 }}
      onMouseEnter={handleEnter}
      onMouseLeave={handleLeave}
    >
      {/* Top arrow */}
      <div className="flex-shrink-0 flex justify-center transition-all duration-200" style={{ height: 36, width: COL_WIDTH, opacity: hovered ? 1 : 0.35 }}>
        <button onClick={() => scrollTo('[data-msg-start]')} className="flex items-center justify-center w-8 h-8 rounded hover:bg-[var(--color-bg-elevated)]" style={{ color: 'var(--color-text-muted)' }}>
          <ChevronUp size={16} />
        </button>
      </div>

      {/* Bars — only when there are Q&A rounds */}
      {hasRounds && (
        <div className="flex-shrink-0 flex flex-col items-center">
          {rounds.map((r, i) => {
            const baseLen = i % 2 === 0 ? 6 : 14
            const isActive = hoveredIdx === i
            const barLen = isActive ? 24 : (hovered ? baseLen + 4 : baseLen)

            return (
              <div key={r.round} className="relative" style={{ width: COL_WIDTH, height: ROW_HEIGHT }}>
                {/* Tooltip */}
                <div
                  className="absolute right-9 whitespace-nowrap text-sm rounded-md px-2.5 py-1.5 pointer-events-none transition-all duration-150"
                  style={{
                    backgroundColor: 'var(--color-surface)',
                    border: '1px solid var(--color-border)',
                    boxShadow: 'var(--shadow-lg)',
                    color: 'var(--color-text)',
                    fontFamily: 'var(--font-ui)',
                    opacity: isActive ? 1 : 0,
                    transform: isActive ? 'translateX(0)' : 'translateX(6px)',
                    zIndex: 30,
                  }}
                >
                  <span style={{ color: 'var(--color-accent)', fontWeight: 600 }}>#{r.round}</span>
                  {' '}{truncate(r.label, 35)}
                </div>

                {/* Hit area fills entire COL_WIDTH × ROW_HEIGHT */}
                <button
                  onClick={() => scrollTo(`[data-round="${r.round}"]`)}
                  onMouseEnter={() => setHoveredIdx(i)}
                  onMouseLeave={() => setHoveredIdx(null)}
                  className={`absolute inset-0 flex items-center cursor-pointer ${narrow ? 'justify-center' : 'justify-end'}`}
                >
                  <span
                    className="block transition-all duration-150"
                    style={{
                      width: barLen,
                      height: 3,
                      backgroundColor: 'var(--color-accent)',
                      opacity: hovered ? 0.7 : 0.35,
                      borderRadius: narrow ? '2px' : '2px 0 0 2px',
                    }}
                  />
                </button>
              </div>
            )
          })}
        </div>
      )}

      {/* Bottom arrow */}
      <div className="flex-shrink-0 flex justify-center transition-all duration-200" style={{ height: 36, width: COL_WIDTH, opacity: hovered ? 1 : 0.35 }}>
        <button onClick={() => scrollTo('[data-msg-end]')} className="flex items-center justify-center w-8 h-8 rounded hover:bg-[var(--color-bg-elevated)]" style={{ color: 'var(--color-text-muted)' }}>
          <ChevronDown size={16} />
        </button>
      </div>
    </div>
  )
}
