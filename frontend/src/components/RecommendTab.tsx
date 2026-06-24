import { useState, useCallback, useEffect, useRef } from 'react'
import { toast } from 'sonner'
import { MoreHorizontal } from 'lucide-react'
import { ArticleList } from './ArticleList'
import { useArticles, useTodayRecommendations, useStats, useSchedulerStatus, fetchNewArticles, generateRecommendations, triggerFullPipeline, pushToFeishu, batchUpdateArticleStatus } from '../hooks/useArticles'
import { useAppStore } from '../stores/appStore'

type FilterValue = number | 'daily'

export function RecommendTab() {
  const [filter, setFilter] = useState<FilterValue>('daily')
  const [fetching, setFetching] = useState(false)
  const [generating, setGenerating] = useState(false)
  const [triggering, setTriggering] = useState(false)
  const [pushing, setPushing] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const { contentWidth } = useAppStore()

  const { articles: dailyArticles, loading: dailyLoading, refetch: refetchDaily } = useTodayRecommendations()
  const { articles: filteredArticles, loading: filteredLoading, hasMore, loadMore, refetch: refetchFiltered } = useArticles(
    typeof filter === 'number' ? filter : null
  )
  const { stats, refetch: refetchStats } = useStats()
  const { status: schedulerStatus, refetch: refetchScheduler } = useSchedulerStatus()

  // Close the ops dropdown on outside click / Escape.
  useEffect(() => {
    if (!menuOpen) return
    const onMouseDown = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false)
      }
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMenuOpen(false)
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [menuOpen])

  const articles = filter === 'daily' ? dailyArticles : filteredArticles
  const loading = filter === 'daily' ? dailyLoading : filteredLoading

  const handleChatClick = useCallback((id: string, title: string) => {
    // Open paper in chat tab — will be handled by App.tsx listener
    window.dispatchEvent(new CustomEvent('recommend-chat', { detail: { arxivId: id, title } }))
  }, [])

  // refetch defaults to true; pass false for hover-to-read so the article
  // stays in the list (the card updates its own data-status locally).
  const handleStatusChange = useCallback((_id?: string, _status?: number, refetch = true) => {
    refetchStats()
    if (refetch) {
      if (filter === 'daily') refetchDaily()
      else refetchFiltered()
    }
  }, [filter, refetchDaily, refetchFiltered, refetchStats])

  const handleTrigger = async () => {
    setMenuOpen(false)
    setTriggering(true)
    try {
      await triggerFullPipeline()
      toast.success('全流程已触发（抓取 RSS → 推荐 → 飞书推送）')
      // Refresh after a short delay to allow pipeline to start
      setTimeout(() => {
        refetchDaily()
        refetchFiltered()
        refetchStats()
        refetchScheduler()
      }, 1000)
    } catch (e) {
      toast.error('触发失败: ' + String(e))
    } finally {
      setTriggering(false)
    }
  }

  const handlePushFeishu = async () => {
    setMenuOpen(false)
    setPushing(true)
    try {
      const res = await pushToFeishu()
      if (res.status === 'no_articles') {
        toast.warning(res.message || '今日无推荐文章')
      } else {
        toast.success(`已推送 ${res.count} 篇文章到飞书`)
      }
      refetchScheduler()
    } catch (e) {
      toast.error('推送失败: ' + String(e))
    } finally {
      setPushing(false)
    }
  }

  const handleFetch = async () => {
    setMenuOpen(false)
    setFetching(true)
    try {
      const count = await fetchNewArticles()
      toast.success(`抓取到 ${count} 篇新论文`)
      refetchDaily()
      refetchFiltered()
      refetchStats()
      refetchScheduler()
    } catch (e) {
      toast.error('抓取失败: ' + String(e))
    } finally {
      setFetching(false)
    }
  }

  const handleGenerate = async () => {
    setMenuOpen(false)
    setGenerating(true)
    try {
      const count = await generateRecommendations()
      toast.success(`已生成 ${count} 篇今日推荐`)
      refetchDaily()
      refetchStats()
      refetchScheduler()
    } catch (e) {
      toast.error('推荐生成失败: ' + String(e))
    } finally {
      setGenerating(false)
    }
  }

  const [markingAll, setMarkingAll] = useState(false)
  // Server caps batch status updates at 500; slice into chunks so a 600+ page
  // load doesn't 400 on the user. The button is also disabled once we've
  // already marked the current page, to avoid double-submit.
  const BATCH_CHUNK = 500
  const handleMarkAllRead = async () => {
    if (articles.length === 0) return
    if (!confirm(`将当前列表中的 ${articles.length} 篇文章全部标记为已读？`)) return
    setMarkingAll(true)
    try {
      const ids = articles.map(a => a.id)
      let total = 0
      for (let i = 0; i < ids.length; i += BATCH_CHUNK) {
        const slice = ids.slice(i, i + BATCH_CHUNK)
        await batchUpdateArticleStatus(slice, 3)
        total += slice.length
      }
      toast.success(`已标记 ${total} 篇为已读`)
      // Always refetch the list after a bulk operation — unlike single-card
      // hover, the parent doesn't know which articles were affected.
      handleStatusChange(undefined, undefined, true)
    } catch (e) {
      toast.error('批量标记失败: ' + String(e))
    } finally {
      setMarkingAll(false)
    }
  }

  // Filter tab labels + their per-status counts. The 今日推荐 count is the
  // length of the today's recommendations payload; the other four come from
  // the /stats endpoint. `clicked` and `total` are intentionally not shown
  // here — they used to live in the old stats bar which we collapsed into
  // these tab badges. Add them back if you find yourself reaching for them.
  const tabCount = (f: FilterValue): number | null => {
    if (!stats) return null
    switch (f) {
      case 'daily': return dailyArticles.length
      case 0: return stats.unread
      case 2: return stats.liked
      case -1: return stats.disliked
      case 3: return stats.read
      default: return null
    }
  }

  return (
    <div className="recommend-tab">
      {/* Single bar: filter tabs (with count badges) on the left, action
          buttons on the right. The old controls bar and stats bar are gone —
          global controls (width / theme / font / size / settings) moved to
          the App-level tab bar, and the four stat cards are folded into the
          tab badges below. */}
      <div className="recommend-toolbar">
        <div className="filter-tabs">
          {(['daily', 0, 2, -1, 3] as const).map(f => {
            const labels: Record<string, string> = { daily: '今日推荐', '0': '未读', '2': '喜欢', '-1': '不喜欢', '3': '已读' }
            const count = tabCount(f)
            return (
              <button
                key={String(f)}
                className={filter === f ? 'active' : ''}
                onClick={() => setFilter(f)}
              >
                {labels[String(f)]}
                {count !== null && (
                  <span
                    className="ml-1.5 inline-flex items-center justify-center min-w-[1.4em] h-[1.25em] px-1 rounded-full text-[0.7em] font-medium tabular-nums"
                    style={
                      filter === f
                        // Active tab: mirror the tab's own color scheme
                        // (light yellow bg + dark amber text) so the badge
                        // doesn't fight the tab's yellow background. White
                        // on --color-accent only hits ~3.4:1 contrast.
                        ? {
                            backgroundColor: 'var(--color-accent-subtle)',
                            color: 'var(--color-accent)',
                            boxShadow: 'inset 0 0 0 1px var(--color-accent-border)',
                          }
                        : {
                            backgroundColor: 'var(--color-bg-inset)',
                            color: 'var(--color-text-muted)',
                          }
                    }
                  >
                    {count}
                  </span>
                )}
              </button>
            )
          })}
        </div>

        <div className="toolbar-actions">
          {/* Status indicator — replaces the "do I need to re-run anything?"
              question that the old ops buttons were implicitly answering.
              Shows today's count and the scheduler's last/next run times
              from the existing /scheduler-status endpoint. */}
          <div
            className="flex items-center gap-1.5 text-xs mr-2"
            style={{ color: 'var(--color-text-muted)', fontFamily: 'var(--font-ui)' }}
            title={
              schedulerStatus?.last_error
                ? `上次执行错误：${schedulerStatus.last_error}`
                : '推荐调度状态'
            }
          >
            {schedulerStatus?.is_running && (
              <span
                className="inline-block rounded-full animate-pulse"
                style={{ width: 6, height: 6, backgroundColor: 'var(--color-accent)' }}
              />
            )}
            <span>
              {dailyArticles.length > 0
                ? `今日已推荐 ${dailyArticles.length} 篇`
                : '今日推荐尚未生成'}
            </span>
            {schedulerStatus?.last_run && (
              <span>· 上次 {formatSchedulerTime(schedulerStatus.last_run)}</span>
            )}
            {schedulerStatus?.next_run && (
              <span>· 下次 {formatSchedulerTime(schedulerStatus.next_run)}</span>
            )}
            {schedulerStatus && schedulerStatus.pending_push_count > 0 && (
              <span style={{ color: 'var(--color-accent)' }}>
                · 积压 {schedulerStatus.pending_push_count} 篇
              </span>
            )}
            {schedulerStatus?.last_push_at && (
              <span>· 上次推送 {formatSchedulerTime(schedulerStatus.last_push_at)}</span>
            )}
          </div>

          {(filter === 'daily' || filter === 0) && (
            <button onClick={handleMarkAllRead} disabled={markingAll || articles.length === 0}>
              {markingAll ? '标记中...' : '✅ 全部已读'}
            </button>
          )}

          {/* Ops dropdown — the 4 maintenance actions (fetch / generate /
              full pipeline / push to feishu) live behind a ⋯ menu so they
              don't crowd the main toolbar. End users almost never need them;
              the status indicator on the left replaces their information
              value. */}
          <div className="relative" ref={menuRef}>
            <button
              onClick={() => setMenuOpen(o => !o)}
              className={menuOpen ? 'active' : ''}
              aria-label="更多操作"
              aria-haspopup="menu"
              aria-expanded={menuOpen}
            >
              <MoreHorizontal size={15} />
            </button>
            {menuOpen && (
              <div
                role="menu"
                className="absolute right-0 top-full mt-1.5 rounded-lg shadow-lg overflow-hidden z-50 animate-scale-in"
                style={{
                  minWidth: 200,
                  backgroundColor: 'var(--color-surface)',
                  border: '1px solid var(--color-border)',
                  boxShadow: 'var(--shadow-lg)',
                }}
              >
                <OpsMenuItem
                  icon="📥"
                  label={fetching ? '抓取中…' : '抓取新文章'}
                  onClick={handleFetch}
                  disabled={fetching}
                />
                <OpsMenuItem
                  icon="📅"
                  label={generating ? '生成中…' : '生成今日推荐'}
                  onClick={handleGenerate}
                  disabled={generating}
                />
                <OpsMenuItem
                  icon="⚡"
                  label={triggering ? '触发中…' : '全流程触发'}
                  onClick={handleTrigger}
                  disabled={triggering}
                />
                <OpsMenuItem
                  icon="💬"
                  label={pushing ? '推送中…' : '推送到飞书'}
                  onClick={handlePushFeishu}
                  disabled={pushing}
                />
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Article list */}
      <div className="recommend-content">
        <div className={contentWidth === 'narrow' ? 'max-w-[50%] mx-auto' : 'max-w-[75%] mx-auto'}>
        <ArticleList
          articles={articles}
          loading={loading}
          error={null}
          hasMore={filter !== 'daily' ? hasMore : false}
          onLoadMore={loadMore}
          onStatusChange={handleStatusChange}
          onChatClick={handleChatClick}
        />
        </div>
      </div>
    </div>
  )
}

