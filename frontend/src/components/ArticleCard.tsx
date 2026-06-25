import { useRef, useState } from 'react'
import type { RecommendArticle } from '../types'
import { updateArticleStatus, updateArticleComment } from '../hooks/useArticles'

// Hover duration (ms) after which an unread article is auto-marked as read.
const HOVER_READ_DELAY_MS = 500

interface ArticleCardProps {
  article: RecommendArticle
  // Third arg `refetch` (default true) lets callers like hover-to-read skip
  // the parent list-refetch so the article stays in place.
  onStatusChange?: (id: string, status: number, refetch?: boolean) => void
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

// Detect HTTP(S) URLs (and bare github.com/...) so the abstract text becomes
// clickable. Mirrors the backend regex in internal/urlparse/github.go for the
// github.com case; the broader URL regex catches arXiv links and any other
// repos the translation model preserved verbatim.
//
// We intentionally keep the URL detection on the raw (translated) abstract
// rather than relying on a stored field: the LLM usually preserves URLs
// verbatim, so re-detecting on render is robust to the rare case where
// translation rewrites a URL. Trailing punctuation is stripped so
// "https://x.com/foo." doesn't include the period in the href.
//
// The negated char class for the URL body MUST include CJK ranges —
// translated abstracts frequently embed URLs without surrounding spaces
// (e.g. "可在https://github.com/owner/repo获取"), and without the CJK
// stop the regex happily eats the trailing Chinese text into the URL.
// \u3000-\u303f covers `。`, `、` (CJK Symbols & Punctuation).
// \uff00-\uffef covers `，`, `；`, `：`, `！`, `？`, `（`, `）` (fullwidth forms).
// \u4e00-\u9fff covers CJK Unified Ideographs (the main Chinese characters).
const URL_RE = /\bhttps?:\/\/[^\s<>"'()\u3000-\u303f\uff00-\uffef\u4e00-\u9fff]+|(?:^|\s)github\.com\/[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*/gi
const TRAILING_PUNCT_RE = /[.,;:!?)]+$/

function splitUrls(text: string): { url: boolean; content: string }[] {
  if (!text) return []
  const tokens: { url: boolean; content: string }[] = []
  let lastIndex = 0
  let match: RegExpExecArray | null
  URL_RE.lastIndex = 0
  while ((match = URL_RE.exec(text)) !== null) {
    // For bare github.com matches the leading whitespace is captured in
    // match[0]; only the actual URL starts at match.index + leadingWS.
    const fullMatch = match[0]
    const leadingWS = fullMatch.length - fullMatch.trimStart().length
    const urlStart = match.index + leadingWS
    if (urlStart > lastIndex) {
      tokens.push({ url: false, content: text.slice(lastIndex, urlStart) })
    }
    let url = text.slice(urlStart, match.index + fullMatch.length)
    // Strip trailing punctuation that the regex eagerly captured.
    url = url.replace(TRAILING_PUNCT_RE, '')
    tokens.push({ url: true, content: url })
    lastIndex = match.index + fullMatch.length
  }
  if (lastIndex < text.length) {
    tokens.push({ url: false, content: text.slice(lastIndex) })
  }
  return tokens
}

// normalizeUrl returns a clickable https URL, adding the scheme to bare
// `github.com/...` matches.
function normalizeUrl(url: string): string {
  if (/^https?:\/\//i.test(url)) return url
  return 'https://' + url
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
  // displayStatus mirrors article.status locally so hover/like/dislike
  // updates show up immediately in the CSS accent (data-status) without
  // having to refetch the whole list and risk dropping the article.
  const [displayStatus, setDisplayStatus] = useState(article.status)

  const handleStatus = async (newStatus: number) => {
    const finalStatus = article.status === newStatus ? 0 : newStatus
    try {
      await updateArticleStatus(article.id, finalStatus)
      setDisplayStatus(finalStatus)
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
      if (!t.math) {
        // Further split plain-text segments into URL vs non-URL pieces so
        // abstract URLs (arXiv, GitHub, etc.) become clickable links.
        const parts = splitUrls(t.content)
        return (
          <span key={i}>
            {parts.map((p, j) =>
              p.url ? (
                <a
                  key={j}
                  href={normalizeUrl(p.content)}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="abstract-link"
                >
                  {p.content}
                </a>
              ) : (
                <span key={j}>{p.content}</span>
              )
            )}
          </span>
        )
      }
      return <span key={i} className="math" style={t.display ? { display: 'block', textAlign: 'center', margin: '4px 0' } : undefined}>{t.display ? `$$${t.content}$$` : `$${t.content}$`}</span>
    })
  }

  // Hover-to-mark-read: only for unread articles, fires once per card mount.
  // currentStatusRef mirrors displayStatus to avoid stale-closure issues
  // when the user toggles like/dislike and then hovers again before React
  // re-renders this card.
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [hoverReadDone, setHoverReadDone] = useState(article.status !== 0)
  const currentStatusRef = useRef(article.status)
  currentStatusRef.current = displayStatus

  const handleMouseEnter = () => {
    if (hoverReadDone) return
    if (currentStatusRef.current !== 0) return
    hoverTimer.current = setTimeout(async () => {
      try {
        await updateArticleStatus(article.id, 3)
        // Only mark done after the server confirms. On failure, leave the
        // state intact so the user can re-hover.
        setHoverReadDone(true)
        setDisplayStatus(3)
        // Tell the parent to refetch stats but NOT the list, so the article
        // stays in place (we already updated displayStatus locally).
        onStatusChange?.(article.id, 3, false)
      } catch {
        // network error — leave card as-is; user can re-hover
      }
    }, HOVER_READ_DELAY_MS)
  }

  const handleTitleClick = () => {
    window.open(toPdfUrl(article.link), '_blank', 'noopener,noreferrer')
    // Track PDF click as status=1 (clicked), but only if not already liked (2) or disliked (-1).
    // A PDF click means the user is interested — but a like/dislike is a stronger signal
    // and should not be downgraded.
    //
    // Read currentStatusRef.current (not displayStatus) so a rapid like/dislike
    // followed by a title click sees the updated status instead of a stale
    // closure value — same pattern handleMouseEnter uses.
    if (currentStatusRef.current !== 2 && currentStatusRef.current !== -1) {
      const newStatus = 1
      setDisplayStatus(newStatus)
      updateArticleStatus(article.id, newStatus)
        .then(() => onStatusChange?.(article.id, newStatus))
        .catch(() => {})
    }
  }

  const handleMouseLeave = () => {
    if (hoverTimer.current) {
      clearTimeout(hoverTimer.current)
      hoverTimer.current = null
    }
  }

  return (
    <div
      className="article-card"
      data-status={displayStatus}
      data-article-id={article.id}
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
    >
      <div className="article-header">
        <span className="article-score">兴趣分: {article.score.toFixed(3)}</span>
        {article.category && <span className="article-category">{article.category}</span>}
      </div>

      <h3 className="article-title" onClick={handleTitleClick}>{renderText(article.translated_title || article.title)}</h3>

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
            className={`btn-action ${displayStatus === 2 ? 'active' : ''}`}
            onClick={() => handleStatus(2)}
            title="喜欢"
          >👍</button>
          <button
            className={`btn-action ${displayStatus === -1 ? 'active' : ''}`}
            onClick={() => handleStatus(-1)}
            title="不喜欢"
          >👎</button>
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
