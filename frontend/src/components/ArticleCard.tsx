import { useState } from 'react'
import type { RecommendArticle } from '../types'
import { updateArticleStatus, updateArticleComment } from '../hooks/useArticles'

interface ArticleCardProps {
  article: RecommendArticle
  onStatusChange?: (id: string, status: number) => void
  onChatClick?: (id: string, title: string) => void
}

// Split text into LaTeX math and plain text segments
function splitLatex(input: string): { math: boolean; display: boolean; content: string }[] {
  const RE = /\$\$([\s\S]+?)\$\$|\$([^$\n]+?)\$|\\\[([\s\S]+?)\\\]|\\\(([\s\S]+?)\\\)/g
  const tokens: { math: boolean; display: boolean; content: string }[] = []
  let lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = RE.exec(input)) !== null) {
    if (match.index > lastIndex) {
      tokens.push({ math: false, display: false, content: input.slice(lastIndex, match.index) })
    }
    const isDisplay = match[1] !== undefined || match[3] !== undefined
    const mathContent = match[1] ?? match[2] ?? match[3] ?? match[4] ?? ''
    tokens.push({ math: true, display: isDisplay, content: mathContent })
    lastIndex = RE.lastIndex
  }
  if (lastIndex < input.length) {
    tokens.push({ math: false, display: false, content: input.slice(lastIndex) })
  }
  return tokens
}

function formatAuthors(author: string | null): string {
  if (!author) return ''
  const authors = author.split(',').map(a => a.trim())
  if (authors.length <= 5) return author
  return `${authors.slice(0, 3).join(', ')}, ${authors.slice(-2).join(', ')}, et. al.`
}

function toPdfUrl(link: string): string {
  // Convert arxiv abs link to PDF link
  return link.replace(/^https?:\/\/arxiv\.org\/abs\//, 'https://arxiv.org/pdf/')
}

export function ArticleCard({ article, onStatusChange, onChatClick }: ArticleCardProps) {
  const [showComment, setShowComment] = useState(false)
  const [commentText, setCommentText] = useState(article.comment || '')

  const handleStatus = async (newStatus: number) => {
    const finalStatus = article.status === newStatus ? 0 : newStatus
    try {
      await updateArticleStatus(article.id, finalStatus)
      onStatusChange?.(article.id, finalStatus)
    } catch {}
  }

  const handleSaveComment = async () => {
    try {
      await updateArticleComment(article.id, commentText)
      setShowComment(false)
    } catch {}
  }

  const renderText = (text: string) => {
    const tokens = splitLatex(text)
    return tokens.map((t, i) => {
      if (!t.math) return <span key={i}>{t.content}</span>
      return <span key={i} className="math" style={t.display ? { display: 'block', textAlign: 'center', margin: '4px 0' } : undefined}>{t.display ? `$$${t.content}$$` : `$${t.content}$`}</span>
    })
  }

  return (
    <div className="article-card" data-status={article.status}>
      <div className="article-header">
        <span className="article-score">兴趣分: {article.score.toFixed(3)}</span>
        {article.category && <span className="article-category">{article.category}</span>}
      </div>

      <h3 className="article-title" onClick={() => window.open(toPdfUrl(article.link), '_blank', 'noopener,noreferrer')}>{renderText(article.translated_title || article.title)}</h3>

      {article.author && (
        <p className="article-author">{formatAuthors(article.author)}</p>
      )}

      {(article.translated_abstract || article.abstract) && (
        <p className="article-abstract">{renderText(article.translated_abstract || article.abstract || '')}</p>
      )}

      <div className="article-footer">
        <div className="article-votes">
          {article.hf_upvotes != null && <span className="vote">🤗 {article.hf_upvotes}</span>}
          {article.ax_net_votes != null && <span className="vote">🔬 {article.ax_net_votes}</span>}
        </div>

        <div className="article-actions">
          <button
            className={`btn-action ${article.status === 2 ? 'active' : ''}`}
            onClick={() => handleStatus(2)}
            title="喜欢"
          >👍</button>
          <button
            className={`btn-action ${article.status === -1 ? 'active' : ''}`}
            onClick={() => handleStatus(-1)}
            title="不喜欢"
          >👎</button>
          <button
            className={`btn-action ${article.status === 3 ? 'active' : ''}`}
            onClick={() => handleStatus(3)}
            title="跳过"
          >→</button>
          <button
            className={`btn-action ${article.comment ? 'has-comment' : ''}`}
            onClick={() => setShowComment(!showComment)}
            title="评论"
          >💬</button>
          {onChatClick && (
            <button
              className="btn-action btn-chat"
              onClick={() => onChatClick(article.id, article.title)}
              title="与 AI 讨论这篇论文"
            >🤖</button>
          )}
        </div>
      </div>

      {showComment && (
        <div className="article-comment-box">
          <textarea
            value={commentText}
            onChange={e => setCommentText(e.target.value)}
            placeholder="写下你的看法..."
            rows={2}
          />
          <div className="comment-actions">
            <button onClick={handleSaveComment}>保存</button>
            <button onClick={() => { setShowComment(false); setCommentText(article.comment || '') }}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}
