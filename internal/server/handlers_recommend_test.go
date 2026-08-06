package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/scheduler"
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

// TestHandleRecommendUpdateConfig_MasterSwitch covers the bug where the
// WebUI "启用每日推荐管线" toggle only updated the config file but never
// propagated to the running scheduler: the scheduler kept its boot-time
// enabled state, so unchecking the box still produced daily pushes until
// restart. It also covers the symmetric case: re-enabling from the UI when
// the pipeline was off at boot (scheduler never created) must boot it.
func TestHandleRecommendUpdateConfig_MasterSwitch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	t.Run("disable enabled stops the running scheduler immediately", func(t *testing.T) {
		cfg := makeTestCfg(t)
		cfg.Recommend.Enabled = true
		cfg.ArxivCategories = []string{"cs.AI"}
		s := newTestServer(cfg)
		// Simulate a scheduler that was booted while enabled.
		s.sched = scheduler.New(true, cfg.ArxivCategories, s.scoringClient(), "test-model", 5, 10, 0.3, "08:00", nil)

		doPut(t, s, `{"recommend":{"enabled":false}}`)

		if cfg.Recommend.Enabled {
			t.Fatal("cfg.Recommend.Enabled should be false after unchecking")
		}
		if s.sched.Enabled() {
			t.Fatal("running scheduler should be disabled immediately, not after restart")
		}
	})

	t.Run("re-enable from UI boots a scheduler that wasn't started at boot", func(t *testing.T) {
		cfg := makeTestCfg(t)
		cfg.Recommend.Enabled = false
		cfg.ArxivCategories = []string{"cs.AI"}
		s := newTestServer(cfg)
		if s.sched != nil {
			t.Fatal("precondition: no scheduler should exist when disabled at boot")
		}

		doPut(t, s, `{"recommend":{"enabled":true}}`)

		if s.sched == nil {
			t.Fatal("scheduler should be created when enabled from the UI")
		}
		if !s.sched.Enabled() {
			t.Fatal("newly started scheduler should be enabled")
		}
		if !cfg.Recommend.Enabled {
			t.Fatal("cfg.Recommend.Enabled should be true")
		}

		// The status endpoint (also touched by this change) must now report
		// the runtime-booted scheduler as enabled and scheduled.
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/recommend/scheduler-status", nil)
		s.handleRecommendSchedulerStatus(rr, req)
		if rr.Code != 200 {
			t.Fatalf("GET scheduler-status → HTTP %d: %s", rr.Code, rr.Body.String())
		}
		var st scheduler.SchedulerStatus
		if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
			t.Fatalf("decode scheduler-status: %v", err)
		}
		if !st.Enabled {
			t.Error("scheduler-status should report enabled after runtime boot")
		}
		if st.Scheduled != "08:00" {
			t.Errorf("scheduler-status scheduled = %q, want %q", st.Scheduled, "08:00")
		}

		// buildScheduler started a ticker goroutine; stop it so the test
		// doesn't leak.
		defer s.sched.Stop()
	})

	t.Run("staying disabled leaves no scheduler", func(t *testing.T) {
		cfg := makeTestCfg(t)
		cfg.Recommend.Enabled = false
		cfg.ArxivCategories = []string{"cs.AI"}
		s := newTestServer(cfg)

		doPut(t, s, `{"recommend":{"enabled":false}}`)

		if s.sched != nil {
			t.Fatal("no scheduler should be created while the pipeline stays disabled")
		}
	})

	t.Run("concurrent saves do not double-start the scheduler", func(t *testing.T) {
		cfg := makeTestCfg(t)
		cfg.Recommend.Enabled = false
		cfg.ArxivCategories = []string{"cs.AI"}
		s := newTestServer(cfg)

		// Fire several enable-saves in parallel. schedMu must serialize
		// the s.sched == nil check + buildScheduler assignment so only one
		// scheduler is ever created (a double-start would spawn two ticker
		// loops, each firing onComplete → RunPush).
		const n = 4
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rr := httptest.NewRecorder()
				req := httptest.NewRequest("PUT", "/api/recommend/config", strings.NewReader(`{"recommend":{"enabled":true}}`))
				req.Header.Set("Content-Type", "application/json")
				s.handleRecommendUpdateConfig(rr, req)
				if rr.Code != 200 {
					errs[i] = fmt.Errorf("PUT → HTTP %d: %s", rr.Code, rr.Body.String())
				}
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("concurrent PUT %d: %v", i, err)
			}
		}

		if s.sched == nil {
			t.Fatal("scheduler should exist after concurrent enable")
		}
		if !s.sched.Enabled() {
			t.Fatal("scheduler should be enabled after concurrent enable")
		}
		defer s.sched.Stop()
	})

	t.Run("params-only save keeps the scheduler enabled state", func(t *testing.T) {
		cfg := makeTestCfg(t)
		cfg.Recommend.Enabled = true
		cfg.ArxivCategories = []string{"cs.AI"}
		s := newTestServer(cfg)
		s.sched = scheduler.New(true, cfg.ArxivCategories, s.scoringClient(), "test-model", 5, 10, 0.3, "08:00", nil)

		// Saving e.g. daily_papers must not flip the master switch.
		doPut(t, s, `{"recommend":{"daily_papers":7}}`)

		if !cfg.Recommend.Enabled {
			t.Fatal("cfg.Recommend.Enabled should stay true")
		}
		if !s.sched.Enabled() {
			t.Fatal("scheduler should stay enabled after a params-only save")
		}
	})
}
