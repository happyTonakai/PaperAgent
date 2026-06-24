package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/urlparse"
)

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// Patterns for detecting reference sections in paper content.
	mdRefRe  = regexp.MustCompile(`(?m)^(#{1,4}\s*(?:References|Bibliography|REFERENCES|BIBLIOGRAPHY)\s*)$`)
	texRefRe = regexp.MustCompile(`(?s)\\begin\{thebibliography\}.*?\\end\{thebibliography\}`)
)

type Message struct {
	RoundNumber      int       `json:"round_number"`
	Role             string    `json:"role"`
	Content          string    `json:"content"`
	TokenCount       int       `json:"token_count"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	CachedTokens     int       `json:"cached_tokens,omitempty"`
	SkipContext      bool      `json:"skip_context,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	// ToolCalls is set on assistant messages that triggered a tool call.
	// When non-empty, the assistant message has no Content — the tool
	// call IS the round's "content". Persisted so that subsequent rounds
	// can replay the tool-call sequence (which prevents re-invoking
	// expensive tools and preserves the LLM's chain of reasoning).
	ToolCalls []api.ToolCallCompleted `json:"tool_calls,omitempty"`
	// ToolCallID is set on tool result messages (Role == "tool") and
	// references the ID of the tool call being answered. Required by the
	// OpenAI chat-completions API to associate the result with its call.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type Paper struct {
	// SessionID is the stable identifier used for persistence and /open.
	// ID is retained only for backward compatibility with older numeric sessions.
	SessionID      string    `json:"session_id,omitempty"`
	ID             int       `json:"id,omitempty"`
	Title          string    `json:"title"`
	SourceURL      string    `json:"source_url"`
	ArxivID        string    `json:"arxiv_id,omitempty"`
	// Content and InitialSummary are redundant with messages[0]; omitted from JSON when empty.
	// SavePaper clears them before marshaling; unmarshal from old files still works.
	Content        string    `json:"content,omitempty"`
	InitialSummary string    `json:"initial_summary,omitempty"`
	// References holds the extracted reference section of the paper.
	// It is NOT sent to the LLM by default; available via get_references tool.
	References string `json:"references,omitempty"`
	// GitHubURL is the primary GitHub repo URL extracted from the paper's
	// abstract (e.g. "https://github.com/owner/repo"). Stored so the WebUI
	// can show a dedicated "open GitHub" icon next to the PDF button. Empty
	// when no GitHub URL is found in the abstract; the WebUI hides the
	// button in that case.
	GitHubURL string `json:"github_url,omitempty"`
	ModelUsed      string    `json:"model_used"`
	TotalTokens    int       `json:"total_tokens_used"`
	TotalPromptTokens        int       `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens    int       `json:"total_completion_tokens,omitempty"`
	TotalCachedTokens        int       `json:"total_cached_tokens,omitempty"`
	Rating         int       `json:"rating"`
	Pinned         bool      `json:"pinned"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Messages       []Message `json:"messages"`
	// TruncationAnchor is the round number from which token counting restarts
	// after a budget-exceeded truncation. 0 means "count from the first context round".
	TruncationAnchor int `json:"truncation_anchor,omitempty"`
}

func (p *Paper) Ref() string {
	if p == nil {
		return ""
	}
	if p.SessionID != "" {
		return p.SessionID
	}
	if p.ID > 0 {
		return fmt.Sprintf("%d", p.ID)
	}
	return ""
}

func (p *Paper) AddMessage(msg Message) {
	if p == nil {
		return
	}
	msg.CreatedAt = time.Now()
	p.Messages = append(p.Messages, msg)
	p.UpdatedAt = time.Now()
	p.TotalTokens += msg.TokenCount
	p.TotalPromptTokens += msg.PromptTokens
	p.TotalCompletionTokens += msg.CompletionTokens
	p.TotalCachedTokens += msg.CachedTokens
}

func (p *Paper) SetInitialSummary(summary string) {
	if p == nil {
		return
	}
	p.InitialSummary = summary
	p.UpdatedAt = time.Now()
}

