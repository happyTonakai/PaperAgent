package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	mu              sync.RWMutex
	API             APIConfig        `yaml:"api"`
	Recommend       RecommendConfig  `yaml:"recommend"`
	ArxivCategories []string         `yaml:"arxiv_categories"`
	Obsidian        ObsidianConfig   `yaml:"obsidian"`
	UI              UIConfig         `yaml:"ui"`
	Feishu          FeishuConfig     `yaml:"feishu"`
}

// APIConfig contains API configuration for Q&A chat.
// Scoring and translation have their own sub-configs.
type APIConfig struct {
	BaseURL      string        `yaml:"base_url"`
	APIKey       string        `yaml:"api_key"`
	DefaultModel string        `yaml:"default_model"`
	Scoring      *APIEndpoint  `yaml:"scoring,omitempty"`
	Translation  *APIEndpoint  `yaml:"translation,omitempty"`
}

// APIEndpoint represents an OpenAI-compatible API endpoint configuration.
type APIEndpoint struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
}

// RecommendConfig controls the daily recommendation pipeline.
type RecommendConfig struct {
	DailyPapers      int     `yaml:"daily_papers"`
	ScoringBatchSize int     `yaml:"scoring_batch_size"`
	DiversityRatio    float64 `yaml:"diversity_ratio"` // 0-1: proportion of random exploration articles
	ScheduledTime    string  `yaml:"scheduled_time"`  // HH:MM format, e.g. "08:00"
	PushToFeishu     bool    `yaml:"push_to_feishu"`  // push recommendations to feishu
}

type ObsidianConfig struct {
	VaultPath     string `yaml:"vault_path"`
	ExportFolder  string `yaml:"export_folder"`
}

type UIConfig struct {
	MinRecentRounds int `yaml:"min_recent_rounds"`
	MaxInputTokens  int `yaml:"max_input_tokens"`
}

type FeishuConfig struct {
	Enabled              bool   `yaml:"enabled"`
	AppID                string `yaml:"app_id"`
	AppSecret            string `yaml:"app_secret"`
	DailyRecommendChatID string `yaml:"daily_recommend_chat_id"`
}

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "paperagent")
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yaml")
}

func PapersDir() string {
	return filepath.Join(ConfigDir(), "papers")
}

func PromptsDir() string {
	return filepath.Join(ConfigDir(), "prompts")
}

func Load() (*Config, error) {
	cfg := defaultConfig()

	// Always expand env vars in API key, even if no config file exists.
	// Returns (resolved, error) where error is non-nil when env var
	// references like ${VAR} are left unresolved.
	resolveAPIKey := func(key string) (string, error) {
		expanded := os.ExpandEnv(key)
		if d, err := decryptKey(expanded); err == nil {
			return d, nil
		}
		// Check for unresolved env var references — these indicate
		// a configuration mistake and will cause API calls to fail.
		if hasUnresolvedEnv(expanded) {
			return expanded, fmt.Errorf("env var not set: %q contains unresolved ${...} reference", expanded)
		}
		return expanded, nil
	}

	cfg.API.APIKey, _ = resolveAPIKey(cfg.API.APIKey)

	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Resolve all API keys: expand env vars + decrypt
	var resolveErrors []string
	cfg.API.APIKey, _ = resolveAPIKey(cfg.API.APIKey)
	if cfg.API.Scoring != nil {
		if _, err := resolveAPIKey(cfg.API.Scoring.APIKey); err != nil {
			resolveErrors = append(resolveErrors, "scoring: "+err.Error())
		}
		cfg.API.Scoring.APIKey, _ = resolveAPIKey(cfg.API.Scoring.APIKey)
	}
	if cfg.API.Translation != nil {
		if _, err := resolveAPIKey(cfg.API.Translation.APIKey); err != nil {
			resolveErrors = append(resolveErrors, "translation: "+err.Error())
		}
		cfg.API.Translation.APIKey, _ = resolveAPIKey(cfg.API.Translation.APIKey)
	}
	if len(resolveErrors) > 0 {
		return cfg, fmt.Errorf("unresolved env vars: %s", strings.Join(resolveErrors, "; "))
	}

	// Expand ~ in paths
	cfg.Obsidian.VaultPath = expandHome(cfg.Obsidian.VaultPath)

	return cfg, nil
}

