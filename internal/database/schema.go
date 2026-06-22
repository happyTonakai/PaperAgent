package database

const schemaV1 = `
CREATE TABLE IF NOT EXISTS articles (
    id              TEXT    PRIMARY KEY,
    title           TEXT    NOT NULL,
    link            TEXT    NOT NULL,
    abstract        TEXT,
    status          INTEGER NOT NULL DEFAULT 0,
    score           REAL    NOT NULL DEFAULT 0.0,
    author          TEXT,
    category        TEXT,
    hf_upvotes      INTEGER,
    ax_net_votes    INTEGER,
    votes_updated_at TEXT,
    comment         TEXT,
    recommend_date  TEXT,
    batch_order     INTEGER,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_articles_status ON articles(status);
CREATE INDEX IF NOT EXISTS idx_articles_recommend_date ON articles(recommend_date);
`

// schemaV2 adds the chat_papers table for Q&A paper metadata.
const schemaV2 = `
CREATE TABLE IF NOT EXISTS chat_papers (
    id          TEXT    PRIMARY KEY,
    arxiv_id    TEXT,
    title       TEXT    NOT NULL,
    rating      INTEGER NOT NULL DEFAULT 0,
    source_url  TEXT,
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chat_papers_arxiv_id ON chat_papers(arxiv_id);
CREATE INDEX IF NOT EXISTS idx_chat_papers_updated_at ON chat_papers(updated_at);
`

// schemaV3 adds Chinese translation columns for article titles and abstracts.
const schemaV3 = `
ALTER TABLE articles ADD COLUMN title_cn TEXT;
ALTER TABLE articles ADD COLUMN abstract_cn TEXT;
`

// schemaV4 renames translation columns (title_cn→translated_title, abstract_cn→translated_abstract),
// adds recommendation_type for hybrid algorithm, and adds community vote detail columns.
const schemaV4 = `
ALTER TABLE articles ADD COLUMN translated_title TEXT;
ALTER TABLE articles ADD COLUMN translated_abstract TEXT;
UPDATE articles SET translated_title = title_cn, translated_abstract = abstract_cn;
ALTER TABLE articles ADD COLUMN recommendation_type TEXT;
ALTER TABLE articles ADD COLUMN ax_upvotes INTEGER;
ALTER TABLE articles ADD COLUMN ax_downvotes INTEGER;
UPDATE articles SET ax_upvotes = ax_net_votes, ax_downvotes = 0 WHERE ax_net_votes IS NOT NULL;
`

// schemaV5 adds pushed_at to track whether a recommended article has been
// pushed to the user via Feishu. NULL = pending; non-NULL = already pushed.
// This enables the holiday-skip / backlog-merge push behavior: on a holiday
// the push is skipped and articles stay NULL, then a later workday push
// drains the entire pending backlog.
const schemaV5 = `
ALTER TABLE articles ADD COLUMN pushed_at TEXT;
CREATE INDEX IF NOT EXISTS idx_articles_pushed_at ON articles(pushed_at);
`

// schemaV6 introduces chat_paper_abstracts: a dedicated cache table for
// Q&A paper abstracts. The Feishu `/fetch` handler used to write Q&A
// papers into the `articles` table (the RSS recommendation pool) so the
// preference-update pipeline could read their abstract. That bled Q&A
// entries into daily recommendations and Feishu pushes. Q&A abstracts
// now live in chat_paper_abstracts and are read by
// CollectYesterdayFeedback via UpsertChatPaperAbstract /
// GetChatPaperAbstract, keeping `articles` strictly RSS-sourced.
const schemaV6 = `
CREATE TABLE IF NOT EXISTS chat_paper_abstracts (
    arxiv_id   TEXT    PRIMARY KEY,
    abstract   TEXT    NOT NULL,
    updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);
`
