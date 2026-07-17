//go:build !windows

package update

import (
	"os"
	"os/exec"
)

func replacePlatformBinary(tmpPath, exe string) error {
	return os.Rename(tmpPath, exe)
}

func startPlatformBinary(exe string) error {
	cmd := exec.Command(exe)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