// The scheduler's last_run / next_run fields are formatted as
// "2006-01-02 15:04". For at-a-glance use in the toolbar we want a short
// relative form: just HH:MM if it's today, else MM-DD HH:MM.
function formatSchedulerTime(s: string): string {
  if (!s) return ''
  const m = s.match(/^(\d{4})-(\d{2})-(\d{2})\s+(\d{2}:\d{2})/)
  if (!m) return s
  const [, y, mo, d, hm] = m
  const today = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  const todayStr = `${today.getFullYear()}-${pad(today.getMonth() + 1)}-${pad(today.getDate())}`
  if (`${y}-${mo}-${d}` === todayStr) return `今天 ${hm}`
  return `${mo}-${d} ${hm}`
}

// Single row inside the ops dropdown.
function OpsMenuItem({
  icon, label, onClick, disabled,
}: {
  icon: string
  label: string
  onClick: () => void
  disabled?: boolean
}) {
  return (
    <button
      role="menuitem"
      onClick={onClick}
      disabled={disabled}
      className="w-full flex items-center gap-2.5 px-3 py-2 text-sm text-left transition-colors duration-100 disabled:opacity-50 disabled:cursor-not-allowed"
      style={{
        fontFamily: 'var(--font-ui)',
        color: 'var(--color-text)',
        backgroundColor: 'transparent',
      }}
      onMouseEnter={(e) => {
        if (disabled) return
        e.currentTarget.style.backgroundColor = 'var(--color-bg-elevated)'
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.backgroundColor = 'transparent'
      }}
    >
      <span className="text-base leading-none">{icon}</span>
      <span>{label}</span>
    </button>
  )
}
