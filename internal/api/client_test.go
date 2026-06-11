package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/happyTonakai/paperagent/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		API: config.APIConfig{
			BaseURL:      os.Getenv("OPENAI_BASE_URL"),
			APIKey:       os.Getenv("OPENAI_API_KEY"),
			DefaultModel: "xiaomi/mimo-v2-flash",
		},
	}
	if cfg.API.APIKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}
	if cfg.API.BaseURL == "" {
		cfg.API.BaseURL = "https://api.openai.com/v1"
	}
	return cfg
}

func TestChatIntegration(t *testing.T) {
	cfg := testConfig(t)
	client := NewClient(cfg)

	messages := []ChatMessage{
		{Role: "system", Content: "You are a helpful assistant. Reply concisely."},
		{Role: "user", Content: "What is 2+2? Reply with just the number."},
	}

	result, _, promptTokens, completionTokens, totalTokens, cachedTokens, err := client.Chat(cfg.API.DefaultModel, messages, nil)
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty response")
	}
	if completionTokens < 0 {
		t.Errorf("expected non-negative tokens, got %d", completionTokens)
	}

	t.Logf("Response: %s (prompt: %d, completion: %d, total: %d, cached: %d)", result, promptTokens, completionTokens, totalTokens, cachedTokens)
}

func TestChatStreamIntegration(t *testing.T) {
	cfg := testConfig(t)
	client := NewClient(cfg)

	messages := []ChatMessage{
		{Role: "system", Content: "You are a helpful assistant. Reply concisely."},
		{Role: "user", Content: "Say 'hello' in one word."},
	}

	ch := client.ChatStream(cfg.API.DefaultModel, messages, nil)

	var content strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		if chunk.Done {
			break
		}
		content.WriteString(chunk.Content)
	}

	if content.Len() == 0 {
		t.Error("expected non-empty streamed content")
	}

	t.Logf("Streamed: %s", content.String())
}

func TestExtractTitleIntegration(t *testing.T) {
	cfg := testConfig(t)
	client := NewClient(cfg)

	paperStart := `Attention Is All You Need
Ashish Vaswani, Noam Shazeer, Niki Parmar, Jakob Uszkoreit, Llion Jones, Aidan N. Gomez, Lukasz Kaiser, Illia Polosukhin
Google Brain, Google Research, University of Toronto`

	title, err := client.ExtractTitle(cfg.API.DefaultModel, paperStart)
	if err != nil {
		t.Fatalf("extract title error: %v", err)
	}

	if title == "" {
		t.Error("expected non-empty title")
	}

	t.Logf("Extracted title: %s", title)
}

// --- Tool calling unit tests (no real API needed) ---

func TestGetReferencesTool_Structure(t *testing.T) {
	tool := GetReferencesTool()
	if tool.Type != "function" {
		t.Errorf("expected type 'function', got %q", tool.Type)
	}
	if tool.Function.Name != "get_references" {
		t.Errorf("expected name 'get_references', got %q", tool.Function.Name)
	}
	if tool.Function.Description == "" {
		t.Error("description should not be empty")
	}

	// Verify parameters is a valid JSON schema with empty properties.
	params, ok := tool.Function.Parameters.(map[string]interface{})
	if !ok {
		t.Fatalf("parameters should be a map, got %T", tool.Function.Parameters)
	}
	if params["type"] != "object" {
		t.Errorf("expected type 'object', got %v", params["type"])
	}
	props, ok := params["properties"]
	if !ok {
		t.Error("parameters should have 'properties' key")
	}
	propsMap, ok := props.(map[string]interface{})
	if !ok {
		t.Fatalf("properties should be a map, got %T", props)
	}
	if len(propsMap) != 0 {
		t.Errorf("expected empty properties, got %d entries", len(propsMap))
	}
}

func TestChatRequest_WithToolsSerialization(t *testing.T) {
	req := ChatRequest{
		Model: "test-model",
		Messages: []ChatMessage{
			{Role: "system", Content: "You are a helper."},
			{Role: "user", Content: "Hello"},
		},
		Stream: true,
		Tools:  []Tool{GetReferencesTool()},
	}

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// Verify tools array is present and correct.
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	tools, ok := parsed["tools"].([]interface{})
	if !ok {
		t.Fatal("expected 'tools' array in JSON")
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]interface{})
	if tool["type"] != "function" {
		t.Errorf("expected type function, got %v", tool["type"])
	}

	fn := tool["function"].(map[string]interface{})
	if fn["name"] != "get_references" {
		t.Errorf("expected name get_references, got %v", fn["name"])
	}
}

