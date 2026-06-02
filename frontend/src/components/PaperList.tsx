import { Plus, Trash2, MoreHorizontal, Download, Pencil, ArrowUp, ArrowDown, Settings, ScrollText, Terminal, AlertTriangle } from 'lucide-react'
import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { usePaperList, useDeletePaper, useExportPaper, useUpdateTitle, useUpdateRating, useSummarizeExport } from '../hooks/usePapers'
import { useAppStore } from '../stores/appStore'
import { setActivePaperOnServer } from '../App'
import { toast } from 'sonner'
import { ConfirmDialog } from './ConfirmDialog'
import type { PaperSummary } from '../types'

type SortBy = 'time' | 'rating'
type SortOrder = 'asc' | 'desc'

function getInitialSortBy(): SortBy {
  const v = localStorage.getItem('paperagent-sort-by')
  return v === 'rating' ? 'rating' : 'time'
}
function getInitialSortOrder(): SortOrder {
  const v = localStorage.getItem('paperagent-sort-order')
  return v === 'asc' ? 'asc' : 'desc'
}

function RatingDots({ rating, onRate }: { rating: number; onRate: (n: number) => void }) {
  const [hovered, setHovered] = useState(0)

  return (
    <div
      className="flex items-center gap-px mt-1.5"
      onMouseLeave={() => setHovered(0)}
      title={rating > 0 ? `评分: ${rating}/10` : '点击评分 (1-10)'}
    >
      {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => {
        const filled = hovered ? n <= hovered : n <= rating
        return (
          <button
            key={n}
            onClick={(e) => {
              e.stopPropagation()
              onRate(n === rating ? 0 : n)
            }}
            onMouseEnter={() => setHovered(n)}
            className="w-2.5 h-2.5 rounded-full transition-all duration-150 hover:scale-125"
            style={{
              backgroundColor: filled
                ? 'var(--color-accent)'
                : 'var(--color-text-muted)',
              opacity: filled ? 1 : 0.25,
            }}
            aria-label={`评分 ${n}`}
          />
        )
      })}
      {rating > 0 && (
        <span
          className="text-xs ml-1 tabular-nums"
          style={{ color: 'var(--color-accent)', fontFamily: 'var(--font-ui)' }}
        >
          {rating}
        </span>
      )}
    </div>
  )
}

