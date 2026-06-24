package recommend

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/happyTonakai/paperagent/internal/database"
)

// arXiv RSS endpoint per category.
const arxivRSSURL = "http://export.arxiv.org/rss/%s"

// maxFetchRetries is the number of retries for RSS fetch failures.
const maxFetchRetries = 2

// arxivIDRe extracts arXiv ID from a URL or ID string.
var arxivIDRe = regexp.MustCompile(`(\d{4}\.\d{4,5})(v\d+)?`)

// rssAnnounceRe matches "Announce Type: <type>" within the metadata line of
// an arXiv RSS description block.
var rssAnnounceRe = regexp.MustCompile(`Announce Type:\s*(\S+)`)

// RSS represents the top-level RSS feed response from arXiv.
type rssFeed struct {
	XMLName xml.Name  `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Creator     string `xml:"dc:creator"`
}

// FetchArxivRSS fetches new articles from arXiv RSS feeds for the given categories.
// For each category, it fetches up to maxPerCategory articles.
// Returns the list of newly fetched articles (excluding duplicates already in the DB).
func FetchArxivRSS(categories []string, maxPerCategory int) ([]database.NewArticle, error) {
	var allArticles []database.NewArticle
	seen := make(map[string]bool) // dedup across categories

	for _, cat := range categories {
		articles, err := fetchCategoryRSS(cat, maxPerCategory)
		if err != nil {
			return allArticles, fmt.Errorf("fetch category %s: %w", cat, err)
		}
		for _, a := range articles {
			if !seen[a.ID] {
				seen[a.ID] = true
				allArticles = append(allArticles, a)
			}
		}
	}

	if len(allArticles) == 0 {
		return nil, nil
	}

	// Filter out articles that already exist in the database
	ids := make([]string, len(allArticles))
	for i, a := range allArticles {
		ids[i] = a.ID
	}
	existing, err := database.GetExistingIDs(ids)
	if err != nil {
		return allArticles, fmt.Errorf("check existing: %w", err)
	}

	var newArticles []database.NewArticle
	for _, a := range allArticles {
		if !existing[a.ID] {
			newArticles = append(newArticles, a)
		}
	}

	return newArticles, nil
}

// fetchCategoryRSSFileEnv is an e2e-test bypass: when set, RSS is read from
// this local file path instead of being fetched over HTTP. See e2e_test.go.
const fetchCategoryRSSFileEnv = "PAPER_RECOMMEND_RSS_FILE"

func fetchCategoryRSS(category string, max int) ([]database.NewArticle, error) {
	// E2E bypass: read from a local XML file instead of hitting arXiv.
	if path := os.Getenv(fetchCategoryRSSFileEnv); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read RSS file %s: %w", path, err)
		}
		articles, err := parseArxivRSS(body, category)
		if err != nil {
			return nil, err
		}
		if len(articles) > max {
			articles = articles[:max]
		}
		return articles, nil
	}

	url := fmt.Sprintf(arxivRSSURL, strings.ToLower(category))

	var lastErr error
	for retry := 0; retry <= maxFetchRetries; retry++ {
		if retry > 0 {
			time.Sleep(time.Duration(retry) * time.Second)
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		articles, err := parseArxivRSS(body, category)
		if err != nil {
			lastErr = err
			continue
		}

		if len(articles) > max {
			articles = articles[:max]
		}
		return articles, nil
	}

	return nil, fmt.Errorf("fetch after %d retries: %w", maxFetchRetries, lastErr)
}

func parseArxivRSS(body []byte, category string) ([]database.NewArticle, error) {
	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse RSS XML: %w", err)
	}

	var articles []database.NewArticle
	for _, item := range feed.Channel.Items {
		// Extract arXiv ID from link: https://arxiv.org/abs/2401.00001
		id := extractArxivID(item.Link)
		if id == "" {
			// Try from title as fallback
			id = extractArxivID(item.Title)
		}
		if id == "" {
			continue
		}

		abstract, announceType := parseArxivDescription(item.Description)
		// Only keep new submissions and cross-listings. "replace" announcements
		// re-publish existing papers and are not new today, so skip them.
		if announceType != "new" && announceType != "cross" {
			continue
		}

		title := cleanText(item.Title)
		author := cleanText(item.Creator)
		if author == "" {
			author = cleanText(extractAuthorFromDesc(item.Description))
		}
		link := fmt.Sprintf("https://arxiv.org/abs/%s", id)

		var absPtr *string
		if abstract != "" {
			absPtr = &abstract
		}
		var authorPtr *string
		if author != "" {
			authorPtr = &author
		}

		articles = append(articles, database.NewArticle{
			ID:       id,
			Title:    title,
			Link:     link,
			Abstract: absPtr,
			Author:   authorPtr,
			Category: &category,
		})
	}

	return articles, nil
}

func extractArxivID(s string) string {
	s = strings.TrimSpace(s)
	matches := arxivIDRe.FindStringSubmatch(s)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	// Remove HTML tags
	s = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "")
	// Decode common entities
	s = strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", "\"",
		"&#39;", "'",
	).Replace(s)
	return strings.TrimSpace(s)
}

// extractAuthorFromDesc tries to extract author from the description field.
func extractAuthorFromDesc(desc string) string {
	// arXiv RSS description often contains "Authors: ..." at the beginning
	lines := strings.SplitN(desc, "\n", 2)
	if len(lines) > 0 {
		line := strings.TrimSpace(lines[0])
		if strings.HasPrefix(line, "Authors:") {
			return strings.TrimPrefix(line, "Authors:")
		}
	}
	return ""
}

// parseArxivDescription splits an arXiv RSS description into its abstract
// body and announce type. The raw description is shaped like:
//
//	arXiv:2606.12495v1 Announce Type: new
//	Abstract: <body>
//
// The first line carries metadata and is dropped from the abstract. The
// "Abstract:" prefix is also stripped. The announce type is one of "new",
// "cross", or "replace" per arXiv's RSS spec.
func parseArxivDescription(s string) (abstract, announceType string) {
	s = cleanText(s)
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		// No newline — only metadata (or no body at all).
		if m := rssAnnounceRe.FindStringSubmatch(s); len(m) >= 2 {
			announceType = m[1]
		}
		return "", announceType
	}
	first := s[:nl]
	rest := strings.TrimSpace(s[nl+1:])
	if m := rssAnnounceRe.FindStringSubmatch(first); len(m) >= 2 {
		announceType = m[1]
	}
	rest = strings.TrimPrefix(rest, "Abstract:")
	return strings.TrimSpace(rest), announceType
}
