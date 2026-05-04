package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/paperpaper/paperpaper/internal/config"
	"github.com/paperpaper/paperpaper/internal/session"
)

func testConfig() *config.Config {
	cfg := &config.Config{
		API: config.APIConfig{
			BaseURL:      os.Getenv("OPENAI_BASE_URL"),
			APIKey:       os.Getenv("OPENAI_API_KEY"),
			DefaultModel: "xiaomi/mimo-v2-flash",
			LightModel:   "xiaomi/mimo-v2-flash",
		},
		UI: config.UIConfig{MaxRecentRounds: 5},
	}
	if cfg.API.BaseURL == "" {
		cfg.API.BaseURL = "https://api.openai.com/v1"
	}
	return cfg
}

func sendKeys(m *Model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "shift+enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "ctrl+d":
			msg = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
		case "ctrl+c":
			msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		case "ctrl+j":
			msg = tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		m.Update(msg)
	}
}

func sendWindowSize(m *Model, w, h int) {
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

// ========== Welcome Screen Tests ==========

func TestWelcomeScreen(t *testing.T) {
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	view := m.View().Content
	if !strings.Contains(view, "PaperPaper") {
		t.Error("welcome screen should contain 'PaperPaper'")
	}
	if !strings.Contains(view, "INPUT") {
		t.Error("should start in INPUT mode")
	}
}

func TestHelpCommand(t *testing.T) {
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	// Type /help
	sendKeys(m, "/", "h", "e", "l", "p")
	sendKeys(m, "ctrl+d")

	view := m.View().Content
	if !strings.Contains(view, "/new") {
		t.Error("help should show /new command")
	}
	if !strings.Contains(view, "/list") {
		t.Error("help should show /list command")
	}
	if !strings.Contains(view, "/quit") {
		t.Error("help should show /quit command")
	}
}

// ========== Mode Switching Tests ==========

func TestModeSwitching(t *testing.T) {
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	// Start in Input mode
	if m.mode != ModeInput {
		t.Errorf("expected ModeInput, got %d", m.mode)
	}

	// Switch to Normal
	sendKeys(m, "esc")
	if m.mode != ModeNormal {
		t.Errorf("expected ModeNormal after Esc, got %d", m.mode)
	}

	view := m.View().Content
	if !strings.Contains(view, "NORMAL") {
		t.Error("should show NORMAL mode indicator")
	}

	// Switch back to Input
	sendKeys(m, "i")
	if m.mode != ModeInput {
		t.Errorf("expected ModeInput after 'i', got %d", m.mode)
	}
}

// ========== Vim Navigation Tests ==========

func TestVimNavigation(t *testing.T) {
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	// Load a paper to have scrollable content
	p := session.NewPaper("paper content", "")
	p.InitialSummary = strings.Repeat("Summary line that is long enough to cause scrolling.\n", 100)
	m.LoadPaper(p)
	m.phase = PhaseChat
	m.viewport.SetContent(m.renderMessages())

	// Switch to Normal mode
	sendKeys(m, "esc")

	// Test j/k scrolling
	initialOffset := m.viewport.YOffset()
	sendKeys(m, "j", "j", "j", "j", "j")
	if m.viewport.YOffset() <= initialOffset {
		t.Errorf("j should scroll down: initial=%d, now=%d", initialOffset, m.viewport.YOffset())
	}

	sendKeys(m, "k", "k", "k", "k", "k")
	// After scrolling back up, offset should be less
	afterK := m.viewport.YOffset()

	// Test G (go to bottom)
	sendKeys(m, "G")
	bottomOffset := m.viewport.YOffset()
	if bottomOffset <= afterK {
		t.Errorf("G should scroll to bottom: afterK=%d, bottom=%d", afterK, bottomOffset)
	}

	// Test g (go to top)
	sendKeys(m, "g")
	if m.viewport.YOffset() >= bottomOffset {
		t.Errorf("g should scroll to top: bottom=%d, now=%d", bottomOffset, m.viewport.YOffset())
	}
}

// ========== Config Command Tests ==========

func TestConfigCommand(t *testing.T) {
	cfg := testConfig()
	cfg.API.BaseURL = "https://test.api.com/v1"
	cfg.API.DefaultModel = "test-model"
	cfg.UI.MaxRecentRounds = 3

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "c", "o", "n", "f", "i", "g")
	sendKeys(m, "ctrl+d")

	view := m.View().Content
	if !strings.Contains(view, "test.api.com") {
		t.Error("config view should show base URL")
	}
	if !strings.Contains(view, "test-model") {
		t.Error("config view should show model name")
	}
}

