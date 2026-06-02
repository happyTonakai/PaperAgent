package api

import (
	"os"
	"strings"
	"testing"

	"github.com/happyTonakai/paperagent/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		API: config.APIConfig{
			BaseURL:      os.Getenv("OPENAI_BASE_URL"),
			APIKey:       os.Getenv("OPENAI_API_KEY"),
			DefaultModel: "xiaomi/mimo-v2-flash",
		},
	}
	if cfg.API.APIKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}
	if cfg.API.BaseURL == "" {
		cfg.API.BaseURL = "https://api.openai.com/v1"
	}
	return cfg
}

func TestChatIntegration(t *testing.T) {
	cfg := testConfig(t)
	client := NewClient(cfg)

	messages := []ChatMessage{
		{Role: "system", Content: "You are a helpful assistant. Reply concisely."},
		{Role: "user", Content: "What is 2+2? Reply with just the number."},
	}

	result, promptTokens, completionTokens, totalTokens, cachedTokens, err := client.Chat(cfg.API.DefaultModel, messages)
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty response")
	}
	if completionTokens < 0 {
		t.Errorf("expected non-negative tokens, got %d", completionTokens)
	}

	t.Logf("Response: %s (prompt: %d, completion: %d, total: %d, cached: %d)", result, promptTokens, completionTokens, totalTokens, cachedTokens)
}

func TestChatStreamIntegration(t *testing.T) {
	cfg := testConfig(t)
	client := NewClient(cfg)

	messages := []ChatMessage{
		{Role: "system", Content: "You are a helpful assistant. Reply concisely."},
		{Role: "user", Content: "Say 'hello' in one word."},
	}

	ch := client.ChatStream(cfg.API.DefaultModel, messages)

	var content strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		if chunk.Done {
			break
		}
		content.WriteString(chunk.Content)
	}

	if content.Len() == 0 {
		t.Error("expected non-empty streamed content")
	}

	t.Logf("Streamed: %s", content.String())
}

func TestExtractTitleIntegration(t *testing.T) {
	cfg := testConfig(t)
	client := NewClient(cfg)

	paperStart := `Attention Is All You Need
Ashish Vaswani, Noam Shazeer, Niki Parmar, Jakob Uszkoreit, Llion Jones, Aidan N. Gomez, Lukasz Kaiser, Illia Polosukhin
Google Brain, Google Research, University of Toronto`

	title, err := client.ExtractTitle(cfg.API.DefaultModel, paperStart)
	if err != nil {
		t.Fatalf("extract title error: %v", err)
	}

	if title == "" {
		t.Error("expected non-empty title")
	}

	t.Logf("Extracted title: %s", title)
}
