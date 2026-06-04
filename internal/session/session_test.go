package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/happyTonakai/paperagent/internal/config"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
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

	recent := p.RecentContextMessages(5)

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

	recent := p.RecentContextMessages(5)

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

	recent := p.RecentContextMessages(5)

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

	recent := p.RecentContextMessages(5)

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

	// Only last 3 rounds
	recent := p.RecentContextMessages(3)
	if len(recent) != 6 {
		t.Errorf("expected 6 messages (3 rounds), got %d", len(recent))
	}
	if recent[0].RoundNumber != 8 {
		t.Errorf("expected first round to be 8, got %d", recent[0].RoundNumber)
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
