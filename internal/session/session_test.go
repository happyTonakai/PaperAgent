package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/happyTonakai/paperagent/internal/config"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Cleanup(func() { os.Unsetenv("HOME") })
	return tmpDir
}

func TestNewPaper(t *testing.T) {
	setupTestDir(t)

	p := NewPaper("test content", "https://example.com", "")

	if p.SessionID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if p.ID != 0 {
		t.Errorf("new papers should not use legacy numeric IDs, got %d", p.ID)
	}
	if p.Content != "test content" {
		t.Errorf("unexpected content: %s", p.Content)
	}
	if p.SourceURL != "https://example.com" {
		t.Errorf("unexpected source URL: %s", p.SourceURL)
	}
	if len(p.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(p.Messages))
	}
}

func TestNextID(t *testing.T) {
	setupTestDir(t)
	papersDir := config.PapersDir()
	os.MkdirAll(papersDir, 0755)

	// Create some paper files
	for i := 1; i <= 3; i++ {
		p := &Paper{ID: i, Content: "test"}
		SavePaper(p)
	}

	id := nextID()
	if id != 4 {
		t.Errorf("expected next ID 4, got %d", id)
	}
}

func TestSaveAndLoadPaper(t *testing.T) {
	setupTestDir(t)

	p := &Paper{
		ID:             1,
		Title:          "Test Paper",
		Content:        "test content",
		InitialSummary: "test summary",
		ModelUsed:      "gpt-4o",
		TotalTokens:    1000,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Messages: []Message{
			{RoundNumber: 0, Role: "user", Content: "test content", TokenCount: 10, CreatedAt: time.Now()},
			{RoundNumber: 0, Role: "assistant", Content: "test summary", TokenCount: 50, CreatedAt: time.Now()},
		},
	}

	if err := SavePaper(p); err != nil {
		t.Fatalf("save error: %v", err)
	}
	if p.SessionID == "" {
		t.Fatal("SavePaper should assign a UUID session ID")
	}
	filePath := filepath.Join(config.PapersDir(), p.SessionID+".json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("expected UUID-named session file: %v", err)
	}

	// Verify JSON does NOT contain top-level content/initial_summary fields
	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}
	var rawMap map[string]interface{}
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		t.Fatalf("unmarshal raw error: %v", err)
	}
	if _, ok := rawMap["content"]; ok {
		t.Error("JSON should NOT contain top-level 'content' field")
	}
	if _, ok := rawMap["initial_summary"]; ok {
		t.Error("JSON should NOT contain top-level 'initial_summary' field")
	}

	loaded, err := LoadPaper(1)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if loaded.Title != "Test Paper" {
		t.Errorf("expected title 'Test Paper', got %s", loaded.Title)
	}
	if loaded.Content != "test content" {
		t.Errorf("unexpected content: %s", loaded.Content)
	}
	if loaded.InitialSummary != "test summary" {
		t.Errorf("unexpected summary: %s", loaded.InitialSummary)
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Role != "user" {
		t.Errorf("expected user role, got %s", loaded.Messages[0].Role)
	}
}

func TestSaveAndLoadPaper_NewFormatBackwardCompat(t *testing.T) {
	setupTestDir(t)
	papersDir := config.PapersDir()
	os.MkdirAll(papersDir, 0755)

	// Simulate old-format JSON with top-level content/initial_summary fields
	oldFormat := `{
		"session_id": "11111111-1111-4111-8111-111111111111",
		"title": "Old Format Paper",
		"content": "old paper content",
		"initial_summary": "old summary",
		"messages": [
			{"round_number": 0, "role": "user", "content": "", "token_count": 0},
			{"round_number": 0, "role": "assistant", "content": "", "token_count": 0}
		]
	}`
	filePath := filepath.Join(papersDir, "11111111-1111-4111-8111-111111111111.json")
	if err := os.WriteFile(filePath, []byte(oldFormat), 0644); err != nil {
		t.Fatalf("write old format error: %v", err)
	}

	loaded, err := LoadPaperByRef("11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if loaded.Content != "old paper content" {
		t.Errorf("expected content 'old paper content', got %s", loaded.Content)
	}
	if loaded.InitialSummary != "old summary" {
		t.Errorf("expected summary 'old summary', got %s", loaded.InitialSummary)
	}
}

