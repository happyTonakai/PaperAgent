package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/happyTonakai/paperagent/internal/session"
	"github.com/happyTonakai/paperagent/internal/urlparse"
)

// fetchArxivArgs is the JSON schema for the fetch_arxiv tool call.
// The LLM fills `url_or_id` with either an arXiv URL or a bare ID.
type fetchArxivArgs struct {
	URLOrID string `json:"url_or_id"`
}

// FetchArxivHandler returns a ToolHandler that fetches the arXiv paper
// at the URL/ID given in the tool call's `url_or_id` argument. The body
// is returned as Markdown with references stripped \u2014 references are
// not useful for cross-paper comparison in chat context and would
// inflate the token count. Use the get_references tool on the current
// paper if you need its references.
//
// Errors are returned to the engine, which surfaces them as the tool
// message content so the LLM can see what went wrong and adjust.
func FetchArxivHandler() ToolHandler {
	return func(ctx context.Context, arguments string) (string, error) {
		var args fetchArxivArgs
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("parse arguments: %w", err)
		}
		if args.URLOrID == "" {
			return "", fmt.Errorf("url_or_id is required")
		}
		// NormalizeArxivInput accepts both URLs and bare IDs (including
		// the "arxiv:" prefix). It rejects anything that isn't a valid
		// arXiv reference, so we don't have to worry about non-arxiv
		// URLs slipping through.
		canonical, id, ok := urlparse.NormalizeArxivInput(args.URLOrID)
		if !ok {
			return "", fmt.Errorf("not a valid arXiv URL or ID: %q", args.URLOrID)
		}

		// ctx is propagated through to the HTTP fetch and the arxiv2text
		// subprocess, so caller cancellation (e.g., browser tab close)
		// terminates the work promptly.
		content, err := urlparse.FetchURL(ctx, canonical)
		if err != nil {
			return "", fmt.Errorf("fetch %s: %w", id, err)
		}
		if content == "" {
			return "", fmt.Errorf("fetched empty content for %s", id)
		}

		// Strip references \u2014 same logic the chat pipeline uses on the
		// primary paper. We don't need refs here, and shipping them would
		// burn tokens on something the LLM can't really use for a
		// comparison.
		body, _ := session.ExtractReferences(content)
		return body, nil
	}
}
