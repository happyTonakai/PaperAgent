package prompt

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/paperpaper/paperpaper/internal/config"
)

//go:embed prompts/heavy.txt
var HeavyPrompt string

//go:embed prompts/light.txt
var LightPrompt string

//go:embed prompts/digest.txt
var DigestPrompt string

// Get returns the prompt, checking user override first.
func Get(name string, fallback string) string {
	userPath := filepath.Join(config.PromptsDir(), name+".txt")
	data, err := os.ReadFile(userPath)
	if err == nil {
		return string(data)
	}
	return fallback
}

func GetHeavy() string  { return Get("heavy", HeavyPrompt) }
func GetLight() string  { return Get("light", LightPrompt) }
func GetDigest() string { return Get("digest", DigestPrompt) }

// GetContent returns the effective prompt content for a given name.
// Uses user override if it exists, otherwise returns the built-in default.
func GetContent(name string) string {
	switch name {
	case "heavy":
		return GetHeavy()
	case "light":
		return GetLight()
	case "digest":
		return GetDigest()
	}
	return ""
}

// BuiltinNames returns the list of built-in prompt names.
func BuiltinNames() []string {
	return []string{"heavy", "light", "digest"}
}

// Save writes a user override prompt to disk.
func Save(name string, content string) error {
	dir := config.PromptsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".txt"), []byte(content), 0644)
}