// SetAnchorFromTokens checks if this round's API token usage exceeds the budget.
// If so, sets TruncationAnchor to keep only the most recent minRounds rounds.
func (p *Paper) SetAnchorFromTokens(round int, promptTokens, completionTokens, maxInput, minRounds int) {
	if promptTokens+completionTokens > maxInput {
		anchor := round - minRounds + 1
		if anchor < 1 {
			anchor = 1
		}
		p.TruncationAnchor = anchor
		log.Printf("[truncation] round %d: prompt=%d + completion=%d = %d > max_input=%d → anchor=%d (keep %d recent rounds)",
			round, promptTokens, completionTokens, promptTokens+completionTokens, maxInput, anchor, minRounds)
	}
}

func (p *Paper) SetTitle(title string) {
	if p == nil {
		return
	}
	p.Title = title
}

func (p *Paper) DeleteRound(round int) {
	if p == nil {
		return
	}
	var filtered []Message
	for _, msg := range p.Messages {
		if msg.RoundNumber != round {
			filtered = append(filtered, msg)
		}
	}
	p.Messages = filtered
	if p.TruncationAnchor == round {
		p.TruncationAnchor = 0
	}
	p.UpdatedAt = time.Now()
}

func (p *Paper) CurrentRound() int {
	if p == nil || len(p.Messages) == 0 {
		return 0
	}
	maxRound := 0
	for _, m := range p.Messages {
		if m.RoundNumber > maxRound {
			maxRound = m.RoundNumber
		}
	}
	return maxRound
}

func (p *Paper) RecentMessages(n int) []Message {
	if p == nil {
		return nil
	}
	msgs := p.Messages
	if len(msgs) <= n*2 {
		return msgs
	}
	return msgs[len(msgs)-n*2:]
}

// collectAllContextMessages returns all context-bearing messages (SkipContext=false) in order,
// walking backwards to collect complete user+assistant rounds.
func (p *Paper) collectAllContextMessages() []Message {
	if p == nil {
		return nil
	}
	var result []Message
	for i := len(p.Messages) - 1; i >= 0; {
		if p.Messages[i].Role == "assistant" && !p.Messages[i].SkipContext {
			end := i + 1
			for i >= 0 && p.Messages[i].RoundNumber == p.Messages[end-1].RoundNumber {
				i--
			}
			start := i + 1
			result = append(p.Messages[start:end], result...)
		} else {
			i--
		}
	}
	return result
}

// RecentContextMessages returns context-bearing messages using anchor-based token budgeting.
// The anchor is set by callers (handleChat/cmdChat) based on real API token values via SetAnchorFromTokens.
// When anchor > 0, only rounds from the anchor onward are returned.
func (p *Paper) RecentContextMessages() []Message {
	if p == nil {
		return nil
	}
	all := p.collectAllContextMessages()
	var active []Message
	if p.TruncationAnchor <= 0 {
		active = all
	} else {
		for _, m := range all {
			if m.RoundNumber >= p.TruncationAnchor {
				active = append(active, m)
			}
		}
	}
	if minR, maxR := ContextRoundRange(active); maxR > 0 {
		log.Printf("[chat] context rounds %d-%d (%d msgs, anchor=%d)", minR, maxR, len(active), p.TruncationAnchor)
	} else {
		log.Printf("[chat] context empty (anchor=%d)", p.TruncationAnchor)
	}
	return active
}

// ContextRoundRange returns the min and max round numbers among context messages, or (0,0) if empty.
func ContextRoundRange(msgs []Message) (int, int) {
	if len(msgs) == 0 {
		return 0, 0
	}
	minR, maxR := msgs[0].RoundNumber, msgs[0].RoundNumber
	for _, m := range msgs {
		if m.RoundNumber < minR {
			minR = m.RoundNumber
		}
		if m.RoundNumber > maxR {
			maxR = m.RoundNumber
		}
	}
	return minR, maxR
}



func (p *Paper) Save() error {
	if p == nil {
		return nil
	}
	return SavePaper(p)
}

