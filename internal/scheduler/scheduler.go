// Package scheduler manages background tasks for the recommendation system.
// Currently runs daily arXiv RSS fetch + recommendation generation.
package scheduler

import (
	"log"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/recommend"
)

// AfterRecommendFunc is called after each successful daily recommendation run
// with the recommended articles. Used for Feishu notifications, etc.
type AfterRecommendFunc func(articles []database.Article)

// Scheduler manages periodic background tasks.
type Scheduler struct {
	categories    []string
	scoring       *recommend.ScoringClient
	dailyPapers   int
	batchSize     int
	autoRefresh   bool
	diversityRatio float64
	stopCh        chan struct{}
	lastRunDate   string          // YYYY-MM-DD, to avoid running twice
	onComplete    AfterRecommendFunc
}

// New creates a Scheduler.
func New(categories []string, scoringClient *api.Client, scoringModel string, dailyPapers, batchSize int, autoRefresh bool, diversityRatio float64) *Scheduler {
	return &Scheduler{
		categories:  categories,
		scoring: &recommend.ScoringClient{
			Client: scoringClient,
			Model:  scoringModel,
		},
		dailyPapers:   dailyPapers,
		batchSize:     batchSize,
		autoRefresh:   autoRefresh,
		diversityRatio: diversityRatio,
		stopCh:      make(chan struct{}),
	}
}

// SetOnComplete sets the callback invoked after each successful daily run.
func (s *Scheduler) SetOnComplete(fn AfterRecommendFunc) {
	s.onComplete = fn
}

// Start begins the scheduler loop in a background goroutine.
// Checks every hour if it should run the daily pipeline.
func (s *Scheduler) Start() {
	go func() {
		log.Println("[scheduler] started")

		// Run immediately on startup if auto-refresh is enabled
		if s.autoRefresh {
			s.runOnce()
		}

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if s.autoRefresh && s.shouldRun() {
					s.runOnce()
				}
			case <-s.stopCh:
				log.Println("[scheduler] stopped")
				return
			}
		}
	}()
}

// Stop signals the scheduler to stop gracefully.
func (s *Scheduler) Stop() {
	close(s.stopCh)
}

// shouldRun checks if this is a new day and the pipeline hasn't run yet today.
func (s *Scheduler) shouldRun() bool {
	today := time.Now().Format("2006-01-02")
	if today == s.lastRunDate {
		return false
	}
	return true
}

// runOnce executes one full pipeline cycle.
func (s *Scheduler) runOnce() {
	today := time.Now().Format("2006-01-02")
	log.Printf("[scheduler] starting daily pipeline for %s", today)

	recs, err := recommend.FetchAndRecommend(s.categories, s.scoring, s.dailyPapers, s.batchSize, s.diversityRatio)
	if err != nil {
		log.Printf("[scheduler] daily pipeline error: %v", err)
		return
	}

	s.lastRunDate = today
	log.Printf("[scheduler] daily pipeline done: %d recommendations", len(recs))

	if s.onComplete != nil && len(recs) > 0 {
		s.onComplete(recs)
	}
}