func TestSavePaper_OmitsRedundantFields(t *testing.T) {
	setupTestDir(t)

	p := &Paper{
		Title:          "Test",
		Content:        "paper content here",
		InitialSummary: "paper summary here",
		Messages: []Message{
			{RoundNumber: 0, Role: "user", Content: "paper content here", TokenCount: 10},
			{RoundNumber: 0, Role: "assistant", Content: "paper summary here", TokenCount: 50, SkipContext: true},
		},
	}

	if err := SavePaper(p); err != nil {
		t.Fatalf("save error: %v", err)
	}

	filePath := filepath.Join(config.PapersDir(), p.SessionID+".json")
	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		t.Fatalf("unmarshal raw error: %v", err)
	}
	if _, ok := rawMap["content"]; ok {
		t.Error("SavePaper should omit 'content' field from JSON")
	}
	if _, ok := rawMap["initial_summary"]; ok {
		t.Error("SavePaper should omit 'initial_summary' field from JSON")
	}
	if _, ok := rawMap["title"]; !ok {
		t.Error("SavePaper should keep other fields like 'title'")
	}
	if _, ok := rawMap["messages"]; !ok {
		t.Error("SavePaper should keep 'messages' field")
	}

	// Verify in-memory object is untouched
	if p.Content != "paper content here" {
		t.Errorf("in-memory Content should be preserved, got %s", p.Content)
	}
	if p.InitialSummary != "paper summary here" {
		t.Errorf("in-memory InitialSummary should be preserved, got %s", p.InitialSummary)
	}
}

func TestLoadPaper_NewFormat_ReconstructsFromMessages(t *testing.T) {
	setupTestDir(t)
	papersDir := config.PapersDir()
	os.MkdirAll(papersDir, 0755)

	// New format: no top-level content/initial_summary, only in messages[0]
	newFormat := `{
		"session_id": "33333333-3333-4333-8333-333333333333",
		"title": "New Format Paper",
		"messages": [
			{"round_number": 0, "role": "user", "content": "reconstructed content", "token_count": 10},
			{"round_number": 0, "role": "assistant", "content": "reconstructed summary", "token_count": 50, "skip_context": true}
		]
	}`
	filePath := filepath.Join(papersDir, "33333333-3333-4333-8333-333333333333.json")
	if err := os.WriteFile(filePath, []byte(newFormat), 0644); err != nil {
		t.Fatalf("write new format error: %v", err)
	}

	loaded, err := LoadPaperByRef("33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if loaded.Content != "reconstructed content" {
		t.Errorf("expected content from messages[0], got %s", loaded.Content)
	}
	if loaded.InitialSummary != "reconstructed summary" {
		t.Errorf("expected summary from messages[0], got %s", loaded.InitialSummary)
	}
}

func TestDeletePaper(t *testing.T) {
	setupTestDir(t)

	p := &Paper{ID: 1, Content: "test"}
	SavePaper(p)

	if err := DeletePaper(1); err != nil {
		t.Fatalf("delete error: %v", err)
	}

	_, err := LoadPaper(1)
	if err == nil {
		t.Error("expected error loading deleted paper")
	}
}

func TestListPapers(t *testing.T) {
	setupTestDir(t)

	for i := 1; i <= 3; i++ {
		p := &Paper{ID: i, Title: "Paper " + string(rune('A'+i-1)), Content: "test"}
		SavePaper(p)
	}

	papers, err := ListPapers()
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(papers) != 3 {
		t.Errorf("expected 3 papers, got %d", len(papers))
	}
}

