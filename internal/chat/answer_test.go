package chat

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/prompt"
	"github.com/happyTonakai/paperagent/internal/session"
)

// fakeLLM is a programmable llmClient for tests. Each test sets the
// scripts it expects; the fake dispatches chunks in order. It also
// retains the messages array from the most recent ChatStream call so
// tests can assert on what was sent to the LLM.
type fakeLLM struct {
	mu           sync.Mutex
	scripts      [][]api.StreamChunk
	calls        int
	lastMessages []api.ChatMessage
}

func (f *fakeLLM) ChatStream(model string, messages []api.ChatMessage, tools []api.Tool) <-chan api.StreamChunk {
	f.mu.Lock()
	if f.calls >= len(f.scripts) {
		f.mu.Unlock()
		ch := make(chan api.StreamChunk)
		close(ch)
		return ch
	}
	script := f.scripts[f.calls]
	f.calls++
	// Defensive copy so later mutations to the caller's slice don't
	// affect what tests see.
	f.lastMessages = append([]api.ChatMessage(nil), messages...)
	f.mu.Unlock()

	ch := make(chan api.StreamChunk, len(script)+1)
	for _, c := range script {
		ch <- c
	}
	close(ch)
	return ch
}

// recordingSink captures all sink events for assertions.
type recordingSink struct {
	mu        sync.Mutex
	chunks    []string
	toolCalls []string
	doneArg   struct {
		answer           string
		promptTokens     int
		completionTokens int
		cachedTokens     int
		called           bool
	}
	errArg error
}

func (s *recordingSink) OnChunk(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = append(s.chunks, text)
	return nil
}

func (s *recordingSink) OnToolCall(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCalls = append(s.toolCalls, name)
}

func (s *recordingSink) OnDone(answer string, pTokens, cTokens, ccTokens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doneArg.answer = answer
	s.doneArg.promptTokens = pTokens
	s.doneArg.completionTokens = cTokens
	s.doneArg.cachedTokens = ccTokens
	s.doneArg.called = true
}

func (s *recordingSink) OnError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errArg = err
}

// newTestPaper returns a paper with a minimal content+references pair
// loaded into a temp directory so Save() works.
func newTestPaper(t *testing.T, content, references string) *session.Paper {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	paper := session.NewPaper(content, "https://arxiv.org/abs/0000.0000", "")
	paper.References = references
	if err := paper.Save(); err != nil {
		t.Fatalf("save paper: %v", err)
	}
	return paper
}

// cfgWithDefaults returns a config with the fields the engine reads
// populated with sane defaults. Tests that don't exercise token-anchor
// behavior can ignore most of these.
func cfgWithDefaults() *config.Config {
	return &config.Config{
		UI: config.UIConfig{
			MinRecentRounds: 2,
			MaxInputTokens:  30000,
		},
		API: config.APIConfig{
			DefaultModel: "fake-model",
		},
	}
}

