package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/session"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ChatResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		CompletionTokens     int `json:"completion_tokens"`
		PromptTokens         int `json:"prompt_tokens"`
		TotalTokens          int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
	} `json:"usage"`
}

type StreamChunk struct {
	Content          string
	Done             bool
	TokenCount       int // deprecated, kept for backward compat
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	Err              error
}

type Client struct {
	cfg    *config.Config
	client *http.Client
}

func NewClient(cfg *config.Config) *Client {
	tr := &http.Transport{
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 10 * time.Second,
		Proxy:                http.ProxyFromEnvironment,
	}
	return &Client{
		cfg: cfg,
		client: &http.Client{
			Transport: tr,
			Timeout:   5 * time.Minute,
		},
	}
}

func (c *Client) buildMessages(systemPrompt string, paperContent string, recentMessages []session.Message, userQuestion string) []ChatMessage {
	msgs := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf("以下是论文全文：\n\n%s", paperContent)},
	}

	for _, m := range recentMessages {
		msgs = append(msgs, ChatMessage{Role: m.Role, Content: m.Content})
	}

	if userQuestion != "" {
		msgs = append(msgs, ChatMessage{Role: "user", Content: userQuestion})
	}

	return msgs
}

func (c *Client) ChatStream(model string, messages []ChatMessage) <-chan StreamChunk {
	ch := make(chan StreamChunk, 64)

	go func() {
		defer close(ch)

		req := ChatRequest{
			Model:    model,
			Messages: messages,
			Stream:   true,
		}

		body, err := json.Marshal(req)
		if err != nil {
			ch <- StreamChunk{Err: err}
			return
		}

		url := strings.TrimRight(c.cfg.API.BaseURL, "/") + "/chat/completions"
		httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			ch <- StreamChunk{Err: err}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.API.APIKey)

		resp, err := c.client.Do(httpReq)
		if err != nil {
			ch <- StreamChunk{Err: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			ch <- StreamChunk{Err: fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))}
			return
		}

		var promptTokens, completionTokens, cachedTokens int
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				ch <- StreamChunk{Done: true, PromptTokens: promptTokens, CompletionTokens: completionTokens, CachedTokens: cachedTokens}
				return
			}

			var chunk ChatResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			if chunk.Usage != nil {
				promptTokens = chunk.Usage.PromptTokens
				completionTokens = chunk.Usage.CompletionTokens
				if chunk.Usage.PromptTokensDetails != nil {
					cachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
				}
			}

			if len(chunk.Choices) > 0 {
				content := chunk.Choices[0].Delta.Content
				if content != "" {
					ch <- StreamChunk{Content: content}
				}
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamChunk{Err: err}
		}
	}()

	return ch
}

func (c *Client) Chat(model string, messages []ChatMessage) (string, int, int, int, int, error) {
	req := ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", 0, 0, 0, 0, err
	}

	url := strings.TrimRight(c.cfg.API.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", 0, 0, 0, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.API.APIKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", 0, 0, 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", 0, 0, 0, 0, fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", 0, 0, 0, 0, err
	}

	if len(chatResp.Choices) == 0 {
		return "", 0, 0, 0, 0, fmt.Errorf("no response from API")
	}

	promptTokens := 0
	completionTokens := 0
	totalTokens := 0
	cachedTokens := 0
	if chatResp.Usage != nil {
		promptTokens = chatResp.Usage.PromptTokens
		completionTokens = chatResp.Usage.CompletionTokens
		totalTokens = chatResp.Usage.TotalTokens
		if chatResp.Usage.PromptTokensDetails != nil {
			cachedTokens = chatResp.Usage.PromptTokensDetails.CachedTokens
		}
	}

	return chatResp.Choices[0].Message.Content, promptTokens, completionTokens, totalTokens, cachedTokens, nil
}

func (c *Client) ExtractTitle(model string, content string) (string, error) {
	// Take first 1000 chars for title extraction
	excerpt := content
	if len(excerpt) > 1000 {
		excerpt = excerpt[:1000]
	}
	messages := []ChatMessage{
		{Role: "system", Content: "从以下论文开头提取论文标题，直接输出标题，不要加任何前缀或引号。"},
		{Role: "user", Content: excerpt},
	}
	result, _, _, _, _, err := c.Chat(model, messages)
	return result, err
}