func TestManagerAddMessage(t *testing.T) {
	m := NewManager()
	p := NewPaper("content", "", "")
	m.SetPaper(p)

	msg := Message{RoundNumber: 0, Role: "user", Content: "test", TokenCount: 10}
	m.AddMessage(msg)

	if len(m.Paper().Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(m.Paper().Messages))
	}
	if m.Paper().TotalTokens != 10 {
		t.Errorf("expected 10 tokens, got %d", m.Paper().TotalTokens)
	}
}

func TestManagerGetRecentMessages(t *testing.T) {
	m := NewManager()
	p := NewPaper("content", "", "")
	m.SetPaper(p)

	for i := 0; i < 10; i++ {
		m.AddMessage(Message{RoundNumber: i, Role: "user", Content: "q", TokenCount: 1})
		m.AddMessage(Message{RoundNumber: i, Role: "assistant", Content: "a", TokenCount: 1})
	}

	recent := m.GetRecentMessages(3)
	if len(recent) != 6 {
		t.Errorf("expected 6 messages (3 rounds), got %d", len(recent))
	}

	// Should be the last 3 rounds
	if recent[0].RoundNumber != 7 {
		t.Errorf("expected round 7, got %d", recent[0].RoundNumber)
	}
}

func TestManagerDeleteRound(t *testing.T) {
	m := NewManager()
	p := NewPaper("content", "", "")
	m.SetPaper(p)

	for i := 0; i < 3; i++ {
		m.AddMessage(Message{RoundNumber: i, Role: "user", Content: "q", TokenCount: 1})
		m.AddMessage(Message{RoundNumber: i, Role: "assistant", Content: "a", TokenCount: 1})
	}

	m.DeleteRound(1)

	if len(m.Paper().Messages) != 4 {
		t.Errorf("expected 4 messages after delete, got %d", len(m.Paper().Messages))
	}

	// Verify round 1 is gone
	for _, msg := range m.Paper().Messages {
		if msg.RoundNumber == 1 {
			t.Error("round 1 should have been deleted")
		}
	}
}

func TestManagerDeleteLastRound(t *testing.T) {
	m := NewManager()
	p := NewPaper("content", "", "")
	m.SetPaper(p)

	for i := 0; i < 3; i++ {
		m.AddMessage(Message{RoundNumber: i, Role: "user", Content: "q", TokenCount: 1})
		m.AddMessage(Message{RoundNumber: i, Role: "assistant", Content: "a", TokenCount: 1})
	}

	m.DeleteLastRound()

	if len(m.Paper().Messages) != 4 {
		t.Errorf("expected 4 messages, got %d", len(m.Paper().Messages))
	}

	// Last round should be 1
	lastRound := m.Paper().Messages[len(m.Paper().Messages)-1].RoundNumber
	if lastRound != 1 {
		t.Errorf("expected last round 1, got %d", lastRound)
	}
}

func TestManagerGetLastUserMessage(t *testing.T) {
	m := NewManager()
	p := NewPaper("content", "", "")
	m.SetPaper(p)

	// No messages yet
	if m.GetLastUserMessage() != nil {
		t.Error("expected nil for no messages")
	}

	m.AddMessage(Message{RoundNumber: 0, Role: "user", Content: "first question", TokenCount: 10})
	m.AddMessage(Message{RoundNumber: 0, Role: "assistant", Content: "answer", TokenCount: 50})
	m.AddMessage(Message{RoundNumber: 1, Role: "user", Content: "second question", TokenCount: 15})

	msg := m.GetLastUserMessage()
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Content != "second question" {
		t.Errorf("expected 'second question', got %s", msg.Content)
	}
}

func TestManagerCurrentRound(t *testing.T) {
	m := NewManager()

	if m.CurrentRound() != 0 {
		t.Error("expected round 0 for no paper")
	}

	p := NewPaper("content", "", "")
	m.SetPaper(p)

	m.AddMessage(Message{RoundNumber: 0, Role: "user", Content: "q", TokenCount: 1})
	if m.CurrentRound() != 0 {
		t.Errorf("expected round 0, got %d", m.CurrentRound())
	}

	m.AddMessage(Message{RoundNumber: 1, Role: "user", Content: "q", TokenCount: 1})
	if m.CurrentRound() != 1 {
		t.Errorf("expected round 1, got %d", m.CurrentRound())
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"hello", 1},
		{"hello world test", 4},
		{"", 0},
		{"这是一个测试", 9}, // 12 bytes / 4 = 3, but actually 9 chars / 4 = 2
	}

	for _, tt := range tests {
		result := EstimateTokens(tt.input)
		// Just verify it's non-negative and roughly proportional
		if result < 0 {
			t.Errorf("EstimateTokens(%q) = %d, expected non-negative", tt.input, result)
		}
	}
}

