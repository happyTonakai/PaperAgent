import { useEffect, useRef } from 'react'

interface InlineConfirmProps {
  open: boolean
  message: string
  confirmLabel?: string
  cancelLabel?: string
  danger?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function InlineConfirm({
  open,
  message,
  confirmLabel = '确认',
  cancelLabel = '取消',
  danger = false,
  onConfirm,
  onCancel,
}: InlineConfirmProps) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onCancel()
      }
    }
    const timer = setTimeout(() => {
      document.addEventListener('mousedown', handler)
    }, 0)
    return () => {
      clearTimeout(timer)
      document.removeEventListener('mousedown', handler)
    }
  }, [open, onCancel])

  if (!open) return null

  return (
    <div
      ref={ref}
      className="absolute z-50 mt-1 right-0 rounded-lg shadow-lg p-3 animate-fade-in"
      style={{
        backgroundColor: 'var(--color-surface)',
        border: '1px solid var(--color-border)',
        minWidth: '160px',
        boxShadow: '0 8px 24px rgba(0,0,0,0.18)',
      }}
      onClick={(e) => e.stopPropagation()}
    >
      <p
        className="text-xs mb-2.5 leading-relaxed"
        style={{ color: 'var(--color-text-secondary)', fontFamily: 'var(--font-ui)' }}
      >
        {message}
      </p>
      <div className="flex gap-2 justify-end">
        <button
          onClick={onCancel}
          className="px-2.5 py-1 text-xs rounded-md transition-colors duration-150"
          style={{
            fontFamily: 'var(--font-ui)',
            color: 'var(--color-text-secondary)',
            backgroundColor: 'var(--color-bg-elevated)',
          }}
        >
          {cancelLabel}
        </button>
        <button
          onClick={onConfirm}
          className="px-2.5 py-1 text-xs rounded-md transition-colors duration-150 text-white hover:scale-[1.04] active:scale-[0.96]"
          style={{
            fontFamily: 'var(--font-ui)',
            backgroundColor: danger ? 'var(--color-danger)' : 'var(--color-accent)',
          }}
        >
          {confirmLabel}
        </button>
      </div>
    </div>
  )
}
