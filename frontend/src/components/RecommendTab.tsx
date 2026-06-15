import { useState, useCallback } from 'react'
import { toast } from 'sonner'
import { ArticleList } from './ArticleList'
import { useArticles, useTodayRecommendations, useStats, fetchNewArticles, generateRecommendations } from '../hooks/useArticles'
import { useAppStore } from '../stores/appStore'

type FilterValue = number | 'daily'

export function RecommendTab() {
  const [filter, setFilter] = useState<FilterValue>('daily')
  const [fetching, setFetching] = useState(false)
  const [generating, setGenerating] = useState(false)
  const contentWidth = useAppStore((s) => s.contentWidth)

  const { articles: dailyArticles, loading: dailyLoading, refetch: refetchDaily } = useTodayRecommendations()
  const { articles: filteredArticles, loading: filteredLoading, refetch: refetchFiltered } = useArticles(
    typeof filter === 'number' ? filter : null
  )
  const { stats, refetch: refetchStats } = useStats()

  const articles = filter === 'daily' ? dailyArticles : filteredArticles
  const loading = filter === 'daily' ? dailyLoading : filteredLoading

  const handleChatClick = useCallback((id: string, title: string) => {
    // Open paper in chat tab — will be handled by App.tsx listener
    window.dispatchEvent(new CustomEvent('recommend-chat', { detail: { arxivId: id, title } }))
  }, [])

  const handleStatusChange = useCallback(() => {
    refetchStats()
    if (filter === 'daily') refetchDaily()
    else refetchFiltered()
  }, [filter, refetchDaily, refetchFiltered, refetchStats])

  const handleFetch = async () => {
    setFetching(true)
    try {
      const count = await fetchNewArticles()
      toast.success(`抓取到 ${count} 篇新论文`)
      refetchDaily()
      refetchFiltered()
      refetchStats()
    } catch (e) {
      toast.error('抓取失败: ' + String(e))
    } finally {
      setFetching(false)
    }
  }

  const handleGenerate = async () => {
    setGenerating(true)
    try {
      const count = await generateRecommendations()
      toast.success(`已生成 ${count} 篇今日推荐`)
      refetchDaily()
      refetchStats()
    } catch (e) {
      toast.error('推荐生成失败: ' + String(e))
    } finally {
      setGenerating(false)
    }
  }

  return (
    <div className={`recommend-tab ${contentWidth === 'narrow' ? 'max-w-1/2 mx-auto' : 'max-w-3/4 mx-auto'}`}>
      {/* Stats bar */}
      <div className="recommend-stats">
        <div className="stat">
          <span className="stat-value">{stats?.unread ?? '-'}</span>
          <span className="stat-label">未读</span>
        </div>
        <div className="stat">
          <span className="stat-value">{stats?.liked ?? '-'}</span>
          <span className="stat-label">喜欢</span>
        </div>
        <div className="stat">
          <span className="stat-value">{stats?.clicked ?? '-'}</span>
          <span className="stat-label">点击</span>
        </div>
        <div className="stat">
          <span className="stat-value">{stats?.total ?? '-'}</span>
          <span className="stat-label">总计</span>
        </div>
      </div>

      {/* Toolbar */}
      <div className="recommend-toolbar">
        <div className="filter-tabs">
          {(['daily', 0, 2, -1] as const).map(f => {
            const labels: Record<string, string> = { daily: '今日推荐', '0': '未读', '2': '喜欢', '-1': '不喜欢' }
            return (
              <button
                key={String(f)}
                className={filter === f ? 'active' : ''}
                onClick={() => setFilter(f)}
              >
                {labels[String(f)]}
              </button>
            )
          })}
        </div>

        <div className="toolbar-actions">
          <button onClick={handleFetch} disabled={fetching}>
            {fetching ? '抓取中...' : '📥 抓取新文章'}
          </button>
          <button onClick={handleGenerate} disabled={generating}>
            {generating ? '生成中...' : '📅 生成今日推荐'}
          </button>
        </div>
      </div>

      {/* Article list */}
      <div className="recommend-content">
        <ArticleList
          articles={articles}
          loading={loading}
          error={null}
          onStatusChange={handleStatusChange}
          onChatClick={handleChatClick}
        />
      </div>
    </div>
  )
}
