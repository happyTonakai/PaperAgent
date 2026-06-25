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
	t.Setenv("XDG_CONFIG_HOME", "")
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
	t.Setenv("XDG_CONFIG_HOME", "")
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
	t.Setenv("XDG_CONFIG_HOME", "")
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
	t.Setenv("XDG_CONFIG_HOME", "")
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

func TestLoadReencryptsPlaintextAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "paperagent")
	os.MkdirAll(configDir, 0755)

	plaintextKey := "sk-plaintext-should-be-encrypted-12345"
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`api:
  base_url: "https://test.api.com/v1"
  api_key: "`+plaintextKey+`"
  default_model: "test-model"
`), 0644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer os.Setenv("HOME", origHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// In-memory key must still be the plaintext (so the app can use it).
	if cfg.API.APIKey != plaintextKey {
		t.Errorf("in-memory API key changed: got %q, want %q", cfg.API.APIKey, plaintextKey)
	}

	// On-disk form must now be encrypted.
	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read-back error: %v", err)
	}
	content := string(data)
	if !contains(content, encPrefix) {
		t.Errorf("on-disk api_key was not re-encrypted; file content:\n%s", content)
	}
	if contains(content, plaintextKey) {
		t.Errorf("on-disk config still contains plaintext key; file content:\n%s", content)
	}
}

func TestLoadPreservesEnvVarRef(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "paperagent")
	os.MkdirAll(configDir, 0755)

	// Use a real env var so resolveAPIKey doesn't error.
	envVar := "PAPERAGENT_TEST_KEY_RE_ENCRYPT"
	os.Setenv(envVar, "expanded-value-for-test")
	defer os.Unsetenv(envVar)

	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`api:
  base_url: "https://test.api.com/v1"
  api_key: "${`+envVar+`}"
  default_model: "test-model"
`), 0644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer os.Setenv("HOME", origHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.API.APIKey != "expanded-value-for-test" {
		t.Errorf("expected expanded key, got %q", cfg.API.APIKey)
	}

	// On-disk form must STILL be the ${VAR} reference, not encrypted.
	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read-back error: %v", err)
	}
	content := string(data)
	if !contains(content, "${"+envVar+"}") {
		t.Errorf("${VAR} reference was changed; expected it to be preserved. file:\n%s", content)
	}
	if contains(content, encPrefix) {
		t.Errorf("on-disk config was unexpectedly encrypted; file:\n%s", content)
	}
}

func TestLoadPreservesAlreadyEncrypted(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "paperagent")
	os.MkdirAll(configDir, 0755)

	// Encrypt a key manually, write that, then load — disk form must not change.
	cfg := &Config{API: APIConfig{APIKey: "preserved-plaintext"}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	beforeDisk, _ := os.ReadFile(filepath.Join(configDir, "config.yaml"))

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer os.Setenv("HOME", origHome)

	_, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	afterDisk, _ := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if string(beforeDisk) != string(afterDisk) {
		t.Errorf("disk content changed for already-encrypted key.\nbefore:\n%s\nafter:\n%s", beforeDisk, afterDisk)
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
	t.Setenv("XDG_CONFIG_HOME", "")
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

func TestConfigExists(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer os.Setenv("HOME", origHome)

	// Initially no config file exists.
	if ConfigExists() {
		t.Error("expected ConfigExists() == false when config.yaml is missing")
	}

	// Create the file -> ConfigExists should return true.
	configDir := filepath.Join(tmpDir, ".config", "paperagent")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("api: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !ConfigExists() {
		t.Error("expected ConfigExists() == true after writing config.yaml")
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
