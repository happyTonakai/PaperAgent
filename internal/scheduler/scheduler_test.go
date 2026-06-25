package scheduler

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/recommend"
)

// ---------------------------------------------------------------------------
// parseTimeOfDay
// ---------------------------------------------------------------------------

func TestParseTimeOfDay(t *testing.T) {
	tests := []struct {
		input string
		h, m  int
		ok    bool
	}{
		{"08:00", 8, 0, true},
		{"00:00", 0, 0, true},
		{"23:59", 23, 59, true},
		{"12:30", 12, 30, true},
		{"", 0, 0, false},
		{"abc", 0, 0, false},
		{"08:00:00", 0, 0, false}, // too long
		{"8:00", 0, 0, false},     // no leading zero
		{"24:00", 0, 0, false},    // hour out of range
		{"00:60", 0, 0, false},    // minute out of range
		{"-1:00", 0, 0, false},    // impossible via byte arithmetic
		{"ab:cd", 0, 0, false},    // non-digit
	}
	for _, tt := range tests {
		h, m, ok := parseTimeOfDay(tt.input)
		if h != tt.h || m != tt.m || ok != tt.ok {
			t.Errorf("parseTimeOfDay(%q) = (%d, %d, %v), want (%d, %d, %v)",
				tt.input, h, m, ok, tt.h, tt.m, tt.ok)
		}
	}
}

// ---------------------------------------------------------------------------
// rssFetchTimes / rssFetchTimesLocked
// ---------------------------------------------------------------------------

