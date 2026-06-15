// Package scheduler manages background tasks for the recommendation system.
// Currently runs daily arXiv RSS fetch + recommendation generation at a
// configurable time (ScheduledTime, HH:MM format).
package scheduler

import (
	"log"
	"sync"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/recommend"
)

// AfterRecommendFunc is called after each successful daily recommendation run
// with the recommended articles. Used for Feishu notifications, etc.
type AfterRecommendFunc func(articles []database.Article)

// SchedulerStatus exposes current scheduler state for the UI.
type SchedulerStatus struct {
	IsRunning    bool   `json:"is_running"`
	LastRun      string `json:"last_run"`    // "2006-01-02 15:04" or empty
	LastError    string `json:"last_error"`  // last error message or empty
	NextRun      string `json:"next_run"`    // estimated "2006-01-02 15:04" or empty
	Scheduled    string `json:"scheduled"`   // configured time "HH:MM"
	DailyCount   int    `json:"daily_count"` // how many papers the last run recommended
	PushToFeishu bool   `json:"push_to_feishu"`
}

// Scheduler manages periodic background tasks.
type Scheduler struct {
	mu            sync.Mutex
	categories    []string
	scoring       *recommend.ScoringClient
	dailyPapers   int
	batchSize     int
	diversityRatio float64
	scheduledTime string // "HH:MM"
	stopCh        chan struct{}
	lastRunStr    string // "2006-01-02 15:04" or empty
	lastRunDate   string // YYYY-MM-DD, to avoid running twice on same day
	lastError     string
	isRunning     bool
	dailyCount    int
	onComplete    AfterRecommendFunc
}

// New creates a Scheduler.
func New(categories []string, scoringClient *api.Client, scoringModel string, dailyPapers, batchSize int, diversityRatio float64, scheduledTime string) *Scheduler {
	return &Scheduler{
		categories:    categories,
		scoring: &recommend.ScoringClient{
			Client: scoringClient,
			Model:  scoringModel,
		},
		dailyPapers:   dailyPapers,
		batchSize:     batchSize,
		diversityRatio: diversityRatio,
		scheduledTime: scheduledTime,
		stopCh:        make(chan struct{}),
	}
}

// SetOnComplete sets the callback invoked after each successful daily run.
func (s *Scheduler) SetOnComplete(fn AfterRecommendFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onComplete = fn
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

// Start begins the scheduler loop in a background goroutine.
// Checks every minute if it should run the daily pipeline at the scheduled time.
func (s *Scheduler) Start() {
	go func() {
		log.Printf("[scheduler] started, scheduled time: %s", s.scheduledTime)

		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if s.shouldRun() {
					s.runOnce()
				}
			case <-s.stopCh:
				log.Printf("[scheduler] stopped")
				return
			}
		}
	}()
}

// Stop signals the scheduler to stop gracefully.
func (s *Scheduler) Stop() {
	close(s.stopCh)
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

	return SchedulerStatus{
		IsRunning:  s.isRunning,
		LastRun:    s.lastRunStr,
		LastError:  s.lastError,
		NextRun:    nextRun,
		Scheduled:  s.scheduledTime,
		DailyCount: s.dailyCount,
	}
}

// ManualTrigger executes one full pipeline cycle immediately.
func (s *Scheduler) ManualTrigger() {
	s.runOnce()
}

// shouldRun checks if the current time matches the scheduled time
// and the pipeline hasn't run yet today.
func (s *Scheduler) shouldRun() bool {
	s.mu.Lock()
	scheduled := s.scheduledTime
	s.mu.Unlock()

	if scheduled == "" {
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
	if today == s.lastRunDate {
		return false
	}
	return true
}

// runOnce executes one full pipeline cycle.
func (s *Scheduler) runOnce() {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		log.Printf("[scheduler] already running, skipping")
		return
	}
	s.isRunning = true
	s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	log.Printf("[scheduler] starting daily pipeline for %s", today)

	recs, err := recommend.FetchAndRecommend(s.categories, s.scoring, s.dailyPapers, s.batchSize, s.diversityRatio)

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

	if onComplete != nil && len(recs) > 0 {
		onComplete(recs)
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
