package chat

import (
	"context"

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