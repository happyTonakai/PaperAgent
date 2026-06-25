// Package chat contains the shared question-answering engine used by both
// the Web SSE handler and the Feishu bot. It owns message construction,
// the LLM stream loop (including tool-call follow-up), and message
// persistence — the per-transport rendering lives in sink implementations
// outside this package.
//
// Behavior notes:
//   - When `skipContext` is true (e.g. /btw on Feishu), the engine
//     propagates the flag to both the user message and the assistant
//     message. This causes collectAllContextMessages to exclude the
//     entire round from subsequent context — the prior behavior marked
//     only the user message, which leaked the btw answer into later
//     context. This is an intentional correctness fix.
//   - Tool-call follow-up: when the LLM returns a tool_calls delta, the
//     engine persists the assistant tool_calls message and the tool
//     result message into paper.Messages BEFORE the follow-up stream.
//     This ensures subsequent rounds can replay the tool call instead
//     of re-invoking it, and that the on-disk history matches what was
//     sent to the LLM.
//   - Only one tool (get_references) is currently exposed, so tool-call
//     arrays of length >1 are not expected. The engine handles the
//     single-call case explicitly; adding a second tool would require
//     extending the persistence and follow-up loops to handle per-call
//     routing.
//   - Locking is the caller's responsibility. The engine assumes the
//     in-memory paper pointer is stable for the duration of Answer.
package chat

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/prompt"
	"github.com/happyTonakai/paperagent/internal/session"
)

// Sink receives incremental output from Engine.Answer. Implementations
// translate each event into a transport-specific representation: the Web
// SSE handler emits "chunk"/"done"/"error" events, while the Feishu bot
// patches interactive cards.
//
// All callbacks are advisory except OnError and OnDone, which the engine
// uses to signal terminal state. A non-nil return from OnChunk aborts
// streaming (e.g., the SSE client disconnected). All other callbacks
// log and continue — the engine does not surface their errors.
type Sink interface {
	// OnChunk receives an incremental text fragment from the LLM.
	OnChunk(text string) error
	// OnToolCall is called when the LLM invokes a tool. The engine will
	// inject the tool result and continue streaming. Implementations
	// may surface this to the user, or no-op it.
	OnToolCall(name string)
	// OnDone is called once after the final answer is fully streamed.
	// By this point the assistant message has already been persisted
	// to the paper; the token counts reflect the final streaming pass.
	OnDone(answer string, promptTokens, completionTokens, cachedTokens int)
	// OnError is called when streaming fails. After OnError the engine
	// stops; the caller decides how to surface the error to the user.
	OnError(err error)
}

// llmClient is the subset of api.Client that the Engine depends on.
// Defined here so tests can supply a fake stream without spinning up
// a real HTTP client. *api.Client satisfies this interface. Unexported
// because the only consumer is in this package — external code can
// still construct an Engine by passing a *api.Client, since Go's
// structural typing accepts the real type for an unexported interface.
type llmClient interface {
	ChatStream(model string, messages []api.ChatMessage, tools []api.Tool) <-chan api.StreamChunk
}

// Engine runs the question-answering pipeline. It is safe for concurrent
// use across different papers; concurrent calls for the same paper are
// the caller's responsibility to serialize (the Web SSE handler holds a
// per-paper mutex; the Feishu bot relies on its message handler being
// single-threaded per chat).
type Engine struct {
	llm   llmClient
	cfg   *config.Config
}

// NewEngine returns an Engine that uses llm for LLM calls and reads
// model/UI settings from cfg. Pass *api.Client in production.
func NewEngine(llm llmClient, cfg *config.Config) *Engine {
	return &Engine{llm: llm, cfg: cfg}
}

