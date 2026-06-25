package server

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
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
// It mirrors every line to stderr and (if Open was called) to a
// daily-rotated file under ConfigDir/logs/.
type logBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	next    int
	full    bool

	// disk is the file writer for the daily-rotated on-disk log file
	// (under ConfigDir/logs/). May be nil before Open() is called, in
	// which case the buffer still works for in-memory + stderr output.
	diskMu sync.Mutex
	disk   *dailyRotateWriter
}

func newLogBuffer() *logBuffer {
	return &logBuffer{
		entries: make([]LogEntry, maxLogEntries),
	}
}

// Open attaches a daily-rotated file writer so log lines are also
// persisted to disk under <dir>/paperagent-YYYY-MM-DD.log. Safe to
// call once at startup; later calls replace the writer (rare, useful
// for tests).
//
// The daily rotate pattern matches macOS-style system logs: a new
// file at each local-day boundary, no automatic size cap (a single
// day of PaperAgent activity stays well under any reasonable
// threshold).
func (lb *logBuffer) Open(dir string) error {
	w, err := newDailyRotateWriter(dir)
	if err != nil {
		return err
	}
	lb.diskMu.Lock()
	if lb.disk != nil {
		_ = lb.disk.Close()
	}
	lb.disk = w
	lb.diskMu.Unlock()
	return nil
}

// Close releases the file handle held by the rotate writer. Safe to
// call even if Open was never called (no-op).
func (lb *logBuffer) Close() error {
	lb.diskMu.Lock()
	defer lb.diskMu.Unlock()
	if lb.disk == nil {
		return nil
	}
	err := lb.disk.Close()
	lb.disk = nil
	return err
}

func (lb *logBuffer) Write(p []byte) (int, error) {
	lb.mu.Lock()
	lb.entries[lb.next] = LogEntry{
		Time:    time.Now().Format("15:04:05.000"),
		Message: string(p),
	}
	lb.next = (lb.next + 1) % maxLogEntries
	if lb.next == 0 {
		lb.full = true
	}
	lb.mu.Unlock()

	// Snapshot the disk writer under diskMu so a concurrent Open() can't
	// swap it out mid-write. The local `disk` reference keeps the
	// writer alive even if lb.disk is reassigned.
	lb.diskMu.Lock()
	disk := lb.disk
	lb.diskMu.Unlock()
	if disk != nil {
		_, _ = disk.Write(p)
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
// that writes to stderr, the in-memory buffer, and (if Open was called)
// the on-disk file. log.SetOutput is process-global so it's racy with
// concurrent writers; the sync.Mutex inside dailyRotateWriter keeps the
// file swap atomic.
func initLogCapture(lb *logBuffer) {
	log.SetOutput(io.MultiWriter(os.Stderr, lb))
}

// dailyRotateWriter is an io.Writer that appends to a file whose path
// changes when the local calendar day rolls over. The constructor
// takes the directory; the file name is derived from the current date
// as <baseDir>/paperagent-YYYY-MM-DD.log.
//
// Implementation note: we re-open on every Write that crosses a day
// boundary rather than running a background goroutine. That avoids the
// "the rotate goroutine died and the file stayed closed" failure mode
// at the cost of a single stat on every write (cheap, log output is
// not on a hot path).
type dailyRotateWriter struct {
	mu      sync.Mutex
	baseDir string
	file    *os.File
	day     string // YYYY-MM-DD
}

func newDailyRotateWriter(baseDir string) (*dailyRotateWriter, error) {
	w := &dailyRotateWriter{baseDir: baseDir}
	if err := w.rotateIfNeeded(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyRotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if err := w.rotateIfNeeded(); err != nil {
		// Disk failures must not crash the process; report and drop.
		// We MUST NOT call log.Printf here: that would re-enter the
		// log.SetOutput writer chain (→ lb.Write → w.Write → w.mu.Lock)
		// and deadlock the goroutine on the non-reentrant sync.Mutex.
		fmt.Fprintf(os.Stderr, "[log] rotate/write error (continuing without disk log): %v\n", err)
		w.mu.Unlock()
		return len(p), nil
	}
	n, err := w.file.Write(p)
	w.mu.Unlock()
	return n, err
}

func (w *dailyRotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	w.day = ""
	return err
}

func (w *dailyRotateWriter) rotateIfNeeded() error {
	today := time.Now().Format("2006-01-02")
	if w.file != nil && w.day == today {
		return nil
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	if err := os.MkdirAll(w.baseDir, 0700); err != nil {
		return err
	}
	newPath := filepath.Join(w.baseDir, "paperagent-"+today+".log")
	f, err := os.OpenFile(newPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.file = f
	w.day = today
	return nil
}

// pruneOldLogs deletes paperagent-YYYY-MM-DD.log files in dir whose
// embedded date is more than keepDays ago. Called once at server
// startup so the logs/ directory doesn't grow unboundedly for users
// who keep the daemon running for months.
//
// Files whose name doesn't parse as paperagent-<date>.log are left
// alone — that protects unrelated files (e.g. crash dumps, manual
// backups) the user might have placed in the logs dir.
//
// Errors reading the directory or deleting a single file are reported
// on stderr but never fatal: a partial cleanup is still better than
// refusing to start.
func pruneOldLogs(dir string, keepDays int) {
	if keepDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Most common reason: dir doesn't exist yet (first run). Silent.
		return
	}
	const prefix = "paperagent-"
	const suffix = ".log"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		// Strict parse: YYYY-MM-DD. Anything else (e.g. user-renamed
		// files) is left alone.
		if _, err := time.Parse("2006-01-02", dateStr); err != nil {
			continue
		}
		if dateStr >= cutoff {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			fmt.Fprintf(os.Stderr, "[log] prune: could not remove %s: %v\n", name, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "[log] pruned %s (older than %d days)\n", name, keepDays)
	}
}