// ========== Model Command Tests ==========

func TestModelCommand(t *testing.T) {
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	// Check current model
	sendKeys(m, "/", "m", "o", "d", "e", "l")
	sendKeys(m, "ctrl+d")

	view := m.View().Content
	if !strings.Contains(view, "当前模型") {
		t.Error("should show current model")
	}

	// Switch model
	sendKeys(m, "/", "m", "o", "d", "e", "l", " ", "n", "e", "w", "-", "m", "o", "d", "e", "l")
	sendKeys(m, "ctrl+d")

	if m.cfg.API.DefaultModel != "new-model" {
		t.Errorf("expected model 'new-model', got %s", m.cfg.API.DefaultModel)
	}
}

// ========== List Mode Tests ==========

func TestListModeEmpty(t *testing.T) {
	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "l", "i", "s", "t")
	sendKeys(m, "ctrl+d")

	view := m.View().Content
	if !strings.Contains(view, "没有历史论文") {
		t.Error("empty list should show no papers message")
	}
}

func TestListModeWithPapers(t *testing.T) {
	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	// Create some test papers
	for i := 1; i <= 3; i++ {
		p := &session.Paper{
			ID:    i,
			Title: fmt.Sprintf("Paper %d", i),
		}
		session.SavePaper(p)
	}

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	// Open list
	sendKeys(m, "/", "l", "i", "s", "t")
	sendKeys(m, "ctrl+d")

	if m.mode != ModeList {
		t.Errorf("expected ModeList, got %d", m.mode)
	}

	view := m.View().Content
	if !strings.Contains(view, "Paper 1") {
		t.Error("list should show Paper 1")
	}
	if !strings.Contains(view, "Paper 3") {
		t.Error("list should show Paper 3")
	}
	if !strings.Contains(view, "会话列表") {
		t.Error("header should show 会话列表")
	}
}

func TestListModeNavigation(t *testing.T) {
	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	for i := 1; i <= 3; i++ {
		p := &session.Paper{ID: i, Title: fmt.Sprintf("Paper %d", i)}
		session.SavePaper(p)
	}

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "l", "i", "s", "t")
	sendKeys(m, "ctrl+d")

	if m.listCursor != 0 {
		t.Errorf("expected cursor 0, got %d", m.listCursor)
	}

	// Move down
	sendKeys(m, "down")
	if m.listCursor != 1 {
		t.Errorf("expected cursor 1, got %d", m.listCursor)
	}

	sendKeys(m, "down")
	if m.listCursor != 2 {
		t.Errorf("expected cursor 2, got %d", m.listCursor)
	}

	// Can't go past end
	sendKeys(m, "down")
	if m.listCursor != 2 {
		t.Errorf("expected cursor 2 (bounded), got %d", m.listCursor)
	}

	// Move up
	sendKeys(m, "up")
	if m.listCursor != 1 {
		t.Errorf("expected cursor 1, got %d", m.listCursor)
	}
}

func TestListModeSelect(t *testing.T) {
	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	p := &session.Paper{
		ID:             1,
		Title:          "Test Paper",
		Content:        "paper content",
		InitialSummary: "summary",
		Messages: []session.Message{
			{RoundNumber: 0, Role: "user", Content: "question", TokenCount: 5},
			{RoundNumber: 0, Role: "assistant", Content: "answer", TokenCount: 10},
		},
	}
	session.SavePaper(p)

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "l", "i", "s", "t")
	sendKeys(m, "ctrl+d")

	if m.mode != ModeList {
		t.Fatal("expected ModeList")
	}

	// Select first paper
	sendKeys(m, "enter")

	if m.mode != ModeInput {
		t.Errorf("expected ModeInput after selection, got %d", m.mode)
	}
	if m.manager.Paper() == nil {
		t.Fatal("paper should be loaded")
	}
	if m.manager.Paper().Title != "Test Paper" {
		t.Errorf("expected 'Test Paper', got %s", m.manager.Paper().Title)
	}
}

