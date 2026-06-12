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
)

// Tool definition for OpenAI function calling.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// ToolCall represents a (possibly partial) tool call from a streaming delta.
type ToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

// ToolCallCompleted represents a fully assembled tool call (accumulated from deltas).
type ToolCallCompleted struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// GetReferencesTool returns the tool definition for the get_references function.
func GetReferencesTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_references",
			Description: "Get the reference list / bibliography of this paper. Use this when the user asks about specific references, cited papers, or the bibliography section.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
				"required":   []string{},
			},
		},
	}
}

type ChatMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCallCompleted `json:"tool_calls,omitempty"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Tools    []Tool        `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"delta,omitempty"`
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message,omitempty"`
	} `json:"choices"`
	Usage *struct {
		CompletionTokens int `json:"completion_tokens"`
		PromptTokens     int `json:"prompt_tokens"`
		TotalTokens      int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
	} `json:"usage"`
}

type StreamChunk struct {
	Content          string
	Done             bool
	ToolCalls        []ToolCallCompleted // non-nil when the LLM calls a tool
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	Err              error
}

// Client holds an HTTP client and API endpoint configuration for OpenAI-compatible requests.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewClient creates a Client from the global config (Q&A chat API).
func NewClient(cfg *config.Config) *Client {
	return &Client{
		baseURL: cfg.API.BaseURL,
		apiKey:  cfg.API.APIKey,
		model:   cfg.API.DefaultModel,
		http:    newHTTPClient(),
	}
}

// NewClientFromEndpoint creates a Client with explicit endpoint parameters.
// Used for scoring, translation, or any API that differs from the main chat API.
func NewClientFromEndpoint(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		http:    newHTTPClient(),
	}
}

func newHTTPClient() *http.Client {
	tr := &http.Transport{
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 10 * time.Second,
		Proxy:                http.ProxyFromEnvironment,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   5 * time.Minute,
	}
}

