// Package scheduler manages background tasks for the recommendation system.
// Runs arXiv RSS fetch 3 times a day (scheduled_time -1h, +7h, +15h) and
// daily recommendation generation + push at the configured scheduled_time.
package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/recommend"
)

// AfterRecommendFunc is called after each successful daily recommendation run
// with the recommended articles. `force` indicates whether the run was a
// manual trigger that should bypass the holiday-skip rule (i.e. the user
// explicitly asked for a push right now).
type AfterRecommendFunc func(articles []database.Article, force bool)

// SchedulerStatus exposes current scheduler state for the UI.
type SchedulerStatus struct {
	IsRunning      bool   `json:"is_running"`
	LastRun        string `json:"last_run"`         // "2006-01-02 15:04" or empty
	LastError      string `json:"last_error"`       // last pipeline error message or empty
	LastFetchError string `json:"last_fetch_error"` // last RSS fetch error message or empty
	NextRun        string `json:"next_run"`         // estimated "2006-01-02 15:04" or empty
	Scheduled      string `json:"scheduled"`        // configured time "HH:MM"
	DailyCount     int    `json:"daily_count"`      // how many papers the last run recommended
	PushToFeishu   bool   `json:"push_to_feishu"`
	// PendingPushCount is the size of the push backlog (articles with
	// pushed_at IS NULL AND recommend_date IS NOT NULL). Populated by the
	// server, not the scheduler itself. When this is non-zero and today
	// was a holiday, the next workday's push will drain it.
	PendingPushCount int `json:"pending_push_count"`
	// LastPushAt is the timestamp of the most recent successful push, or
	// empty if nothing has been pushed yet. Populated by the server.
	LastPushAt string `json:"last_push_at"`
	// RSSTimes lists the 3 configured RSS fetch times derived from
	// scheduled_time (-1h, +7h, +15h).
	RSSTimes []string `json:"rss_times"`
	// LastFetchAt is the timestamp of the most recent successful RSS fetch.
	LastFetchAt string `json:"last_fetch_at"`
	// Enabled reports the current master-switch state of the
	// recommendation pipeline. False means both the RSS fetcher and
	// the daily run are skipped, regardless of scheduled_time.
	Enabled bool `json:"enabled"`
}

// rssOffsets are the hour offsets from scheduledTime for RSS fetches.
var rssOffsets = []int{-1, 7, 15}

// Scheduler manages periodic background tasks.
type Scheduler struct {
	mu               sync.Mutex
	enabled          bool
	categories       []string
	excludedKeywords []string
	scoring          *recommend.ScoringClient
	dailyPapers      int
	batchSize        int
	diversityRatio   float64
	scheduledTime    string // "HH:MM"
	stopCh           chan struct{}
	stopOnce         sync.Once
	lastRunStr       string // "2006-01-02 15:04" or empty
	lastRunDate      string // YYYY-MM-DD, to avoid running twice on same day
	lastError        string
	lastFetchError   string
	isRunning        bool
	dailyCount       int
	onComplete       AfterRecommendFunc
	lastFetchDate    string // YYYY-MM-DD of last RSS fetch
	lastFetchHour    int    // hour of last RSS fetch, -1 = none
	lastFetchAt      string // "2006-01-02 15:04" or empty
}

// New creates a Scheduler.
func New(enabled bool, categories []string, scoringClient *api.Client, scoringModel string, dailyPapers, batchSize int, diversityRatio float64, scheduledTime string, excludedKeywords []string) *Scheduler {
	return &Scheduler{
		enabled:          enabled,
		categories:       categories,
		excludedKeywords: excludedKeywords,
		scoring: &recommend.ScoringClient{
			Client: scoringClient,
			Model:  scoringModel,
		},
		dailyPapers:    dailyPapers,
		batchSize:      batchSize,
		diversityRatio: diversityRatio,
		scheduledTime:  scheduledTime,
		stopCh:         make(chan struct{}),
		lastFetchHour:  -1,
	}
}

// SetOnComplete sets the callback invoked after each successful daily run.
func (s *Scheduler) SetOnComplete(fn AfterRecommendFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onComplete = fn
}

