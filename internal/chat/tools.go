package chat

import (
	"context"
	"fmt"
	"log"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/session"
)

// BuildChatTools returns the tool list and handler map for a chat round
// on the given paper. Centralized here so the Web SSE handler and the
// Feishu bot agree on which tools are available and how they're wired.
//
// Tool-availability rules:
//   - get_references is attached only when paper.References != "". Without
//     this guard the tool would always return empty content.
//   - fetch_arxiv is always attached \u2014 it's stateless and the LLM can
//     reach it for cross-paper comparison regardless of the current
//     paper's state.
//
// The handler map is keyed by tool name. Handlers close over paper
// state (e.g. get_references closes over paper.References, which varies
// per paper), which is why handlers are constructed per call rather
// than per engine.
func BuildChatTools(paper *session.Paper) ([]api.Tool, map[string]ToolHandler) {
	tools := []api.Tool{}
	handlers := map[string]ToolHandler{}

	if paper.References != "" {
		refs := paper.References
		tools = append(tools, api.GetReferencesTool())
		handlers["get_references"] = func(ctx context.Context, args string) (string, error) {
			return refs, nil
		}
	}

	tools = append(tools, api.FetchArxivTool())
	handlers["fetch_arxiv"] = FetchArxivHandler()

	return tools, handlers
}

// ResolveToolCall looks up the handler for the first tool call in toolCalls,
// executes it, and returns the content to send back to the LLM as the tool
// result message. If no handler is registered for the tool, a descriptive
// message is returned (so the LLM can see the problem and adjust) instead of
// aborting the round. Handler errors are likewise surfaced as the tool result.
//
// This is the single source of truth for tool dispatch: Engine.stream and the
// retry handlers (handleRetryChat / handleRetrySummary, which bypass the engine
// because they replace an existing round rather than appending one) both call
// it, so a tool advertised by BuildChatTools is resolved identically on the
// live-chat and retry paths.
func ResolveToolCall(ctx context.Context, handlers map[string]ToolHandler, toolCalls []api.ToolCallCompleted) string {
	toolName := toolCalls[0].Function.Name
	handler, ok := handlers[toolName]
	if !ok {
		log.Printf("[chat] no handler registered for tool %q", toolName)
		return fmt.Sprintf("Tool %q is not available in this session.", toolName)
	}
	result, herr := handler(ctx, toolCalls[0].Function.Arguments)
	if herr != nil {
		log.Printf("[chat] tool %q execution error: %v", toolName, herr)
		return fmt.Sprintf("Tool %q execution failed: %v", toolName, herr)
	}
	return result
}