package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happyTonakai/paperagent/internal/config"
)

func TestEmbeddedPrompts(t *testing.T) {
	if HeavyPrompt == "" {
		t.Error("HeavyPrompt should not be empty")
	}
	if LightPrompt == "" {
		t.Error("LightPrompt should not be empty")
	}
	if SummarizePrompt == "" {
		t.Error("SummarizePrompt should not be empty")
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

func TestGetSummarize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	result := GetSummarize()
	if result == "" {
		t.Error("GetSummarize should not return empty")
	}
	if !strings.Contains(result, "总结") {
		t.Error("SummarizePrompt should contain '总结'")
	}
}

func TestGetWithUserOverride(t *testing.T) {
	// Create a temp dir for user prompts
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	t.Cleanup(func() { os.Unsetenv("HOME") })

	promptsDir := config.PromptsDir()
	os.MkdirAll(promptsDir, 0755)

	// Write custom prompt
	customPrompt := "This is a custom heavy prompt"
	os.WriteFile(filepath.Join(promptsDir, "heavy.txt"), []byte(customPrompt), 0644)

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
