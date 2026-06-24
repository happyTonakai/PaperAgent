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
// scripts it expects; the fake dispatches chunks in order.
type fakeLLM struct {
	mu      sync.Mutex
	scripts [][]api.StreamChunk
	calls   int
	closed  int
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