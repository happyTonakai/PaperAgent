package recommend

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/happyTonakai/paperagent/internal/api"
)

// ArticleInfo contains the minimum necessary fields for LLM scoring.
type ArticleInfo struct {
	ID       string
	Title    string
	Abstract string
}

const scoringSystemPrompt = `你是一个学术论文推荐系统的评分助手。根据用户的兴趣偏好，为论文打分。

评分规则：
- 分数范围 0.0 到 1.0
- 1.0 表示与用户兴趣完全匹配
- 0.0 表示与用户兴趣完全不相关
- 考虑论文主题、方法和研究方向与用户偏好的匹配度

请严格按照以下 JSON 数组格式返回，不要返回任何其他内容：
[{"id": "论文ID", "score": 0.85}, ...]

注意：
1. 只返回 JSON 数组，不要有解释或注释
2. 每篇论文必须有一个分数
3. id 必须与输入的论文 ID 完全一致`

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
		{Role: "system", Content: scoringSystemPrompt},
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
			if item.Score < 0 {
				item.Score = 0
			}
			if item.Score > 1 {
				item.Score = 1
			}
			scores[item.ID] = item.Score
		}
	}
	return scores
}
