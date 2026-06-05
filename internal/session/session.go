package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/urlparse"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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

// RecentContextMessages returns the last n rounds of messages where SkipContext is false.
// Each round consists of a user+assistant pair. Messages with SkipContext=true are skipped.
func (p *Paper) RecentContextMessages(n int) []Message {
	if p == nil || n <= 0 {
		return nil
	}
	// Walk backwards, collecting complete rounds (user+assistant) that are not skipped.
	var result []Message
	roundsFound := 0
	for i := len(p.Messages) - 1; i >= 0 && roundsFound < n; {
		if p.Messages[i].Role == "assistant" && !p.Messages[i].SkipContext {
			// Found an assistant message that is context-bearing. Look for its user counterpart.
			end := i + 1
			for i >= 0 && p.Messages[i].RoundNumber == p.Messages[end-1].RoundNumber {
				i--
			}
			start := i + 1
			result = append(p.Messages[start:end], result...)
			roundsFound++
		} else {
			i--
		}
	}
	return result
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
	return os.WriteFile(filepath.Join(dir, p.SessionID+".json"), data, 0644)
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