type Manager struct {
	mu    sync.Mutex
	paper *Paper
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Paper() *Paper {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paper
}

func (m *Manager) SetPaper(p *Paper) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paper = p
}

func (m *Manager) AddMessage(msg Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper != nil {
		msg.CreatedAt = time.Now()
		m.paper.Messages = append(m.paper.Messages, msg)
		m.paper.UpdatedAt = time.Now()
		m.paper.TotalTokens += msg.TokenCount
		m.paper.TotalPromptTokens += msg.PromptTokens
		m.paper.TotalCompletionTokens += msg.CompletionTokens
		m.paper.TotalCachedTokens += msg.CachedTokens
	}
}

func (m *Manager) UpdateLastAssistant(content string, tokenCount int, promptTokens, completionTokens, cachedTokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper == nil || len(m.paper.Messages) == 0 {
		return
	}
	for i := len(m.paper.Messages) - 1; i >= 0; i-- {
		if m.paper.Messages[i].Role == "assistant" {
			m.paper.Messages[i].Content = content
			m.paper.Messages[i].TokenCount = tokenCount
			m.paper.Messages[i].PromptTokens = promptTokens
			m.paper.Messages[i].CompletionTokens = completionTokens
			m.paper.Messages[i].CachedTokens = cachedTokens
			m.paper.UpdatedAt = time.Now()
			return
		}
	}
}

func (m *Manager) SetInitialSummary(summary string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper != nil {
		m.paper.InitialSummary = summary
		m.paper.UpdatedAt = time.Now()
	}
}

func (m *Manager) SetTitle(title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper != nil {
		m.paper.Title = title
	}
}

func (m *Manager) GetRecentMessages(n int) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper == nil {
		return nil
	}
	msgs := m.paper.Messages
	if len(msgs) <= n*2 {
		return msgs
	}
	return msgs[len(msgs)-n*2:]
}

func (m *Manager) CurrentRound() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper == nil || len(m.paper.Messages) == 0 {
		return 0
	}
	maxRound := 0
	for _, msg := range m.paper.Messages {
		if msg.RoundNumber > maxRound {
			maxRound = msg.RoundNumber
		}
	}
	return maxRound
}

func (m *Manager) DeleteRound(round int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper == nil {
		return
	}
	var filtered []Message
	for _, msg := range m.paper.Messages {
		if msg.RoundNumber != round {
			filtered = append(filtered, msg)
		}
	}
	m.paper.Messages = filtered
	if m.paper.TruncationAnchor == round {
		m.paper.TruncationAnchor = 0
	}
	m.paper.UpdatedAt = time.Now()
}

func (m *Manager) DeleteLastRound() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper == nil || len(m.paper.Messages) == 0 {
		return
	}
	lastRound := m.paper.Messages[len(m.paper.Messages)-1].RoundNumber
	var filtered []Message
	for _, msg := range m.paper.Messages {
		if msg.RoundNumber != lastRound {
			filtered = append(filtered, msg)
		}
	}
	m.paper.Messages = filtered
	m.paper.UpdatedAt = time.Now()
}

func (m *Manager) GetLastUserMessage() *Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper == nil {
		return nil
	}
	for i := len(m.paper.Messages) - 1; i >= 0; i-- {
		if m.paper.Messages[i].Role == "user" {
			return &m.paper.Messages[i]
		}
	}
	return nil
}

func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paper == nil {
		return nil
	}
	return SavePaper(m.paper)
}

// nextID returns the next available legacy numeric paper ID.
func nextID() int {
	summaries, err := ListPapers()
	if err != nil {
		return 1
	}
	maxID := 0
	for _, p := range summaries {
		if p.ID > maxID {
			maxID = p.ID
		}
	}
	return maxID + 1
}

func NewPaper(content string, sourceURL string, arxivID string) *Paper {
	now := time.Now()
	return &Paper{
		SessionID: newSessionID(),
		SourceURL: sourceURL,
		ArxivID:   arxivID,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []Message{},
	}
}