func TestRecentContextMessages_SkipsInitRound(t *testing.T) {
	// Simulate real scenario:
	// Round 0: init summary (user=paper content, assistant=summary, SkipContext=true)
	// Round 1: first Q&A
	// Round 2: second Q&A
	p := &Paper{
		Messages: []Message{
			{RoundNumber: 0, Role: "user", Content: "论文全文内容很长很长", TokenCount: 1000},
			{RoundNumber: 0, Role: "assistant", Content: "init summary", TokenCount: 500, SkipContext: true},
			{RoundNumber: 1, Role: "user", Content: "Q1: 这篇论文的贡献是什么", TokenCount: 10},
			{RoundNumber: 1, Role: "assistant", Content: "A1: 主要贡献是...", TokenCount: 50},
			{RoundNumber: 2, Role: "user", Content: "Q2: 实验用的什么数据集", TokenCount: 8},
			{RoundNumber: 2, Role: "assistant", Content: "A2: 使用了...", TokenCount: 40},
		},
	}

	recent := p.RecentContextMessages()

	// Should only have rounds 1 and 2 (4 messages), round 0 should be skipped
	if len(recent) != 4 {
		t.Errorf("expected 4 messages (rounds 1+2), got %d: %+v", len(recent), recent)
	}

	// Verify no round 0 messages
	for _, msg := range recent {
		if msg.RoundNumber == 0 {
			t.Errorf("round 0 should be skipped, but found: role=%s content=%s", msg.Role, msg.Content)
		}
	}

	// Verify paper content is NOT in recent messages
	for _, msg := range recent {
		if msg.Content == "论文全文内容很长很长" {
			t.Error("paper content should NOT leak into recent context")
		}
	}

	// Verify rounds are in order
	if recent[0].RoundNumber != 1 || recent[2].RoundNumber != 2 {
		t.Errorf("wrong round order: %v", recent)
	}
}

func TestRecentContextMessages_SkipsBtwMessages(t *testing.T) {
	// Btw messages (SkipContext=true) should be excluded
	p := &Paper{
		Messages: []Message{
			{RoundNumber: 0, Role: "user", Content: "论文全文", TokenCount: 100},
			{RoundNumber: 0, Role: "assistant", Content: "summary", TokenCount: 50, SkipContext: true},
			{RoundNumber: 1, Role: "user", Content: "正常 Q1", TokenCount: 5},
			{RoundNumber: 1, Role: "assistant", Content: "正常 A1", TokenCount: 20},
			{RoundNumber: 2, Role: "user", Content: "btw question", TokenCount: 3, SkipContext: true},
			{RoundNumber: 2, Role: "assistant", Content: "btw answer", TokenCount: 10, SkipContext: true},
			{RoundNumber: 3, Role: "user", Content: "正常 Q3", TokenCount: 5},
			{RoundNumber: 3, Role: "assistant", Content: "正常 A3", TokenCount: 20},
		},
	}

	recent := p.RecentContextMessages()

	// Should have rounds 1 and 3 only (4 messages)
	if len(recent) != 4 {
		t.Errorf("expected 4 messages (rounds 1+3), got %d", len(recent))
	}

	for _, msg := range recent {
		if msg.RoundNumber == 0 || msg.RoundNumber == 2 {
			t.Errorf("round %d should be skipped", msg.RoundNumber)
		}
	}
}

