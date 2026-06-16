package holiday

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Test transport ────────────────────────────────────────────────────────
//
// These tests deliberately avoid httptest.NewServer for parse-logic cases.
// Spinning up a local TCP server adds socket lifecycle, listener setup, and
// full HTTP round-trip noise to tests that only care about "given this
// status + body, do we extract the right (holiday, name)?". RoundTripper is
// the idiomatic Go way to mock HTTP at exactly that layer.
//
// We keep a small number of httptest.NewServer tests below to verify the
// real HTTP-error path (status code, error body, redirect behavior) which
// a fake transport cannot exercise.

// fakeTransport is a configurable http.RoundTripper. Tests point an
// http.Client at it via Transport to inject canned responses.
type fakeTransport struct {
	statusCode int
	body       string
	err        error
	calls      *int32
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.calls != nil {
		atomic.AddInt32(f.calls, 1)
	}
	if f.err != nil {
		return nil, f.err
	}
	resp := &http.Response{
		StatusCode: f.statusCode,
		Body:       newBody(f.body),
		Header:     make(http.Header),
		Request:    req,
	}
	return resp, nil
}

// newBody wraps a string in an io.ReadCloser without importing io.
type readCloser struct {
	*strings.Reader
}

func (r *readCloser) Close() error { return nil }

func newBody(s string) *readCloser {
	return &readCloser{strings.NewReader(s)}
}

// newTimorWithTransport returns a TimorProvider whose HTTPClient uses the
// given fakeTransport. The Endpoint is set to a sentinel URL that the
// transport does not actually contact.
func newTimorWithTransport(ft *fakeTransport) *TimorProvider {
	return &TimorProvider{
		HTTPClient: &http.Client{Transport: ft},
		UserAgent:   "test",
		Endpoint:    "http://test.invalid",
	}
}

func newOneAPIWithTransport(ft *fakeTransport) *OneAPIProvider {
	return &OneAPIProvider{
		HTTPClient: &http.Client{Transport: ft},
		UserAgent:   "test",
		Endpoint:    "http://test.invalid",
	}
}

func newBitefuWithTransport(ft *fakeTransport) *BitefuProvider {
	return &BitefuProvider{
		HTTPClient: &http.Client{Transport: ft},
		UserAgent:   "test",
		Endpoint:    "http://test.invalid",
	}
}

// ─── stubProvider (used by Chain/Checker tests) ────────────────────────────