// ChatStream streams a chat completion. If tools is non-nil, the tool definitions
// are included in the request. If the LLM responds with a tool call, the stream
// returns a single chunk with ToolCalls populated, then closes.
// The caller should check chunk.ToolCalls first; if non-nil, handle the tool call
// and issue a follow-up stream.
// model can be empty string to use the client's default model.
func (c *Client) ChatStream(model string, messages []ChatMessage, tools []Tool) <-chan StreamChunk {
	ch := make(chan StreamChunk, 64)

	go func() {
		defer close(ch)

		req := ChatRequest{
			Model:    model,
			Messages: messages,
			Stream:   true,
			Tools:    tools,
		}

		if model == "" {
			req.Model = c.model
		}

		body, err := json.Marshal(req)
		if err != nil {
			ch <- StreamChunk{Err: err}
			return
		}

		url := strings.TrimRight(c.baseURL, "/") + "/chat/completions"
		httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			ch <- StreamChunk{Err: err}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.http.Do(httpReq)
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

		// Accumulate tool calls from streaming deltas.
		type accToolCall struct {
			id       string
			typ      string
			name     string
			argument string
		}
		accToolCalls := make(map[int]*accToolCall)
		var hasToolCall bool

		var promptTokens, completionTokens, cachedTokens int
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				if hasToolCall {
					// Assemble completed tool calls and return them.
					var completed []ToolCallCompleted
					for i := 0; i < len(accToolCalls); i++ {
						tc := accToolCalls[i]
						if tc != nil {
							completed = append(completed, ToolCallCompleted{
								ID:   tc.id,
								Type: tc.typ,
								Function: struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								}{
									Name:      tc.name,
									Arguments: tc.argument,
								},
							})
						}
					}
					ch <- StreamChunk{
						ToolCalls:        completed,
						Done:             true,
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						CachedTokens:     cachedTokens,
					}
				} else {
					ch <- StreamChunk{
						Done:             true,
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						CachedTokens:     cachedTokens,
					}
				}
				return
			}

			var cr chatResponse
			if err := json.Unmarshal([]byte(data), &cr); err != nil {
				continue
			}

			if cr.Usage != nil {
				promptTokens = cr.Usage.PromptTokens
				completionTokens = cr.Usage.CompletionTokens
				if cr.Usage.PromptTokensDetails != nil {
					cachedTokens = cr.Usage.PromptTokensDetails.CachedTokens
				}
			}

			if len(cr.Choices) > 0 {
				delta := cr.Choices[0].Delta

				// Check for tool calls in delta.
				if len(delta.ToolCalls) > 0 {
					hasToolCall = true
					for _, tc := range delta.ToolCalls {
						acc, ok := accToolCalls[tc.Index]
						if !ok {
							acc = &accToolCall{}
							accToolCalls[tc.Index] = acc
						}
						if tc.ID != "" {
							acc.id = tc.ID
						}
						if tc.Type != "" {
							acc.typ = tc.Type
						}
						if tc.Function.Name != "" {
							acc.name = tc.Function.Name
						}
						if tc.Function.Arguments != "" {
							acc.argument += tc.Function.Arguments
						}
					}
					// Don't forward tool call chunks — we'll assemble and send one combined chunk at the end.
					continue
				}

				// Regular content delta.
				content := delta.Content
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

// Chat sends a non-streaming chat completion. If tools is non-nil, the tool
// definitions are included.
// Returns (content, toolCalls, promptTokens, completionTokens, totalTokens, cachedTokens, error).
// If the LLM responds with a tool call, content will be empty and toolCalls populated.
// model can be empty string to use the client's default model.
func (c *Client) Chat(model string, messages []ChatMessage, tools []Tool) (string, []ToolCallCompleted, int, int, int, int, error) {
	if model == "" {
		model = c.model
	}
	req := ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
		Tools:    tools,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", nil, 0, 0, 0, 0, err
	}

	url := strings.TrimRight(c.baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", nil, 0, 0, 0, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", nil, 0, 0, 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", nil, 0, 0, 0, 0, fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", nil, 0, 0, 0, 0, err
	}

	if len(cr.Choices) == 0 {
		return "", nil, 0, 0, 0, 0, fmt.Errorf("no response from API")
	}

	promptTokens := 0
	completionTokens := 0
	totalTokens := 0
	cachedTokens := 0
	if cr.Usage != nil {
		promptTokens = cr.Usage.PromptTokens
		completionTokens = cr.Usage.CompletionTokens
		totalTokens = cr.Usage.TotalTokens
		if cr.Usage.PromptTokensDetails != nil {
			cachedTokens = cr.Usage.PromptTokensDetails.CachedTokens
		}
	}

	msg := cr.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		var completed []ToolCallCompleted
		for _, tc := range msg.ToolCalls {
			completed = append(completed, ToolCallCompleted{
				ID:   tc.ID,
				Type: tc.Type,
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		return "", completed, promptTokens, completionTokens, totalTokens, cachedTokens, nil
	}

	return msg.Content, nil, promptTokens, completionTokens, totalTokens, cachedTokens, nil
}

const translateSystemPrompt = `Translate the following academic paper content from English to Chinese.

Rules:
1. Keep technical terms, model names (e.g. Transformer, BERT, ResNet), and proper nouns in their original English form
2. Preserve LaTeX math expressions and code snippets exactly as-is
3. Output only the translation, no explanations or prefixes
4. If the text appears to already be in Chinese or is not meaningful text, return it as-is`

// TranslateText translates a single piece of text using the configured translation API.
// Returns the translated text, or the original if the client is nil or translation fails.
func (c *Client) TranslateText(model string, text string) (string, error) {
	if text == "" {
		return "", nil
	}
	messages := []ChatMessage{
		{Role: "system", Content: translateSystemPrompt},
		{Role: "user", Content: text},
	}
	result, _, _, _, _, _, err := c.Chat(model, messages, nil)
	return result, err
}

// TranslateTexts translates a batch of texts in a single API call.
// Each input text is translated independently using numbered placeholders.
func (c *Client) TranslateTexts(model string, texts []string) ([]string, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) == 1 {
		r, err := c.TranslateText(model, texts[0])
		return []string{r}, err
	}

	var input strings.Builder
	for i, t := range texts {
		if i > 0 {
			input.WriteString("\n\n---\n\n")
		}
		input.WriteString(fmt.Sprintf("[%d]\n%s\n[/%d]", i+1, t, i+1))
	}

	prompt := "Translate each of the following texts from English to Chinese. " +
		"Keep technical terms, model names, and proper nouns in their original English form. " +
		"Preserve LaTeX math and code exactly as-is. " +
		"Output each translation prefixed with its number marker (e.g. [1], [2], etc.)."

	messages := []ChatMessage{
		{Role: "system", Content: prompt},
		{Role: "user", Content: input.String()},
	}
	result, _, _, _, _, _, err := c.Chat(model, messages, nil)
	if err != nil {
		return nil, err
	}

	// Parse numbered results
	results := make([]string, len(texts))
	// Try to find [N]...[/N] patterns
	for i := range texts {
		marker := fmt.Sprintf("[%d]", i+1)
		endMarker := fmt.Sprintf("[/%d]", i+1)
		startIdx := strings.Index(result, marker)
		if startIdx >= 0 {
			startIdx += len(marker)
			// Skip past newline after marker
			for startIdx < len(result) && (result[startIdx] == '\n' || result[startIdx] == ' ') {
				startIdx++
			}
			endIdx := strings.Index(result[startIdx:], endMarker)
			if endIdx >= 0 {
				results[i] = strings.TrimSpace(result[startIdx : startIdx+endIdx])
				continue
			}
		}
		// Fallback: individual translation
		r, _ := c.TranslateText(model, texts[i])
		results[i] = r
	}

	return results, nil
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
	result, _, _, _, _, _, err := c.Chat(model, messages, nil)
	return result, err
}
