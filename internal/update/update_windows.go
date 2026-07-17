//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// replacePlatformBinary uses a detached batch script to replace the binary.
// The current process (which IS paperagent.exe) cannot rename itself while
// running on Windows, so we schedule the swap for a moment after exit.
//
// The batch script:
//  1. Waits 2 s (parent update process should exit during this window).
//  2. Deletes the old paperagent.exe (no longer in use after parent exits).
//  3. Renames .paperagent.update.exe → paperagent.exe.
//  4. Starts the new binary.
func replacePlatformBinary(tmpPath, exe string) error {
	base := filepath.Base(exe) // "paperagent.exe"

	script := fmt.Sprintf(`@echo off
timeout /t 2 /nobreak >nul
del /f /q "%s"
ren "%s" "%s"
start "" "%s"
`, exe, tmpPath, base, exe)

	scriptPath := filepath.Join(os.TempDir(), "paperagent_update.bat")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return fmt.Errorf("failed to write update script: %w", err)
	}

	cmd := exec.Command("cmd", "/c", scriptPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start update script: %w", err)
	}
	fmt.Println("Update script started. The old binary will be replaced in a few seconds.")
	return nil
}

// startPlatformBinary starts the binary without showing a console window.
func startPlatformBinary(exe string) error {
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}
