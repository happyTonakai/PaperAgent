package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"gopkg.in/yaml.v3"
)

// makeTestCfg returns a Config that mimics a fresh install with the main API
// set to ${OPENAI_API_KEY} (resolved to a fake plaintext so buildTranslationClient
// can construct a real client). HOME must already be redirected to a tmp dir
// so subsequent Save() calls don't touch the user's real config.
func makeTestCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.API.BaseURL = "https://api.test.com/v1"
	cfg.API.APIKey = "sk-test-plaintext" // resolved plaintext in memory
	cfg.API.DefaultModel = "test-model"
	cfg.RawOnDiskAPIKey = "${OPENAI_API_KEY}"
	// Make sure no stale translation/scoring sub-config leaks in.
	cfg.API.Translation = nil
	cfg.API.Scoring = nil
	cfg.RawOnDiskTranslationAPIKey = ""
	cfg.RawOnDiskScoringAPIKey = ""
	return cfg
}

// newTestServer builds a Server without calling server.New — so no scheduler,
// no logger capture, no migration. Just enough for handleRecommendUpdateConfig.
func newTestServer(cfg *config.Config) *Server {
	return &Server{
		cfg:            cfg,
		api:            api.NewClient(cfg),
		scoringAPI:     buildScoringClient(cfg),
		translationAPI: buildTranslationClient(cfg),
	}
}

// doPut drives handleRecommendUpdateConfig with the given JSON body and
// returns the response body. Fails the test on non-200.
func doPut(t *testing.T, s *Server, body string) map[string]interface{} {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/recommend/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.handleRecommendUpdateConfig(rr, req)
	if rr.Code != 200 {
		t.Fatalf("PUT body=%s → HTTP %d: %s", body, rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rr.Body.String())
	}
	return out
}

// readConfigYAML re-reads the config file from disk and unmarshals it, so
// tests can assert the on-disk form (encryption, ${VAR} preservation, etc).
func readConfigYAML(t *testing.T) *config.Config {
	t.Helper()
	data, err := os.ReadFile(config.ConfigPath())
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := &config.Config{}
	if err := unmarshalYAML(data, cfg); err != nil {
		t.Fatalf("unmarshal config.yaml: %v", err)
	}
	return cfg
}

// --- Translation round-trip tests ---------------------------------------