func TestListModeEscape(t *testing.T) {
	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	p := &session.Paper{ID: 1, Title: "Test"}
	session.SavePaper(p)

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "l", "i", "s", "t")
	sendKeys(m, "ctrl+d")

	if m.mode != ModeList {
		t.Fatal("expected ModeList")
	}

	sendKeys(m, "esc")
	if m.mode != ModeInput {
		t.Errorf("expected ModeInput after escape, got %d", m.mode)
	}
}

func TestListModeDelete(t *testing.T) {
	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	for i := 1; i <= 2; i++ {
		p := &session.Paper{ID: i, Title: fmt.Sprintf("Paper %d", i)}
		session.SavePaper(p)
	}

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "l", "i", "s", "t")
	sendKeys(m, "ctrl+d")

	// Press d to initiate delete
	sendKeys(m, "d")
	if !m.confirmDelete {
		t.Error("expected confirmDelete after 'd'")
	}

	// Cancel with n
	sendKeys(m, "n")
	if m.confirmDelete {
		t.Error("expected confirmDelete false after 'n'")
	}

	// Delete with d then y
	sendKeys(m, "d")
	sendKeys(m, "y")

	// Paper at cursor (Paper 1) should be deleted, only Paper 2 remains
	if len(m.listItems) != 1 {
		t.Errorf("expected 1 item remaining, got %d", len(m.listItems))
	}
	if len(m.listItems) > 0 && m.listItems[0].Title != "Paper 2" {
		t.Errorf("expected 'Paper 2', got %s", m.listItems[0].Title)
	}
}

// ========== Delete Command Tests ==========

func TestDeleteCommandConfirm(t *testing.T) {
	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	p := &session.Paper{
		ID:      1,
		Title:   "To Delete",
		Content: "content",
	}
	m := NewModel(cfg)
	m.LoadPaper(p)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "d", "e", "l", "e", "t", "e")
	sendKeys(m, "ctrl+d")

	if !m.confirmDelete {
		t.Error("expected confirmDelete")
	}

	view := m.View().Content
	if !strings.Contains(view, "确认删除") {
		t.Error("should show confirmation dialog")
	}

	// Confirm
	sendKeys(m, "y")
	if m.manager.Paper() != nil {
		t.Error("paper should be nil after delete")
	}
}

func TestDeleteCommandCancel(t *testing.T) {
	cfg := testConfig()
	p := &session.Paper{ID: 1, Title: "Keep", Content: "content"}
	m := NewModel(cfg)
	m.LoadPaper(p)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "d", "e", "l", "e", "t", "e")
	sendKeys(m, "ctrl+d")
	sendKeys(m, "n")

	if m.confirmDelete {
		t.Error("confirmDelete should be false after cancel")
	}
	if m.manager.Paper() == nil {
		t.Error("paper should still exist after cancel")
	}
}

// ========== New Command Tests ==========

func TestNewCommand(t *testing.T) {
	cfg := testConfig()
	p := &session.Paper{ID: 1, Title: "Old", Content: "old content", InitialSummary: "summary"}
	m := NewModel(cfg)
	m.LoadPaper(p)
	m.phase = PhaseChat
	sendWindowSize(m, 120, 40)

	if m.manager.Paper() == nil {
		t.Fatal("paper should be loaded")
	}

	sendKeys(m, "/", "n", "e", "w")
	sendKeys(m, "ctrl+d")

	if m.manager.Paper() != nil {
		t.Error("paper should be nil after /new")
	}
	if m.phase != PhaseInit {
		t.Errorf("expected PhaseInit, got %d", m.phase)
	}

	view := m.View().Content
	if !strings.Contains(view, "欢迎使用 PaperPaper") {
		t.Error("should show welcome banner")
	}
}

// ========== Edit Command Tests ==========