func TestRSSFetchTimes(t *testing.T) {
	tests := []struct {
		scheduled string
		want      []string
	}{
		{"08:00", []string{"07:00", "15:00", "23:00"}},
		{"23:00", []string{"22:00", "06:00", "14:00"}}, // cross midnight
		{"00:30", []string{"23:30", "07:30", "15:30"}}, // cross midnight, non-zero minute
		{"12:15", []string{"11:15", "19:15", "03:15"}},
		{"", nil},      // empty scheduled time
		{"abc", nil},   // invalid format
		{"25:00", nil}, // out of range
	}
	for _, tt := range tests {
		s := New(true, nil, nil, "", 0, 0, 0, tt.scheduled, nil)
		got := s.rssFetchTimes()
		if len(got) != len(tt.want) {
			t.Errorf("scheduled=%q: got %v, want %v", tt.scheduled, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("scheduled=%q index %d: got %q, want %q", tt.scheduled, i, got[i], tt.want[i])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// shouldFetchRSSAt
// ---------------------------------------------------------------------------

func TestShouldFetchRSSAt(t *testing.T) {
	// Use a baseline scheduler with scheduledTime = "08:00"
	// Offsets: -1→07:00, +7→15:00, +15→23:00
	base := func() *Scheduler {
		return New(true, nil, nil, "", 0, 0, 0, "08:00", nil)
	}

	// Helper to create a time with given yyyy-mm-dd, HH:MM in UTC
	tm := func(date string, hour, min int) time.Time {
		t, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %02d:%02d", date, hour, min))
		if err != nil {
			panic(err)
		}
		return t.UTC()
	}

	tests := []struct {
		name     string
		setup    func(s *Scheduler)
		now      time.Time
		wantOK   bool
		wantHour int
	}{
		{
			name:     "hit 07:00 window",
			now:      tm("2026-06-23", 7, 0),
			wantOK:   true,
			wantHour: 7,
		},
		{
			name:     "hit 15:00 window",
			now:      tm("2026-06-23", 15, 0),
			wantOK:   true,
			wantHour: 15,
		},
		{
			name:     "hit 23:00 window",
			now:      tm("2026-06-23", 23, 0),
			wantOK:   true,
			wantHour: 23,
		},
		{
			name:     "minute mismatch",
			now:      tm("2026-06-23", 7, 5),
			wantOK:   false,
			wantHour: -1,
		},
		{
			name:     "hour not in any window",
			now:      tm("2026-06-23", 10, 0),
			wantOK:   false,
			wantHour: -1,
		},
		{
			name: "already fetched this hour today",
			setup: func(s *Scheduler) {
				s.lastFetchDate = "2026-06-23"
				s.lastFetchHour = 7
			},
			now:      tm("2026-06-23", 7, 0),
			wantOK:   false,
			wantHour: -1,
		},
		{
			name: "fetched yesterday same hour — new day, should run",
			setup: func(s *Scheduler) {
				s.lastFetchDate = "2026-06-22"
				s.lastFetchHour = 7
			},
			now:      tm("2026-06-23", 7, 0),
			wantOK:   true,
			wantHour: 7,
		},
		{
			name: "fetched different hour today — should run",
			setup: func(s *Scheduler) {
				s.lastFetchDate = "2026-06-23"
				s.lastFetchHour = 7
			},
			now:      tm("2026-06-23", 15, 0),
			wantOK:   true,
			wantHour: 15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := base()
			if tt.setup != nil {
				tt.setup(s)
			}
			ok, hour := s.shouldFetchRSSAt(tt.now)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if hour != tt.wantHour {
				t.Errorf("hour = %d, want %d", hour, tt.wantHour)
			}
		})
	}
}

func TestShouldFetchRSSAt_EmptyScheduledTime(t *testing.T) {
	s := New(true, nil, nil, "", 0, 0, 0, "", nil)
	now := time.Date(2026, 6, 23, 7, 0, 0, 0, time.UTC)
	ok, hour := s.shouldFetchRSSAt(now)
	if ok {
		t.Error("expected no fetch when scheduledTime is empty")
	}
	if hour != -1 {
		t.Errorf("hour = %d, want -1", hour)
	}
}

func TestShouldFetchRSSAt_InvalidScheduledTime(t *testing.T) {
	s := New(true, nil, nil, "", 0, 0, 0, "bad", nil)
	now := time.Date(2026, 6, 23, 7, 0, 0, 0, time.UTC)
	ok, _ := s.shouldFetchRSSAt(now)
	if ok {
		t.Error("expected no fetch with invalid scheduledTime")
	}
}

func TestShouldFetchRSSAt_CrossMidnight(t *testing.T) {
	// scheduledTime = 23:00 → offsets produce 22:00, 06:00(+1d), 14:00(+1d)
	s := New(true, nil, nil, "", 0, 0, 0, "23:00", nil)

	// 06:00 next day — should match the +7h offset (23+7=30→6)
	now := time.Date(2026, 6, 24, 6, 0, 0, 0, time.UTC)
	ok, hour := s.shouldFetchRSSAt(now)
	if !ok {
		t.Error("expected fetch at 06:00 for scheduled=23:00")
	}
	if hour != 6 {
		t.Errorf("hour = %d, want 6", hour)
	}
}

// ---------------------------------------------------------------------------
// FetchAndStoreRSS integration tests
// ---------------------------------------------------------------------------

// setupFetchTestDB creates an in-memory SQLite database and sets
// PAPER_RECOMMEND_RSS_FILE to the local testdata. Returns a cleanup function.
func setupFetchTestDB(t *testing.T) func() {
	conn, cleanup, err := database.OpenTestDB()
	if err != nil {
		t.Fatalf("OpenTestDB: %v", err)
	}
	database.SetDB(conn)

	rssPath := filepath.Join("..", "..", "testdata", "arxiv_cs.SD.rss.xml")
	if _, err := os.Stat(rssPath); err != nil {
		cleanup()
		database.SetDB(nil)
		t.Skipf("testdata not available: %v", err)
	}
	os.Setenv("PAPER_RECOMMEND_RSS_FILE", rssPath)

	return func() {
		os.Unsetenv("PAPER_RECOMMEND_RSS_FILE")
		database.SetDB(nil)
		cleanup()
	}
}

func TestFetchAndStoreRSS_InsertsArticles(t *testing.T) {
	cleanup := setupFetchTestDB(t)
	defer cleanup()

	inserted, err := recommend.FetchAndStoreRSS([]string{"cs.SD"}, nil)
	if err != nil {
		t.Fatalf("FetchAndStoreRSS: %v", err)
	}
	if inserted == 0 {
		t.Error("expected at least 1 inserted article")
	}
	t.Logf("FetchAndStoreRSS inserted %d articles", inserted)

	// Verify articles are in the database
	articles, err := database.GetArticles(nil, 100, 0)
	if err != nil {
		t.Fatalf("GetArticles: %v", err)
	}
	if len(articles) < inserted {
		t.Errorf("GetArticles returned %d, want >= %d", len(articles), inserted)
	}

	// All articles should have non-empty ID and Title
	for i, a := range articles {
		if a.ID == "" {
			t.Errorf("article[%d] has empty ID", i)
		}
		if a.Title == "" {
			t.Errorf("article[%d] (%s) has empty Title", i, a.ID)
		}
		if a.Link == "" {
			t.Errorf("article[%d] (%s) has empty Link", i, a.ID)
		}
	}
}

func TestFetchAndStoreRSS_Deduplicates(t *testing.T) {
	cleanup := setupFetchTestDB(t)
	defer cleanup()

	// First fetch → inserts articles
	first, err := recommend.FetchAndStoreRSS([]string{"cs.SD"}, nil)
	if err != nil {
		t.Fatalf("first FetchAndStoreRSS: %v", err)
	}
	if first == 0 {
		t.Fatal("first fetch inserted 0, need at least 1 for dedup test")
	}

	// Second fetch with same data → INSERT OR IGNORE skips all duplicates
	second, err := recommend.FetchAndStoreRSS([]string{"cs.SD"}, nil)
	if err != nil {
		t.Fatalf("second FetchAndStoreRSS: %v", err)
	}
	if second != 0 {
		t.Errorf("second fetch inserted %d, want 0 (INSERT OR IGNORE dedup)", second)
	}
}

func TestFetchAndStoreRSS_EmptyCategories(t *testing.T) {
	inserted, err := recommend.FetchAndStoreRSS(nil, nil)
	if err != nil {
		t.Errorf("FetchAndStoreRSS(nil): %v", err)
	}
	if inserted != 0 {
		t.Errorf("inserted = %d with nil categories, want 0", inserted)
	}

	inserted, err = recommend.FetchAndStoreRSS([]string{}, nil)
	if err != nil {
		t.Errorf("FetchAndStoreRSS([]): %v", err)
	}
	if inserted != 0 {
		t.Errorf("inserted = %d with empty categories, want 0", inserted)
	}
}

// ---------------------------------------------------------------------------
// runRSSFetch
// ---------------------------------------------------------------------------

func TestRunRSSFetch_SkipsWhenPipelineRunning(t *testing.T) {
	conn, cleanup, err := database.OpenTestDB()
	if err != nil {
		t.Fatalf("OpenTestDB: %v", err)
	}
	database.SetDB(conn)
	defer func() {
		database.SetDB(nil)
		cleanup()
	}()

	s := New(true, []string{"cs.SD"}, nil, "", 0, 0, 0, "08:00", nil)

	// Simulate pipeline running
	s.mu.Lock()
	s.isRunning = true
	s.mu.Unlock()

	s.runRSSFetch(7)

	s.mu.Lock()
	if s.lastFetchAt != "" {
		t.Errorf("lastFetchAt = %q, want empty (should have skipped)", s.lastFetchAt)
	}
	if s.lastFetchHour != -1 {
		t.Errorf("lastFetchHour = %d, want -1 (should have skipped)", s.lastFetchHour)
	}
	if s.lastFetchError != "" {
		t.Errorf("lastFetchError = %q, want empty", s.lastFetchError)
	}
	s.mu.Unlock()
}

func TestRunRSSFetch_Success(t *testing.T) {
	cleanup := setupFetchTestDB(t)
	defer cleanup()

	s := New(true, []string{"cs.SD"}, nil, "", 0, 0, 0, "08:00", nil)

	s.runRSSFetch(7)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastFetchAt == "" {
		t.Error("lastFetchAt should be set on success")
	}
	if s.lastFetchHour != 7 {
		t.Errorf("lastFetchHour = %d, want 7", s.lastFetchHour)
	}
	if s.lastFetchDate == "" {
		t.Error("lastFetchDate should be set on success")
	}
	if s.lastFetchError != "" {
		t.Errorf("lastFetchError = %q, want empty on success", s.lastFetchError)
	}
}

func TestRunRSSFetch_RecordsError(t *testing.T) {
	// Use a non-existent RSS file path to trigger an error
	os.Setenv("PAPER_RECOMMEND_RSS_FILE", "/nonexistent/path/rss.xml")
	defer os.Unsetenv("PAPER_RECOMMEND_RSS_FILE")

	// Need a DB connection so FetchAndStoreRSS doesn't crash — but since
	// FetchArxivRSS will fail first (file not found), DB is never touched.
	s := New(true, []string{"cs.SD"}, nil, "", 0, 0, 0, "08:00", nil)

	s.runRSSFetch(7)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastFetchError == "" {
		t.Error("lastFetchError should be set on failure")
	}
	if s.lastFetchAt != "" {
		t.Errorf("lastFetchAt = %q, want empty on failure", s.lastFetchAt)
	}
	if s.lastFetchHour != -1 {
		t.Errorf("lastFetchHour = %d, want -1 on failure", s.lastFetchHour)
	}
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

func TestStatus_IncludesRSSTimesAndFetchInfo(t *testing.T) {
	s := New(true, nil, nil, "", 0, 0, 0, "08:00", nil)
	s.lastFetchAt = "2026-06-23 07:00"
	s.lastFetchError = "boom"

	st := s.Status()

	wantTimes := []string{"07:00", "15:00", "23:00"}
	if !reflect.DeepEqual(st.RSSTimes, wantTimes) {
		t.Errorf("RSSTimes = %v, want %v", st.RSSTimes, wantTimes)
	}
	if st.LastFetchAt != "2026-06-23 07:00" {
		t.Errorf("LastFetchAt = %q, want %q", st.LastFetchAt, "2026-06-23 07:00")
	}
	if st.LastFetchError != "boom" {
		t.Errorf("LastFetchError = %q, want %q", st.LastFetchError, "boom")
	}
	// LastError (pipeline) should still be empty — we only set fetch error
	if st.LastError != "" {
		t.Errorf("LastError = %q, want empty (only LastFetchError was set)", st.LastError)
	}
}