func SavePaper(p *Paper) error {
	if p.SessionID == "" {
		p.SessionID = newSessionID()
	}
	dir := config.PapersDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Create a copy to avoid mutating the in-memory object.
	copy := *p
	copy.Content = ""
	copy.InitialSummary = ""
	data, err := json.MarshalIndent(&copy, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, p.SessionID+".json"), data, 0644); err != nil {
		return err
	}

	// Sync metadata to chat_papers for preference updates (best-effort).
	if p.ArxivID != "" {
		title := p.Title
		if title == "" {
			title = "Paper " + p.SessionID
		}
		cp := &database.ChatPaper{
			ID:        p.SessionID,
			ArxivID:   p.ArxivID,
			Title:     title,
			Rating:    p.Rating,
			SourceURL: p.SourceURL,
			CreatedAt: p.CreatedAt.Format("2006-01-02 15:04"),
			UpdatedAt: p.UpdatedAt.Format("2006-01-02 15:04"),
			GitHubURL: p.GitHubURL,
		}
		if err := database.UpsertChatPaper(cp); err != nil {
			log.Printf("[session] sync to chat_papers: %v", err)
		}
	}

	return nil
}

func LoadPaper(id int) (*Paper, error) {
	return LoadPaperByRef(fmt.Sprintf("%d", id))
}

func LoadPaperByRef(ref string) (*Paper, error) {
	path, err := paperPathByRef(ref)
	if err != nil {
		return nil, err
	}
	return loadPaperPath(path)
}

func DeletePaper(id int) error {
	return DeletePaperByRef(fmt.Sprintf("%d", id))
}

func DeletePaperByRef(ref string) error {
	path, err := paperPathByRef(ref)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

type PaperSummary struct {
	ID        int
	SessionID string
	Title     string
	Rating    int
	Pinned    bool
	UpdatedAt time.Time
}

func (p PaperSummary) Ref() string {
	if p.SessionID != "" {
		return p.SessionID
	}
	if p.ID > 0 {
		return fmt.Sprintf("%d", p.ID)
	}
	return ""
}

func (p PaperSummary) ShortRef() string {
	ref := p.Ref()
	if uuidPattern.MatchString(ref) && len(ref) >= 8 {
		return ref[:8]
	}
	return ref
}

func ListPapers() ([]PaperSummary, error) {
	dir := config.PapersDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var papers []PaperSummary
	seen := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p, err := loadPaperPath(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		ref := p.Ref()
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		title := p.Title
		if title == "" {
			if p.SessionID != "" {
				title = "Paper " + p.SessionID[:8]
			} else {
				title = fmt.Sprintf("Paper #%d", p.ID)
			}
		}
		papers = append(papers, PaperSummary{ID: p.ID, SessionID: p.SessionID, Title: title, Rating: p.Rating, Pinned: p.Pinned, UpdatedAt: p.UpdatedAt})
	}

	sort.Slice(papers, func(i, j int) bool {
		// Pinned papers come first
		if papers[i].Pinned != papers[j].Pinned {
			return papers[i].Pinned
		}
		if !papers[i].UpdatedAt.Equal(papers[j].UpdatedAt) {
			return papers[i].UpdatedAt.After(papers[j].UpdatedAt)
		}
		if papers[i].ID != papers[j].ID {
			return papers[i].ID < papers[j].ID
		}
		return papers[i].Ref() < papers[j].Ref()
	})

	return papers, nil
}

// FindPaperByArxivID looks for a paper with the given arXiv ID.
// Returns the paper if found, or os.ErrNotExist if not found.
func FindPaperByArxivID(arxivID string) (*Paper, error) {
	if arxivID == "" {
		return nil, os.ErrNotExist
	}
	dir := config.PapersDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p, err := loadPaperPath(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if p.ArxivID == arxivID {
			return p, nil
		}
	}
	return nil, os.ErrNotExist
}

// ExtractReferences separates the reference section from the main body of a paper.
// It handles both markdown-formatted ("## References") and TeX-formatted
// ("\\begin{thebibliography}") reference sections.
// Returns (body, references). If no reference section is found, returns (content, "").
func ExtractReferences(content string) (body, references string) {
	if content == "" {
		return "", ""
	}

	// Try TeX format first (before markdown heading check, since thebibliography
	// is unambiguous even in markdown-converted output).
	if loc := texRefRe.FindStringIndex(content); loc != nil {
		body = strings.TrimSpace(content[:loc[0]])
		references = strings.TrimSpace(content[loc[0]:])
		return
	}

	// Try markdown reference heading.
	lines := strings.Split(content, "\n")
	refStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if mdRefRe.MatchString(trimmed) {
			refStart = i
			break
		}
	}

	if refStart >= 0 {
		bodyLines := lines[:refStart]
		refLines := lines[refStart:]
		body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
		references = strings.TrimSpace(strings.Join(refLines, "\n"))
		return
	}

	// No recognizable reference section found.
	return content, ""
}