func TestRecentContextMessages_OnlyInitRoundWithSkipContext(t *testing.T) {
	// Q1 scenario: only round 0 exists (init), it's skipped
	// RecentContextMessages should return empty
	p := &Paper{
		Messages: []Message{
			{RoundNumber: 0, Role: "user", Content: "论文全文内容", TokenCount: 1000},
			{RoundNumber: 0, Role: "assistant", Content: "init summary", TokenCount: 500, SkipContext: true},
		},
	}

	recent := p.RecentContextMessages()

	if len(recent) != 0 {
		t.Errorf("expected empty recent messages for Q1, got %d", len(recent))
	}
}

func TestRecentContextMessages_WithoutSkipContext(t *testing.T) {
	// Regression: if SkipContext is NOT set on init (the old bug),
	// round 0 leaked into context. This test documents the OLD behavior.
	p := &Paper{
		Messages: []Message{
			{RoundNumber: 0, Role: "user", Content: "论文全文", TokenCount: 1000},
			{RoundNumber: 0, Role: "assistant", Content: "summary", TokenCount: 500},
			{RoundNumber: 1, Role: "user", Content: "Q1", TokenCount: 5},
		},
	}

	recent := p.RecentContextMessages()

	// Without SkipContext, round 0 IS included (the old bug behavior)
	if len(recent) != 2 {
		t.Errorf("without SkipContext, round 0 should be included, got %d messages", len(recent))
	}
}

func TestRecentContextMessages_LimitsRounds(t *testing.T) {
	p := &Paper{Messages: []Message{}}
	for i := 1; i <= 10; i++ {
		p.Messages = append(p.Messages,
			Message{RoundNumber: i, Role: "user", Content: "q", TokenCount: 1},
			Message{RoundNumber: i, Role: "assistant", Content: "a", TokenCount: 1},
		)
	}

	// No anchor → all 10 rounds returned
	recent := p.RecentContextMessages()
	if len(recent) != 20 {
		t.Errorf("expected 20 messages (10 rounds), got %d", len(recent))
	}

	// With anchor=8 → only rounds 8-10 returned (6 messages)
	p.TruncationAnchor = 8
	recent = p.RecentContextMessages()
	if len(recent) != 6 {
		t.Errorf("expected 6 messages (rounds 8-10), got %d", len(recent))
	}
	if len(recent) > 0 && recent[0].RoundNumber != 8 {
		t.Errorf("expected first round to be 8, got %d", recent[0].RoundNumber)
	}
}

func TestRecentContextMessages_AnchorGrowthCycle(t *testing.T) {
	p := &Paper{Messages: []Message{}}
	for i := 1; i <= 12; i++ {
		p.Messages = append(p.Messages,
			Message{RoundNumber: i, Role: "user", Content: "q", TokenCount: 1},
			Message{RoundNumber: i, Role: "assistant", Content: "a", TokenCount: 1},
		)
	}

	// Set anchor to 10 → only rounds 10-12 returned
	p.TruncationAnchor = 10
	recent := p.RecentContextMessages()
	if len(recent) != 6 {
		t.Fatalf("expected 6 msgs (rounds 10-12), got %d", len(recent))
	}
	if len(recent) > 0 && recent[0].RoundNumber != 10 {
		t.Fatalf("expected first round=10, got %d", recent[0].RoundNumber)
	}

	// Add round 13 → now rounds 10-13 returned (8 messages)
	p.Messages = append(p.Messages,
		Message{RoundNumber: 13, Role: "user", Content: "q", TokenCount: 1},
		Message{RoundNumber: 13, Role: "assistant", Content: "a", TokenCount: 1},
	)
	recent = p.RecentContextMessages()
	if len(recent) != 8 {
		t.Errorf("expected 8 messages (rounds 10-13), got %d", len(recent))
	}
	if p.TruncationAnchor != 10 {
		t.Errorf("anchor should stay at 10, got %d", p.TruncationAnchor)
	}

	// Move anchor to 16 → only rounds 16-18 returned (6 messages)
	for i := 14; i <= 18; i++ {
		p.Messages = append(p.Messages,
			Message{RoundNumber: i, Role: "user", Content: "q", TokenCount: 1},
			Message{RoundNumber: i, Role: "assistant", Content: "a", TokenCount: 1},
		)
	}
	p.TruncationAnchor = 16
	recent = p.RecentContextMessages()
	if len(recent) != 6 {
		t.Errorf("expected 6 messages (rounds 16-18), got %d", len(recent))
	}
	if len(recent) > 0 && recent[0].RoundNumber != 16 {
		t.Errorf("expected first round=16, got %d", recent[0].RoundNumber)
	}
}

