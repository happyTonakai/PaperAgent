import { Plus, Trash2, MoreHorizontal, Download } from 'lucide-react'
import { useState, useEffect } from 'react'
import { usePaperList, useDeletePaper, useExportPaper } from '../hooks/usePapers'
import { useAppStore } from '../stores/appStore'
import { toast } from 'sonner'

export function PaperList() {
  const { data: papers, isLoading, isError, refetch } = usePaperList()
  const deletePaper = useDeletePaper()
  const exportPaper = useExportPaper()
  const { currentPaperId, setCurrentPaperId, setNewPaperOpen } = useAppStore()
  const [menuOpen, setMenuOpen] = useState<string | null>(null)

  // Close menu on outside click — tracks by data attribute, not ref
  useEffect(() => {
    if (!menuOpen) return
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      const menuEl = document.querySelector(`[data-menu-id="${menuOpen}"]`)
      if (menuEl && !menuEl.contains(target)) {
        setMenuOpen(null)
      }
    }
    // Delay adding listener so the click that opened menu doesn't close it
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
    <div className="w-64 flex-shrink-0 border-r border-gray-200 dark:border-gray-800 bg-gray-50/50 dark:bg-gray-950/50 flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-3 border-b border-gray-200 dark:border-gray-800">
        <h1 className="text-sm font-semibold text-gray-700 dark:text-gray-300">📄 论文列表</h1>
        <button
          onClick={() => setNewPaperOpen(true)}
          className="p-1 rounded-md hover:bg-gray-200 dark:hover:bg-gray-800 transition-colors"
          title="新建论文"
          aria-label="新建论文"
        >
          <Plus size={16} />
        </button>
      </div>

      {/* List */}
      <div className="flex-1 overflow-y-auto custom-scrollbar">
        {isLoading && (
          <div className="p-3 space-y-3">
            {[1, 2, 3].map((i) => (
              <div key={i} className="animate-pulse">
                <div className="h-4 bg-gray-200 dark:bg-gray-800 rounded w-full mb-1.5" />
                <div className="h-3 bg-gray-200 dark:bg-gray-800 rounded w-1/2" />
              </div>
            ))}
          </div>
        )}

        {isError && (
          <div className="p-3 text-center text-sm text-red-500">
            <p>加载失败</p>
            <button onClick={() => refetch()} className="underline mt-1 text-xs">
              重试
            </button>
          </div>
        )}

        {!isLoading && !isError && papers?.length === 0 && (
          <div className="p-6 text-center text-sm text-gray-400 dark:text-gray-600">
            <p>暂无论文</p>
            <p className="text-xs mt-1">点击 + 新建</p>
          </div>
        )}

        {papers?.map((p) => (
          <div
            key={p.id}
            className={`group px-3 py-2.5 transition-colors border-b border-gray-100 dark:border-gray-900 ${
              menuOpen === p.id
                ? 'relative z-30 bg-gray-100 dark:bg-gray-900'
                : 'relative'
            } ${
              currentPaperId === p.id && menuOpen !== p.id
                ? 'bg-blue-50 dark:bg-blue-950/40 border-l-2 border-l-blue-500'
                : menuOpen !== p.id
                  ? 'hover:bg-gray-100 dark:hover:bg-gray-900 border-l-2 border-l-transparent'
                  : 'border-l-2 border-l-transparent'
            }`}
          >
            {/* Clickable title area (separate from action buttons) */}
            <div
              role="button"
              tabIndex={0}
              className="cursor-pointer outline-none"
              onClick={() => setCurrentPaperId(p.id)}
              onKeyDown={(e) => { if (e.key === 'Enter') setCurrentPaperId(p.id) }}
            >
              <div className="text-sm font-medium truncate pr-6 text-gray-800 dark:text-gray-200">
                {p.title || '未命名论文'}
              </div>
              <div className="text-xs text-gray-400 dark:text-gray-600 mt-0.5">
                {formatDate(p.updated_at)}
              </div>
            </div>

            {/* Action buttons */}
            <div
              className={`absolute right-1 top-1/2 -translate-y-1/2 transition-opacity ${
                menuOpen === p.id
                  ? 'opacity-100'
                  : 'opacity-0 group-hover:opacity-100'
              }`}
            >
              <button
                onClick={(e) => { e.stopPropagation(); setMenuOpen(menuOpen === p.id ? null : p.id) }}
                className="p-1 rounded hover:bg-gray-200 dark:hover:bg-gray-700"
                aria-label="论文操作"
                aria-expanded={menuOpen === p.id}
              >
                <MoreHorizontal size={14} />
              </button>

              {menuOpen === p.id && (
                <div
                  data-menu-id={p.id}
                  className="absolute right-0 top-full mt-0.5 w-28 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg shadow-lg py-1 z-50"
                  role="menu"
                >
                  <button
                    onClick={(e) => { e.stopPropagation(); handleExport(p.id) }}
                    className="w-full px-3 py-1.5 text-xs text-left hover:bg-gray-100 dark:hover:bg-gray-700 flex items-center gap-1.5"
                    role="menuitem"
                  >
                    <Download size={12} /> 导出
                  </button>
                  <button
                    onClick={(e) => { e.stopPropagation(); handleDelete(p.id) }}
                    className="w-full px-3 py-1.5 text-xs text-left hover:bg-red-50 dark:hover:bg-red-950/30 text-red-500 flex items-center gap-1.5"
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
