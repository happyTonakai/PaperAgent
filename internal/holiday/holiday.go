// Package holiday determines whether a given date is a Chinese public holiday
// or compensatory work day. It abstracts multiple data providers with a
// chain-of-responsibility fallback, and degrades to a simple weekday-based
// rule when all providers fail.
package holiday

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Source identifies how a holiday decision was made.
type Source string

const (
	// SourceProviderN is returned by Chain.Check when provider N (1-indexed)
	// supplied the answer. We support up to three providers.
	SourceProvider1 Source = "provider-1"
	SourceProvider2 Source = "provider-2"
	SourceProvider3 Source = "provider-3"
	// SourceWeekdayRule is the fallback when every provider failed: Saturday
	// and Sunday count as holidays, everything else is a workday.
	SourceWeekdayRule Source = "weekday-rule"
)

// providerSource returns the Source constant for the 1-indexed provider slot.
// Out-of-range indices get a generic "provider-N" label so a future 4th
// provider still produces a unique, debuggable source tag.
func providerSource(idx int) Source {
	switch idx {
	case 1:
		return SourceProvider1
	case 2:
		return SourceProvider2
	case 3:
		return SourceProvider3
	default:
		return Source(fmt.Sprintf("provider-%d", idx))
	}
}

// CheckResult is the outcome of an IsHoliday query.
type CheckResult struct {
	IsHoliday bool
	Source    Source
	Name      string // holiday name (e.g. "春节") if known, "" otherwise
	Err       error  // non-nil only when the result is a degraded fallback
}

// Provider supplies holiday decisions for a given date.
// Implementations should be safe for concurrent use.
type Provider interface {
	// Name returns a short stable identifier for logging.
	Name() string
	// Check returns true if the date is a non-working day in the provider's
	// calendar. Compensatory work days (调休补班) must return false.
	// ctx is used for cancellation and timeouts.
	Check(ctx context.Context, date time.Time) (holiday bool, name string, err error)
}

// ─── Timor.tech provider ──────────────────────────────────────────────────

const timorDateFormat = "2006-01-02"
const timorDefaultEndpoint = "https://timor.tech/api/holiday/info"

// TimorProvider queries timor.tech for Chinese holiday data.
// type.type field semantics:
//
//	0 = workday
//	1 = ordinary weekend (not in holiday calendar)
//	2 = legal holiday / holiday continuation
//	3 = compensatory work day (调休补班, NOT a holiday)
type TimorProvider struct {
	HTTPClient *http.Client
	// UserAgent is sent on every request. Cloudflare blocks default Go UAs.
	UserAgent string
	// Endpoint is the base URL. Empty means timorDefaultEndpoint.
	// Tests override this to point at a httptest server.
	Endpoint string
}

