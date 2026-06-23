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
	mu sync.RWMutex

	API             APIConfig       `yaml:"api"`
	Recommend       RecommendConfig `yaml:"recommend"`
	ArxivCategories []string        `yaml:"arxiv_categories"`
	Obsidian        ObsidianConfig  `yaml:"obsidian"`
	UI              UIConfig        `yaml:"ui"`
	Feishu          FeishuConfig    `yaml:"feishu"`

	// RawOnDiskAPIKey captures the verbatim string of `api_key` as it appeared
	// in the on-disk YAML (e.g. "${OPENAI_API_KEY}", "!!aes:...", or a
	// plaintext key). Save() uses this to decide whether to re-encrypt, leave
	// the ${...} reference, or pass the ciphertext through untouched — so
	// that resolving env vars at Load time doesn't accidentally clobber a
	// user's deliberate ${...} reference on the next Save.
	//
	// Set to the empty string when the on-disk key was empty (Save falls back
	// to "encrypt plaintext" behaviour in that case for backward compat).
	RawOnDiskAPIKey           string `yaml:"-"`
	RawOnDiskScoringAPIKey    string `yaml:"-"`
	RawOnDiskTranslationAPIKey string `yaml:"-"`
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
	ExportPath string `yaml:"export_path"`
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

// ConfigExists reports whether the user config file exists on disk.
// Used by the Web UI to detect a first-run state and auto-open the
// settings dialog, since a missing config.yaml means the user has not
// yet configured the API key etc.
func ConfigExists() bool {
	_, err := os.Stat(ConfigPath())
	return err == nil
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

	// Parse the raw on-disk form of api_key fields once, so we can both
	// (a) decide whether to auto-re-encrypt and (b) remember each field's
	// verbatim on-disk form for Save() to honour.
	var rawKeys struct {
		API struct {
			APIKey      string `yaml:"api_key"`
			Scoring     *struct {
				APIKey string `yaml:"api_key"`
			} `yaml:"scoring,omitempty"`
			Translation *struct {
				APIKey string `yaml:"api_key"`
			} `yaml:"translation,omitempty"`
		} `yaml:"api"`
	}
	_ = yaml.Unmarshal(data, &rawKeys)

	// Migration: older configs split the export destination into
	// vault_path + export_folder. If we read an old config (no export_path
	// but the legacy fields present), concatenate them into export_path.
	if cfg.Obsidian.ExportPath == "" {
		var legacy struct {
			Obsidian struct {
				VaultPath    string `yaml:"vault_path"`
				ExportFolder string `yaml:"export_folder"`
			} `yaml:"obsidian"`
		}
		if err := yaml.Unmarshal(data, &legacy); err == nil {
			if legacy.Obsidian.VaultPath != "" || legacy.Obsidian.ExportFolder != "" {
				cfg.Obsidian.ExportPath = filepath.Join(legacy.Obsidian.VaultPath, legacy.Obsidian.ExportFolder)
			}
		}
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
	if cfg.Obsidian.ExportPath != "" {
		cfg.Obsidian.ExportPath = expandHome(cfg.Obsidian.ExportPath)
	}

	// Record the verbatim on-disk form of each api_key so that Save() can
	// preserve a deliberate ${...} reference (don't encrypt the resolved
	// plaintext) and pass through existing !!aes: ciphertext untouched.
	cfg.RawOnDiskAPIKey = rawKeys.API.APIKey
	if rawKeys.API.Scoring != nil {
		cfg.RawOnDiskScoringAPIKey = rawKeys.API.Scoring.APIKey
	}
	if rawKeys.API.Translation != nil {
		cfg.RawOnDiskTranslationAPIKey = rawKeys.API.Translation.APIKey
	}

	// Auto-re-encrypt: if any API key on disk was stored as plaintext
	// (legacy or hand-written), re-save so the on-disk form is always
	// either ${VAR} reference or !!aes: ciphertext. We must inspect the
	// raw on-disk form here — cfg's APIKey field is already resolved to
	// plaintext, so it can't tell us what the user originally wrote.
	if needsReencrypt(data) {
		if err := cfg.Save(); err != nil {
			// Non-fatal: in-memory cfg is usable; next user-triggered Save()
			// (e.g. via Web UI) will retry re-encryption.
			fmt.Fprintf(os.Stderr, "warning: could not re-encrypt API key on disk: %v\n", err)
		}
	}

	return cfg, nil
}

// needsReencrypt reports whether the raw config file bytes contain any
// api_key field whose value is plaintext (neither !!aes: ciphertext
// nor a ${VAR} env-var reference).
func needsReencrypt(data []byte) bool {
	var raw struct {
		API struct {
			APIKey      string `yaml:"api_key"`
			Scoring     *struct {
				APIKey string `yaml:"api_key"`
			} `yaml:"scoring,omitempty"`
			Translation *struct {
				APIKey string `yaml:"api_key"`
			} `yaml:"translation,omitempty"`
		} `yaml:"api"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false
	}
	if isPlaintextAPIKey(raw.API.APIKey) {
		return true
	}
	if raw.API.Scoring != nil && isPlaintextAPIKey(raw.API.Scoring.APIKey) {
		return true
	}
	if raw.API.Translation != nil && isPlaintextAPIKey(raw.API.Translation.APIKey) {
		return true
	}
	return false
}

func isPlaintextAPIKey(s string) bool {
	return s != "" && !hasEncPrefix(s) && !strings.HasPrefix(s, "${")
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
			ExportPath: "~/Papers",
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

	// Encrypt API keys. For each key, the decision depends on what was on
	// disk last time (recorded in c.RawOnDisk*). This lets us preserve a
	// user's deliberate ${...} reference (which is expanded to plaintext
	// in memory) and pass existing !!aes: ciphertext through untouched,
	// rather than naively encrypting whatever plaintext we hold in memory.
	encryptAPICfg := func(resolved, rawOnDisk string) string {
		if resolved == "" {
			return ""
		}
		if rawOnDisk != "" {
			// The user has a deliberate on-disk form: honour it.
			if hasEncPrefix(rawOnDisk) {
				return rawOnDisk // pass through ciphertext untouched
			}
			if strings.HasPrefix(rawOnDisk, "${") {
				return rawOnDisk // preserve env-var reference
			}
			// On-disk was plaintext → re-encrypt the (possibly-updated) in-memory value.
			if enc, err := encryptKey(resolved); err == nil {
				return enc
			}
			return resolved
		}
		// No raw form recorded (first Save, or key was added in memory after Load).
		// Fall back to the legacy behaviour: encrypt plaintext, keep ${...}/ciphertext.
		if hasEncPrefix(resolved) || strings.HasPrefix(resolved, "${") {
			return resolved
		}
		if enc, err := encryptKey(resolved); err == nil {
			return enc
		}
		return resolved
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
			APIKey:       encryptAPICfg(c.API.APIKey, c.RawOnDiskAPIKey),
			DefaultModel: c.API.DefaultModel,
			Scoring:      encryptAPIEndpointWithRaw(c.API.Scoring, c.RawOnDiskScoringAPIKey),
			Translation:  encryptAPIEndpointWithRaw(c.API.Translation, c.RawOnDiskTranslationAPIKey),
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
			ExportPath: c.Obsidian.ExportPath,
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

// encryptAPIEndpointWithRaw mirrors encryptAPIEndpoint but threads the
// raw on-disk api_key string through to the encryptor so the on-disk form
// of a sub-config's key (api.scoring.api_key, api.translation.api_key) is
// preserved across Save() the same way the top-level api_key is.
func encryptAPIEndpointWithRaw(ep *APIEndpoint, rawOnDisk string) *APIEndpoint {
	if ep == nil {
		return nil
	}
	cpy := *ep
	encFn := func(resolved string) string {
		if resolved == "" {
			return ""
		}
		if rawOnDisk != "" {
			if hasEncPrefix(rawOnDisk) {
				return rawOnDisk
			}
			if strings.HasPrefix(rawOnDisk, "${") {
				return rawOnDisk
			}
			if enc, err := encryptKey(resolved); err == nil {
				return enc
			}
			return resolved
		}
		if hasEncPrefix(resolved) || strings.HasPrefix(resolved, "${") {
			return resolved
		}
		if enc, err := encryptKey(resolved); err == nil {
			return enc
		}
		return resolved
	}
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
