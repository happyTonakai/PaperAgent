package server

import (
	"log"
	"net"
	"net/http"
	"time"
)

// handleShutdown accepts POST /api/shutdown and triggers a graceful shutdown
// of the HTTP server.  Only requests from localhost are accepted — this is an
// internal endpoint used by the update subcommand, not a public API.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	log.Println("[shutdown] received shutdown request")

	// Restrict to localhost only.  The update subcommand runs on the same
	// machine, so remote shutdown via this endpoint is never legitimate.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "shutdown only allowed from localhost", http.StatusForbidden)
		return
	}

	if s.ShutdownFunc == nil {
		http.Error(w, "shutdown not configured", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("shutting down"))

	// Flush the response so the caller receives it before the server stops.
	// 200ms is sufficient for localhost RTT; ShutdownFunc (which eventually
	// calls httpServer.Shutdown) runs in a goroutine so this handler returns
	// before the listener is fully closed.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		s.ShutdownFunc()
	}()
}
