package server

import (
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// LogEntry represents a single log message with timestamp.
type LogEntry struct {
	Time    string `json:"time"`
	Message string `json:"message"`
}

const maxLogEntries = 500

// logBuffer is a thread-safe circular buffer that captures log output.
type logBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	next    int
	full    bool
}

func newLogBuffer() *logBuffer {
	return &logBuffer{
		entries: make([]LogEntry, maxLogEntries),
	}
}

func (lb *logBuffer) Write(p []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	lb.entries[lb.next] = LogEntry{
		Time:    time.Now().Format("15:04:05.000"),
		Message: string(p),
	}
	lb.next = (lb.next + 1) % maxLogEntries
	if lb.next == 0 {
		lb.full = true
	}
	return len(p), nil
}

// Recent returns the most recent n log entries in chronological order.
func (lb *logBuffer) Recent(n int) []LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if n <= 0 || n > maxLogEntries {
		n = maxLogEntries
	}

	var result []LogEntry
	if lb.full {
		// Circular: start from next, wrap around
		start := lb.next
		for i := 0; i < n; i++ {
			idx := (start + i) % maxLogEntries
			if lb.entries[idx].Message == "" {
				continue
			}
			result = append(result, lb.entries[idx])
		}
	} else {
		// Linear: from 0 to next-1
		count := lb.next
		if count > n {
			count = n
		}
		for i := lb.next - count; i < lb.next; i++ {
			if lb.entries[i].Message == "" {
				continue
			}
			result = append(result, lb.entries[i])
		}
	}
	return result
}

// initLogCapture replaces the standard log output with a multi-writer
// that writes to both stderr and the in-memory buffer.
func initLogCapture(lb *logBuffer) {
	log.SetOutput(io.MultiWriter(os.Stderr, lb))
}
