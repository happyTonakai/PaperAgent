package recommend

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/prompt"
)

// PreferencesPath returns the path to the user preference file.
func PreferencesPath() string {
	return filepath.Join(config.ConfigDir(), "preferences.md")
}

// ReadPreferences reads the user preference file.
// Returns empty string if the file does not exist.
func ReadPreferences() (string, error) {
	path := PreferencesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read preferences: %w", err)
	}
	return string(data), nil
}

// WritePreferences writes the user preference file.
func WritePreferences(content string) error {
	path := PreferencesPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create preferences dir: %w", err)
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// FeedbackArticle represents a single piece of user feedback used for preference updates.
type FeedbackArticle struct {
	Title    string
	Abstract string
	Status   int    // 0:unread, 1:clicked, 2:liked, -1:disliked, 3:mark_read
	Comment  *string
	Source   string // "recommend" 或 "chat"
	Rating   *int   // 问答系统评分 (1-10)，仅 chat 来源
}

// UpdatePreferences asks the LLM to update the user preference file based on feedback.
// model is the LLM model to use (e.g. "gpt-4o").
func UpdatePreferences(client *api.Client, model string, currentPrefs string, feedbacks []FeedbackArticle) (string, error) {
	if len(feedbacks) == 0 {
		return currentPrefs, nil
	}

	userContent := ""

	if currentPrefs != "" {
		userContent += fmt.Sprintf("## 当前用户偏好\n%s\n\n", currentPrefs)
	}

	userContent += "## 新的用户反馈\n"
	for _, fb := range feedbacks {
		action := "未知"
		switch {
		case fb.Status == 2 && fb.Source == "recommend":
			action = "点赞(非常感兴趣)-推荐系统"
		case fb.Status == 1 && fb.Source == "recommend":
			action = "点击(比较感兴趣)-推荐系统"
		case fb.Status == -1 && fb.Source == "recommend":
			action = "点踩(不感兴趣)-推荐系统"
		case fb.Rating != nil && *fb.Rating >= 8:
			action = fmt.Sprintf("评分%d(非常感兴趣)-问答系统", *fb.Rating)
		case fb.Rating != nil && *fb.Rating <= 3:
			action = fmt.Sprintf("评分%d(不感兴趣)-问答系统", *fb.Rating)
		case fb.Rating != nil:
			action = fmt.Sprintf("评分%d(一般)-问答系统", *fb.Rating)
		}

		userContent += fmt.Sprintf("- [%s] %s\n  摘要: %s\n", action, fb.Title, truncateText(fb.Abstract, 300))
		if fb.Comment != nil && *fb.Comment != "" {
			userContent += fmt.Sprintf("  用户评论: %s\n", *fb.Comment)
		}
	}

	result, _, _, _, _, _, err := client.Chat(model, []api.ChatMessage{
		{Role: "system", Content: prompt.GetUpdatePrefs()},
		{Role: "user", Content: userContent},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("update preferences: %w", err)
	}

	return result, nil
}

// CollectYesterdayFeedback gathers feedback signals from both recommend system (articles table)
// and Q&A system (chat_papers table) for the past day.
// Returns feedback articles suitable for UpdatePreferences.
func CollectYesterdayFeedback() ([]FeedbackArticle, error) {
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")

	var feedbacks []FeedbackArticle

	// 1. Recommend system: articles with status changes yesterday or today
	//    (status: 1=clicked, 2=liked, -1=disliked, 3=mark_read)
	for _, status := range []int{1, 2, -1, 3} {
		articles, err := database.GetArticles(&status, 200, 0)
		if err != nil {
			continue
		}
		for _, a := range articles {
			abstract := ""
			if a.Abstract != nil {
				abstract = *a.Abstract
			}
			fb := FeedbackArticle{
				Title:    a.Title,
				Abstract: abstract,
				Status:   status,
				Comment:  a.Comment,
				Source:   "recommend",
			}
			feedbacks = append(feedbacks, fb)
		}
	}

	// 2. Q&A system: chat papers updated since yesterday (have arxiv_id and rating)
	chatPapers, err := database.GetChatPapersUpdatedSince(yesterday)
	if err == nil {
		for _, cp := range chatPapers {
			title := cp.Title
			if title == "" {
				title = "Paper " + cp.ID
			}
			rating := cp.Rating

			// Look up abstract from articles table (may have been inserted by Q&A system)
			abstract := ""
			if cp.ArxivID != "" {
				if article, err := database.GetArticleByID(cp.ArxivID); err == nil && article != nil && article.Abstract != nil {
					abstract = *article.Abstract
				}
			}

			fb := FeedbackArticle{
				Title:    title,
				Abstract: abstract,
				Status:   0, // neutral; rating carries the signal
				Source:   "chat",
				Rating:   &rating,
			}
			feedbacks = append(feedbacks, fb)
		}
	}

	_ = today // today used implicitly via yesterday comparison
	return feedbacks, nil
}

func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
