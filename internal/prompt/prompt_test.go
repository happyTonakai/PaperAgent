package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedPrompts(t *testing.T) {
	if HeavyPrompt == "" {
		t.Error("HeavyPrompt should not be empty")
	}
	if LightPrompt == "" {
		t.Error("LightPrompt should not be empty")
	}
	if DigestPrompt == "" {
		t.Error("DigestPrompt should not be empty")
	}
}

func TestGetHeavy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	result := GetHeavy()
	if result == "" {
		t.Error("GetHeavy should not return empty")
	}
	if !strings.Contains(result, "Markdown") {
		t.Error("HeavyPrompt should contain 'Markdown'")
	}
}

func TestGetLight(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	result := GetLight()
	if result == "" {
		t.Error("GetLight should not return empty")
	}
	if !strings.Contains(result, "回答") {
		t.Error("LightPrompt should contain '回答'")
	}
}

func TestGetDigest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	result := GetDigest()
	if result == "" {
		t.Error("GetDigest should not return empty")
	}
	if !strings.Contains(result, "一句话") {
		t.Error("DigestPrompt should contain '一句话'")
	}
}

func TestGetWithUserOverride(t *testing.T) {
	// Create a temp dir for user prompts
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, ".paperpaper", "prompts")
	os.MkdirAll(promptsDir, 0755)

	// Write custom prompt
	customPrompt := "This is a custom heavy prompt"
	os.WriteFile(filepath.Join(promptsDir, "heavy.txt"), []byte(customPrompt), 0644)

	// Override HOME so config.PromptsDir() returns our temp dir
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	result := Get("heavy", "default fallback")
	if result != customPrompt {
		t.Errorf("expected custom prompt, got %s", result)
	}
}

func TestGetFallback(t *testing.T) {
	// No custom prompt exists
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	result := Get("nonexistent", "fallback value")
	if result != "fallback value" {
		t.Errorf("expected fallback, got %s", result)
	}
}