export function PaperList() {
  const { data: papers, isLoading, isError, refetch } = usePaperList()
  const deletePaper = useDeletePaper()
  const exportPaper = useExportPaper()
  const updateTitle = useUpdateTitle()
  const updateRating = useUpdateRating()
  const summarizeExport = useSummarizeExport()
  const { currentPaperId, setCurrentPaperId, setNewPaperOpen, setSettingsOpen, setLogOpen, sidebarWidth, setSidebarWidth } = useAppStore()
  const [menuOpen, setMenuOpen] = useState<string | null>(null)
  const [contextMenu, setContextMenu] = useState<{ id: string; x: number; y: number } | null>(null)
  const [editingTitle, setEditingTitle] = useState<string | null>(null)
  const [editValue, setEditValue] = useState('')
  const editInputRef = useRef<HTMLInputElement>(null)

  // Delete confirm
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)

  // Sort state
  const [sortBy, setSortBy] = useState<SortBy>(getInitialSortBy)
  const [sortOrder, setSortOrder] = useState<SortOrder>(getInitialSortOrder)

  // Resize state
  const [dragging, setDragging] = useState(false)
  const dragStartX = useRef(0)
  const dragStartWidth = useRef(0)

  const toggleSortBy = () => {
    const next: SortBy = sortBy === 'time' ? 'rating' : 'time'
    localStorage.setItem('paperagent-sort-by', next)
    setSortBy(next)
  }

  const toggleSortOrder = () => {
    const next: SortOrder = sortOrder === 'desc' ? 'asc' : 'desc'
    localStorage.setItem('paperagent-sort-order', next)
    setSortOrder(next)
  }

  // Resize handlers
  const onMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    setDragging(true)
    dragStartX.current = e.clientX
    dragStartWidth.current = sidebarWidth
  }, [sidebarWidth])

  useEffect(() => {
    if (!dragging) return
    const onMove = (e: MouseEvent) => {
      const delta = e.clientX - dragStartX.current
      const w = Math.max(180, Math.min(500, dragStartWidth.current + delta))
      setSidebarWidth(w)
    }
    const onUp = () => setDragging(false)
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
    return () => {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
    }
  }, [dragging, setSidebarWidth])

  // Close menu on outside click
  useEffect(() => {
    if (!menuOpen && !contextMenu) return
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      if (menuOpen) {
        const menuEl = document.querySelector(`[data-menu-id="${menuOpen}"]`)
        if (menuEl && !menuEl.contains(target)) {
          setMenuOpen(null)
        }
      }
      if (contextMenu) {
        const ctxEl = document.querySelector(`[data-ctx-menu-id="${contextMenu.id}"]`)
        if (ctxEl && !ctxEl.contains(target)) {
          setContextMenu(null)
        }
      }
    }
    const id = requestAnimationFrame(() => {
      document.addEventListener('mousedown', handler)
      document.addEventListener('scroll', handler as EventListener, true)
    })
    return () => {
      cancelAnimationFrame(id)
      document.removeEventListener('mousedown', handler)
      document.removeEventListener('scroll', handler as EventListener, true)
    }
  }, [menuOpen, contextMenu])

  const handleDeleteConfirm = async (id: string) => {
    try {
      await deletePaper.mutateAsync(id)
      toast.success('论文已删除')
      if (currentPaperId === id) {
      setCurrentPaperId(null)
      setActivePaperOnServer(null)
    }
    } catch {
      toast.error('删除失败')
    }
    setDeleteConfirmId(null)
    setMenuOpen(null)
    setContextMenu(null)
  }

  const handleDelete = (id: string) => {
    setDeleteConfirmId(id)
  }

  const handleContextMenu = (e: React.MouseEvent, id: string) => {
    e.preventDefault()
    e.stopPropagation()
    setMenuOpen(null)
    setContextMenu({ id, x: e.clientX, y: e.clientY })
  }

  const handleExport = async (id: string) => {
    try {
      const result = await exportPaper.mutateAsync(id)
      toast.success(`已导出到 ${result.path}`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '导出失败')
    }
    setMenuOpen(null)
    setContextMenu(null)
  }

  const handleSummarizeExport = async (id: string) => {
    try {
      const result = await summarizeExport.mutateAsync(id)
      toast.success(`总结已导出到 ${result.path}`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '总结导出失败')
    }
    setMenuOpen(null)
    setContextMenu(null)
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

  const handleRate = async (id: string, rating: number) => {
    try {
      await updateRating.mutateAsync({ id, rating })
    } catch {
      toast.error('评分失败')
    }
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

  // Sort papers
  const sortedPapers = useMemo(() => {
    if (!papers) return []
    const sorted = [...papers]
    sorted.sort((a, b) => {
      let cmp: number
      if (sortBy === 'rating') {
        cmp = ((a as PaperSummary).rating ?? 0) - ((b as PaperSummary).rating ?? 0)
      } else {
        cmp = new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime()
      }
      return sortOrder === 'asc' ? cmp : -cmp
    })
    return sorted
  }, [papers, sortBy, sortOrder])

  return (
    <div
      className="flex-shrink-0 flex flex-col h-full relative"
      style={{
        width: sidebarWidth,
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
        <div className="flex items-center gap-1">
          <button
            onClick={toggleSortBy}
            className="p-1 rounded-md transition-all duration-200 hover:scale-105 active:scale-95 text-xs"
            style={{
              color: sortBy === 'rating' ? 'var(--color-accent)' : 'var(--color-text-muted)',
              fontFamily: 'var(--font-ui)',
            }}
            title={`排序: ${sortBy === 'time' ? '时间' : '评分'}`}
            aria-label={`按${sortBy === 'time' ? '评分' : '时间'}排序`}
          >
            {sortBy === 'time' ? '时间' : '评分'}
          </button>
          <button
            onClick={toggleSortOrder}
            className="p-1 rounded-md transition-all duration-200 hover:scale-105 active:scale-95"
            style={{ color: 'var(--color-text-muted)' }}
            title={sortOrder === 'desc' ? '降序' : '升序'}
            aria-label={sortOrder === 'desc' ? '切换升序' : '切换降序'}
          >
            {sortOrder === 'desc' ? <ArrowDown size={11} /> : <ArrowUp size={11} />}
          </button>
          <button
            onClick={() => setNewPaperOpen(true)}
            className="p-1.5 rounded-md transition-all duration-200 hover:scale-110 active:scale-95 ml-2"
            style={{
              color: 'var(--color-accent)',
              backgroundColor: 'var(--color-accent-subtle)',
            }}
            title="新建论文"
            aria-label="新建论文"
          >
            <Plus size={15} />
          </button>
          <button
            onClick={() => setSettingsOpen(true)}
            className="p-1.5 rounded-md transition-all duration-200 hover:scale-110 active:scale-95 ml-0.5 hover:bg-[var(--color-bg-inset)]"
            style={{ color: 'var(--color-text-muted)' }}
            title="设置"
            aria-label="设置"
          >
            <Settings size={15} />
          </button>
        </div>
      </div>

      {/* List */}
      <div className="flex-1 overflow-y-auto custom-scrollbar">
        {isLoading && (
          <div className="p-3 space-y-3">
            {[1, 2, 3].map((i) => (
              <div key={i} className="animate-pulse px-2 py-2">
                <div className="h-4 rounded w-full mb-1.5" style={{
                  background: 'var(--color-bg-inset)',
                  backgroundImage: 'linear-gradient(90deg, transparent, var(--color-border-light), transparent)',
                  backgroundSize: '200% 100%',
                  animation: 'shimmer 1.5s infinite',
                }} />
                <div className="h-3 rounded w-1/2" style={{
                  background: 'var(--color-bg-inset)',
                  backgroundImage: 'linear-gradient(90deg, transparent, var(--color-border-light), transparent)',
                  backgroundSize: '200% 100%',
                  animation: 'shimmer 1.5s infinite',
                }} />
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

        {!isLoading && !isError && sortedPapers.length === 0 && (
          <div className="p-8 text-center">
            <p className="text-sm" style={{ color: 'var(--color-text-muted)', fontFamily: 'var(--font-body)' }}>
              暂无论文
            </p>
            <p className="text-xs mt-1" style={{ color: 'var(--color-text-muted)' }}>
              点击 + 新建
            </p>
          </div>
        )}

        {sortedPapers.map((p) => (
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
            onContextMenu={(e) => handleContextMenu(e, p.id)}
          >
            <div
              role="button"
              tabIndex={0}
              className="cursor-pointer outline-none"
              onClick={() => {
                if (editingTitle !== p.id) {
                  setCurrentPaperId(p.id)
                  setActivePaperOnServer(p.id)
                }
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && editingTitle !== p.id) {
                  setCurrentPaperId(p.id)
                  setActivePaperOnServer(p.id)
                }
              }}
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
                  <RatingDots rating={p.rating ?? 0} onRate={(n) => handleRate(p.id, n)} />
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
                    onClick={(e) => { e.stopPropagation(); handleSummarizeExport(p.id) }}
                    className="w-full px-3 py-1.5 text-xs text-left hover:bg-[var(--color-bg-elevated)] flex items-center gap-1.5 transition-colors duration-100"
                    style={{ color: 'var(--color-text)' }}
                    role="menuitem"
                  >
                    <ScrollText size={12} style={{ color: 'var(--color-text-muted)' }} /> 总结导出
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

      {/* Bottom bar */}
      <div
        className="flex-shrink-0 px-4 py-2 flex items-center gap-1"
        style={{
          borderTop: '1px solid var(--color-border-light)',
        }}
      >
        <button
          onClick={() => setLogOpen(true)}
          className="flex items-center gap-1.5 px-2 py-1 rounded-md text-xs transition-all duration-200 hover:bg-[var(--color-bg-inset)]"
          style={{
            fontFamily: 'var(--font-ui)',
            color: 'var(--color-text-muted)',
          }}
          title="查看服务器日志"
          aria-label="日志"
        >
          <Terminal size={13} />
          日志
        </button>
        <div className="flex-1" />
      </div>

      {/* Right-click context menu */}
      {contextMenu && (() => {
        const p = papers?.find(pp => pp.id === contextMenu.id)
        if (!p) return null
        return (
          <div
            data-ctx-menu-id={contextMenu.id}
            className="fixed rounded-lg shadow-lg py-1 z-[100] animate-scale-in"
            style={{
              left: contextMenu.x,
              top: contextMenu.y,
              backgroundColor: 'var(--color-surface)',
              border: '1px solid var(--color-border)',
              boxShadow: 'var(--shadow-lg)',
              minWidth: 128,
            }}
            role="menu"
          >
            <button
              onClick={() => { handleEditTitle(p.id, p.title) }}
              className="w-full px-3 py-1.5 text-xs text-left hover:bg-[var(--color-bg-elevated)] flex items-center gap-1.5 transition-colors duration-100"
              style={{ color: 'var(--color-text)' }}
              role="menuitem"
            >
              <Pencil size={12} style={{ color: 'var(--color-text-muted)' }} /> 编辑标题
            </button>
            <button
              onClick={() => { handleExport(p.id) }}
              className="w-full px-3 py-1.5 text-xs text-left hover:bg-[var(--color-bg-elevated)] flex items-center gap-1.5 transition-colors duration-100"
              style={{ color: 'var(--color-text)' }}
              role="menuitem"
            >
              <Download size={12} style={{ color: 'var(--color-text-muted)' }} /> 导出
            </button>
            <button
              onClick={() => { handleSummarizeExport(p.id) }}
              className="w-full px-3 py-1.5 text-xs text-left hover:bg-[var(--color-bg-elevated)] flex items-center gap-1.5 transition-colors duration-100"
              style={{ color: 'var(--color-text)' }}
              role="menuitem"
            >
              <ScrollText size={12} style={{ color: 'var(--color-text-muted)' }} /> 总结导出
            </button>
            <button
              onClick={() => { handleDelete(p.id) }}
              className="w-full px-3 py-1.5 text-xs text-left hover:bg-[var(--color-danger-subtle)] flex items-center gap-1.5 transition-colors duration-100"
              style={{ color: 'var(--color-danger)' }}
              role="menuitem"
            >
              <Trash2 size={12} /> 删除
            </button>
          </div>
        )
      })()}

      {/* Delete confirm dialog */}
      {papers && deleteConfirmId && (() => {
        const p = papers.find(pp => pp.id === deleteConfirmId)
        return (
          <ConfirmDialog
            open={!!deleteConfirmId}
            title="删除论文"
            message={<>确定要删除「{p?.title || '未命名论文'}」吗？此操作不可撤销。</>}
            confirmLabel="删除"
            cancelLabel="取消"
            danger
            onConfirm={() => handleDeleteConfirm(deleteConfirmId)}
            onCancel={() => { setDeleteConfirmId(null); setMenuOpen(null); setContextMenu(null) }}
          />
        )
      })()}

      {/* Resize handle */}
      <div
        className="absolute top-0 right-0 w-1 h-full cursor-col-resize select-none transition-opacity duration-150 z-10"
        style={{
          opacity: dragging ? 1 : 0.3,
          background: dragging ? 'var(--color-accent)' : 'var(--color-border)',
        }}
        onMouseDown={onMouseDown}
        onMouseEnter={(e) => {
          if (!dragging) {
            (e.target as HTMLElement).style.opacity = '0.7'
            ;(e.target as HTMLElement).style.background = 'var(--color-accent)'
          }
        }}
        onMouseLeave={(e) => {
          if (!dragging) {
            (e.target as HTMLElement).style.opacity = '0.3'
            ;(e.target as HTMLElement).style.background = 'var(--color-border)'
          }
        }}
      />

      {/* Drag overlay to prevent text selection while resizing */}
      {dragging && (
        <div className="fixed inset-0 z-20 cursor-col-resize" />
      )}
    </div>
  )
}