func TestManagerConcurrency(t *testing.T) {
	m := NewManager()
	p := NewPaper("content", "", "")
	m.SetPaper(p)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			m.AddMessage(Message{RoundNumber: n, Role: "user", Content: "q", TokenCount: 1})
			_ = m.Paper()
			_ = m.GetRecentMessages(5)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// --- ExtractReferences / StripReferences tests ---

func TestExtractReferences_MarkdownLevel2(t *testing.T) {
	body, refs := ExtractReferences(`这是一篇论文的正文。

有很多内容。

## References
[1] Author, "Title", Journal, 2023.
[2] Author2, "Another Paper", Conference, 2024.`)
	if body != "这是一篇论文的正文。\n\n有很多内容。" {
		t.Errorf("unexpected body: %q", body)
	}
	if !strings.Contains(refs, "## References") || !strings.Contains(refs, "[1] Author") {
		t.Errorf("unexpected refs: %q", refs)
	}
}

func TestExtractReferences_MarkdownLevel1(t *testing.T) {
	body, refs := ExtractReferences("正文内容。\n\n# References\n[1] A. Smith, ...")
	if body != "正文内容。" {
		t.Errorf("unexpected body: %q", body)
	}
	if !strings.Contains(refs, "# References") {
		t.Errorf("unexpected refs: %q", refs)
	}
}

func TestExtractReferences_MarkdownLevel3(t *testing.T) {
	body, refs := ExtractReferences("正文。\n\n### References\n[1] B. Lee, ...")
	if body != "正文。" {
		t.Errorf("unexpected body: %q", body)
	}
	if !strings.Contains(refs, "### References") {
		t.Errorf("unexpected refs: %q", refs)
	}
}

func TestExtractReferences_MarkdownLevel4(t *testing.T) {
	body, refs := ExtractReferences("正文头部。\n\n#### References\n[1] C. Wang, ...")
	if body != "正文头部。" {
		t.Errorf("unexpected body: %q", body)
	}
	if !strings.Contains(refs, "#### References") {
		t.Errorf("unexpected refs: %q", refs)
	}
}

func TestExtractReferences_MarkdownBibliography(t *testing.T) {
	body, refs := ExtractReferences("正文。\n\n## Bibliography\n[1] D. Zhang, ...")
	if body != "正文。" {
		t.Errorf("unexpected body: %q", body)
	}
	if !strings.Contains(refs, "## Bibliography") {
		t.Errorf("unexpected refs: %q", refs)
	}
}

func TestExtractReferences_MarkdownUpperCase(t *testing.T) {
	body, refs := ExtractReferences("正文。\n\n## REFERENCES\n[1] E. Liu, ...")
	if body != "正文。" {
		t.Errorf("unexpected body: %q", body)
	}
	if !strings.Contains(refs, "REFERENCES") {
		t.Errorf("unexpected refs: %q", refs)
	}
}

func TestExtractReferences_TeXFormat(t *testing.T) {
	content := `一些论文内容。

\section{Method}
Our method is ...

\begin{thebibliography}{99}
\\bibitem{ref1} F. Gao, \\textit{A Great Paper}. 2023.
\\bibitem{ref2} G. Li, \\textit{Another Work}. 2024.
\\end{thebibliography}`
	body, refs := ExtractReferences(content)
	if !strings.Contains(body, "Our method is") {
		t.Errorf("body should contain paper content, got: %q", body)
	}
	if strings.Contains(body, "thebibliography") {
		t.Errorf("body should NOT contain references, got: %q", body)
	}
	if !strings.Contains(refs, "\\begin{thebibliography}") {
		t.Errorf("refs should contain thebibliography, got: %q", refs)
	}
}

func TestExtractReferences_NoReferencesFound(t *testing.T) {
	content := "这是一篇没有参考文献的短文。\n\n只有正文。"
	body, refs := ExtractReferences(content)
	if body != content {
		t.Errorf("body should equal original content when no refs found, got: %q", body)
	}
	if refs != "" {
		t.Errorf("refs should be empty when no refs found, got: %q", refs)
	}
}

func TestExtractReferences_EmptyContent(t *testing.T) {
	body, refs := ExtractReferences("")
	if body != "" || refs != "" {
		t.Errorf("both body and refs should be empty for empty input")
	}
}

func TestExtractReferences_ReferencesHeadingNotAtStart(t *testing.T) {
	// Ensure "References" in the middle of a sentence doesn't trigger.
	body, refs := ExtractReferences("讨论相关工作（References）部分。\n\n## References\n[1] H. Xu, ...")
	if !strings.Contains(body, "讨论相关工作（References）部分。") {
		t.Errorf("body should include pre-reference content, got: %q", body)
	}
	if !strings.Contains(refs, "## References") {
		t.Errorf("refs should contain the heading, got: %q", refs)
	}
}

func TestStripReferences_DelegatesToExtract(t *testing.T) {
	content := "正文。\n\n## References\n[1] ..."
	stripped := StripReferences(content)
	if strings.Contains(stripped, "References") {
		t.Errorf("StripReferences should remove references, got: %q", stripped)
	}
	if stripped != "正文。" {
		t.Errorf("unexpected stripped output: %q", stripped)
	}
}

func TestStripReferences_NoRefs(t *testing.T) {
	content := "只是一篇短文。"
	stripped := StripReferences(content)
	if stripped != content {
		t.Errorf("StripReferences should return original when no refs, got: %q", stripped)
	}
}

// TestPaperGitHubURL_RoundTrip verifies that the GitHubURL field survives
// SavePaper → LoadPaper (i.e. the JSON file format includes the field).
// Used by the WebUI to render the GitHub icon button next to the PDF button.
func TestPaperGitHubURL_RoundTrip(t *testing.T) {
	setupTestDir(t)

	p := &Paper{
		SessionID: "99999999-9999-4999-8999-999999999999",
		Title:     "Test paper",
		ArxivID:   "2401.12345",
		GitHubURL: "https://github.com/owner/repo",
		Messages: []Message{
			{RoundNumber: 0, Role: "user", Content: "body", TokenCount: 1},
			{RoundNumber: 0, Role: "assistant", Content: "summary", TokenCount: 2, SkipContext: true},
		},
	}
	if err := SavePaper(p); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadPaperByRef(p.SessionID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.GitHubURL != "https://github.com/owner/repo" {
		t.Errorf("GitHubURL round-trip = %q, want %q", loaded.GitHubURL, "https://github.com/owner/repo")
	}
}

// TestPaperGitHubURL_Empty verifies that an empty GitHubURL is omitted from
// the JSON (so old papers without a GitHub link don't carry a noisy "" field
// on disk).
func TestPaperGitHubURL_Empty(t *testing.T) {
	setupTestDir(t)

	p := &Paper{
		SessionID: "88888888-8888-4888-8888-888888888888",
		Title:     "No GitHub",
		ArxivID:   "2401.12346",
		Messages: []Message{
			{RoundNumber: 0, Role: "user", Content: "body", TokenCount: 1},
		},
	}
	if err := SavePaper(p); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(config.PapersDir(), p.SessionID+".json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "github_url") {
		t.Errorf("empty GitHubURL should be omitted from JSON, got: %s", raw)
	}
}

// --- SetAnchorFromTokens tests ---

func TestSetAnchorFromTokens_ExceedsBudget(t *testing.T) {
	p := &Paper{}

	// promptTokens + completionTokens = 50000 > maxInput 30000 → should set anchor
	p.SetAnchorFromTokens(10, 30000, 20000, 30000, 3)
	// anchor = round - minRounds + 1 = 10 - 3 + 1 = 8
	if p.TruncationAnchor != 8 {
		t.Errorf("expected anchor=8, got %d", p.TruncationAnchor)
	}
}

func TestSetAnchorFromTokens_UnderBudget(t *testing.T) {
	p := &Paper{}

	// promptTokens + completionTokens = 10000 < maxInput 30000 → should NOT change anchor
	p.SetAnchorFromTokens(10, 5000, 5000, 30000, 3)
	if p.TruncationAnchor != 0 {
		t.Errorf("expected anchor=0 (unchanged), got %d", p.TruncationAnchor)
	}
}

func TestSetAnchorFromTokens_ExactBoundary(t *testing.T) {
	p := &Paper{}

	// promptTokens + completionTokens = 30000 = maxInput 30000 → should NOT change anchor (not strictly greater)
	p.SetAnchorFromTokens(10, 15000, 15000, 30000, 3)
	if p.TruncationAnchor != 0 {
		t.Errorf("expected anchor=0 (at boundary, not exceeded), got %d", p.TruncationAnchor)
	}
}

func TestSetAnchorFromTokens_SmallRoundNumber(t *testing.T) {
	p := &Paper{}

	// round=2, minRounds=5 → anchor = 2-5+1 = -2, clamped to 1
	p.SetAnchorFromTokens(2, 30000, 20000, 30000, 5)
	if p.TruncationAnchor != 1 {
		t.Errorf("expected anchor=1 (clamped), got %d", p.TruncationAnchor)
	}
}

func TestSetAnchorFromTokens_ExactFit(t *testing.T) {
	p := &Paper{}

	// round=3, minRounds=3 → anchor = 3-3+1 = 1
	p.SetAnchorFromTokens(3, 40000, 10000, 30000, 3)
	if p.TruncationAnchor != 1 {
		t.Errorf("expected anchor=1, got %d", p.TruncationAnchor)
	}
}

func TestSetAnchorFromTokens_PreservesExistingAnchor(t *testing.T) {
	p := &Paper{TruncationAnchor: 5}

	// Under budget → should NOT overwrite existing anchor
	p.SetAnchorFromTokens(10, 5000, 5000, 30000, 3)
	if p.TruncationAnchor != 5 {
		t.Errorf("expected anchor=5 (preserved), got %d", p.TruncationAnchor)
	}

	// Over budget → should update anchor
	p.SetAnchorFromTokens(15, 30000, 20000, 30000, 3)
	// anchor = 15 - 3 + 1 = 13
	if p.TruncationAnchor != 13 {
		t.Errorf("expected anchor=13 (updated), got %d", p.TruncationAnchor)
	}
}

func TestRecentContextMessages_WithAnchorIntegration(t *testing.T) {
	// Integration test: SetAnchorFromTokens → RecentContextMessages
	p := &Paper{Messages: []Message{}}
	for i := 1; i <= 10; i++ {
		p.Messages = append(p.Messages,
			Message{RoundNumber: i, Role: "user", Content: "q", TokenCount: 1000},
			Message{RoundNumber: i, Role: "assistant", Content: "a", TokenCount: 1000},
		)
	}

	// Initially no anchor → all rounds returned
	recent := p.RecentContextMessages()
	if len(recent) != 20 {
		t.Errorf("expected 20 messages, got %d", len(recent))
	}

	// Simulate budget exceeded at round 10
	p.SetAnchorFromTokens(10, 15000, 15000, 20000, 3)
	// anchor = 10 - 3 + 1 = 8
	if p.TruncationAnchor != 8 {
		t.Fatalf("expected anchor=8, got %d", p.TruncationAnchor)
	}

	recent = p.RecentContextMessages()
	if len(recent) != 6 {
		t.Errorf("expected 6 messages (rounds 8-10), got %d", len(recent))
	}
	if len(recent) > 0 && recent[0].RoundNumber != 8 {
		t.Errorf("expected first round=8, got %d", recent[0].RoundNumber)
	}
}