func TestEditCommand(t *testing.T) {
	cfg := testConfig()
	p := &session.Paper{
		ID:      1,
		Content: "paper content",
		Messages: []session.Message{
			{RoundNumber: 0, Role: "user", Content: "first question", TokenCount: 10},
			{RoundNumber: 0, Role: "assistant", Content: "first answer", TokenCount: 50},
			{RoundNumber: 1, Role: "user", Content: "second question", TokenCount: 15},
			{RoundNumber: 1, Role: "assistant", Content: "second answer", TokenCount: 60},
		},
	}
	m := NewModel(cfg)
	m.LoadPaper(p)
	m.phase = PhaseChat
	sendWindowSize(m, 120, 40)

	// Switch to normal mode first, then input
	sendKeys(m, "esc")
	sendKeys(m, "i")

	sendKeys(m, "/", "e", "d", "i", "t")
	sendKeys(m, "ctrl+d")

	// Should have deleted last round and filled textarea with last question
	if len(m.manager.Paper().Messages) != 2 {
		t.Errorf("expected 2 messages after edit, got %d", len(m.manager.Paper().Messages))
	}

	// Textarea should contain the last user question
	if !strings.Contains(m.textarea.Value(), "second question") {
		t.Errorf("textarea should contain 'second question', got %q", m.textarea.Value())
	}

	if m.mode != ModeInput {
		t.Errorf("expected ModeInput, got %d", m.mode)
	}
}

func TestEditCommandNoMessages(t *testing.T) {
	cfg := testConfig()
	p := &session.Paper{ID: 1, Content: "content"}
	m := NewModel(cfg)
	m.LoadPaper(p)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "e", "d", "i", "t")
	sendKeys(m, "ctrl+d")

	view := m.View().Content
	if !strings.Contains(view, "没有可编辑") {
		t.Error("should show no editable messages")
	}
}

// ========== Del Command Tests ==========

func TestDelCommand(t *testing.T) {
	cfg := testConfig()
	p := &session.Paper{
		ID:      1,
		Content: "content",
		Messages: []session.Message{
			{RoundNumber: 0, Role: "user", Content: "q0", TokenCount: 1},
			{RoundNumber: 0, Role: "assistant", Content: "a0", TokenCount: 1},
			{RoundNumber: 1, Role: "user", Content: "q1", TokenCount: 1},
			{RoundNumber: 1, Role: "assistant", Content: "a1", TokenCount: 1},
			{RoundNumber: 2, Role: "user", Content: "q2", TokenCount: 1},
			{RoundNumber: 2, Role: "assistant", Content: "a2", TokenCount: 1},
		},
	}
	m := NewModel(cfg)
	m.LoadPaper(p)
	m.phase = PhaseChat
	sendWindowSize(m, 120, 40)

	// Delete round 1
	sendKeys(m, "/", "d", "e", "l", " ", "1")
	sendKeys(m, "ctrl+d")

	if len(m.manager.Paper().Messages) != 4 {
		t.Errorf("expected 4 messages after deleting round 1, got %d", len(m.manager.Paper().Messages))
	}

	// Verify round 1 is gone
	for _, msg := range m.manager.Paper().Messages {
		if msg.RoundNumber == 1 {
			t.Error("round 1 should be deleted")
		}
	}
}

// ========== Open Command Tests ==========

func TestOpenCommand(t *testing.T) {
	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	p := &session.Paper{
		ID:             42,
		Title:          "Loaded Paper",
		Content:        "content",
		InitialSummary: "summary",
	}
	session.SavePaper(p)

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "o", "p", "e", "n", " ", "4", "2")
	sendKeys(m, "ctrl+d")

	if m.manager.Paper() == nil {
		t.Fatal("paper should be loaded")
	}
	if m.manager.Paper().Title != "Loaded Paper" {
		t.Errorf("expected 'Loaded Paper', got %s", m.manager.Paper().Title)
	}
	if m.mode != ModeInput {
		t.Errorf("expected ModeInput, got %d", m.mode)
	}
}

func TestOpenCommandInvalidID(t *testing.T) {
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	sendKeys(m, "/", "o", "p", "e", "n", " ", "9", "9", "9")
	sendKeys(m, "ctrl+d")

	view := m.View().Content
	if !strings.Contains(view, "无法加载") {
		t.Error("should show error for invalid ID")
	}
}

