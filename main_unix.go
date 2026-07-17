//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// daemonize forks a background child process via os/exec and exits the parent.
// The child inherits stdout/stderr (both nil → detached from terminal) and
// runs in a new session (Setsid) so it survives the parent's death.
//
// It is a no-op when PAPER_FOREGROUND=1 (development mode) or when the
// process is already the daemonized child (PAPER_DAEMONIZED=1, set by the
// parent before exec).
//
// On Windows this function is replaced by a no-op (main_windows.go) because
// the systray icon manages the process lifecycle directly and Windows has no
// fork/setsid mechanism.
func daemonize() {
	if os.Getenv("PAPER_FOREGROUND") != "" {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to find executable: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	// Use an env var instead of a --daemon flag. This keeps the public CLI
	// clean (no hidden flags shown in --help) and avoids the need to register
	// daemon with the flag parser.
	cmd.Env = append(os.Environ(), "PAPER_DAEMONIZED=1")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start background process: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("PaperAgent started in background.")
	os.Exit(0)
}
