import { Plus, Trash2, MoreHorizontal, Download, Pencil } from 'lucide-react'
import { useState, useEffect, useRef } from 'react'
import { usePaperList, useDeletePaper, useExportPaper, useUpdateTitle } from '../hooks/usePapers'
import { useAppStore } from '../stores/appStore'
import { toast } from 'sonner'

export function PaperList() {
  const { data: papers, isLoading, isError, refetch } = usePaperList()
  const deletePaper = useDeletePaper()
  const exportPaper = useExportPaper()
  const updateTitle = useUpdateTitle()
  const { currentPaperId, setCurrentPaperId, setNewPaperOpen } = useAppStore()
  const [menuOpen, setMenuOpen] = useState<string | null>(null)
  const [editingTitle, setEditingTitle] = useState<string | null>(null)
  const [editValue, setEditValue] = useState('')
  const editInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!menuOpen) return
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      const menuEl = document.querySelector(`[data-menu-id="${menuOpen}"]`)
      if (menuEl && !menuEl.contains(target)) {
        setMenuOpen(null)
      }
    }
    const id = requestAnimationFrame(() => document.addEventListener('mousedown', handler))
    return () => {
      cancelAnimationFrame(id)
      document.removeEventListener('mousedown', handler)
    }
  }, [menuOpen])

  const handleDelete = async (id: string) => {
    try {
      await deletePaper.mutateAsync(id)
      toast.success('论文已删除')
      if (currentPaperId === id) setCurrentPaperId(null)
    } catch {
      toast.error('删除失败')
    }
    setMenuOpen(null)
  }

  const handleExport = async (id: string) => {
    try {
      const result = await exportPaper.mutateAsync(id)
      toast.success(`已导出到 ${result.path}`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '导出失败')
    }
    setMenuOpen(null)
  }

  const handleEditTitle = (id: string, currentTitle: string) => {
    setEditingTitle(id)
    setEditValue(currentTitle)
    setMenuOpen(null)
    requestAnimationFrame(() => editInputRef.current?.focus())
  }

  const handleSaveTitle = async (id: string) => {
    const trimmed = editValue.trim()
    if (trimmed) {
      try {
        await updateTitle.mutateAsync({ id, title: trimmed })
        toast.success('标题已更新')
      } catch {
        toast.error('更新标题失败')
      }
    }
    setEditingTitle(null)
    setEditValue('')
  }

  const handleCancelEdit = () => {
    setEditingTitle(null)
    setEditValue('')
  }

  const formatDate = (dateStr: string) => {
    try {
      const d = new Date(dateStr)
      const now = new Date()
      const diff = now.getTime() - d.getTime()
      if (diff < 60 * 1000) return '刚刚'
      if (diff < 60 * 60 * 1000) return `${Math.floor(diff / 60000)} 分钟前`
      if (diff < 24 * 60 * 60 * 1000) return `${Math.floor(diff / 3600000)} 小时前`
      return dateStr
    } catch {
      return dateStr
    }
  }

  return (
    <div
      className="w-64 flex-shrink-0 flex flex-col h-full"
      style={{
        backgroundColor: 'var(--color-bg-elevated)',
        borderRight: '1px solid var(--color-border)',
      }}
    >
      {/* Header */}
      <div
        className="flex items-center justify-between px-4 py-3"
        style={{ borderBottom: '1px solid var(--color-border-light)' }}
      >
        <h1
          className="text-sm font-semibold tracking-wide"
          style={{ fontFamily: 'var(--font-display)', color: 'var(--color-text-secondary)' }}
        >
          论文列表
        </h1>
        <button
          onClick={() => setNewPaperOpen(true)}
          className="p-1.5 rounded-md transition-all duration-200 hover:scale-110 active:scale-95"
          style={{
            color: 'var(--color-accent)',
            backgroundColor: 'var(--color-accent-subtle)',
          }}
          title="新建论文"
          aria-label="新建论文"
        >
          <Plus size={15} />
        </button>
      </div>

      {/* List */}
      <div className="flex-1 overflow-y-auto custom-scrollbar">
        {isLoading && (
          <div className="p-3 space-y-3">
            {[1, 2, 3].map((i) => (
              <div key={i} className="animate-pulse px-2 py-2">
                <div
                  className="h-4 rounded w-full mb-1.5"
                  style={{
                    background: 'var(--color-bg-inset)',
                    backgroundImage: 'linear-gradient(90deg, transparent, var(--color-border-light), transparent)',
                    backgroundSize: '200% 100%',
                    animation: 'shimmer 1.5s infinite',
                  }}
                />
                <div
                  className="h-3 rounded w-1/2"
                  style={{
                    background: 'var(--color-bg-inset)',
                    backgroundImage: 'linear-gradient(90deg, transparent, var(--color-border-light), transparent)',
                    backgroundSize: '200% 100%',
                    animation: 'shimmer 1.5s infinite',
                  }}
                />
              </div>
            ))}
          </div>
        )}

        {isError && (
          <div className="p-4 text-center">
            <p className="text-sm" style={{ color: 'var(--color-danger)' }}>加载失败</p>
            <button onClick={() => refetch()} className="mt-1 text-xs underline" style={{ color: 'var(--color-accent)' }}>
              重试
            </button>
          </div>
        )}

        {!isLoading && !isError && papers?.length === 0 && (
          <div className="p-8 text-center">
            <p className="text-sm" style={{ color: 'var(--color-text-muted)', fontFamily: 'var(--font-body)' }}>
              暂无论文
            </p>
            <p className="text-xs mt-1" style={{ color: 'var(--color-text-muted)' }}>
              点击 + 新建
            </p>
          </div>
        )}

        {papers?.map((p, idx) => (
          <div
            key={p.id}
            className="group relative px-4 py-3 transition-all duration-200"
            style={{
              borderBottom: '1px solid var(--color-border-light)',
              backgroundColor: menuOpen === p.id
                ? 'var(--color-bg-inset)'
                : currentPaperId === p.id
                  ? 'var(--color-accent-subtle)'
                  : 'transparent',
              borderLeft: currentPaperId === p.id && menuOpen !== p.id
                ? '2px solid var(--color-accent)'
                : '2px solid transparent',
              zIndex: menuOpen === p.id ? 30 : 'auto',
            }}
          >
            <div
              role="button"
              tabIndex={0}
              className="cursor-pointer outline-none"
              onClick={() => { if (editingTitle !== p.id) setCurrentPaperId(p.id) }}
              onKeyDown={(e) => { if (e.key === 'Enter' && editingTitle !== p.id) setCurrentPaperId(p.id) }}
            >
              {editingTitle === p.id ? (
                <div className="flex items-center gap-1">
                  <input
                    ref={editInputRef}
                    type="text"
                    value={editValue}
                    onChange={(e) => setEditValue(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') handleSaveTitle(p.id)
                      if (e.key === 'Escape') handleCancelEdit()
                    }}
                    onBlur={() => handleSaveTitle(p.id)}
                    className="flex-1 text-sm px-2 py-1 rounded border outline-none min-w-0"
                    style={{
                      fontFamily: 'var(--font-ui)',
                      borderColor: 'var(--color-accent)',
                      backgroundColor: 'var(--color-surface)',
                      color: 'var(--color-text)',
                    }}
                  />
                </div>
              ) : (
                <>
                  <div
                    className="text-sm truncate pr-6"
                    style={{
                      fontFamily: 'var(--font-display)',
                      fontWeight: 550,
                      color: currentPaperId === p.id ? 'var(--color-accent)' : 'var(--color-text)',
                      transition: 'color var(--transition-fast)',
                    }}
                  >
                    {p.title || '未命名论文'}
                  </div>
                  <div
                    className="text-xs mt-1"
                    style={{
                      fontFamily: 'var(--font-ui)',
                      color: 'var(--color-text-muted)',
                    }}
                  >
                    {formatDate(p.updated_at)}
                  </div>
                </>
              )}
            </div>

            {/* Action button */}
            <div
              className={`absolute right-2 top-1/2 -translate-y-1/2 transition-opacity duration-200 ${
                menuOpen === p.id ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'
              }`}
            >
              <button
                onClick={(e) => { e.stopPropagation(); setMenuOpen(menuOpen === p.id ? null : p.id) }}
                className="p-1 rounded-md transition-colors duration-150 hover:bg-[var(--color-bg-inset)]"
                style={{ color: 'var(--color-text-muted)' }}
                aria-label="论文操作"
                aria-expanded={menuOpen === p.id}
              >
                <MoreHorizontal size={14} />
              </button>

              {menuOpen === p.id && (
                <div
                  data-menu-id={p.id}
                  className="absolute right-0 top-full mt-1 w-32 rounded-lg shadow-lg py-1 z-50 animate-scale-in"
                  style={{
                    backgroundColor: 'var(--color-surface)',
                    border: '1px solid var(--color-border)',
                    boxShadow: 'var(--shadow-lg)',
                  }}
                  role="menu"
                >
                  <button
                    onClick={(e) => { e.stopPropagation(); handleEditTitle(p.id, p.title) }}
                    className="w-full px-3 py-1.5 text-xs text-left hover:bg-[var(--color-bg-elevated)] flex items-center gap-1.5 transition-colors duration-100"
                    style={{ color: 'var(--color-text)' }}
                    role="menuitem"
                  >
                    <Pencil size={12} style={{ color: 'var(--color-text-muted)' }} /> 编辑标题
                  </button>
                  <button
                    onClick={(e) => { e.stopPropagation(); handleExport(p.id) }}
                    className="w-full px-3 py-1.5 text-xs text-left hover:bg-[var(--color-bg-elevated)] flex items-center gap-1.5 transition-colors duration-100"
                    style={{ color: 'var(--color-text)' }}
                    role="menuitem"
                  >
                    <Download size={12} style={{ color: 'var(--color-text-muted)' }} /> 导出
                  </button>
                  <button
                    onClick={(e) => { e.stopPropagation(); handleDelete(p.id) }}
                    className="w-full px-3 py-1.5 text-xs text-left hover:bg-[var(--color-danger-subtle)] flex items-center gap-1.5 transition-colors duration-100"
                    style={{ color: 'var(--color-danger)' }}
                    role="menuitem"
                  >
                    <Trash2 size={12} /> 删除
                  </button>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