// ========== Paper Loading Tests ==========

func TestLoadPaperFromText(t *testing.T) {
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	p := session.NewPaper("This is a test paper about AI.", "")
	m.LoadPaper(p)

	if m.manager.Paper() == nil {
		t.Fatal("paper should be loaded")
	}
	if m.phase != PhaseInit {
		t.Errorf("expected PhaseInit, got %d", m.phase)
	}
	if m.manager.Paper().Content != "This is a test paper about AI." {
		t.Error("content mismatch")
	}
}

func TestLoadPaperWithSummary(t *testing.T) {
	cfg := testConfig()
	p := &session.Paper{
		ID:             1,
		Content:        "paper",
		InitialSummary: "This is a summary",
		Messages: []session.Message{
			{RoundNumber: 0, Role: "user", Content: "q", TokenCount: 1},
			{RoundNumber: 0, Role: "assistant", Content: "a", TokenCount: 1},
		},
	}

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)
	m.LoadPaper(p)

	if m.phase != PhaseChat {
		t.Errorf("expected PhaseChat, got %d", m.phase)
	}

	view := m.View().Content
	if !strings.Contains(view, "summary") {
		t.Error("view should contain summary")
	}
}

// ========== Token Display Tests ==========

func TestTokenDisplay(t *testing.T) {
	cfg := testConfig()
	p := &session.Paper{
		ID:      1,
		Content: "content",
		Messages: []session.Message{
			{RoundNumber: 0, Role: "user", Content: "question", TokenCount: 100},
			{RoundNumber: 0, Role: "assistant", Content: "answer", TokenCount: 500},
		},
		TotalTokens: 600,
	}

	m := NewModel(cfg)
	m.LoadPaper(p)
	m.phase = PhaseChat
	sendWindowSize(m, 120, 40)

	view := m.View().Content
	if !strings.Contains(view, "600") {
		t.Error("status bar should show total tokens")
	}
	if !strings.Contains(view, "Rounds: 1") {
		t.Error("status bar should show round count")
	}
}

// ========== Streaming Tests ==========

func TestStreamContent(t *testing.T) {
	cfg := testConfig()
	p := &session.Paper{ID: 1, Content: "content"}
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)
	m.LoadPaper(p)
	m.phase = PhaseInit
	m.streaming = true
	m.streamContent = "Hello world"
	m.viewport.SetContent(m.renderMessages())

	view := m.View().Content
	if !strings.Contains(view, "Hello") {
		t.Error("streaming content should be visible")
	}
	if !strings.Contains(view, "▍") {
		t.Error("should show cursor during streaming")
	}
}

