import { useState, useEffect, useCallback } from 'react'
import { AlertTriangle, X } from 'lucide-react'

interface ConfirmDialogProps {
  open: boolean
  title: string
  message: React.ReactNode
  confirmLabel?: string
  cancelLabel?: string
  danger?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = '确认',
  cancelLabel = '取消',
  danger = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const [visible, setVisible] = useState(false)
  const [closing, setClosing] = useState(false)

  useEffect(() => {
    if (open) {
      setVisible(true)
      setClosing(false)
    } else if (visible && !closing) {
      setClosing(true)
    }
  }, [open, visible, closing])

  useEffect(() => {
    if (!closing) return
    const timer = setTimeout(() => setVisible(false), 200)
    return () => clearTimeout(timer)
  }, [closing])

  const close = useCallback(() => onCancel(), [onCancel])

  useEffect(() => {
    if (!open) return
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') close() }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [open, close])

  if (!visible) return null

  return (
    <div
      className={`fixed inset-0 z-[60] flex items-center justify-center ${closing ? 'animate-fade-out' : 'animate-fade-in'}`}
      style={{ backgroundColor: 'rgba(0,0,0,0.35)', backdropFilter: 'blur(2px)' }}
      onClick={(e) => { if (e.target === e.currentTarget) close() }}
    >
      <div
        className={`rounded-2xl shadow-lg w-full max-w-sm mx-4 overflow-hidden ${closing ? 'animate-scale-out' : 'animate-scale-in'}`}
        style={{
          backgroundColor: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          boxShadow: 'var(--shadow-lg)',
        }}
      >
        {/* Header */}
        <div
          className="flex items-center justify-between px-5 py-3.5"
          style={{ borderBottom: '1px solid var(--color-border-light)' }}
        >
          <h2
            className="text-sm font-semibold flex items-center gap-2"
            style={{ fontFamily: 'var(--font-display)', color: 'var(--color-text)' }}
          >
            <AlertTriangle
              size={15}
              style={{ color: danger ? 'var(--color-danger)' : 'var(--color-accent)' }}
            />
            {title}
          </h2>
          <button
            onClick={() => close()}
            className="p-1.5 rounded-md hover:bg-[var(--color-bg-elevated)] transition-colors duration-150"
            style={{ color: 'var(--color-text-muted)' }}
          >
            <X size={15} />
          </button>
        </div>

        {/* Body */}
        <div className="p-5">
          <p
            className="text-sm leading-relaxed"
            style={{ color: 'var(--color-text-secondary)', fontFamily: 'var(--font-ui)' }}
          >
            {message}
          </p>

          <div className="mt-5 flex justify-end gap-2.5">
            <button
              onClick={() => close()}
              className="px-4 py-2 text-sm rounded-lg transition-all duration-200 font-medium"
              style={{
                fontFamily: 'var(--font-ui)',
                color: 'var(--color-text-secondary)',
                backgroundColor: 'var(--color-bg-elevated)',
              }}
            >
              {cancelLabel}
            </button>
            <button
              onClick={() => { onConfirm(); close() }}
              className="px-4 py-2 text-sm rounded-lg transition-all duration-200 font-medium text-white hover:scale-[1.02] active:scale-[0.98]"
              style={{
                fontFamily: 'var(--font-ui)',
                backgroundColor: danger ? 'var(--color-danger)' : 'var(--color-accent)',
              }}
            >
              {confirmLabel}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
