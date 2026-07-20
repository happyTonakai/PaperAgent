package config

import (
	"os"
	"path/filepath"
	"strings"
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
		UI:              UIConfig{MinRecentRounds: 3, MaxInputTokens: 30000},
		Recommend:       RecommendConfig{DailyPapers: 20, ScoringBatchSize: 10, DiversityRatio: 0.3, ScheduledTime: "08:00"},
		ArxivCategories: []string{"cs.SD"},
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

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer os.Setenv("HOME", origHome)

	// Encrypt a key manually, write that, then load — disk form must not change.
	cfg := &Config{
		API:             APIConfig{BaseURL: "https://test.com/v1", APIKey: "preserved-plaintext", DefaultModel: "test-model"},
		UI:              UIConfig{MinRecentRounds: 2, MaxInputTokens: 30000},
		Recommend:       RecommendConfig{DailyPapers: 20, ScoringBatchSize: 10, DiversityRatio: 0.3, ScheduledTime: "08:00"},
		ArxivCategories: []string{"cs.SD"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	beforeDisk, _ := os.ReadFile(filepath.Join(configDir, "config.yaml"))

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

// validCfg returns a minimal *Config that passes Validate. Tests
// override one field at a time to assert the corresponding error
// path is enforced.
func validCfg() *Config {
	return &Config{
		API: APIConfig{
			BaseURL:      "https://test.api/v1",
			APIKey:       "sk-valid",
			DefaultModel: "test-model",
		},
		UI:              UIConfig{MinRecentRounds: 2, MaxInputTokens: 30000},
		Recommend:       RecommendConfig{DailyPapers: 20, ScoringBatchSize: 10, DiversityRatio: 0.3, ScheduledTime: "08:00"},
		ArxivCategories: []string{"cs.SD"},
	}
}

func TestValidate(t *testing.T) {
	t.Run("accepts a fully-populated valid config", func(t *testing.T) {
		if err := validCfg().Validate(); err != nil {
			t.Errorf("expected valid cfg, got: %v", err)
		}
	})

	t.Run("rejects empty api_key", func(t *testing.T) {
		c := validCfg()
		c.API.APIKey = ""
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "api.api_key") {
			t.Errorf("expected api.api_key error, got: %v", err)
		}
	})

	t.Run("rejects empty base_url", func(t *testing.T) {
		c := validCfg()
		c.API.BaseURL = ""
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "base_url") {
			t.Errorf("expected base_url error, got: %v", err)
		}
	})

	t.Run("rejects empty default_model", func(t *testing.T) {
		c := validCfg()
		c.API.DefaultModel = ""
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "default_model") {
			t.Errorf("expected default_model error, got: %v", err)
		}
	})

	t.Run("rejects min_recent_rounds < 1", func(t *testing.T) {
		c := validCfg()
		c.UI.MinRecentRounds = 0
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "min_recent_rounds") {
			t.Errorf("expected min_recent_rounds error, got: %v", err)
		}
	})

	t.Run("rejects max_input_tokens < 1000", func(t *testing.T) {
		c := validCfg()
		c.UI.MaxInputTokens = 500
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "max_input_tokens") {
			t.Errorf("expected max_input_tokens error, got: %v", err)
		}
	})

	t.Run("rejects feishu.enabled=true with empty app_id", func(t *testing.T) {
		c := validCfg()
		c.Feishu.Enabled = true
		c.Feishu.AppID = ""
		c.Feishu.AppSecret = "valid-secret"
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "app_id") {
			t.Errorf("expected app_id error, got: %v", err)
		}
	})

	t.Run("rejects feishu.enabled=true with empty app_secret", func(t *testing.T) {
		c := validCfg()
		c.Feishu.Enabled = true
		c.Feishu.AppID = "cli_xxx"
		c.Feishu.AppSecret = ""
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "app_secret") {
			t.Errorf("expected app_secret error, got: %v", err)
		}
	})

	t.Run("rejects daily_papers <= 0", func(t *testing.T) {
		c := validCfg()
		c.Recommend.DailyPapers = 0
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "daily_papers") {
			t.Errorf("expected daily_papers error, got: %v", err)
		}
	})

	t.Run("rejects scoring_batch_size <= 0", func(t *testing.T) {
		c := validCfg()
		c.Recommend.ScoringBatchSize = 0
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "scoring_batch_size") {
			t.Errorf("expected scoring_batch_size error, got: %v", err)
		}
	})

	t.Run("rejects diversity_ratio out of range", func(t *testing.T) {
		c := validCfg()
		c.Recommend.DiversityRatio = 1.5
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "diversity_ratio") {
			t.Errorf("expected diversity_ratio error, got: %v", err)
		}
		c.Recommend.DiversityRatio = -0.1
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "diversity_ratio") {
			t.Errorf("expected diversity_ratio error, got: %v", err)
		}
	})

	t.Run("rejects malformed scheduled_time", func(t *testing.T) {
		c := validCfg()
		c.Recommend.ScheduledTime = "8am"
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "scheduled_time") {
			t.Errorf("expected scheduled_time error, got: %v", err)
		}
		c.Recommend.ScheduledTime = "25:00"
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "scheduled_time") {
			t.Errorf("expected scheduled_time error for out-of-range hour, got: %v", err)
		}
	})

	t.Run("rejects recommend.enabled=true with empty arxiv_categories", func(t *testing.T) {
		c := validCfg()
		c.Recommend.Enabled = true
		c.ArxivCategories = nil
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "arxiv_categories") {
			t.Errorf("expected arxiv_categories error, got: %v", err)
		}
	})

	t.Run("allows recommend.enabled=false with empty arxiv_categories", func(t *testing.T) {
		c := validCfg()
		c.Recommend.Enabled = false
		c.ArxivCategories = nil
		if err := c.Validate(); err != nil {
			t.Errorf("expected nil (categories only required when enabled), got: %v", err)
		}
	})
}

