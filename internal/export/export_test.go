package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/session"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "Hello World"},
		{"File/Name", "File_Name"},
		{"File:Name?", "File_Name_"},
		{"Normal-Name", "Normal-Name"},
	}

	for _, tt := range tests {
		result := sanitizeFilename(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestEscapeYAML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{`hello "world"`, `hello \"world\"`},
		{`path\to\file`, `path\\to\\file`},
	}

	for _, tt := range tests {
		result := escapeYAML(tt.input)
		if result != tt.expected {
			t.Errorf("escapeYAML(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestExportToObsidian(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Obsidian: config.ObsidianConfig{
			ExportPath: filepath.Join(tmpDir, "Papers"),
		},
	}

	p := &session.Paper{
		ID:             1,
		Title:          "Test Paper",
		SourceURL:      "https://example.com",
		Content:        "paper content",
		InitialSummary: "This is a summary",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Messages: []session.Message{
			// Round 0: paper content + initial summary. Should NOT appear in 问答记录.
			{RoundNumber: 0, Role: "user", Content: "FULL_PAPER_TEXT", TokenCount: 10, CreatedAt: time.Now()},
			{RoundNumber: 0, Role: "assistant", Content: "INITIAL_SUMMARY_DUP", TokenCount: 20, CreatedAt: time.Now()},
			// Round 1: real Q&A. Should appear in 问答记录.
			{RoundNumber: 1, Role: "user", Content: "What is this?", TokenCount: 10, CreatedAt: time.Now()},
			{RoundNumber: 1, Role: "assistant", Content: "This is a test.", TokenCount: 20, CreatedAt: time.Now()},
		},
	}

	path, err := ExportToObsidian(cfg, p)
	if err != nil {
		t.Fatalf("export error: %v", err)
	}

	if !strings.HasSuffix(path, "_session.md") {
		t.Errorf("expected .md file, got %s", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	s := string(content)
	if !strings.Contains(s, "title: \"Test Paper\"") {
		t.Error("missing title in frontmatter")
	}
	if !strings.Contains(s, "source_url:") {
		t.Error("missing source_url in frontmatter")
	}
	if !strings.Contains(s, "# Test Paper") {
		t.Error("missing title heading")
	}
	if !strings.Contains(s, "## 论文总结") {
		t.Error("missing summary section")
	}
	if !strings.Contains(s, "This is a summary") {
		t.Error("missing summary content")
	}
	if !strings.Contains(s, "## 问答记录") {
		t.Error("missing Q&A section")
	}
	if !strings.Contains(s, "What is this?") {
		t.Error("missing question content")
	}
	if !strings.Contains(s, "This is a test.") {
		t.Error("missing answer content")
	}
	if strings.Contains(s, "FULL_PAPER_TEXT") {
		t.Error("round 0 paper content should not appear in 问答记录")
	}
	if strings.Contains(s, "INITIAL_SUMMARY_DUP") {
		t.Error("round 0 assistant message should not appear in 问答记录")
	}
	if strings.Contains(s, "第 0 轮") {
		t.Error("round 0 should not appear as a Q&A round")
	}
	if !strings.Contains(s, "第 1 轮") {
		t.Error("round 1 heading missing from 问答记录")
	}
}

func TestExportNoVaultPath(t *testing.T) {
	cfg := &config.Config{
		Obsidian: config.ObsidianConfig{
			ExportPath: "",
		},
	}
	p := &session.Paper{ID: 1, Title: "Test"}

	_, err := ExportToObsidian(cfg, p)
	if err == nil {
		t.Error("expected error for empty export path")
	}
}

func TestExportNoTitle(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Obsidian: config.ObsidianConfig{
			ExportPath: filepath.Join(tmpDir, "Papers"),
		},
	}
	p := &session.Paper{ID: 42, Content: "test"}

	path, err := ExportToObsidian(cfg, p)
	if err != nil {
		t.Fatalf("export error: %v", err)
	}

	if !strings.Contains(path, "Paper_42") {
		t.Errorf("expected Paper_42 in filename, got %s", path)
	}
}

func TestExportCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	exportPath := filepath.Join(tmpDir, "NewVault", "Research", "Papers")

	cfg := &config.Config{
		Obsidian: config.ObsidianConfig{
			ExportPath: exportPath,
		},
	}
	p := &session.Paper{ID: 1, Title: "Test"}

	_, err := ExportToObsidian(cfg, p)
	if err != nil {
		t.Fatalf("export should create dirs, got error: %v", err)
	}

	if _, err := os.Stat(exportPath); os.IsNotExist(err) {
		t.Error("export directory should have been created")
	}
}
