import type { RecommendArticle } from '../types'
import { ArticleCard } from './ArticleCard'

interface ArticleListProps {
  articles: RecommendArticle[]
  loading: boolean
  error: string | null
  hasMore?: boolean
  onStatusChange?: (id: string, status: number, refetch?: boolean) => void
  onChatClick?: (id: string, title: string) => void
  onLoadMore?: () => void
}

export function ArticleList({ articles, loading, error, hasMore, onStatusChange, onChatClick, onLoadMore }: ArticleListProps) {
  if (loading && articles.length === 0) {
    return <div className="recommend-loading">加载中...</div>
  }

  if (error) {
    return <div className="recommend-error">加载失败: {error}</div>
  }

  if (articles.length === 0) {
    return <div className="recommend-empty">暂无文章。点击「📥 抓取新文章」从 arXiv 获取论文。</div>
  }

  return (
    <div className="article-list">
      {articles.map(article => (
        <ArticleCard
          key={article.id}
          article={article}
          onStatusChange={onStatusChange}
          onChatClick={onChatClick}
        />
      ))}
      {onLoadMore && hasMore && (
        <div className="article-load-more">
          <button onClick={onLoadMore} disabled={loading}>
            {loading ? '加载中...' : '加载更多'}
          </button>
        </div>
      )}
    </div>
  )
}