func TestBuildMessages_BasicShape(t *testing.T) {
	paper := &session.Paper{
		Content: "paper body",
		Messages: []session.Message{
			{RoundNumber: 0, Role: "user", Content: "first q"},
			{RoundNumber: 0, Role: "assistant", Content: "first a"},
			{RoundNumber: 1, Role: "user", Content: "second q"},
		},
	}
	msgs := BuildMessages(paper, "third q", prompt.GetLight())

	if len(msgs) < 4 {
		t.Fatalf("expected at least 4 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("messages[0].Role = %q, want system", msgs[0].Role)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "paper body" {
		t.Errorf("messages[1] = %+v, want user/paper body", msgs[1])
	}
	if msgs[2].Role != "user" || msgs[2].Content != prompt.GetLight() {
		t.Errorf("messages[2] = %+v, want user/light prompt", msgs[2])
	}
	if last := msgs[len(msgs)-1]; last.Role != "user" || last.Content != "third q" {
		t.Errorf("last message = %+v, want user/third q", last)
	}
}

func TestAnswer_SimpleStream(t *testing.T) {
	paper := newTestPaper(t, "body", "")

	fake := &fakeLLM{
		scripts: [][]api.StreamChunk{
			{
				{Content: "hello "},
				{Content: "world"},
				{Done: true, PromptTokens: 100, CompletionTokens: 50, CachedTokens: 20},
			},
		},
	}
	sink := &recordingSink{}
	engine := NewEngine(fake, cfgWithDefaults())

	if err := engine.Answer(context.Background(), paper, "what?", false, sink); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	if got := strings.Join(sink.chunks, ""); got != "hello world" {
		t.Errorf("chunks = %q, want %q", got, "hello world")
	}
	if !sink.doneArg.called {
		t.Fatal("OnDone not called")
	}
	if sink.doneArg.answer != "hello world" {
		t.Errorf("doneArg.answer = %q, want %q", sink.doneArg.answer, "hello world")
	}
	if sink.doneArg.promptTokens != 100 || sink.doneArg.completionTokens != 50 || sink.doneArg.cachedTokens != 20 {
		t.Errorf("doneArg tokens = (%d, %d, %d), want (100, 50, 20)", sink.doneArg.promptTokens, sink.doneArg.completionTokens, sink.doneArg.cachedTokens)
	}
	if sink.errArg != nil {
		t.Errorf("OnError called with %v", sink.errArg)
	}

	// Paper should now have user + assistant messages for round 1.
	if got := len(paper.Messages); got != 2 {
		t.Fatalf("paper.Messages len = %d, want 2", got)
	}
	if paper.Messages[0].Role != "user" || paper.Messages[0].Content != "what?" {
		t.Errorf("Messages[0] = %+v, want user/what?", paper.Messages[0])
	}
	if paper.Messages[1].Role != "assistant" || paper.Messages[1].Content != "hello world" {
		t.Errorf("Messages[1] = %+v, want assistant/hello world", paper.Messages[1])
	}
	if paper.Messages[1].PromptTokens != 100 {
		t.Errorf("Messages[1].PromptTokens = %d, want 100", paper.Messages[1].PromptTokens)
	}
}

func TestAnswer_ToolCallFollowUp(t *testing.T) {
	paper := newTestPaper(t, "body", "ref1\nref2\n")

	toolCallID := "call_abc"
	toolCall := api.ToolCallCompleted{
		ID:   toolCallID,
		Type: "function",
	}
	toolCall.Function.Name = "get_references"
	toolCall.Function.Arguments = "{}"

	fake := &fakeLLM{
		scripts: [][]api.StreamChunk{
			{
				{ToolCalls: []api.ToolCallCompleted{toolCall}},
			},
			{
				{Content: "answer "},
				{Content: "after refs"},
				{Done: true, PromptTokens: 200, CompletionTokens: 80, CachedTokens: 40},
			},
		},
	}
	sink := &recordingSink{}
	engine := NewEngine(fake, cfgWithDefaults())

	if err := engine.Answer(context.Background(), paper, "what refs?", false, sink); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	if len(sink.toolCalls) != 1 || sink.toolCalls[0] != "get_references" {
		t.Errorf("toolCalls = %v, want [get_references]", sink.toolCalls)
	}
	if got := strings.Join(sink.chunks, ""); got != "answer after refs" {
		t.Errorf("chunks = %q, want %q", got, "answer after refs")
	}
	if !sink.doneArg.called {
		t.Fatal("OnDone not called")
	}
	// Token counts come from the second (follow-up) pass, not the first.
	if sink.doneArg.promptTokens != 200 || sink.doneArg.completionTokens != 80 || sink.doneArg.cachedTokens != 40 {
		t.Errorf("doneArg tokens = (%d, %d, %d), want (200, 80, 40)", sink.doneArg.promptTokens, sink.doneArg.completionTokens, sink.doneArg.cachedTokens)
	}

	// Engine should have made exactly two ChatStream calls (first + follow-up).
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.calls != 2 {
		t.Errorf("fake.calls = %d, want 2 (first + tool follow-up)", fake.calls)
	}
}

func TestAnswer_ToolCallPersistsMessages(t *testing.T) {
	paper := newTestPaper(t, "body", "ref1\nref2\n")

	toolCallID := "call_xyz"
	toolCall := api.ToolCallCompleted{
		ID:   toolCallID,
		Type: "function",
	}
	toolCall.Function.Name = "get_references"
	toolCall.Function.Arguments = "{}"

	fake := &fakeLLM{
		scripts: [][]api.StreamChunk{
			{
				{ToolCalls: []api.ToolCallCompleted{toolCall}},
			},
			{
				{Content: "the answer"},
				{Done: true, PromptTokens: 200, CompletionTokens: 80, CachedTokens: 40},
			},
		},
	}
	sink := &recordingSink{}
	engine := NewEngine(fake, cfgWithDefaults())

	if err := engine.Answer(context.Background(), paper, "what refs?", false, sink); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	// Paper should now contain 4 messages for round 1:
	//   user, assistant(tool_calls), tool(result), assistant(final)
	if got := len(paper.Messages); got != 4 {
		t.Fatalf("paper.Messages len = %d, want 4 (user, assistant_calls, tool, assistant_final)", got)
	}

	// 0: user
	if paper.Messages[0].Role != "user" || paper.Messages[0].Content != "what refs?" {
		t.Errorf("Messages[0] = %+v, want user/what refs?", paper.Messages[0])
	}

	// 1: assistant with ToolCalls, no Content
	if paper.Messages[1].Role != "assistant" {
		t.Errorf("Messages[1].Role = %q, want assistant", paper.Messages[1].Role)
	}
	if paper.Messages[1].Content != "" {
		t.Errorf("Messages[1].Content = %q, want empty", paper.Messages[1].Content)
	}
	if len(paper.Messages[1].ToolCalls) != 1 || paper.Messages[1].ToolCalls[0].ID != toolCallID {
		t.Errorf("Messages[1].ToolCalls = %+v, want one call with ID %q", paper.Messages[1].ToolCalls, toolCallID)
	}
	if paper.Messages[1].TokenCount != 0 {
		t.Errorf("Messages[1].TokenCount = %d, want 0 (no text content)", paper.Messages[1].TokenCount)
	}

	// 2: tool result with ToolCallID and references content
	if paper.Messages[2].Role != "tool" {
		t.Errorf("Messages[2].Role = %q, want tool", paper.Messages[2].Role)
	}
	if paper.Messages[2].ToolCallID != toolCallID {
		t.Errorf("Messages[2].ToolCallID = %q, want %q", paper.Messages[2].ToolCallID, toolCallID)
	}
	if paper.Messages[2].Content != "ref1\nref2\n" {
		t.Errorf("Messages[2].Content = %q, want references string", paper.Messages[2].Content)
	}
	wantToolTokens := session.EstimateTokens("ref1\nref2\n")
	if paper.Messages[2].TokenCount != wantToolTokens {
		t.Errorf("Messages[2].TokenCount = %d, want %d", paper.Messages[2].TokenCount, wantToolTokens)
	}

	// 3: final assistant with answer
	if paper.Messages[3].Role != "assistant" || paper.Messages[3].Content != "the answer" {
		t.Errorf("Messages[3] = %+v, want assistant/the answer", paper.Messages[3])
	}
	if paper.Messages[3].PromptTokens != 200 {
		t.Errorf("Messages[3].PromptTokens = %d, want 200", paper.Messages[3].PromptTokens)
	}

	// All four must share the same round.
	for i, m := range paper.Messages {
		if m.RoundNumber != 1 {
			t.Errorf("Messages[%d].RoundNumber = %d, want 1", i, m.RoundNumber)
		}
	}
}

func TestAnswer_ToolCallPersistsSkipContext(t *testing.T) {
	paper := newTestPaper(t, "body", "refs")

	toolCall := api.ToolCallCompleted{ID: "call_btw", Type: "function"}
	toolCall.Function.Name = "get_references"
	toolCall.Function.Arguments = "{}"

	fake := &fakeLLM{
		scripts: [][]api.StreamChunk{
			{{ToolCalls: []api.ToolCallCompleted{toolCall}}},
			{{Content: "answer"}, {Done: true, PromptTokens: 100, CompletionTokens: 50}},
		},
	}
	sink := &recordingSink{}
	engine := NewEngine(fake, cfgWithDefaults())

	// skipContext=true simulates /btw — the entire round should be excluded.
	if err := engine.Answer(context.Background(), paper, "btw q", true, sink); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if got := len(paper.Messages); got != 4 {
		t.Fatalf("paper.Messages len = %d, want 4", got)
	}
	for i, m := range paper.Messages {
		if !m.SkipContext {
			t.Errorf("Messages[%d] (%s) SkipContext = false, want true (round is /btw)", i, m.Role)
		}
	}
}

func TestAnswer_ToolCallHistoryIncludedInNextRound(t *testing.T) {
	// Round 1: triggers a tool call. Verify that round 2's LLM context
	// includes the tool-call sequence from round 1 (via BuildMessages).
	paper := newTestPaper(t, "body", "ref content")

	toolCall := api.ToolCallCompleted{ID: "call_1", Type: "function"}
	toolCall.Function.Name = "get_references"
	toolCall.Function.Arguments = "{}"

	fake := &fakeLLM{
		scripts: [][]api.StreamChunk{
			// Round 1, first pass: tool call
			{{ToolCalls: []api.ToolCallCompleted{toolCall}}},
			// Round 1, follow-up: answer
			{{Content: "answer1"}, {Done: true, PromptTokens: 100, CompletionTokens: 50}},
			// Round 2, single pass: answer
			{{Content: "answer2"}, {Done: true, PromptTokens: 110, CompletionTokens: 55}},
		},
	}
	sink := &recordingSink{}
	engine := NewEngine(fake, cfgWithDefaults())

	// Round 1
	if err := engine.Answer(context.Background(), paper, "q1", false, sink); err != nil {
		t.Fatalf("round 1: %v", err)
	}

	// Inspect the messages array passed to the second ChatStream call
	// (round 1's follow-up). It should contain assistant(tool_calls) and
	// tool(result) at the end.
	fake.mu.Lock()
	followUpMsgs := fake.lastMessages
	fake.mu.Unlock()
	if len(followUpMsgs) < 2 {
		t.Fatalf("follow-up messages len = %d, want at least 2", len(followUpMsgs))
	}
	lastTwo := followUpMsgs[len(followUpMsgs)-2:]
	if lastTwo[0].Role != "assistant" || len(lastTwo[0].ToolCalls) == 0 {
		t.Errorf("second-to-last message = %+v, want assistant with ToolCalls", lastTwo[0])
	}
	if lastTwo[1].Role != "tool" || lastTwo[1].ToolCallID != "call_1" {
		t.Errorf("last message = %+v, want tool with ToolCallID=call_1", lastTwo[1])
	}

	// Round 2
	sink2 := &recordingSink{}
	if err := engine.Answer(context.Background(), paper, "q2", false, sink2); err != nil {
		t.Fatalf("round 2: %v", err)
	}

	// Round 2's first (and only) ChatStream call should have a context
	// that includes round 1's tool-call sequence.
	fake.mu.Lock()
	round2Msgs := fake.lastMessages
	fake.mu.Unlock()

	// Expect: system + paper body + light prompt + (round1: user, assistant_calls, tool, assistant_final) + q2
	// = 3 prefix + 4 round-1 messages + 1 question = 8 messages.
	if len(round2Msgs) != 8 {
		t.Fatalf("round 2 messages len = %d, want 8", len(round2Msgs))
	}

	// Round 1's tool call must be in the context (positions 3..6).
	toolCallMsg := round2Msgs[3]
	if toolCallMsg.Role != "user" || toolCallMsg.Content != "q1" {
		t.Errorf("round2Msgs[3] = %+v, want user/q1", toolCallMsg)
	}
	toolCallMsg = round2Msgs[4]
	if toolCallMsg.Role != "assistant" || len(toolCallMsg.ToolCalls) != 1 || toolCallMsg.ToolCalls[0].ID != "call_1" {
		t.Errorf("round2Msgs[4] = %+v, want assistant with tool_call id=call_1", toolCallMsg)
	}
	toolResultMsg := round2Msgs[5]
	if toolResultMsg.Role != "tool" || toolResultMsg.ToolCallID != "call_1" {
		t.Errorf("round2Msgs[5] = %+v, want tool with tool_call_id=call_1", toolResultMsg)
	}
}

func TestAnswer_StreamingErrorPersistsPartial(t *testing.T) {
	paper := newTestPaper(t, "body", "")

	fake := &fakeLLM{
		scripts: [][]api.StreamChunk{
			{
				{Content: "partial "},
				{Content: "answer"},
				{Err: errors.New("network blip")},
			},
		},
	}
	sink := &recordingSink{}
	engine := NewEngine(fake, cfgWithDefaults())

	// Streaming errors are reported via OnError; engine returns nil.
	if err := engine.Answer(context.Background(), paper, "what?", false, sink); err != nil {
		t.Fatalf("Answer should swallow streaming errors, got %v", err)
	}
	if sink.errArg == nil {
		t.Fatal("expected OnError to be called")
	}
	if got := strings.Join(sink.chunks, ""); got != "partial answer" {
		t.Errorf("chunks = %q, want %q", got, "partial answer")
	}

	// Both user and assistant (partial) messages should be persisted.
	if got := len(paper.Messages); got != 2 {
		t.Fatalf("paper.Messages len = %d, want 2", got)
	}
	if paper.Messages[1].Content != "partial answer" {
		t.Errorf("Messages[1].Content = %q, want %q", paper.Messages[1].Content, "partial answer")
	}
}

func TestAnswer_ContextCancellation(t *testing.T) {
	paper := newTestPaper(t, "body", "")

	// Use a fake that holds the stream channel open until the caller
	// cancels — the only way out is via ctx.
	blocking := &blockingFake{}
	sink := &recordingSink{}
	engine := NewEngine(blocking, cfgWithDefaults())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if err := engine.Answer(ctx, paper, "what?", false, sink); err != nil {
		t.Fatalf("Answer returned %v, want nil on cancellation", err)
	}
	if sink.errArg == nil {
		t.Fatal("expected OnError to be called on cancellation")
	}
	if !errors.Is(sink.errArg, context.Canceled) {
		t.Errorf("errArg = %v, want context.Canceled", sink.errArg)
	}
}

// blockingFake holds ChatStream open until the context passed to the
// caller is canceled. Used to verify the engine respects ctx.
type blockingFake struct{}

func (b *blockingFake) ChatStream(model string, messages []api.ChatMessage, tools []api.Tool) <-chan api.StreamChunk {
	ch := make(chan api.StreamChunk)
	// Never close; the test cancels its context to break out.
	return ch
}

// silentLogger suppresses log.Printf output during tests so the run
// stays readable. Restore the default in TestMain if/when needed.
func silentLogger() {
	log.SetOutput(io.Discard)
}

func TestMain(m *testing.M) {
	silentLogger()
	m.Run()
}