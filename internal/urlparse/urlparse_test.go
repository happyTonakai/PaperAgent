package urlparse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsURL(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"https://arxiv.org/abs/2301.00001", true},
		{"http://example.com", true},
		{"/path/to/file", false},
		{"./relative", false},
		{"~/home/file", false},
		{"just text", false},
	}

	for _, tt := range tests {
		result := IsURL(tt.input)
		if result != tt.expected {
			t.Errorf("IsURL(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestIsFilePath(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"/absolute/path", true},
		{"./relative", true},
		{"../parent", true},
		{"~/home/file", true},
		{"https://example.com", false},
		{"just text", false},
	}

	for _, tt := range tests {
		result := IsFilePath(tt.input)
		if result != tt.expected {
			t.Errorf("IsFilePath(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestLoadFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "This is test paper content"
	os.WriteFile(testFile, []byte(content), 0644)

	result, err := LoadFile(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != content {
		t.Errorf("expected %q, got %q", content, result)
	}
}

func TestLoadFileWithTilde(t *testing.T) {
	// Create a file in temp dir
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "tilde test content"
	os.WriteFile(testFile, []byte(content), 0644)

	// Set HOME to tmpDir
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	result, err := LoadFile("~/test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != content {
		t.Errorf("expected %q, got %q", content, result)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := LoadFile("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestHTTPFetchInvalidURL(t *testing.T) {
	_, err := httpFetch(context.Background(), "http://localhost:1")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestHTTPFetchRespectsContextCancellation(t *testing.T) {
	// httpFetch on a URL that hangs should return promptly when ctx is
	// cancelled. We use a deliberately-slow test server (long sleep on a
	// 127.0.0.1 endpoint) and cancel the context partway through.
	// Skipped if a usable bind address isn't available.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(30 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := httpFetch(ctx, srv.URL)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected error after ctx cancellation, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("httpFetch took %v after ctx cancel; should return promptly", elapsed)
	}
}

func TestNormalizeArxivInput(t *testing.T) {
	tests := []struct {
		input string
		url   string
		id    string
		ok    bool
	}{
		{"2301.00001", "https://arxiv.org/abs/2301.00001", "2301.00001", true},
		{"arXiv:2301.00001v2", "https://arxiv.org/abs/2301.00001v2", "2301.00001v2", true},
		{"https://arxiv.org/abs/2404.12345", "https://arxiv.org/abs/2404.12345", "2404.12345", true},
		{"https://arxiv.org/pdf/2404.12345.pdf", "https://arxiv.org/abs/2404.12345", "2404.12345", true},
		{"cs/9901001", "https://arxiv.org/abs/cs/9901001", "cs/9901001", true},
		{"https://example.com/abs/2404.12345", "", "", false},
		{"not an id", "", "", false},
	}

	for _, tt := range tests {
		url, id, ok := NormalizeArxivInput(tt.input)
		if ok != tt.ok || url != tt.url || id != tt.id {
			t.Errorf("NormalizeArxivInput(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.input, url, id, ok, tt.url, tt.id, tt.ok)
		}
	}
}