// Answer asks the configured model a question about paper and writes the
// response to sink. It is the single source of truth for the chat phase:
// both the Web SSE handler and the Feishu bot call into it.
//
// Locking is the caller's responsibility. The engine assumes the caller
// holds a per-paper write lock for the duration of the call — it does
// not acquire or release locks internally, so the caller can interleave
// Answer with other operations if needed (though that would be racy).
//
// The engine:
//  1. Computes the next round number and appends the user message to
//     paper.Messages, persisting immediately so the question survives
//     even if streaming fails.
//  2. Builds the LLM message array (system + paper content + light
//     prompt + recent context + question). When paper.References is
//     non-empty, attaches the get_references tool.
//  3. Streams the response. If the LLM calls a tool, performs a
//     follow-up stream with the tool result injected.
//  4. Appends the assistant message, updates the truncation anchor,
//     and persists. The answer passed to sink.OnDone is the raw LLM
//     output — no transport-specific transformations (LaTeX conversion,
//     HTML escaping, etc.) happen here.
//
// Returns a non-nil error only on persistence failure. Streaming errors
// are reported via sink.OnError and do not propagate.
func (e *Engine) Answer(ctx context.Context, paper *session.Paper, question string, skipContext bool, sink Sink) error {
	if paper == nil {
		return errors.New("chat: nil paper")
	}
	if question == "" {
		return errors.New("chat: empty question")
	}
	if sink == nil {
		return errors.New("chat: nil sink")
	}

	round := paper.CurrentRound() + 1

	// 1. Save user message immediately.
	paper.AddMessage(session.Message{
		RoundNumber: round,
		Role:        "user",
		Content:     question,
		TokenCount:  session.EstimateTokens(question),
		SkipContext: skipContext,
	})
	if err := paper.Save(); err != nil {
		return fmt.Errorf("chat: save user message: %w", err)
	}

	// 2. Build messages + tools.
	messages := BuildMessages(paper, question, prompt.GetLight())
	var tools []api.Tool
	if paper.References != "" {
		tools = []api.Tool{api.GetReferencesTool()}
	}

	// 3. Stream + tool follow-up.
	answer, pTokens, cTokens, ccTokens, streamErr := e.stream(ctx, paper, round, messages, tools, paper.References, skipContext, sink)
	if streamErr != nil {
		sink.OnError(streamErr)
		// Persist what we have so far so the user can see the partial
		// response on reload. If even the partial answer is empty this
		// is a no-op. Persistence failures here are best-effort: the
		// user has already been notified of the stream error via
		// sink.OnError, so we log and continue rather than masking
		// the original error.
		if answer != "" {
			if perr := e.persistAssistant(paper, round, answer, pTokens, cTokens, ccTokens, skipContext); perr != nil {
				log.Printf("[chat] persist partial answer: %v", perr)
			}
		}
		return nil
	}

	// 4. Save assistant + update anchor.
	if err := e.persistAssistant(paper, round, answer, pTokens, cTokens, ccTokens, skipContext); err != nil {
		return err
	}

	// 5. Notify sink.
	sink.OnDone(answer, pTokens, cTokens, ccTokens)
	return nil
}

// ToAPIMessage converts a persisted session.Message into the LLM-facing
// api.ChatMessage, forwarding tool-call metadata. Without the forwarding,
// replays of past tool-calling rounds would send a tool result message
// without its tool_call_id, which the OpenAI API rejects.
//
// Centralized here so every caller that constructs LLM messages from
// paper.Messages uses the same conversion and stays in sync if more
// fields are added later.
func ToAPIMessage(msg session.Message) api.ChatMessage {
	cm := api.ChatMessage{Role: msg.Role, Content: msg.Content}
	if len(msg.ToolCalls) > 0 {
		cm.ToolCalls = msg.ToolCalls
	}
	if msg.ToolCallID != "" {
		cm.ToolCallID = msg.ToolCallID
	}
	return cm
}

// BuildMessages assembles the LLM message array for a chat round.
// paper.Content supplies the body; recent context comes from the
// paper's truncated message history. The chat-phase task prompt is
// `prompt.GetLight()` — the summary phase uses `prompt.GetHeavy()`
// inline in `handleNewPaper` instead, so this helper is chat-only.
func BuildMessages(paper *session.Paper, question, taskPrompt string) []api.ChatMessage {
	recent := paper.RecentContextMessages()
	messages := make([]api.ChatMessage, 0, 4+len(recent)+1)
	messages = append(messages,
		api.ChatMessage{Role: "system", Content: prompt.GetSystem()},
		api.ChatMessage{Role: "user", Content: paper.Content},
		api.ChatMessage{Role: "user", Content: taskPrompt},
	)
	for _, msg := range recent {
		messages = append(messages, ToAPIMessage(msg))
	}
	messages = append(messages, api.ChatMessage{Role: "user", Content: question})
	return messages
}

// persistAssistant appends the assistant message and updates the
// truncation anchor, persisting both. Split out for reuse by the
// partial-answer path on streaming errors.
func (e *Engine) persistAssistant(paper *session.Paper, round int, answer string, pTokens, cTokens, ccTokens int, skipContext bool) error {
	paper.AddMessage(session.Message{
		RoundNumber:      round,
		Role:             "assistant",
		Content:          answer,
		TokenCount:       session.EstimateTokens(answer),
		PromptTokens:     pTokens,
		CompletionTokens: cTokens,
		CachedTokens:     ccTokens,
		SkipContext:      skipContext,
	})
	paper.SetAnchorFromTokens(round, pTokens, cTokens, e.cfg.UI.MaxInputTokens, e.cfg.UI.MinRecentRounds)
	if err := paper.Save(); err != nil {
		return fmt.Errorf("chat: save assistant message: %w", err)
	}
	return nil
}

