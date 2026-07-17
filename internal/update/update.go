package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const repo = "happyTonakai/PaperAgent"

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func Run(currentVersion string) {
	// 1. Check latest version
	tag, err := getLatestTag()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking latest version: %v\n", err)
		os.Exit(1)
	}
	if tag == currentVersion {
		fmt.Printf("PaperAgent %s is already up to date.\n", currentVersion)
		return
	}
	fmt.Printf("Updating PaperAgent %s → %s...\n", currentVersion, tag)

	// 2. Detect running daemon and shut it down
	wasRunning := false
	port := tryDetectRunningDaemon()
	if port > 0 {
		fmt.Println("Stopping running PaperAgent...")
		if err := shutdownDaemon(port); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to stop running PaperAgent: %v\n", err)
			fmt.Fprintln(os.Stderr, "Continuing with update anyway...")
		} else {
			wasRunning = true
			// Allow the old server to release its socket (port, open files)
			// before we start downloading.  300ms is best-effort — if the port
			// isn't free yet the next server instance will find another via
			// findAvailablePort's range scan anyway.
			time.Sleep(300 * time.Millisecond)
		}
	}

	// 3. Download new binary
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error determining executable path: %v\n", err)
		os.Exit(1)
	}
	dir := filepath.Dir(exe)

	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	assetName := fmt.Sprintf("paperagent_%s_%s%s", runtime.GOOS, runtime.GOARCH, suffix)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, assetName)

	tmpPath := filepath.Join(dir, ".paperagent.update"+suffix)
	if runtime.GOOS != "windows" {
		defer func() {
			if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Warning: failed to clean up temp file: %v\n", err)
			}
		}()
	}

	fmt.Printf("Downloading %s...\n", assetName)
	if err := downloadFile(url, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}

	// Set executable bit so exec.Command can run the temp binary
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to make binary executable: %v\n", err)
		os.Exit(1)
	}

	// 4. SHA256 check (optional, best-effort)
	if err := verifyChecksum(tag, assetName, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Checksum verification failed: %v\n", err)
		os.Exit(1)
	}

	// 5. Verify the new binary runs
	fmt.Println("Verifying new binary...")
	if err := verifyBinary(tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Verification failed: %v\n", err)
		os.Exit(1)
	}

	// 6. Replace the old binary
	fmt.Println("Installing update...")
	if err := replaceBinary(tmpPath, exe); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to install update: %v\n", err)
		os.Exit(1)
	}

	// 7. Restart if it was running before
	if wasRunning {
		fmt.Println("Restarting PaperAgent...")
		if err := startBinary(exe); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to restart: %v\n", err)
			fmt.Fprintln(os.Stderr, "You can start PaperAgent manually by running the binary directly.")
			os.Exit(1)
		}
		fmt.Println("PaperAgent updated and restarted!")
	} else {
		fmt.Println("PaperAgent updated! Run the binary to start the new version.")
	}
}

// tryDetectRunningDaemon attempts to find a running PaperAgent instance by
// trying the configured or default ports. Returns 0 if none found.
func tryDetectRunningDaemon() int {
	type target struct{ host string; port int }
	targets := []target{{"localhost", 8686}}
	if v := os.Getenv("PAPER_ADDR"); v != "" {
		if h, pStr, err := net.SplitHostPort(v); err == nil && pStr != "" {
			if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
				if h == "" || h == "0.0.0.0" {
					h = "localhost"
				}
				targets = append([]target{{h, p}}, targets...)
			}
		}
	}

	client := &http.Client{Timeout: 2 * time.Second}
	for _, t := range targets {
		url := fmt.Sprintf("http://%s:%d/api/health", t.host, t.port)
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return t.port
			}
		}
	}
	return 0
}

func shutdownDaemon(port int) error {
	url := fmt.Sprintf("http://localhost:%d/api/shutdown", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("shutdown request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shutdown request returned %s", resp.Status)
	}
	return nil
}

func getLatestTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("failed to decode release info: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("release tag is empty")
	}
	return rel.TagName, nil
}

func downloadFile(url, path string) error {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP %s", resp.Status)
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	if written == 0 {
		return fmt.Errorf("downloaded file is empty")
	}
	return nil
}

func verifyChecksum(tag, assetName, tmpPath string) error {
	checksumsURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", repo, tag)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(checksumsURL)
	if err != nil {
		// checksums.txt not available — skip verification
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	// Find the expected hash for our asset
	lines := strings.Split(string(body), "\n")
	var expectedHash string
	for _, line := range lines {
		if strings.Contains(line, assetName) {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				expectedHash = fields[0]
				break
			}
		}
	}
	if expectedHash == "" {
		return nil
	}

	// Compute actual hash
	f, err := os.Open(tmpPath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	fmt.Printf("  SHA256 verified: %s\n", expectedHash)
	return nil
}

func verifyBinary(path string) error {
	cmd := exec.Command(path, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("binary verification failed: %w\noutput: %s", err, out)
	}
	// --version output format is "paperagent <version>"
	if !strings.Contains(string(out), "paperagent") {
		return fmt.Errorf("unexpected --version output (no 'paperagent'): %s", out)
	}
	return nil
}

// replaceBinary and startBinary are platform-specific and defined in
// update_unix.go / update_windows.go".  The stubs below ensure calls
// compile on both platforms.  If neither platform file is present these
// will panic at runtime (which should never happen in a normal build).

func replaceBinary(tmpPath, exe string) error {
	return replacePlatformBinary(tmpPath, exe)
}

func startBinary(exe string) error {
	return startPlatformBinary(exe)
}
