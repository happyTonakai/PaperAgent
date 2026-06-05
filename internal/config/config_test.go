package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	// Clear env vars that override defaults
	origBaseURL := os.Getenv("OPENAI_BASE_URL")
	origModel := os.Getenv("OPENAI_MODEL_NAME")
	os.Unsetenv("OPENAI_BASE_URL")
	os.Unsetenv("OPENAI_MODEL_NAME")
	defer os.Setenv("OPENAI_BASE_URL", origBaseURL)
	defer os.Setenv("OPENAI_MODEL_NAME", origModel)

	cfg := defaultConfig()

	if cfg.API.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("expected default BaseURL, got %s", cfg.API.BaseURL)
	}
	if cfg.API.DefaultModel != "gpt-4o" {
		t.Errorf("expected default model gpt-4o, got %s", cfg.API.DefaultModel)
	}
	if cfg.UI.MinRecentRounds != 2 {
		t.Errorf("expected 2 min recent rounds, got %d", cfg.UI.MinRecentRounds)
	}
	if cfg.UI.MaxInputTokens != 30000 {
		t.Errorf("expected 30000 max input tokens, got %d", cfg.UI.MaxInputTokens)
	}
}

func TestDefaultConfigEnvOverride(t *testing.T) {
	origBaseURL := os.Getenv("OPENAI_BASE_URL")
	origModel := os.Getenv("OPENAI_MODEL_NAME")
	os.Setenv("OPENAI_BASE_URL", "https://custom.api.com/v1")
	os.Setenv("OPENAI_MODEL_NAME", "custom-model")
	defer os.Setenv("OPENAI_BASE_URL", origBaseURL)
	defer os.Setenv("OPENAI_MODEL_NAME", origModel)

	cfg := defaultConfig()

	if cfg.API.BaseURL != "https://custom.api.com/v1" {
		t.Errorf("expected custom BaseURL, got %s", cfg.API.BaseURL)
	}
	if cfg.API.DefaultModel != "custom-model" {
		t.Errorf("expected custom model, got %s", cfg.API.DefaultModel)
	}
}

func TestLoadNonExistent(t *testing.T) {
	origHome := os.Getenv("HOME")
	origBaseURL := os.Getenv("OPENAI_BASE_URL")
	origModel := os.Getenv("OPENAI_MODEL_NAME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	os.Unsetenv("OPENAI_BASE_URL")
	os.Unsetenv("OPENAI_MODEL_NAME")
	defer os.Setenv("HOME", origHome)
	defer os.Setenv("OPENAI_BASE_URL", origBaseURL)
	defer os.Setenv("OPENAI_MODEL_NAME", origModel)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.API.DefaultModel != "gpt-4o" {
		t.Errorf("expected default model, got %s", cfg.API.DefaultModel)
	}
}

func TestLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "paperagent")
	os.MkdirAll(configDir, 0755)

	configContent := `
api:
  base_url: "https://test.api.com/v1"
  api_key: "test-key-123"
  default_model: "test-model"
ui:
  min_recent_rounds: 2
  max_input_tokens: 50000
`
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(configContent), 0644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.API.BaseURL != "https://test.api.com/v1" {
		t.Errorf("expected test BaseURL, got %s", cfg.API.BaseURL)
	}
	if cfg.API.APIKey != "test-key-123" {
		t.Errorf("expected test API key, got %s", cfg.API.APIKey)
	}
	if cfg.API.DefaultModel != "test-model" {
		t.Errorf("expected test model, got %s", cfg.API.DefaultModel)
	}
	if cfg.UI.MinRecentRounds != 2 {
		t.Errorf("expected 2 min rounds, got %d", cfg.UI.MinRecentRounds)
	}
	if cfg.UI.MaxInputTokens != 50000 {
		t.Errorf("expected 50000 max input tokens, got %d", cfg.UI.MaxInputTokens)
	}
}

func TestLoadEnvVarExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "paperagent")
	os.MkdirAll(configDir, 0755)

	configContent := `
api:
  base_url: "https://api.openai.com/v1"
  api_key: "${TEST_API_KEY}"
  default_model: "gpt-4o"
`
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(configContent), 0644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	os.Setenv("TEST_API_KEY", "expanded-key-value")
	defer os.Setenv("HOME", origHome)
	defer os.Unsetenv("TEST_API_KEY")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.API.APIKey != "expanded-key-value" {
		t.Errorf("expected expanded key, got %s", cfg.API.APIKey)
	}
}

func TestSave(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "paperagent")

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg := &Config{
		API: APIConfig{
			BaseURL:      "https://test.com/v1",
			APIKey:       "real-key",
			DefaultModel: "test-model",
		},
		UI: UIConfig{MinRecentRounds: 3, MaxInputTokens: 30000},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("save error: %v", err)
	}

	// Verify file exists
	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	content := string(data)
	if !contains(content, encPrefix) {
		t.Error("saved config should encrypt API key with !!aes: prefix")
	}
	if !contains(content, "test-model") {
		t.Error("saved config should contain model name")
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input    string
		expected string
	}{
		{"~/Documents", filepath.Join(home, "Documents")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		result := expandHome(tt.input)
		if result != tt.expected {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestConfigDirPaths(t *testing.T) {
	home, _ := os.UserHomeDir()

	if ConfigDir() != filepath.Join(home, ".config", "paperagent") {
		t.Errorf("unexpected ConfigDir: %s", ConfigDir())
	}
	if ConfigPath() != filepath.Join(home, ".config", "paperagent", "config.yaml") {
		t.Errorf("unexpected ConfigPath: %s", ConfigPath())
	}
	if PapersDir() != filepath.Join(home, ".config", "paperagent", "papers") {
		t.Errorf("unexpected PapersDir: %s", PapersDir())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