func TestChatRequest_WithoutToolsOmitsField(t *testing.T) {
	req := ChatRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   false,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	if strings.Contains(string(data), "tools") {
		t.Error("JSON should NOT contain 'tools' field when Tools is nil")
	}
}

func TestChatMessage_AssistantToolCallSerialization(t *testing.T) {
	msg := ChatMessage{
		Role: "assistant",
		ToolCalls: []ToolCallCompleted{
			{
				ID:   "call_abc123",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      "get_references",
					Arguments: "{}",
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got %v", parsed["role"])
	}

	tcs, ok := parsed["tool_calls"].([]interface{})
	if !ok {
		t.Fatal("expected 'tool_calls' array")
	}
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(tcs))
	}

	tc := tcs[0].(map[string]interface{})
	if tc["id"] != "call_abc123" {
		t.Errorf("expected id 'call_abc123', got %v", tc["id"])
	}
}

func TestChatMessage_ToolRoleSerialization(t *testing.T) {
	msg := ChatMessage{
		Role:       "tool",
		Content:    "这里是论文的参考文献列表...",
		ToolCallID: "call_abc123",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed["role"] != "tool" {
		t.Errorf("expected role 'tool', got %v", parsed["role"])
	}
	if parsed["tool_call_id"] != "call_abc123" {
		t.Errorf("expected tool_call_id 'call_abc123', got %v", parsed["tool_call_id"])
	}
	if parsed["content"] != "这里是论文的参考文献列表..." {
		t.Errorf("unexpected content: %v", parsed["content"])
	}
}

func TestChatResponse_ToolCallUnmarshal_StreamingDelta(t *testing.T) {
	// Simulate a typical streaming chunk with a tool call delta (first chunk: role + id + name).
	jsonData := `{
		"id": "chatcmpl-abc",
		"object": "chat.completion.chunk",
		"choices": [{
			"index": 0,
			"delta": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"index": 0,
					"id": "call_abc123",
					"type": "function",
					"function": {
						"name": "get_references",
						"arguments": ""
					}
				}]
			}
		}]
	}`

	var cr chatResponse
	if err := json.Unmarshal([]byte(jsonData), &cr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(cr.Choices) == 0 {
		t.Fatal("expected at least 1 choice")
	}

	tcs := cr.Choices[0].Delta.ToolCalls
	if len(tcs) == 0 {
		t.Fatal("expected tool_calls in delta")
	}

	if tcs[0].Index != 0 {
		t.Errorf("expected index 0, got %d", tcs[0].Index)
	}
	if tcs[0].ID != "call_abc123" {
		t.Errorf("expected ID 'call_abc123', got %q", tcs[0].ID)
	}
	if tcs[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", tcs[0].Type)
	}
	if tcs[0].Function.Name != "get_references" {
		t.Errorf("expected function name 'get_references', got %q", tcs[0].Function.Name)
	}
}

func TestChatResponse_ToolCallUnmarshal_ArgumentsDelta(t *testing.T) {
	// Simulate the second streaming chunk with just arguments (the rest of the tool call).
	jsonData := `{
		"id": "chatcmpl-abc",
		"object": "chat.completion.chunk",
		"choices": [{
			"index": 0,
			"delta": {
				"tool_calls": [{
					"index": 0,
					"function": {
						"arguments": "{}"
					}
				}]
			}
		}]
	}`

	var cr chatResponse
	if err := json.Unmarshal([]byte(jsonData), &cr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(cr.Choices) == 0 {
		t.Fatal("expected at least 1 choice")
	}

	tcs := cr.Choices[0].Delta.ToolCalls
	if len(tcs) == 0 {
		t.Fatal("expected tool_calls in delta")
	}

	if tcs[0].Index != 0 {
		t.Errorf("expected index 0, got %d", tcs[0].Index)
	}
	// ID, Type, Name should be empty in this delta chunk.
	if tcs[0].ID != "" {
		t.Errorf("expected empty ID in arguments-only chunk, got %q", tcs[0].ID)
	}
	if tcs[0].Function.Arguments != "{}" {
		t.Errorf("expected arguments '{}', got %q", tcs[0].Function.Arguments)
	}
}

func TestChatResponse_ToolCallUnmarshal_NonStreaming(t *testing.T) {
	// Simulate a non-streaming response with tool calls.
	jsonData := `{
		"id": "chatcmpl-abc",
		"object": "chat.completion",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_abc123",
					"type": "function",
					"function": {
						"name": "get_references",
						"arguments": "{}"
					}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {
			"prompt_tokens": 50,
			"completion_tokens": 10,
			"total_tokens": 60
		}
	}`

	var cr chatResponse
	if err := json.Unmarshal([]byte(jsonData), &cr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(cr.Choices) == 0 {
		t.Fatal("expected at least 1 choice")
	}

	tcs := cr.Choices[0].Message.ToolCalls
	if len(tcs) == 0 {
		t.Fatal("expected tool_calls in message")
	}

	if tcs[0].ID != "call_abc123" {
		t.Errorf("expected ID 'call_abc123', got %q", tcs[0].ID)
	}
	if tcs[0].Function.Name != "get_references" {
		t.Errorf("expected function name 'get_references', got %q", tcs[0].Function.Name)
	}
	if tcs[0].Function.Arguments != "{}" {
		t.Errorf("expected arguments '{}', got %q", tcs[0].Function.Arguments)
	}

	if cr.Usage == nil {
		t.Fatal("expected usage")
	}
	if cr.Usage.PromptTokens != 50 {
		t.Errorf("expected 50 prompt tokens, got %d", cr.Usage.PromptTokens)
	}
}

func TestToolCallCompleted_Accumulation(t *testing.T) {
	// Simulate the accumulation pattern used in ChatStream goroutine.
	type accToolCall struct {
		id       string
		typ      string
		name     string
		argument string
	}
	acc := make(map[int]*accToolCall)

	// First delta: role + id + name (arguments empty).
	delta1 := ToolCall{Index: 0, ID: "call_abc123", Type: "function"}
	delta1.Function.Name = "get_references"

	acc[delta1.Index] = &accToolCall{
		id:   delta1.ID,
		typ:  delta1.Type,
		name: delta1.Function.Name,
	}

	// Second delta: arguments only.
	delta2 := ToolCall{Index: 0}
	delta2.Function.Arguments = "{}"

	acc[delta2.Index].argument += delta2.Function.Arguments

	// Verify accumulated result.
	tc := acc[0]
	if tc.id != "call_abc123" {
		t.Errorf("expected ID 'call_abc123', got %q", tc.id)
	}
	if tc.name != "get_references" {
		t.Errorf("expected name 'get_references', got %q", tc.name)
	}
	if tc.argument != "{}" {
		t.Errorf("expected argument '{}', got %q", tc.argument)
	}

	// Build final ToolCallCompleted.
	completed := ToolCallCompleted{
		ID:   tc.id,
		Type: tc.typ,
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{
			Name:      tc.name,
			Arguments: tc.argument,
		},
	}

	if completed.ID != "call_abc123" {
		t.Errorf("expected ID 'call_abc123', got %q", completed.ID)
	}
	if completed.Function.Name != "get_references" {
		t.Errorf("expected function name 'get_references', got %q", completed.Function.Name)
	}
}

func TestStreamChunk_ToolCallsPriority(t *testing.T) {
	// When ToolCalls is non-nil, caller should check it BEFORE Done.
	chunk := StreamChunk{
		ToolCalls: []ToolCallCompleted{
			{ID: "call_abc", Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "get_references", Arguments: "{}"}},
		},
		Done: true,
	}

	if chunk.ToolCalls == nil {
		t.Fatal("ToolCalls should not be nil")
	}
	if len(chunk.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(chunk.ToolCalls))
	}
	if chunk.ToolCalls[0].Function.Name != "get_references" {
		t.Errorf("unexpected function name: %s", chunk.ToolCalls[0].Function.Name)
	}
	if !chunk.Done {
		t.Error("Done should be true when tool call is complete")
	}
}