func NewTimorProvider() *TimorProvider {
	return &TimorProvider{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		UserAgent:  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
}

func (p *TimorProvider) endpoint() string {
	if p.Endpoint != "" {
		return p.Endpoint
	}
	return timorDefaultEndpoint
}

func (p *TimorProvider) Name() string { return "timor" }

type timorResponse struct {
	Code int `json:"code"`
	Type struct {
		Type int    `json:"type"`
		Name string `json:"name"`
	} `json:"type"`
	Holiday *struct {
		Holiday bool   `json:"holiday"`
		Name    string `json:"name"`
	} `json:"holiday"`
}

func (p *TimorProvider) Check(ctx context.Context, date time.Time) (bool, string, error) {
	dateStr := date.Format(timorDateFormat)
	url := fmt.Sprintf("%s/%s", p.endpoint(), dateStr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("User-Agent", p.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return false, "", fmt.Errorf("timor: http %d: %s", resp.StatusCode, string(body))
	}

	var r timorResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return false, "", fmt.Errorf("timor: decode: %w", err)
	}
	if r.Code != 0 {
		return false, "", fmt.Errorf("timor: code %d", r.Code)
	}

	// type 0 (workday) and type 3 (调休补班) are both workdays.
	// type 1 (ordinary weekend) and type 2 (legal holiday) are non-workdays.
	isHoliday := r.Type.Type == 1 || r.Type.Type == 2
	name := ""
	if r.Holiday != nil && r.Holiday.Holiday {
		name = r.Holiday.Name
	}
	return isHoliday, name, nil
}

// ─── OneAPI provider ───────────────────────────────────────────────────

const oneapiDefaultEndpoint = "https://oneapi.coderbox.cn/openapi/public/holiday"

// OneAPIProvider queries oneapi.coderbox.cn for Chinese holiday data.
//
// Response shape:
//   - {"code":0,"data":[]}                      → ordinary workday/weekend
//   - {"code":0,"data":[{status:1, badDay:1}]}  → compensatory work day
//   - {"code":0,"data":[{status:2, festival:"..."}]} → legal holiday
//
// `status: 2` means "holiday" (放假); `status: 1` means "work" (上班, including
// 调休补班). `badDay: 1` explicitly tags a 调休补班 day. The provider treats
// `data: []` as "no special status" — the caller decides what to do with
// ordinary weekends via the weekday rule.
type OneAPIProvider struct {
	HTTPClient *http.Client
	UserAgent  string
	Endpoint   string
}

func NewOneAPIProvider() *OneAPIProvider {
	return &OneAPIProvider{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		UserAgent:  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
}

func (p *OneAPIProvider) endpoint() string {
	if p.Endpoint != "" {
		return p.Endpoint
	}
	return oneapiDefaultEndpoint
}

func (p *OneAPIProvider) Name() string { return "oneapi" }

type oneapiResponse struct {
	Code int `json:"code"`
	Data []struct {
		Status   int    `json:"status"`   // 1=work, 2=holiday
		BadDay   int    `json:"badDay"`   // 1=调休补班 (compulsory work day)
		Festival string `json:"festival"` // holiday name, e.g. "国庆节"
	} `json:"data"`
}

func (p *OneAPIProvider) Check(ctx context.Context, date time.Time) (bool, string, error) {
	dateStr := date.Format(timorDateFormat)
	url := fmt.Sprintf("%s?date=%s", p.endpoint(), dateStr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("User-Agent", p.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return false, "", fmt.Errorf("oneapi: http %d: %s", resp.StatusCode, string(body))
	}

	var r oneapiResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return false, "", fmt.Errorf("oneapi: decode: %w", err)
	}
	if r.Code != 0 {
		return false, "", fmt.Errorf("oneapi: code %d", r.Code)
	}
	if len(r.Data) == 0 {
		// No special status from the API. Returning nil here would let the
		// Chain treat this as success ("not a holiday") and skip downstream
		// providers + the weekday fallback. That misclassifies ordinary
		// weekends as workdays when the primary provider (Timor) is down.
		// Instead, return an error so the Chain falls through to Bitefu and
		// finally to the weekday rule.
		return false, "", fmt.Errorf("oneapi: no holiday data for %s", date.Format(timorDateFormat))
	}
	first := r.Data[0]
	if first.Status == 2 {
		return true, first.Festival, nil
	}
	// status == 1 with badDay == 1 is a 调休补班 day (work).
	// status == 1 without badDay is rare but treated as work.
	return false, first.Festival, nil
}

// ─── Bitefu provider ──────────────────────────────────────────────────

const bitefuDefaultEndpoint = "https://tool.bitefu.net/jiari/"

// BitefuProvider queries tool.bitefu.net for Chinese holiday data.
//
// Response is a single-digit integer as the response body:
//
//	0 = workday (including 调休补班)
//	1 = weekend or holiday continuation day (i.e. off-day but not the
//	    named festival itself)
//	2 = the festival day itself
//
// For our purposes, any non-zero value means "skip push".
type BitefuProvider struct {
	HTTPClient *http.Client
	UserAgent  string
	Endpoint   string
}

func NewBitefuProvider() *BitefuProvider {
	return &BitefuProvider{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		UserAgent:  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
}

func (p *BitefuProvider) endpoint() string {
	if p.Endpoint != "" {
		return p.Endpoint
	}
	return bitefuDefaultEndpoint
}

func (p *BitefuProvider) Name() string { return "bitefu" }

func (p *BitefuProvider) Check(ctx context.Context, date time.Time) (bool, string, error) {
	dateStr := date.Format(timorDateFormat)
	url := fmt.Sprintf("%s?d=%s", p.endpoint(), dateStr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("User-Agent", p.UserAgent)
	req.Header.Set("Accept", "text/plain")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return false, "", fmt.Errorf("bitefu: http %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32))
	if err != nil {
		return false, "", fmt.Errorf("bitefu: read: %w", err)
	}
	// Body may have leading/trailing whitespace. Trim and parse.
	val, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return false, "", fmt.Errorf("bitefu: parse %q: %w", string(body), err)
	}
	// Any non-zero value means "off-day" per the API's documented contract.
	// We don't have a festival name in the response.
	return val != 0, "", nil
}

// ─── Chain ────────────────────────────────────────────────────────────────

// Chain tries providers in order; the first one to succeed wins.
type Chain struct {
	providers []Provider
}

func NewChain(providers ...Provider) *Chain {
	return &Chain{providers: providers}
}

func (c *Chain) Check(ctx context.Context, date time.Time) (holiday bool, name string, src Source, err error) {
	var lastErr error
	for i, p := range c.providers {
		ok, n, e := p.Check(ctx, date)
		if e == nil {
			return ok, n, providerSource(i + 1), nil
		}
		lastErr = e
		log.Printf("[holiday] provider %s failed: %v", p.Name(), e)
	}
	return false, "", "", fmt.Errorf("all providers failed: %w", lastErr)
}

// ─── Checker (top-level API) ─────────────────────────────────────────────

// Cache TTL for a single date's result. Holiday status never changes once
// determined, so this only matters for memory pressure.
const cacheTTL = 24 * time.Hour

type cacheEntry struct {
	result   CheckResult
	storedAt time.Time
}

// Checker is the public holiday-query entry point. It memoizes results per
// date and falls back to a weekday rule when all configured providers fail.
type Checker struct {
	chain *Chain
	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// NewChecker wraps a provider chain with caching and weekday fallback.
func NewChecker(chain *Chain) *Checker {
	return &Checker{
		chain: chain,
		cache: make(map[string]cacheEntry),
	}
}

// IsHoliday returns whether the given date should be treated as a non-workday.
// The Source field of the result indicates which path was used. The result
// is cached per calendar date; the holiday status of a given date never
// changes once determined, so a cache hit is always safe.
func (c *Checker) IsHoliday(date time.Time) CheckResult {
	dateKey := date.Format(timorDateFormat)

	c.mu.RLock()
	entry, ok := c.cache[dateKey]
	c.mu.RUnlock()
	if ok && time.Since(entry.storedAt) < cacheTTL {
		return entry.result
	}

	result := c.compute(date)

	c.mu.Lock()
	c.cache[dateKey] = cacheEntry{result: result, storedAt: time.Now()}
	c.mu.Unlock()

	return result
}

// Refresh forces a re-query for the given date, ignoring cache.
func (c *Checker) Refresh(date time.Time) CheckResult {
	dateKey := date.Format(timorDateFormat)
	result := c.compute(date)
	c.mu.Lock()
	c.cache[dateKey] = cacheEntry{result: result, storedAt: time.Now()}
	c.mu.Unlock()
	return result
}

func (c *Checker) compute(date time.Time) CheckResult {
	// Outer context budget covers all providers in the chain plus margin.
	// Each provider has a 5s HTTP timeout; with 3 providers serially that's
	// 15s worst case, so we give 16s. If the chain exhausts this budget, we
	// fall back to the weekday rule below.
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Second)
	defer cancel()

	holiday, name, src, err := c.chain.Check(ctx, date)
	if err == nil {
		return CheckResult{IsHoliday: holiday, Source: src, Name: name}
	}

	// All providers failed. Fall back to weekday rule (Sat/Sun = holiday).
	weekday := date.Weekday()
	weekdayHoliday := weekday == time.Saturday || weekday == time.Sunday
	log.Printf("[holiday] all providers failed, using weekday rule for %s (Sat/Sun=%v, weekday=%s)",
		date.Format(timorDateFormat), weekdayHoliday, weekday)
	return CheckResult{
		IsHoliday: weekdayHoliday,
		Source:    SourceWeekdayRule,
		Err:       err,
	}
}

// IsWeekendHoliday applies the simple weekday rule only (Sat/Sun).
// Exposed for tests and for callers that want to bypass providers entirely.
func IsWeekendHoliday(date time.Time) bool {
	w := date.Weekday()
	return w == time.Saturday || w == time.Sunday
}

// ClearCache drops all cached entries. Intended for tests.
func (c *Checker) ClearCache() {
	c.mu.Lock()
	c.cache = make(map[string]cacheEntry)
	c.mu.Unlock()
}
