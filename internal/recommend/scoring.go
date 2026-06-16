package recommend

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/prompt"
)

// ArticleInfo contains the minimum necessary fields for LLM scoring.
type ArticleInfo struct {
	ID       string
	Title    string
	Abstract string
}

// ScoreArticlesBatch scores articles in batches using LLM.
// preferences is the user preference file content.
// Each batch of batchSize articles is sent to the LLM.
// onProgress, if non-nil, is called after each completed batch.
// Uses the model configured in the client. Pass empty string to use client's default.
// Returns a map of article ID to score.
func ScoreArticlesBatch(client *api.Client, model string, preferences string, articles []ArticleInfo, batchSize int, onProgress func(completed, total int)) (map[string]float64, error) {
	if len(articles) == 0 {
		return map[string]float64{}, nil
	}

	allScores := make(map[string]float64)
	totalBatches := (len(articles) + batchSize - 1) / batchSize

	for i := 0; i < len(articles); i += batchSize {
		end := i + batchSize
		if end > len(articles) {
			end = len(articles)
		}
		chunk := articles[i:end]

		scores, err := scoreChunk(client, model, preferences, chunk)
		if err != nil {
			log.Printf("[scoring] batch %d/%d failed: %v", i/batchSize+1, totalBatches, err)
			continue
		}
		for id, score := range scores {
			allScores[id] = score
		}

		if onProgress != nil {
			onProgress(i/batchSize+1, totalBatches)
		}
	}

	return allScores, nil
}

func scoreChunk(client *api.Client, model string, preferences string, articles []ArticleInfo) (map[string]float64, error) {
	var userContent strings.Builder

	if preferences != "" {
		userContent.WriteString(fmt.Sprintf("## 用户兴趣偏好\n%s\n\n", preferences))
	}

	userContent.WriteString("## 待评分论文\n")
	for _, article := range articles {
		abstract := truncateText(article.Abstract, 500)
		userContent.WriteString(fmt.Sprintf(
			"ID: %s\n标题: %s\n摘要: %s\n---\n",
			article.ID, article.Title, abstract,
		))
	}

	raw, _, _, _, _, _, err := client.Chat(model, []api.ChatMessage{
		{Role: "system", Content: prompt.GetScoring()},
		{Role: "user", Content: userContent.String()},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("score chunk: %w", err)
	}

	return parseScoringResponse(raw), nil
}

// parseScoringResponse parses the LLM's JSON response into a score map.
// Handles markdown code block fences (```json ... ```).
func parseScoringResponse(raw string) map[string]float64 {
	// Strip markdown code block fences
	content := strings.TrimSpace(raw)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var items []struct {
		ID    string  `json:"id"`
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(content), &items); err != nil {
		return nil
	}

	scores := make(map[string]float64, len(items))
	for _, item := range items {
		if item.ID != "" {
			// Score range is [-1, 1]: -1 = clearly not interested (excluded
			// from recommendations), 0 = neutral/unscored, 1 = perfect match.
			// Round to 1 decimal to swallow LLM float jitter (e.g. -0.9999).
			if item.Score < -1 {
				item.Score = -1
			}
			if item.Score > 1 {
				item.Score = 1
			}
			scores[item.ID] = item.Score
		}
	}
	return scores
}
