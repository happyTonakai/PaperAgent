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
	mu       sync.RWMutex
	API      APIConfig      `yaml:"api"`
	Obsidian ObsidianConfig `yaml:"obsidian"`
	UI       UIConfig       `yaml:"ui"`
}

type APIConfig struct {
	BaseURL      string `yaml:"base_url"`
	APIKey       string `yaml:"api_key"`
	DefaultModel string `yaml:"default_model"`
	LightModel   string `yaml:"light_model"`
}

type ObsidianConfig struct {
	VaultPath     string `yaml:"vault_path"`
	ExportFolder  string `yaml:"export_folder"`
}

type UIConfig struct {
	MaxRecentRounds int `yaml:"max_recent_rounds"`
}

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".paperpaper")
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

	// Decrypt encrypted api_key, then expand env vars
	cfg.API.APIKey = os.ExpandEnv(cfg.API.APIKey)
	if decrypted, err := decryptKey(cfg.API.APIKey); err == nil {
		cfg.API.APIKey = decrypted
	}

	// Expand ~ in paths
	cfg.Obsidian.VaultPath = expandHome(cfg.Obsidian.VaultPath)

	return cfg, nil
}

func defaultConfig() *Config {
	cfg := &Config{
		API: APIConfig{
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "${OPENAI_API_KEY}",
			DefaultModel: "gpt-4o",
			LightModel:   "gpt-4o-mini",
		},
		Obsidian: ObsidianConfig{
			VaultPath:    "~/Documents/Obsidian/MyVault",
			ExportFolder: "Papers",
		},
		UI: UIConfig{
			MaxRecentRounds: 5,
		},
	}

	// Env vars can override defaults (but config file values take precedence)
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		cfg.API.BaseURL = v
	}
	if v := os.Getenv("OPENAI_MODEL_NAME"); v != "" {
		cfg.API.DefaultModel = v
		cfg.API.LightModel = v
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

	apiKey := c.API.APIKey
	if apiKey != "" && !hasEncPrefix(apiKey) && !strings.HasPrefix(apiKey, "${") {
		enc, err := encryptKey(apiKey)
		if err != nil {
			return fmt.Errorf("encrypt api key: %w", err)
		}
		apiKey = enc
	}

	saveCfg := Config{
		API: APIConfig{
			BaseURL:      c.API.BaseURL,
			APIKey:       apiKey,
			DefaultModel: c.API.DefaultModel,
			LightModel:   c.API.LightModel,
		},
		Obsidian: ObsidianConfig{
			VaultPath:    c.Obsidian.VaultPath,
			ExportFolder: c.Obsidian.ExportFolder,
		},
		UI: UIConfig{
			MaxRecentRounds: c.UI.MaxRecentRounds,
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
