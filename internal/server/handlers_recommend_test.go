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

// makeTestCfg returns a Config that mimics a fresh install. HOME must already
// be redirected to a tmp dir so subsequent Save() calls don't touch the user's
// real config.
func makeTestCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.API.BaseURL = "https://api.test.com/v1"
	cfg.API.APIKey = "sk-test-plaintext"
	cfg.API.DefaultModel = "test-model"
	cfg.RawOnDiskAPIKey = "${OPENAI_API_KEY}"
	return cfg
}

// newTestServer builds a minimal Server for testing handleRecommendUpdateConfig.
func newTestServer(cfg *config.Config) *Server {
	return &Server{
		cfg: cfg,
		api: api.NewClient(cfg),
	}
}

// doPut drives handleRecommendUpdateConfig with the given JSON body.
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

func TestHandleRecommendUpdateConfig_EnableTranslation(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	t.Run("enable translation via recommend tab checkbox", func(t *testing.T) {
		cfg := makeTestCfg(t)
		s := newTestServer(cfg)

		// Initially translation is disabled.
		if cfg.Recommend.EnableTranslation {
			t.Fatal("precondition: EnableTranslation should be false")
		}
		if c := s.translationClient(); c != nil {
			t.Fatal("precondition: translationClient should be nil")
		}

		// Enable translation via the recommend tab.
		doPut(t, s, `{"recommend":{"enable_translation":true}}`)

		if !cfg.Recommend.EnableTranslation {
			t.Fatal("EnableTranslation should be true after save")
		}
		if c := s.translationClient(); c == nil {
			t.Fatal("translationClient should return main API client when enabled")
		}

		// Round-trip: config.yaml reflects the change.
		onDisk := readConfigYAML(t)
		if !onDisk.Recommend.EnableTranslation {
			t.Error("on-disk enable_translation should be true")
		}
	})

	t.Run("disable translation", func(t *testing.T) {
		cfg := makeTestCfg(t)
		cfg.Recommend.EnableTranslation = true
		s := newTestServer(cfg)

		doPut(t, s, `{"recommend":{"enable_translation":false}}`)

		if cfg.Recommend.EnableTranslation {
			t.Fatal("EnableTranslation should be false after save")
		}
		if c := s.translationClient(); c != nil {
			t.Fatal("translationClient should be nil when disabled")
		}
	})

	t.Run("get config includes enable_translation", func(t *testing.T) {
		cfg := makeTestCfg(t)
		cfg.Recommend.EnableTranslation = true
		s := newTestServer(cfg)

		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/recommend/config", nil)
		s.handleRecommendGetConfig(rr, req)

		if rr.Code != 200 {
			t.Fatalf("GET → HTTP %d", rr.Code)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		rec, ok := resp["recommend"].(map[string]interface{})
		if !ok {
			t.Fatal("recommend key missing")
		}
		if v, ok := rec["enable_translation"].(bool); !ok || !v {
			t.Errorf("enable_translation = %v, want true", rec["enable_translation"])
		}
		// No legacy api.scoring/api.translation sections.
		if _, exists := resp["api"]; exists {
			t.Error("api key should not exist in response")
		}
	})
}

func unmarshalYAML(data []byte, out *config.Config) error {
	return yaml.Unmarshal(data, out)
}