// SetEnabled toggles the master switch on the recommendation pipeline.
// When false, both the RSS fetcher and the daily run no-op for the
// rest of the process lifetime (or until SetEnabled(true) is called).
// Status() reflects the new state immediately so the Web UI sees it
// without waiting for the next loop tick.
func (s *Scheduler) SetEnabled(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = v
}

// Enabled reports the current master-switch state.
func (s *Scheduler) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// UpdateConfig updates scheduler parameters at runtime.
func (s *Scheduler) UpdateConfig(scheduledTime string, dailyPapers, batchSize int, diversityRatio float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduledTime = scheduledTime
	s.dailyPapers = dailyPapers
	s.batchSize = batchSize
	s.diversityRatio = diversityRatio
}

// SetExcludedKeywords updates the excluded keywords used for RSS filtering.
func (s *Scheduler) SetExcludedKeywords(keywords []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.excludedKeywords = keywords
}

// Start begins the scheduler loop in a background goroutine.
// Checks every minute for both RSS fetch and daily pipeline triggers.
func (s *Scheduler) Start() {
	rssTimes := s.rssFetchTimes()
	log.Printf("[scheduler] started, scheduled time: %s, RSS fetch times: %v", s.scheduledTime, rssTimes)

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if ok, hour := s.shouldFetchRSS(); ok {
					s.runRSSFetch(hour)
				}
				if s.shouldRun() {
					s.runOnce(false)
				}
			case <-s.stopCh:
				log.Printf("[scheduler] stopped")
				return
			}
		}
	}()
}

// Stop signals the scheduler to stop gracefully. Safe to call
// multiple times — the first call closes the stop channel, subsequent
// calls are no-ops. This matters because main.go defers feishuBot.Stop
// alongside the systray teardown and a future caller may add their
// own defer without realising Stop has already fired.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

// Status returns the current scheduler state.
func (s *Scheduler) Status() SchedulerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	nextRun := ""
	if s.scheduledTime != "" {
		now := time.Now()
		if h, m, ok := parseTimeOfDay(s.scheduledTime); ok {
			candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
			if candidate.Before(now) || candidate.Equal(now) {
				candidate = candidate.Add(24 * time.Hour)
			}
			nextRun = candidate.Format("2006-01-02 15:04")
		}
	}

	rssTimes := s.rssFetchTimesLocked()

	return SchedulerStatus{
		IsRunning:      s.isRunning,
		LastRun:        s.lastRunStr,
		LastError:      s.lastError,
		LastFetchError: s.lastFetchError,
		NextRun:        nextRun,
		Scheduled:      s.scheduledTime,
		DailyCount:     s.dailyCount,
		RSSTimes:       rssTimes,
		LastFetchAt:    s.lastFetchAt,
		Enabled:        s.enabled,
	}
}

// ManualTrigger executes one full pipeline cycle immediately and forces a
// push (force=true) regardless of whether the scheduler would have skipped
// the push because of a holiday. Used by the Web UI "全流程触发" button and
// by the Feishu /push command path.
func (s *Scheduler) ManualTrigger() {
	s.runOnce(true)
}

// rssFetchTimes returns the 3 RSS fetch times (HH:MM strings) derived from
// scheduledTime. Caller must hold s.mu or call rssFetchTimesLocked.
func (s *Scheduler) rssFetchTimes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rssFetchTimesLocked()
}

func (s *Scheduler) rssFetchTimesLocked() []string {
	h, m, ok := parseTimeOfDay(s.scheduledTime)
	if !ok {
		return nil
	}
	times := make([]string, len(rssOffsets))
	for i, offset := range rssOffsets {
		fh := (h + offset + 24) % 24
		times[i] = fmt.Sprintf("%02d:%02d", fh, m)
	}
	return times
}

// shouldFetchRSS checks if the current time matches any of the 3 RSS fetch
// windows and the fetch hasn't already run in this window.
func (s *Scheduler) shouldFetchRSS() (bool, int) {
	return s.shouldFetchRSSAt(time.Now())
}

