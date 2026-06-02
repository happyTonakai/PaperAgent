package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSE event types
const (
	SSEEventChunk    = "chunk"
	SSEEventDone     = "done"
	SSEEventError    = "error"
	SSEEventTitle    = "title"
	SSEEventCreated  = "created"
)

type SSEEvent struct {
	Type             string `json:"type"`
	Content          string `json:"content,omitempty"`
	Error            string `json:"error,omitempty"`
	PaperID          string `json:"paper_id,omitempty"`
	Title            string `json:"title,omitempty"`
	RoundID          int    `json:"round_id,omitempty"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	CachedTokens     int    `json:"cached_tokens,omitempty"`
}

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, error) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}

	return &sseWriter{w: w, flusher: flusher}, nil
}

func (s *sseWriter) WriteEvent(evt SSEEvent) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.w, "data: %s\n\n", data)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *sseWriter) WriteChunk(content string) error {
	return s.WriteEvent(SSEEvent{Type: SSEEventChunk, Content: content})
}

func (s *sseWriter) WriteCreated(paperID string) error {
	return s.WriteEvent(SSEEvent{Type: SSEEventCreated, PaperID: paperID})
}

func (s *sseWriter) WriteDone(paperID string) error {
	return s.WriteEvent(SSEEvent{Type: SSEEventDone, PaperID: paperID})
}

func (s *sseWriter) WriteDoneWithTokens(paperID string, promptTokens, completionTokens, cachedTokens int) error {
	return s.WriteEvent(SSEEvent{
		Type:             SSEEventDone,
		PaperID:          paperID,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     cachedTokens,
	})
}

func (s *sseWriter) WriteError(errMsg string) error {
	return s.WriteEvent(SSEEvent{Type: SSEEventError, Error: errMsg})
}

func (s *sseWriter) WriteTitle(title string) error {
	return s.WriteEvent(SSEEvent{Type: SSEEventTitle, Title: title})
}