// stream runs the LLM stream and the tool-call follow-up. Streaming
// errors are returned; sink callback errors are logged and treated as
// advisory.
//
// When the LLM invokes a tool, the assistant message (with ToolCalls)
// and the tool result message are appended to paper.Messages BEFORE
// the follow-up stream. This is required so that:
//   - the follow-up stream sees a self-consistent conversation history;
//   - subsequent chat rounds can replay the tool call instead of
//     re-invoking the (potentially expensive) tool;
//   - the paper on disk reflects what was actually sent to the LLM.
//
// Tool-message persistence failure aborts the round (the caller is
// notified via the returned error and sink.OnError). This is deliberate:
// without persisted tool history, the next round would re-trigger the
// tool, which is both wasteful and confusing for the user.
func (e *Engine) stream(ctx context.Context, paper *session.Paper, round int, messages []api.ChatMessage, tools []api.Tool, references string, skipContext bool, sink Sink) (answer string, pTokens, cTokens, ccTokens int, err error) {
	var buf strings.Builder

	firstPass, firstTokens, firstErr := e.streamOnce(ctx, messages, tools, sink, &buf)
	pTokens, cTokens, ccTokens = firstTokens.prompt, firstTokens.completion, firstTokens.cached
	if firstErr != nil {
		return buf.String(), pTokens, cTokens, ccTokens, firstErr
	}
	if firstPass.toolCalls == nil || len(firstPass.toolCalls) == 0 || references == "" {
		return buf.String(), pTokens, cTokens, ccTokens, nil
	}

	// Tool follow-up: emit the assistant tool_calls message + the tool
	// result, then re-stream without tools.
	toolCalls := firstPass.toolCalls
	sink.OnToolCall(toolCalls[0].Function.Name)

	// Persist the assistant tool_calls message. Content is empty — the
	// tool call itself is the round's content. TokenCount is 0 because
	// there's no text to estimate.
	paper.AddMessage(session.Message{
		RoundNumber: round,
		Role:        "assistant",
		Content:     "",
		TokenCount:  0,
		SkipContext: skipContext,
		ToolCalls:   toolCalls,
	})
	// Persist the tool result message. The tool result content is the
	// references string, which can be large — estimate its tokens so
	// the round's token accounting is accurate. SkipContext is mirrored
	// from the round (e.g. /btw) for consistency with the user and
	// assistant messages in the same round, even though
	// collectAllContextMessages excludes by assistant.SkipContext, not
	// tool.SkipContext.
	paper.AddMessage(session.Message{
		RoundNumber: round,
		Role:        "tool",
		ToolCallID:  toolCalls[0].ID,
		Content:     references,
		TokenCount:  session.EstimateTokens(references),
		SkipContext: skipContext,
	})
	if err := paper.Save(); err != nil {
		sink.OnError(fmt.Errorf("chat: save tool-call messages: %w", err))
		return buf.String(), pTokens, cTokens, ccTokens, fmt.Errorf("save tool-call messages: %w", err)
	}

	followUp := make([]api.ChatMessage, 0, len(messages)+2)
	followUp = append(followUp, messages...)
	followUp = append(followUp,
		api.ChatMessage{Role: "assistant", ToolCalls: toolCalls},
		api.ChatMessage{Role: "tool", ToolCallID: toolCalls[0].ID, Content: references},
	)

	_, secondTokens, secondErr := e.streamOnce(ctx, followUp, nil, sink, &buf)
	// The follow-up pass is the authoritative source of token counts;
	// override the first-pass values.
	pTokens, cTokens, ccTokens = secondTokens.prompt, secondTokens.completion, secondTokens.cached
	if secondErr != nil {
		return buf.String(), pTokens, cTokens, ccTokens, secondErr
	}
	return buf.String(), pTokens, cTokens, ccTokens, nil
}

// streamTokens captures the token counts reported at the end of a stream.
type streamTokens struct {
	prompt     int
	completion int
	cached     int
}

// streamPass captures the result of a single LLM stream pass.
type streamPass struct {
	toolCalls []api.ToolCallCompleted
}

// streamOnce reads a single LLM stream, forwarding chunks to sink and
// appending them to buf. Token counts are taken from the final "done"
// chunk. The function respects ctx — if the caller cancels mid-stream,
// it returns ctx.Err() without draining the channel.
func (e *Engine) streamOnce(ctx context.Context, messages []api.ChatMessage, tools []api.Tool, sink Sink, buf *strings.Builder) (streamPass, streamTokens, error) {
	ch := e.llm.ChatStream(e.cfg.API.DefaultModel, messages, tools)
	tokens := streamTokens{}
	var pass streamPass

	for {
		select {
		case <-ctx.Done():
			return pass, tokens, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				// Channel closed without a "done" chunk; treat as success.
				return pass, tokens, nil
			}
			if chunk.Err != nil {
				return pass, tokens, fmt.Errorf("stream: %w", chunk.Err)
			}
			if chunk.ToolCalls != nil {
				pass.toolCalls = chunk.ToolCalls
				return pass, tokens, nil
			}
			if chunk.Done {
				tokens.prompt = chunk.PromptTokens
				tokens.completion = chunk.CompletionTokens
				tokens.cached = chunk.CachedTokens
				return pass, tokens, nil
			}
			if chunk.Content != "" {
				buf.WriteString(chunk.Content)
				if err := sink.OnChunk(chunk.Content); err != nil {
					// Sink returned an error (e.g., SSE client disconnected).
					// Log and abort streaming; this is not a streaming error
					// per se, so we don't surface it as one — caller can
					// still persist whatever made it through.
					log.Printf("[chat] sink OnChunk returned error: %v", err)
					return pass, tokens, nil
				}
			}
		}
	}
}