// shouldFetchRSSAt is the testable core of shouldFetchRSS, accepting an
// explicit "now" time.
func (s *Scheduler) shouldFetchRSSAt(now time.Time) (bool, int) {
	s.mu.Lock()
	enabled := s.enabled
	scheduled := s.scheduledTime
	lastDate := s.lastFetchDate
	lastHour := s.lastFetchHour
	s.mu.Unlock()

	if !enabled || scheduled == "" {
		return false, -1
	}

	h, m, ok := parseTimeOfDay(scheduled)
	if !ok {
		return false, -1
	}

	if now.Minute() != m {
		return false, -1
	}

	today := now.Format("2006-01-02")
	for _, offset := range rssOffsets {
		targetHour := (h + offset + 24) % 24
		if now.Hour() == targetHour {
			// Skip if already fetched in this hour today
			if today == lastDate && targetHour == lastHour {
				return false, -1
			}
			return true, targetHour
		}
	}
	return false, -1
}

// runRSSFetch performs a standalone RSS fetch and stores results in the database.
func (s *Scheduler) runRSSFetch(hour int) {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		log.Printf("[scheduler] pipeline running, deferring RSS fetch (hour=%02d, will retry at next window)", hour)
		return
	}
	categories := s.categories
	excludedKeywords := s.excludedKeywords
	s.mu.Unlock()

	log.Printf("[scheduler] starting RSS fetch (hour=%02d)", hour)

	inserted, err := recommend.FetchAndStoreRSS(categories, excludedKeywords)

	s.mu.Lock()
	if err != nil {
		s.lastFetchError = err.Error()
		s.mu.Unlock()
		log.Printf("[scheduler] RSS fetch error: %v", err)
		return
	}

	s.lastFetchDate = time.Now().Format("2006-01-02")
	s.lastFetchHour = hour
	s.lastFetchAt = time.Now().Format("2006-01-02 15:04")
	s.lastFetchError = ""
	s.mu.Unlock()

	log.Printf("[scheduler] RSS fetch done: %d new articles", inserted)
}

// shouldRun checks if the current time matches the scheduled time
// and the pipeline hasn't run yet today.
func (s *Scheduler) shouldRun() bool {
	s.mu.Lock()
	enabled := s.enabled
	scheduled := s.scheduledTime
	s.mu.Unlock()

	if !enabled || scheduled == "" {
		return false
	}

	h, m, ok := parseTimeOfDay(scheduled)
	if !ok {
		return false
	}

	now := time.Now()
	if now.Hour() != h || now.Minute() != m {
		return false
	}

	today := now.Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	return today != s.lastRunDate
}

// runOnce executes one full pipeline cycle. `force` propagates to onComplete
// so the push layer knows whether to bypass the holiday-skip rule.
func (s *Scheduler) runOnce(force bool) {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		log.Printf("[scheduler] already running, skipping")
		return
	}
	s.isRunning = true
	categories := s.categories
	scoring := s.scoring
	dailyPapers := s.dailyPapers
	batchSize := s.batchSize
	diversityRatio := s.diversityRatio
	excludedKeywords := s.excludedKeywords
	s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	log.Printf("[scheduler] starting daily pipeline for %s (force=%v)", today, force)

	recs, err := recommend.FetchAndRecommend(categories, scoring, dailyPapers, batchSize, diversityRatio, excludedKeywords)

	s.mu.Lock()
	s.isRunning = false
	if err != nil {
		s.lastError = err.Error()
		s.mu.Unlock()
		log.Printf("[scheduler] daily pipeline error: %v", err)
		return
	}

	s.lastRunDate = today
	s.lastRunStr = time.Now().Format("2006-01-02 15:04")
	s.lastError = ""
	s.dailyCount = len(recs)
	onComplete := s.onComplete
	s.mu.Unlock()

	log.Printf("[scheduler] daily pipeline done: %d recommendations", len(recs))

	if onComplete != nil {
		onComplete(recs, force)
	}
}

// parseTimeOfDay parses "HH:MM" into (hour, minute, ok).
func parseTimeOfDay(s string) (int, int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}
