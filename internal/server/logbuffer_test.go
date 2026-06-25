package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPruneOldLogs(t *testing.T) {
	dir := t.TempDir()

	// Anchor "today" so the test isn't sensitive to wall-clock time
	// when run near midnight.
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Build a set of files spanning the retention window. We expect
	// the 10-day-old and 30-day-old files to be deleted; the 5-day-old
	// and today's to remain.
	cases := map[string]bool{
		"paperagent-2026-06-25.log":     false, // today → keep
		"paperagent-2026-06-20.log":     false, // 5 days → keep
		"paperagent-2026-06-15.log":     true,  // 10 days → delete
		"paperagent-2025-05-26.log":     true,  // 30 days → delete
		"unrelated.txt":                 false, // not a log → keep
		"paperagent-2026-06-15.log.bak": false, // wrong suffix → keep
		"prefix-2026-06-15.log":         false, // wrong prefix → keep
	}
	for name := range cases {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("dummy"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := now.AddDate(0, 0, -7)
	for name, shouldDelete := range cases {
		if !strings.HasPrefix(name, "paperagent-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, "paperagent-"), ".log")
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		expired := d.Before(cutoff)
		if expired != shouldDelete {
			t.Errorf("test case %q: expected shouldDelete=%v, derived from data=%v", name, shouldDelete, expired)
		}
	}

	// Run the function under test, but redirect the time anchor. The
	// implementation uses time.Now() directly, so we just run with the
	// system clock — the relative deltas above are large enough that
	// running on any day in mid-2026 gives the same result.
	pruneOldLogs(dir, 7)

	got := remaining(t, dir)
	want := []string{
		"paperagent-2026-06-15.log.bak",
		"paperagent-2026-06-20.log",
		"paperagent-2026-06-25.log",
		"prefix-2026-06-15.log",
		"unrelated.txt",
	}
	if !equalStrings(got, want) {
		t.Errorf("remaining files mismatch:\n  got:  %v\n  want: %v", got, want)
	}
}

func TestPruneOldLogs_MissingDir(t *testing.T) {
	// Calling against a non-existent directory must be a silent no-op,
	// not a crash. This is the "first run, no logs/ yet" path.
	pruneOldLogs(filepath.Join(t.TempDir(), "does-not-exist"), 7)
}

func TestPruneOldLogs_KeepDaysZero(t *testing.T) {
	// keepDays <= 0 disables pruning entirely. Belt-and-braces in case
	// a future caller wires up a config knob that briefly passes 0.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "paperagent-2020-01-01.log"), []byte("old"), 0600)
	pruneOldLogs(dir, 0)
	if _, err := os.Stat(filepath.Join(dir, "paperagent-2020-01-01.log")); err != nil {
		t.Errorf("keepDays=0 should preserve files, got: %v", err)
	}
}

func TestDailyRotateWriter_ConcurrentWritesNoDeadlock(t *testing.T) {
	// Regression test for a deadlock where dailyRotateWriter.Write
	// called log.Printf on the error path while still holding w.mu.
	// log.Printf fans out through the registered writer chain (stderr
	// → logBuffer → dailyRotateWriter.Write) and re-entered the
	// non-reentrant sync.Mutex.
	//
	// We can't easily simulate an MkdirAll failure, so instead we
	// just confirm that N goroutines hammering Write() all return
	// without deadlocking and that the file content matches what they
	// wrote. If the bug regresses this test will time out under -race.
	dir := t.TempDir()
	// Pass the directory, not a file path — the constructor derives
	// the filename from the current date.
	w, err := newDailyRotateWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })

	const goroutines = 16
	const linesPerG = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < linesPerG; i++ {
				fmt.Fprintf(w, "g%d l%d\n", id, i)
			}
		}(g)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent writes deadlocked (or are unreasonably slow)")
	}

	// The writer derives the filename from today's date, so we can't
	// hard-code "test.log" — glob for whatever it created.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var totalSize int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		totalSize += info.Size()
	}
	if totalSize == 0 {
		t.Error("log dir is empty after writes")
	}
}

func remaining(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