// hasUnresolvedEnv reports whether s still contains any ${VAR} patterns
// that os.ExpandEnv failed to resolve.
func hasUnresolvedEnv(s string) bool {
	return strings.Contains(s, "${") && strings.Contains(s, "}")
}

func defaultConfig() *Config {
	cfg := &Config{
		API: APIConfig{
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "${OPENAI_API_KEY}",
			DefaultModel: "gpt-4o",
		},
		Recommend: RecommendConfig{
			DailyPapers:      20,
			ScoringBatchSize: 10,
			DiversityRatio:   0.3,
			ScheduledTime:    "08:00",
			PushToFeishu:     true,
		},
		Obsidian: ObsidianConfig{
			VaultPath:    "~/Documents/Obsidian/MyVault",
			ExportFolder: "Papers",
		},
		UI: UIConfig{
			MinRecentRounds: 2,
			MaxInputTokens:  30000,
		},
	}

	// Env vars can override defaults (but config file values take precedence)
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		cfg.API.BaseURL = v
	}
	if v := os.Getenv("OPENAI_MODEL_NAME"); v != "" {
		cfg.API.DefaultModel = v
	}

	return cfg
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Encrypt API keys
	encryptAPICfg := func(key string) string {
		if key == "" || hasEncPrefix(key) || strings.HasPrefix(key, "${") {
			return key
		}
		enc, err := encryptKey(key)
		if err != nil {
			return key
		}
		return enc
	}

	saveCfg := Config{
		Feishu: FeishuConfig{
			Enabled:              c.Feishu.Enabled,
			AppID:                c.Feishu.AppID,
			AppSecret:            c.Feishu.AppSecret,
			DailyRecommendChatID: c.Feishu.DailyRecommendChatID,
		},
		API: APIConfig{
			BaseURL:      c.API.BaseURL,
			APIKey:       encryptAPICfg(c.API.APIKey),
			DefaultModel: c.API.DefaultModel,
			Scoring:      encryptAPIEndpoint(c.API.Scoring, encryptAPICfg),
			Translation:  encryptAPIEndpoint(c.API.Translation, encryptAPICfg),
		},
		Recommend: RecommendConfig{
			DailyPapers:      c.Recommend.DailyPapers,
			ScoringBatchSize: c.Recommend.ScoringBatchSize,
			DiversityRatio:   c.Recommend.DiversityRatio,
			ScheduledTime:    c.Recommend.ScheduledTime,
			PushToFeishu:     c.Recommend.PushToFeishu,
		},
		ArxivCategories: c.ArxivCategories,
		Obsidian: ObsidianConfig{
			VaultPath:    c.Obsidian.VaultPath,
			ExportFolder: c.Obsidian.ExportFolder,
		},
		UI: UIConfig{
			MinRecentRounds: c.UI.MinRecentRounds,
			MaxInputTokens:  c.UI.MaxInputTokens,
		},
	}

	data, err := yaml.Marshal(&saveCfg)
	if err != nil {
		return err
	}

	configPath := ConfigPath()
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}

	// Ensure .key file is 0600 too
	keyFile := keyPath()
	if _, err := os.Stat(keyFile); err == nil {
		os.Chmod(keyFile, 0600)
	}

	return nil
}

func encryptAPIEndpoint(ep *APIEndpoint, encFn func(string) string) *APIEndpoint {
	if ep == nil {
		return nil
	}
	cpy := *ep
	cpy.APIKey = encFn(cpy.APIKey)
	return &cpy
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, path[1:])
}

func (c *Config) Lock()    { c.mu.Lock() }
func (c *Config) Unlock()  { c.mu.Unlock() }
func (c *Config) RLock()   { c.mu.RLock() }
func (c *Config) RUnlock() { c.mu.RUnlock() }
