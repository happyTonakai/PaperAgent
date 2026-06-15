package prompt

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/happyTonakai/paperagent/internal/config"
)

//go:embed prompts/system.txt
var SystemPrompt string

//go:embed prompts/heavy.txt
var HeavyPrompt string

//go:embed prompts/light.txt
var LightPrompt string

//go:embed prompts/summarize.txt
var SummarizePrompt string

//go:embed prompts/scoring.txt
var ScoringPrompt string

//go:embed prompts/update-prefs.txt
var UpdatePrefsPrompt string

// Get returns the prompt, checking user override first.
func Get(name string, fallback string) string {
	userPath := filepath.Join(config.PromptsDir(), name+".txt")
	data, err := os.ReadFile(userPath)
	if err == nil {
		return string(data)
	}
	return fallback
}

// GetSystem always returns the embedded system prompt; user overrides at
// prompts/system.txt are intentionally not honored. Any pre-existing override
// file is left on disk but silently ignored.
func GetSystem() string { return SystemPrompt }
func GetHeavy() string     { return Get("heavy", HeavyPrompt) }
func GetLight() string     { return Get("light", LightPrompt) }
func GetSummarize() string { return Get("summarize", SummarizePrompt) }
func GetScoring() string   { return Get("scoring", ScoringPrompt) }
func GetUpdatePrefs() string { return Get("update-prefs", UpdatePrefsPrompt) }

// GetContent returns the effective prompt content for a given name.
// Uses user override if it exists, otherwise returns the built-in default.
// Note: "system" always returns the embedded default.
func GetContent(name string) string {
	switch name {
	case "system":
		return GetSystem()
	case "heavy":
		return GetHeavy()
	case "light":
		return GetLight()
	case "summarize":
		return GetSummarize()
	case "scoring":
		return GetScoring()
	case "update-prefs":
		return GetUpdatePrefs()
	}
	return ""
}

// BuiltinNames returns the list of built-in prompt names that can be
// overridden by the user. "system" is intentionally excluded.
func BuiltinNames() []string {
	return []string{"heavy", "light", "summarize", "scoring", "update-prefs"}
}

// Save writes a user override prompt to disk.
func Save(name string, content string) error {
	dir := config.PromptsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".txt"), []byte(content), 0644)
}
