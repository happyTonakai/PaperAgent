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

// FetchArxivTool returns the tool definition for the fetch_arxiv function.
// It fetches the content of an arXiv paper given a URL or bare ID, with
// references stripped — useful for cross-paper comparison inside a Q&A
// session, where the user wants to ask about another paper alongside
// the currently-loaded one.
func FetchArxivTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name: "fetch_arxiv",
			Description: "Fetch the content of an arXiv paper by URL or ID and return it as Markdown (references stripped — use get_references on the current paper if you need that). Use this when the user wants to compare with, contrast, or reference another paper.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url_or_id": map[string]interface{}{
						"type":        "string",
						"description": "An arXiv URL (https://arxiv.org/abs/2106.09685 or https://arxiv.org/pdf/2106.09685) or bare ID (2106.09685, arXiv:2106.09685, cs.AI/0001001, etc.).",
					},
				},
				"required": []string{"url_or_id"},
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
	Model         string        `json:"model"`
	Messages      []ChatMessage `json:"messages"`
	Stream        bool          `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Tools         []Tool        `json:"tools,omitempty"`
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

// keyPrefix returns the first 5 chars of an API key for diagnostic logging,
// or "***" if the key is too short. Never returns the full key.
func keyPrefix(s string) string {
	if len(s) < 5 {
		return "***"
	}
	return s[:5]
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
			StreamOptions: &struct {
				IncludeUsage bool `json:"include_usage"`
			}{IncludeUsage: true},
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
			ch <- StreamChunk{Err: fmt.Errorf("API error %d (key=%s...): %s", resp.StatusCode, keyPrefix(c.apiKey), string(bodyBytes))}
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
		return "", nil, 0, 0, 0, 0, fmt.Errorf("API error %d (key=%s...): %s", resp.StatusCode, keyPrefix(c.apiKey), string(bodyBytes))
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

// articleTranslation is the JSON shape returned by the translation model.
type articleTranslation struct {
	Title    string `json:"title"`
	Abstract string `json:"abstract"`
}

// TranslateArticle translates a single article's title and abstract in one API call.
// The model is asked to return JSON {"title":..., "abstract":...} so callers
// don't have to disambiguate from a single text blob.
func (c *Client) TranslateArticle(model, title, abstract string) (string, string, error) {
	if title == "" && abstract == "" {
		return "", "", nil
	}
	var user strings.Builder
	user.WriteString("Title:\n")
	user.WriteString(title)
	if abstract != "" {
		user.WriteString("\n\nAbstract:\n")
		user.WriteString(abstract)
	}

	prompt := `Translate the title and abstract of an academic paper from English to Chinese.
Return a JSON object with two fields: "title" and "abstract" containing the Chinese translations.
Rules:
1. Translate the WHOLE title and abstract into Chinese. Keep ONLY individual technical terms, model names, and proper nouns in their original English form (e.g. "Transformer", "BERT", "ResNet", "GAN", "Bach"). Do NOT leave an entire sentence untranslated just because it contains such words.
2. Preserve LaTeX math expressions and code snippets exactly as-is
3. Output ONLY the JSON object, no explanations, no markdown fences, no code blocks`

	messages := []ChatMessage{
		{Role: "system", Content: prompt},
		{Role: "user", Content: user.String()},
	}
	result, _, _, _, _, _, err := c.Chat(model, messages, nil)
	if err != nil {
		return "", "", err
	}

	jsonStr := extractJSONObject(result)
	var tr articleTranslation
	if err := json.Unmarshal([]byte(jsonStr), &tr); err != nil {
		return "", "", fmt.Errorf("parse translation JSON: %w (raw: %q)", err, truncate(result, 200))
	}
	return tr.Title, tr.Abstract, nil
}

// extractJSONObject pulls the first {...} JSON object from s. Handles ```json
// fences and surrounding prose; returns the original string if no braces found.
func extractJSONObject(s string) string {
	if i := strings.Index(s, "```"); i >= 0 {
		if j := strings.Index(s[i+3:], "```"); j >= 0 {
			inner := s[i+3 : i+3+j]
			if nl := strings.Index(inner, "\n"); nl >= 0 {
				inner = inner[nl+1:]
			}
			s = inner
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