// StripReferences returns the paper content with the reference section removed.
func StripReferences(content string) string {
	body, _ := ExtractReferences(content)
	return body
}

func EstimateTokens(text string) int {
	return len(text) / 4
}

func loadPaperPath(path string) (*Paper, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Paper
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if uuidPattern.MatchString(name) {
			p.SessionID = name
		}
	}
	// Migrate: extract arxiv_id from source_url for old papers.
	if p.ArxivID == "" && p.SourceURL != "" {
		if _, id, ok := urlparse.NormalizeArxivInput(p.SourceURL); ok {
			p.ArxivID = id
			// Persist back — only touch arxiv_id, keep original file intact.
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err == nil {
				raw["arxiv_id"] = id
				if data2, err := json.MarshalIndent(raw, "", "  "); err == nil {
					os.WriteFile(path, data2, 0644)
				}
			}
		}
	}

	// Reconstruct Content/InitialSummary from messages[0] for new-format files (no top-level fields).
	if p.Content == "" && len(p.Messages) > 0 {
		for _, m := range p.Messages {
			if m.RoundNumber == 0 && m.Role == "user" {
				p.Content = m.Content
				break
			}
		}
	}
	if p.InitialSummary == "" && len(p.Messages) > 0 {
		for _, m := range p.Messages {
			if m.RoundNumber == 0 && m.Role == "assistant" {
				p.InitialSummary = m.Content
				break
			}
		}
	}
	return &p, nil
}

func paperPathByRef(ref string) (string, error) {
	ref = strings.TrimSpace(strings.TrimSuffix(ref, ".json"))
	if ref == "" {
		return "", fmt.Errorf("empty paper ref")
	}
	dir := config.PapersDir()

	// Exact UUID/full filename lookup.
	if uuidPattern.MatchString(ref) {
		path := filepath.Join(dir, ref+".json")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	// Short UUID prefix lookup, e.g. /open a1b2c3d4.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		if strings.HasSuffix(e.Name(), ".json") && strings.HasPrefix(name, ref) {
			matches = append(matches, filepath.Join(dir, e.Name()))
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("paper ref %q is ambiguous", ref)
	}

	// Legacy numeric lookup: first old filename, then scan JSON id fields.
	legacyPath := filepath.Join(dir, ref+".json")
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath, nil
	}
	var id int
	if _, err := fmt.Sscanf(ref, "%d", &id); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			p, err := loadPaperPath(path)
			if err == nil && p.ID == id {
				return path, nil
			}
		}
	}

	return "", os.ErrNotExist
}

// activePaperPath returns the path to the active paper file.
func activePaperPath() string {
	return filepath.Join(config.ConfigDir(), "active_paper")
}

// SetActivePaper persists the currently active paper ID.
func SetActivePaper(ref string) error {
	dir := config.ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(activePaperPath(), []byte(strings.TrimSpace(ref)), 0644)
}

// GetActivePaper returns the persisted active paper ID, or empty string if none.
func GetActivePaper() string {
	data, err := os.ReadFile(activePaperPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ClearActivePaper removes the active paper persistence file.
func ClearActivePaper() error {
	return os.Remove(activePaperPath())
}

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexStr := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
}