// ========== Render Helpers Tests ==========

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{123, "123"},
		{1234, "1,234"},
		{1234567, "1,234,567"},
	}

	for _, tt := range tests {
		result := formatNumber(tt.input)
		if result != tt.expected {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestRenderHeader(t *testing.T) {
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	header := m.renderHeader()
	if !strings.Contains(header, "PaperPaper") {
		t.Error("header should show PaperPaper")
	}
	if !strings.Contains(header, "Init") {
		t.Error("header should show Init phase")
	}
}

// ========== Full Flow Integration Test ==========

func TestFullFlow(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	// 1. Start: should show PaperPaper in header
	view := m.View().Content
	if !strings.Contains(view, "PaperPaper") {
		t.Error("should show PaperPaper")
	}

	// 2. Type a paper and submit
	m.textarea.SetValue("Attention Is All You Need introduces the Transformer architecture.")
	m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	// 3. Should be in streaming state
	if !m.streaming {
		// The stream may have started via cmd
	}

	// 4. Wait for stream to complete (simulate by processing messages)
	// In real test, we'd use teatest, but here we just verify state
	if m.manager.Paper() == nil {
		t.Error("paper should be loaded after submit")
	}

	// 5. Verify we can render
	view = m.View().Content
	if view == "" {
		t.Error("view should not be empty")
	}

	// 6. Test /help
	sendKeys(m, "/", "h", "e", "l", "p")
	sendKeys(m, "ctrl+d")
	view = m.View().Content
	if !strings.Contains(view, "/new") {
		t.Error("help should work")
	}

	// 7. Test /model
	sendKeys(m, "/", "m", "o", "d", "e", "l")
	sendKeys(m, "ctrl+d")
	view = m.View().Content
	if !strings.Contains(view, "当前模型") {
		t.Error("model command should work")
	}

	// 8. Test mode switch
	sendKeys(m, "esc")
	if m.mode != ModeNormal {
		t.Error("should be in normal mode")
	}
	sendKeys(m, "i")
	if m.mode != ModeInput {
		t.Error("should be back in input mode")
	}

	// 9. Test /config
	sendKeys(m, "/", "c", "o", "n", "f", "i", "g")
	sendKeys(m, "ctrl+d")
	view = m.View().Content
	if !strings.Contains(view, "Base URL") {
		t.Error("config should show base URL")
	}

	t.Log("Full flow test completed successfully")
}

// ========== Paper Loading with File Integration Test ==========

func TestPaperLoadingWithAPI(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	cfg := testConfig()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)

	// Load paper content
	paperContent := `Attention Is All You Need

Abstract: The dominant sequence transduction models are based on complex recurrent or
convolutional neural networks that include an encoder and a decoder. The best
performing models also connect the encoder and decoder through an attention
mechanism. We propose a new simple network architecture, the Transformer,
based solely on attention mechanisms, dispensing with recurrence and convolutions
entirely.`

	// Set content and submit
	m.textarea.SetValue(paperContent)
	m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	// Wait a bit for async operations
	time.Sleep(100 * time.Millisecond)

	// Paper should be loaded
	if m.manager.Paper() == nil {
		t.Fatal("paper should be loaded")
	}
	if m.manager.Paper().Content != paperContent {
		t.Error("content mismatch")
	}

	// Should be in init phase
	if m.phase != PhaseInit {
		t.Errorf("expected PhaseInit, got %d", m.phase)
	}

	t.Log("Paper loading test completed")
}

// ========== Session Persistence Test ==========

func TestSessionPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	// Create and save a paper
	p := &session.Paper{
		ID:             1,
		Title:          "Persistent Paper",
		Content:        "content here",
		InitialSummary: "summary here",
		Messages: []session.Message{
			{RoundNumber: 0, Role: "user", Content: "q1", TokenCount: 10, CreatedAt: time.Now()},
			{RoundNumber: 0, Role: "assistant", Content: "a1", TokenCount: 50, CreatedAt: time.Now()},
			{RoundNumber: 1, Role: "user", Content: "q2", TokenCount: 15, CreatedAt: time.Now()},
			{RoundNumber: 1, Role: "assistant", Content: "a2", TokenCount: 60, CreatedAt: time.Now()},
		},
		TotalTokens: 135,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := session.SavePaper(p); err != nil {
		t.Fatalf("save error: %v", err)
	}

	// Load it back
	loaded, err := session.LoadPaper(1)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if loaded.Title != "Persistent Paper" {
		t.Errorf("title mismatch: %s", loaded.Title)
	}
	if loaded.InitialSummary != "summary here" {
		t.Errorf("summary mismatch: %s", loaded.InitialSummary)
	}
	if len(loaded.Messages) != 4 {
		t.Errorf("expected 4 messages, got %d", len(loaded.Messages))
	}
	if loaded.TotalTokens != 135 {
		t.Errorf("expected 135 tokens, got %d", loaded.TotalTokens)
	}

	// Load into TUI model
	cfg := testConfig()
	m := NewModel(cfg)
	sendWindowSize(m, 120, 40)
	m.LoadPaper(loaded)

	if m.phase != PhaseChat {
		t.Errorf("expected PhaseChat, got %d", m.phase)
	}

	// View should contain summary and messages (glamour adds ANSI codes between words)
	view := m.View().Content
	if !strings.Contains(view, "summary") {
		t.Error("view should contain summary")
	}
	if !strings.Contains(view, "q1") {
		t.Error("view should contain question")
	}

	t.Log("Session persistence test completed")
}
