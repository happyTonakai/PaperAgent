import { useRef, useEffect, useState } from 'react'
import { ChevronUp, ChevronDown } from 'lucide-react'
import type { Message } from '../types'

interface RoundNavProps {
  messages: Message[]
  containerRef: React.RefObject<HTMLDivElement | null>
}

function truncate(s: string, max: number) {
  if (s.length <= max) return s
  return s.slice(0, max) + '…'
}

const COL_WIDTH = 30
const ROW_HEIGHT = 14

export function RoundNav({ messages, containerRef }: RoundNavProps) {
  const [visible, setVisible] = useState(false)
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

  const rounds: { round: number; digest: string }[] = []
  const seen = new Set<number>()
  for (const msg of messages) {
    if (msg.role === 'user' && msg.round_number > 0 && !seen.has(msg.round_number)) {
      seen.add(msg.round_number)
      rounds.push({ round: msg.round_number, digest: msg.digest || msg.content.slice(0, 50) })
    }
  }

  // Show nav whenever there are Q&A rounds
  useEffect(() => {
    setVisible(rounds.length > 0)
  }, [rounds.length])

  if (!visible || rounds.length === 0) return null

  const scrollTo = (selector: string) => {
    const el = containerRef.current
    if (!el) return
    const target = el.querySelector(selector)
    if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  return (
    <div
      className="absolute right-0 top-0 bottom-0 z-20 flex flex-col items-center justify-center"
      onMouseEnter={handleEnter}
      onMouseLeave={handleLeave}
    >
      {/* Top arrow */}
      <div className="flex-shrink-0 flex justify-center transition-all duration-200" style={{ height: 28, width: COL_WIDTH, opacity: hovered ? 1 : 0 }}>
        <button onClick={() => scrollTo('[data-msg-start]')} className="flex items-center justify-center w-6 h-6 rounded hover:bg-[var(--color-bg-elevated)]" style={{ color: 'var(--color-text-muted)' }}>
          <ChevronUp size={14} />
        </button>
      </div>

      {/* Bars — packed compactly, vertically centered */}
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
                {' '}{truncate(r.digest, 35)}
              </div>

              {/* Hit area fills entire COL_WIDTH × ROW_HEIGHT */}
              <button
                onClick={() => scrollTo(`[data-round="${r.round}"]`)}
                onMouseEnter={() => setHoveredIdx(i)}
                onMouseLeave={() => setHoveredIdx(null)}
                className="absolute inset-0 flex items-center justify-end cursor-pointer"
              >
                <span
                  className="block rounded-l transition-all duration-150"
                  style={{
                    width: barLen,
                    height: 3,
                    backgroundColor: 'var(--color-accent)',
                    opacity: hovered ? 0.7 : 0.35,
                    borderRadius: '2px 0 0 2px',
                  }}
                />
              </button>
            </div>
          )
        })}
      </div>

      {/* Bottom arrow */}
      <div className="flex-shrink-0 flex justify-center transition-all duration-200" style={{ height: 28, width: COL_WIDTH, opacity: hovered ? 1 : 0 }}>
        <button onClick={() => scrollTo('[data-msg-end]')} className="flex items-center justify-center w-6 h-6 rounded hover:bg-[var(--color-bg-elevated)]" style={{ color: 'var(--color-text-muted)' }}>
          <ChevronDown size={14} />
        </button>
      </div>
    </div>
  )
}