func TestHandleCrossFieldChecks(t *testing.T) {
	t.Run("accepts push_to_feishu=false with feishu disabled", func(t *testing.T) {
		c := validCfg()
		c.Feishu.Enabled = false
		c.Recommend.PushToFeishu = false
		if err := c.HandleCrossFieldChecks(); err != nil {
			t.Errorf("expected nil, got: %v", err)
		}
	})

	t.Run("rejects push_to_feishu=true with feishu disabled", func(t *testing.T) {
		c := validCfg()
		c.Feishu.Enabled = false
		c.Recommend.PushToFeishu = true
		if err := c.HandleCrossFieldChecks(); err == nil || !strings.Contains(err.Error(), "feishu.enabled") {
			t.Errorf("expected cross-field error, got: %v", err)
		}
	})

	t.Run("accepts push_to_feishu=true with feishu enabled", func(t *testing.T) {
		c := validCfg()
		c.Feishu.Enabled = true
		c.Feishu.AppID = "cli_xxx"
		c.Feishu.AppSecret = "valid"
		c.Recommend.PushToFeishu = true
		if err := c.HandleCrossFieldChecks(); err != nil {
			t.Errorf("expected nil, got: %v", err)
		}
	})
}

func TestIsValidHHMM(t *testing.T) {
	cases := map[string]bool{
		"00:00": true, "08:00": true, "23:59": true,
		"": false, "8:00": false, "24:00": false, "12:60": false,
		"8am": false, " 08:00": false, "08:00 ": false, "08-00": false,
	}
	for input, want := range cases {
		if got := isValidHHMM(input); got != want {
			t.Errorf("isValidHHMM(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestLoad_MigratesRecommendEnabled verifies the four backward-
// compat scenarios for the recommend.enabled master switch (added
// 2026-06-25). Existing users upgrading from a pre-switch version
// must end up with the right Enabled value without losing any other
// state.
//
// The migration uses *bool on a side struct to detect "field absent
// in YAML" (nil) vs "field present and false" (&false), which is
// critical: we must not flip a user's explicit disabled choice back
// to true, but a legacy config (no field at all) should be enabled
// iff they had arxiv categories — the only signal that they were
// actually using the recommender.
func TestLoad_MigratesRecommendEnabled(t *testing.T) {
	cases := []struct {
		name        string
		yaml        string
		wantEnabled bool
	}{
		{
			name: "legacy config with arxiv_categories, no enabled field",
			yaml: `api:
  base_url: "https://x/v1"
  api_key: "${X}"
  default_model: "m"
recommend:
  daily_papers: 20
  scoring_batch_size: 10
  diversity_ratio: 0.3
  scheduled_time: "08:00"
arxiv_categories:
  - cs.SD
feishu:
  enabled: false
`,
			// User was running the recommender → keep them running.
			wantEnabled: true,
		},
		{
			name: "legacy config with empty arxiv_categories, no enabled field",
			yaml: `api:
  base_url: "https://x/v1"
  api_key: "${X}"
  default_model: "m"
recommend:
  daily_papers: 20
ui:
  min_recent_rounds: 2
  max_input_tokens: 30000
feishu:
  enabled: false
`,
			// No categories = never used the recommender = stay disabled.
			wantEnabled: false,
		},
		{
			name: "config with explicit enabled: false must be preserved",
			yaml: `api:
  base_url: "https://x/v1"
  api_key: "${X}"
  default_model: "m"
recommend:
  enabled: false
  daily_papers: 20
arxiv_categories:
  - cs.SD
feishu:
  enabled: false
`,
			// User explicitly disabled. Don't override.
			wantEnabled: false,
		},
		{
			name: "config with explicit enabled: true must be preserved",
			yaml: `api:
  base_url: "https://x/v1"
  api_key: "${X}"
  default_model: "m"
recommend:
  enabled: true
  daily_papers: 20
arxiv_categories:
  - cs.SD
feishu:
  enabled: false
`,
			wantEnabled: true,
		},
		{
			name: "no recommend section at all, no arxiv_categories",
			yaml: `api:
  base_url: "https://x/v1"
  api_key: "${X}"
  default_model: "m"
feishu:
  enabled: false
`,
			// Truly fresh config that never had recommend = stay disabled.
			wantEnabled: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			origHome := os.Getenv("HOME")
			os.Setenv("HOME", tmpDir)
			t.Setenv("XDG_CONFIG_HOME", "")
			defer os.Setenv("HOME", origHome)

			configDir := filepath.Join(tmpDir, ".config", "paperagent")
			if err := os.MkdirAll(configDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() failed: %v", err)
			}
			if cfg.Recommend.Enabled != tc.wantEnabled {
				t.Errorf("cfg.Recommend.Enabled = %v, want %v", cfg.Recommend.Enabled, tc.wantEnabled)
			}
		})
	}
}

func TestSave_RejectsInvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer os.Setenv("HOME", origHome)

	cfg := validCfg()
	cfg.API.APIKey = "" // invalidate
	if err := cfg.Save(); err == nil {
		t.Fatal("expected Save() to refuse invalid config, got nil error")
	}

	// And the disk file must not have been created/written.
	if _, err := os.Stat(filepath.Join(tmpDir, ".config", "paperagent", "config.yaml")); err == nil {
		t.Error("config.yaml should not exist on disk after a rejected Save()")
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
