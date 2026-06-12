import type { RecommendArticle } from '../types'
import { ArticleCard } from './ArticleCard'

interface ArticleListProps {
  articles: RecommendArticle[]
  loading: boolean
  error: string | null
  onStatusChange?: (id: string, status: number) => void
  onChatClick?: (id: string, title: string) => void
}

export function ArticleList({ articles, loading, error, onStatusChange, onChatClick }: ArticleListProps) {
  if (loading) {
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
    </div>
  )
}
