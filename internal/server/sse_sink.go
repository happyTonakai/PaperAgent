package server

import (
	"log"
)

// sseSink adapts a chat.Sink to Server-Sent Events streaming. It writes
// "chunk" events for each text fragment and a "done" event (with token
// counts) at the end. Errors are emitted as "error" events.
//
// sseSink is not safe for concurrent use; the engine calls its methods
// serially from a single goroutine.
type sseSink struct {
	sw *sseWriter
}

// OnChunk forwards an LLM text fragment to the client as an SSE "chunk" event.
func (s *sseSink) OnChunk(text string) error {
	return s.sw.WriteChunk(text)
}

// OnToolCall emits a "tool_call" event so the Web UI can show a fetching
// indicator while the engine executes the tool. Without this, a slow tool
// (e.g. fetch_arxiv, which hits the network) leaves the page with no
// streaming output for several seconds, looking frozen.
func (s *sseSink) OnToolCall(name string) {
	if err := s.sw.WriteToolCall(name); err != nil {
		log.Printf("[sse-sink] write tool_call event: %v", err)
	}
}

// OnDone emits the terminal "done" event with token counts so the
// client can update its footer.
func (s *sseSink) OnDone(answer string, promptTokens, completionTokens, cachedTokens int) {
	s.sw.WriteDoneWithTokens("", promptTokens, completionTokens, cachedTokens)
}

// OnError emits an SSE "error" event. The connection is left open so
// the client can render the partial answer, matching the prior behavior
// of the inline handler.
func (s *sseSink) OnError(err error) {
	if err := s.sw.WriteError(err.Error()); err != nil {
		log.Printf("[sse-sink] write error event: %v", err)
	}
}