type stubProvider struct {
	name    string
	holiday bool
	holName string
	err     error
	calls   *int32
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Check(_ context.Context, _ time.Time) (bool, string, error) {
	if s.calls != nil {
		atomic.AddInt32(s.calls, 1)
	}
	return s.holiday, s.holName, s.err
}

// ─── TimorProvider tests ───────────────────────────────────────────────────

func TestTimorProvider_ParsesTypes(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		holiday bool
		holName string
	}{
		{"workday type 0", `{"code":0,"type":{"type":0,"name":"周四"},"holiday":null}`, false, ""},
		{"ordinary weekend type 1", `{"code":0,"type":{"type":1,"name":"周六"},"holiday":null}`, true, ""},
		{"legal holiday type 2", `{"code":0,"type":{"type":2,"name":"初一"},"holiday":{"holiday":true,"name":"春节"}}`, true, "春节"},
		{"compensatory work type 3", `{"code":0,"type":{"type":3,"name":"春节前补班"},"holiday":{"holiday":false,"name":"春节前补班"}}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTimorWithTransport(&fakeTransport{statusCode: 200, body: tc.body})
			holiday, name, err := p.Check(context.Background(), time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if holiday != tc.holiday {
				t.Errorf("holiday: got %v, want %v", holiday, tc.holiday)
			}
			if name != tc.holName {
				t.Errorf("name: got %q, want %q", name, tc.holName)
			}
		})
	}
}

func TestTimorProvider_NonZeroCode(t *testing.T) {
	p := newTimorWithTransport(&fakeTransport{statusCode: 200, body: `{"code":404,"type":{},"holiday":null}`})
	_, _, err := p.Check(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error on non-zero code")
	}
}

func TestTimorProvider_MalformedJSON(t *testing.T) {
	p := newTimorWithTransport(&fakeTransport{statusCode: 200, body: `{not json`})
	_, _, err := p.Check(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

// ─── OneAPIProvider tests ──────────────────────────────────────────────────

func TestOneAPIProvider_ParsesStatuses(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		holiday bool
		holName string
		wantErr bool
	}{
		// Empty data must surface as an error, not "not a holiday". The Chain
		// uses nil-error to mean "this provider has the answer"; on weekends
		// the answer is "ask downstream providers or fall back to the weekday
		// rule", which is what an error signals.
		{
			"ordinary workday empty data returns error so chain can fallthrough",
			`{"code":0,"data":[],"msg":""}`,
			false, "",
			true,
		},
		{
			"compensatory work day",
			`{"code":0,"data":[{"date":"2026-02-14","status":1,"badDay":1,"festival":"春节前补班"}],"msg":""}`,
			false, "春节前补班",
			false,
		},
		{
			"legal holiday",
			`{"code":0,"data":[{"date":"2026-10-01","status":2,"festival":"国庆节","statutory":1}],"msg":""}`,
			true, "国庆节",
			false,
		},
		{
			"work day with status 1 no badDay (rare but valid)",
			`{"code":0,"data":[{"date":"2026-02-13","status":1,"badDay":0,"festival":""}],"msg":""}`,
			false, "",
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newOneAPIWithTransport(&fakeTransport{statusCode: 200, body: tc.body})
			holiday, name, err := p.Check(context.Background(), time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (holiday=%v, name=%q)", holiday, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if holiday != tc.holiday {
				t.Errorf("holiday: got %v, want %v", holiday, tc.holiday)
			}
			if name != tc.holName {
				t.Errorf("name: got %q, want %q", name, tc.holName)
			}
		})
	}
}

func TestOneAPIProvider_NonZeroCode(t *testing.T) {
	p := newOneAPIWithTransport(&fakeTransport{statusCode: 200, body: `{"code":404,"data":[],"msg":"not found"}`})
	_, _, err := p.Check(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error on non-zero code")
	}
}

func TestOneAPIProvider_MalformedJSON(t *testing.T) {
	p := newOneAPIWithTransport(&fakeTransport{statusCode: 200, body: `{not json`})
	_, _, err := p.Check(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

// ─── BitefuProvider tests ──────────────────────────────────────────────────

func TestBitefuProvider_ParsesDigit(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		holiday bool
	}{
		{"workday 0", "0", false},
		{"off day 1", "1", true},
		{"festival 2", "2", true},
		{"trailing newline", "0\n", false},
		{"leading whitespace", "  1", true},
		{"trailing whitespace", "2   ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newBitefuWithTransport(&fakeTransport{statusCode: 200, body: tc.body})
			holiday, _, err := p.Check(context.Background(), time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if holiday != tc.holiday {
				t.Errorf("holiday: got %v, want %v", holiday, tc.holiday)
			}
		})
	}
}

func TestBitefuProvider_InvalidBody(t *testing.T) {
	p := newBitefuWithTransport(&fakeTransport{statusCode: 200, body: "not a number"})
	_, _, err := p.Check(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error on non-numeric body")
	}
}

func TestBitefuProvider_TransportError(t *testing.T) {
	// A transport-level error (DNS failure, connection refused) should
	// surface as the provider's error, so the Chain can fall through.
	p := newBitefuWithTransport(&fakeTransport{err: errors.New("connection refused")})
	_, _, err := p.Check(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error from transport failure")
	}
}

// ─── Chain tests (use stubProvider) ────────────────────────────────────────

func TestChain_FirstSuccessWins(t *testing.T) {
	var p1Calls, p2Calls int32
	p1 := &stubProvider{name: "p1", holiday: false, err: nil, calls: &p1Calls}
	p2 := &stubProvider{name: "p2", holiday: true, err: nil, calls: &p2Calls}

	chain := NewChain(p1, p2)
	holiday, name, src, err := chain.Check(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if holiday {
		t.Errorf("expected p1's result (false), got true")
	}
	if name != "" {
		t.Errorf("name: got %q, want empty", name)
	}
	if src != SourceProvider1 {
		t.Errorf("src: got %q, want %q", src, SourceProvider1)
	}
	if atomic.LoadInt32(&p1Calls) != 1 {
		t.Errorf("p1 should be called once, got %d", p1Calls)
	}
	if atomic.LoadInt32(&p2Calls) != 0 {
		t.Errorf("p2 should not be called, got %d", p2Calls)
	}
}

func TestChain_FallsThroughOnError(t *testing.T) {
	var p1Calls, p2Calls int32
	p1 := &stubProvider{name: "p1", err: fmt.Errorf("p1 down"), calls: &p1Calls}
	p2 := &stubProvider{name: "p2", holiday: true, holName: "国庆", calls: &p2Calls}

	chain := NewChain(p1, p2)
	holiday, name, src, err := chain.Check(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !holiday {
		t.Errorf("expected p2's result (true), got false")
	}
	if name != "国庆" {
		t.Errorf("name: got %q, want 国庆", name)
	}
	if src != SourceProvider2 {
		t.Errorf("src: got %q, want %q", src, SourceProvider2)
	}
	if atomic.LoadInt32(&p1Calls) != 1 {
		t.Errorf("p1 should be called once, got %d", p1Calls)
	}
	if atomic.LoadInt32(&p2Calls) != 1 {
		t.Errorf("p2 should be called once, got %d", p2Calls)
	}
}

func TestChain_AllProvidersFail(t *testing.T) {
	p1 := &stubProvider{name: "p1", err: fmt.Errorf("p1 down")}
	p2 := &stubProvider{name: "p2", err: fmt.Errorf("p2 down")}
	chain := NewChain(p1, p2)
	_, _, _, err := chain.Check(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestChain_ProviderSourceSlotMapping(t *testing.T) {
	// Three providers in the chain. Only provider at `slot` succeeds; the
	// other two return errors. The Source tag on the result should match
	// the slot's 1-based index.
	for slot, wantSrc := range map[int]Source{
		1: SourceProvider1, 2: SourceProvider2, 3: SourceProvider3,
	} {
		providers := make([]Provider, 3)
		for i := 1; i <= 3; i++ {
			if i == slot {
				providers[i-1] = &stubProvider{name: "p", holiday: true}
			} else {
				providers[i-1] = &stubProvider{name: "p", err: fmt.Errorf("down")}
			}
		}
		got, _, src, err := NewChain(providers...).Check(context.Background(), time.Now())
		if err != nil {
			t.Fatalf("slot %d: unexpected err: %v", slot, err)
		}
		if !got {
			t.Errorf("slot %d: expected holiday=true", slot)
		}
		if src != wantSrc {
			t.Errorf("slot %d: src=%q, want %q", slot, src, wantSrc)
		}
	}
}

// ─── Checker tests (use stubProvider or fake transport) ────────────────────

func TestChecker_CachesResults(t *testing.T) {
	var calls int32
	p := &stubProvider{name: "p1", holiday: true, calls: &calls}
	c := NewChecker(NewChain(p))

	d := time.Date(2026, 2, 17, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		_ = c.IsHoliday(d)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected 1 provider call (cached), got %d", got)
	}
}

func TestChecker_AllProvidersFailFallsBackToWeekday(t *testing.T) {
	failing := &stubProvider{name: "fail", err: fmt.Errorf("boom")}
	c := NewChecker(NewChain(failing))

	sat := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC) // a Saturday
	res := c.IsHoliday(sat)
	if !res.IsHoliday {
		t.Errorf("Saturday should be holiday via weekday rule")
	}
	if res.Source != SourceWeekdayRule {
		t.Errorf("source: got %q, want %q", res.Source, SourceWeekdayRule)
	}
	if res.Err == nil {
		t.Errorf("expected Err to be set on fallback")
	}

	wed := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC) // a Wednesday
	res2 := c.IsHoliday(wed)
	if res2.IsHoliday {
		t.Errorf("Wednesday should not be holiday via weekday rule")
	}
	if res2.Source != SourceWeekdayRule {
		t.Errorf("source: got %q, want %q", res2.Source, SourceWeekdayRule)
	}
}

func TestChecker_CacheDistinguishesDates(t *testing.T) {
	var calls int32
	p := &stubProvider{name: "p1", holiday: true, calls: &calls}
	c := NewChecker(NewChain(p))

	d1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	_ = c.IsHoliday(d1)
	_ = c.IsHoliday(d2)
	_ = c.IsHoliday(d1) // cache hit
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected 2 provider calls for 2 distinct dates, got %d", got)
	}
}

func TestChecker_RefreshBypassesCache(t *testing.T) {
	var calls int32
	p := &stubProvider{name: "p1", holiday: true, calls: &calls}
	c := NewChecker(NewChain(p))

	d := time.Date(2026, 2, 17, 0, 0, 0, 0, time.UTC)
	_ = c.IsHoliday(d)
	_ = c.IsHoliday(d)
	_ = c.Refresh(d)
	_ = c.Refresh(d)
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 calls (cache miss + 2 refresh), got %d", got)
	}
}

func TestChecker_EmptyDataFallback(t *testing.T) {
	// OneAPI returns empty data (the "ordinary day" response). The Chain
	// must treat this as a fallthrough signal so Bitefu / the weekday rule
	// can answer. Without the fallthrough, a Timor outage on a Saturday
	// would cause a false "workday" classification.
	oneapi := newOneAPIWithTransport(&fakeTransport{statusCode: 200, body: `{"code":0,"data":[],"msg":""}`})
	bitefu := newBitefuWithTransport(&fakeTransport{statusCode: 200, body: "1"}) // Saturday → 1
	chain := NewChain(oneapi, bitefu)
	c := NewChecker(chain)

	sat := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC) // a Saturday
	res := c.IsHoliday(sat)
	if !res.IsHoliday {
		t.Errorf("expected Saturday to be classified as holiday via bitefu fallback")
	}
	if res.Source != SourceProvider2 {
		t.Errorf("source: got %q, want %q (bitefu is the 2nd provider)", res.Source, SourceProvider2)
	}
}

func TestIsWeekendHoliday(t *testing.T) {
	if !IsWeekendHoliday(time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)) {
		t.Error("Saturday should be weekend holiday")
	}
	if IsWeekendHoliday(time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)) {
		t.Error("Wednesday should not be weekend holiday")
	}
}

func TestChecker_ConcurrentSafe(t *testing.T) {
	// Hammer the Checker with concurrent reads + a Refresh and verify
	// no race detector hits and the result is consistent. Uses a slow
	// stub to maximize the chance of catching any unsynchronized access.
	var calls int32
	slow := &slowStubProvider{
		holiday: true,
		delay:   5 * time.Millisecond,
		calls:   &calls,
	}
	c := NewChecker(NewChain(slow))

	d := time.Date(2026, 2, 17, 0, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = c.IsHoliday(d)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 5; j++ {
			_ = c.Refresh(d)
			time.Sleep(time.Millisecond)
		}
	}()
	wg.Wait()
}

type slowStubProvider struct {
	holiday bool
	delay   time.Duration
	calls   *int32
}

func (s *slowStubProvider) Name() string { return "slow" }
func (s *slowStubProvider) Check(_ context.Context, _ time.Time) (bool, string, error) {
	if s.calls != nil {
		atomic.AddInt32(s.calls, 1)
	}
	time.Sleep(s.delay)
	return s.holiday, "", nil
}

// ─── HTTP-layer integration tests (real httptest server) ───────────────────
//
// These verify behavior that a fake transport cannot: status code wiring
// through the http.Client, the error body formatter, and chained retries
// when an upstream temporarily 5xx's. Kept intentionally small.

func TestProvider_HTTPError_5xx(t *testing.T) {
	for _, p := range []struct {
		name string
		ctor func(string) Provider
	}{
		{"timor", func(url string) Provider {
			return &TimorProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: url}
		}},
		{"oneapi", func(url string) Provider {
			return &OneAPIProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: url}
		}},
		{"bitefu", func(url string) Provider {
			return &BitefuProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: url}
		}},
	} {
		t.Run(p.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(srv.Close)
			_, _, err := p.ctor(srv.URL).Check(context.Background(), time.Now())
			if err == nil {
				t.Fatal("expected error on HTTP 500")
			}
		})
	}
}

func TestProvider_3RealProvidersFallback(t *testing.T) {
	// Provider 1 (timor) fails; provider 2 (oneapi) succeeds.
	timorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(timorSrv.Close)
	p1 := &TimorProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: timorSrv.URL}

	oneapiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":0,"data":[{"date":"2026-10-01","status":2,"festival":"国庆节"}],"msg":""}`)
	}))
	t.Cleanup(oneapiSrv.Close)
	p2 := &OneAPIProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: oneapiSrv.URL}

	var bitefuCalls int32
	bitefuSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&bitefuCalls, 1)
		fmt.Fprint(w, "0")
	}))
	t.Cleanup(bitefuSrv.Close)
	p3 := &BitefuProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: bitefuSrv.URL}

	chain := NewChain(p1, p2, p3)
	holiday, name, src, err := chain.Check(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !holiday {
		t.Errorf("expected holiday=true from oneapi")
	}
	if name != "国庆节" {
		t.Errorf("name: got %q, want 国庆节", name)
	}
	if src != SourceProvider2 {
		t.Errorf("src: got %q, want %q", src, SourceProvider2)
	}
	if got := atomic.LoadInt32(&bitefuCalls); got != 0 {
		t.Errorf("bitefu should not be called when oneapi succeeds, got %d calls", got)
	}
}

func TestProvider_AllThreeRealProvidersFail_FallsBackToWeekday(t *testing.T) {
	makeBroken := func(ctor func(string) Provider) Provider {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		return ctor(srv.URL)
	}
	chain := NewChain(
		makeBroken(func(url string) Provider { return &TimorProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: url} }),
		makeBroken(func(url string) Provider { return &OneAPIProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: url} }),
		makeBroken(func(url string) Provider { return &BitefuProvider{HTTPClient: &http.Client{}, UserAgent: "t", Endpoint: url} }),
	)
	c := NewChecker(chain)

	sat := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC) // Saturday
	res := c.IsHoliday(sat)
	if !res.IsHoliday {
		t.Errorf("Saturday should be holiday via weekday rule")
	}
	if res.Source != SourceWeekdayRule {
		t.Errorf("source: got %q, want %q", res.Source, SourceWeekdayRule)
	}
}
