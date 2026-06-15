package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happyTonakai/paperagent/internal/config"
)

func TestEmbeddedPrompts(t *testing.T) {
	if SystemPrompt == "" {
		t.Error("SystemPrompt should not be empty")
	}
	if HeavyPrompt == "" {
		t.Error("HeavyPrompt should not be empty")
	}
	if LightPrompt == "" {
		t.Error("LightPrompt should not be empty")
	}
	if SummarizePrompt == "" {
		t.Error("SummarizePrompt should not be empty")
	}
	if ScoringPrompt == "" {
		t.Error("ScoringPrompt should not be empty")
	}
	if UpdatePrefsPrompt == "" {
		t.Error("UpdatePrefsPrompt should not be empty")
	}
}

func TestGetSystem(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	result := GetSystem()
	if result == "" {
		t.Error("GetSystem should not return empty")
	}
	if !strings.Contains(result, "论文") {
		t.Error("SystemPrompt should contain '论文'")
	}
}

// TestGetSystemIgnoresUserOverride verifies that even when a stale override
// file exists at prompts/system.txt, GetSystem always returns the embedded
// default.
func TestGetSystemIgnoresUserOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	promptsDir := config.PromptsDir()
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "system.txt"), []byte("USER OVERRIDE — should be ignored"), 0644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	if got := GetSystem(); got == "USER OVERRIDE — should be ignored" {
		t.Error("GetSystem honored user override file; should always return embedded")
	}
	if got := GetSystem(); got != SystemPrompt {
		t.Errorf("GetSystem returned %q, want embedded SystemPrompt", got)
	}
}

func TestGetScoring(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	result := GetScoring()
	if result == "" {
		t.Error("GetScoring should not return empty")
	}
	if !strings.Contains(result, "0.0") || !strings.Contains(result, "1.0") {
		t.Error("ScoringPrompt should describe the 0.0–1.0 score range")
	}
}

func TestGetUpdatePrefs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	result := GetUpdatePrefs()
	if result == "" {
		t.Error("GetUpdatePrefs should not return empty")
	}
	if !strings.Contains(result, "感兴趣") {
		t.Error("UpdatePrefsPrompt should mention '感兴趣'")
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

func TestBuiltinNamesExcludesSystem(t *testing.T) {
	for _, n := range BuiltinNames() {
		if n == "system" {
			t.Error("BuiltinNames should not include 'system' (it is no longer user-overridable)")
		}
	}
	// Sanity: the two new recommend prompts are present.
	have := map[string]bool{}
	for _, n := range BuiltinNames() {
		have[n] = true
	}
	if !have["scoring"] || !have["update-prefs"] {
		t.Errorf("BuiltinNames missing new recommend prompts: %v", BuiltinNames())
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