func TestHandleRecommendUpdateConfig_TranslationRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	t.Run("nil → reuse-main (empty fields) keeps ${VAR} reference on disk", func(t *testing.T) {
		cfg := makeTestCfg(t)
		s := newTestServer(cfg)

		if buildTranslationClient(cfg) != nil {
			t.Fatal("precondition: translation client should be nil before save")
		}

		doPut(t, s, `{"api":{"translation":{"base_url":"","api_key":"","model":""}}}`)

		// In-memory: Translation sub-config is populated with the main API values.
		if cfg.API.Translation == nil {
			t.Fatal("Translation should be set after reuse-main save")
		}
		if cfg.API.Translation.BaseURL != "https://api.test.com/v1" {
			t.Errorf("Translation.BaseURL = %q, want main API base URL", cfg.API.Translation.BaseURL)
		}
		if cfg.API.Translation.APIKey != "sk-test-plaintext" {
			t.Errorf("Translation.APIKey = %q, want resolved plaintext (so client can authenticate)", cfg.API.Translation.APIKey)
		}
		if cfg.API.Translation.Model != "test-model" {
			t.Errorf("Translation.Model = %q, want main API model", cfg.API.Translation.Model)
		}

		// RawOnDisk* tracks the on-disk reference form so Save() preserves ${VAR}.
		if cfg.RawOnDiskTranslationAPIKey != "${OPENAI_API_KEY}" {
			t.Errorf("RawOnDiskTranslationAPIKey = %q, want ${OPENAI_API_KEY}", cfg.RawOnDiskTranslationAPIKey)
		}

		// Client should be non-nil now (we have a valid endpoint).
		if c := buildTranslationClient(cfg); c == nil {
			t.Error("buildTranslationClient returned nil — HIGH 1 regression: key not resolved?")
		}

		// On disk: api_key must be the literal ${OPENAI_API_KEY} reference.
		onDisk := readConfigYAML(t)
		if onDisk.API.Translation == nil {
			t.Fatal("on-disk translation section missing")
		}
		if onDisk.API.Translation.APIKey != "${OPENAI_API_KEY}" {
			t.Errorf("on-disk api_key = %q, want ${OPENAI_API_KEY} (must NOT be !!aes:...)", onDisk.API.Translation.APIKey)
		}
	})

	t.Run("reuse-main → null removes the section entirely", func(t *testing.T) {
		cfg := makeTestCfg(t)
		// Seed translation as if reuse-main had already been saved.
		cfg.API.Translation = &config.APIEndpoint{
			BaseURL: cfg.API.BaseURL,
			APIKey:  cfg.API.APIKey,
			Model:   cfg.API.DefaultModel,
		}
		cfg.RawOnDiskTranslationAPIKey = cfg.RawOnDiskAPIKey
		s := newTestServer(cfg)

		doPut(t, s, `{"api":{"translation":null}}`)

		if cfg.API.Translation != nil {
			t.Errorf("Translation should be nil after explicit null, got %+v", cfg.API.Translation)
		}

		onDisk := readConfigYAML(t)
		if onDisk.API.Translation != nil {
			t.Errorf("on-disk translation should be absent, got %+v", onDisk.API.Translation)
		}

		if c := buildTranslationClient(cfg); c != nil {
			t.Error("buildTranslationClient should return nil when translation is unset")
		}
	})

	t.Run("dedicated key (plaintext) is encrypted on disk", func(t *testing.T) {
		cfg := makeTestCfg(t)
		s := newTestServer(cfg)

		doPut(t, s, `{"api":{"translation":{"base_url":"https://deepl.test/v2","api_key":"deepl-plain-key","model":"deepl-model"}}}`)

		// In-memory is the resolved/wrapped form.
		if cfg.API.Translation.APIKey != "deepl-plain-key" {
			t.Errorf("in-memory api_key = %q, want plaintext", cfg.API.Translation.APIKey)
		}
		if cfg.RawOnDiskTranslationAPIKey != "deepl-plain-key" {
			t.Errorf("RawOnDiskTranslationAPIKey = %q, want plaintext (Save will encrypt it)", cfg.RawOnDiskTranslationAPIKey)
		}

		onDisk := readConfigYAML(t)
		if onDisk.API.Translation == nil {
			t.Fatal("on-disk translation section missing")
		}
		if !strings.HasPrefix(onDisk.API.Translation.APIKey, "!!aes:") {
			t.Errorf("on-disk api_key = %q, want !!aes:... prefix", onDisk.API.Translation.APIKey)
		}
	})

	t.Run("bare env var name is wrapped as ${VAR} reference on disk", func(t *testing.T) {
		cfg := makeTestCfg(t)
		s := newTestServer(cfg)

		doPut(t, s, `{"api":{"translation":{"base_url":"https://x.test/v1","api_key":"OPENAI_API_KEY","model":"m"}}}`)

		onDisk := readConfigYAML(t)
		if onDisk.API.Translation == nil {
			t.Fatal("on-disk translation section missing")
		}
		if onDisk.API.Translation.APIKey != "${OPENAI_API_KEY}" {
			t.Errorf("on-disk api_key = %q, want ${OPENAI_API_KEY} (LOW #2: bare name should be wrapped, not encrypted)", onDisk.API.Translation.APIKey)
		}
	})

	t.Run("switching from dedicated → reuse-main overwrites in place", func(t *testing.T) {
		cfg := makeTestCfg(t)
		// Seed a dedicated translation config (different from main API).
		cfg.API.Translation = &config.APIEndpoint{
			BaseURL: "https://deepl.test/v2",
			APIKey:  "deepl-old-key",
			Model:   "deepl-model",
		}
		cfg.RawOnDiskTranslationAPIKey = "${DEEPL_API_KEY}" // suppose it was a ref
		s := newTestServer(cfg)

		// Verify pre-state: client built from the dedicated config.
		if c := buildTranslationClient(cfg); c == nil {
			t.Fatal("precondition: translation client should be non-nil")
		}

		// Simulate UI toggle: send empty fields (= reuse-main).
		doPut(t, s, `{"api":{"translation":{"base_url":"","api_key":"","model":""}}}`)

		// HIGH 2 regression guard: this MUST overwrite, not silently keep the old values.
		if cfg.API.Translation.BaseURL != "https://api.test.com/v1" {
			t.Errorf("BaseURL = %q, want main API (HIGH 2 regression: did not overwrite)", cfg.API.Translation.BaseURL)
		}
		if cfg.API.Translation.APIKey != "sk-test-plaintext" {
			t.Errorf("APIKey = %q, want main API resolved plaintext", cfg.API.Translation.APIKey)
		}
		if cfg.API.Translation.Model != "test-model" {
			t.Errorf("Model = %q, want main API model", cfg.API.Translation.Model)
		}
		if cfg.RawOnDiskTranslationAPIKey != "${OPENAI_API_KEY}" {
			t.Errorf("RawOnDiskTranslationAPIKey = %q, want ${OPENAI_API_KEY} (old DEEPL ref should be gone)", cfg.RawOnDiskTranslationAPIKey)
		}
	})
}

// --- Scoring symmetry tests ---------------------------------------------

func TestHandleRecommendUpdateConfig_ScoringRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	t.Run("scoring plain key is encrypted on disk; RawOnDisk is updated (LOW #1)", func(t *testing.T) {
		cfg := makeTestCfg(t)
		s := newTestServer(cfg)

		doPut(t, s, `{"api":{"scoring":{"base_url":"https://score.test/v1","api_key":"score-plain-key","model":"score-model"}}}`)

		if cfg.API.Scoring == nil {
			t.Fatal("Scoring should be set")
		}
		// LOW #1 regression guard: RawOnDiskScoringAPIKey must be set, otherwise
		// Save() would re-write the stale main-API reference and silently drop
		// the user's new scoring key on restart.
		if cfg.RawOnDiskScoringAPIKey != "score-plain-key" {
			t.Errorf("RawOnDiskScoringAPIKey = %q, want plaintext (LOW #1: must track new key)", cfg.RawOnDiskScoringAPIKey)
		}

		onDisk := readConfigYAML(t)
		if onDisk.API.Scoring == nil {
			t.Fatal("on-disk scoring section missing")
		}
		if !strings.HasPrefix(onDisk.API.Scoring.APIKey, "!!aes:") {
			t.Errorf("on-disk scoring api_key = %q, want !!aes:... prefix", onDisk.API.Scoring.APIKey)
		}
	})

	t.Run("scoring bare env var name is wrapped as ${VAR} (LOW #2 symmetry)", func(t *testing.T) {
		cfg := makeTestCfg(t)
		s := newTestServer(cfg)

		doPut(t, s, `{"api":{"scoring":{"base_url":"https://score.test/v1","api_key":"OPENAI_API_KEY","model":"m"}}}`)

		onDisk := readConfigYAML(t)
		if onDisk.API.Scoring == nil {
			t.Fatal("on-disk scoring section missing")
		}
		if onDisk.API.Scoring.APIKey != "${OPENAI_API_KEY}" {
			t.Errorf("on-disk scoring api_key = %q, want ${OPENAI_API_KEY}", onDisk.API.Scoring.APIKey)
		}
	})
}

// --- YAML helpers --------------------------------------------------------

func unmarshalYAML(data []byte, out *config.Config) error {
	return yaml.Unmarshal(data, out)
}