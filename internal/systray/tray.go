package systray

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"fyne.io/systray"

	"github.com/happyTonakai/paperagent/internal/server"
)

const repoURL = "https://github.com/happyTonakai/PaperAgent"

// Options configures the systray behavior.
type Options struct {
	Port    int
	Version string
}

// Run starts the system tray icon and blocks until the user quits.
// onStart is called once the tray is ready (icon set, menu items added).
// The returned stop function is called when the user selects "Quit".
func Run(opts Options, httpServer *http.Server) {
	systray.Run(
		func() { onReady(opts) },
		func() { onExit(httpServer) },
	)
}

func onReady(opts Options) {
	systray.SetIcon(iconData)
	systray.SetTooltip("PaperAgent - AI 论文阅读助手")

	url := fmt.Sprintf("http://localhost:%d", opts.Port)

	// Left-click opens web UI directly
	systray.SetOnTapped(func() { openBrowser(url) })

	mVersion := systray.AddMenuItem(opts.Version, opts.Version)
	mVersion.Disable()

	mAbout := systray.AddMenuItem("关于", "AI 论文阅读助手")

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("退出", "退出 PaperAgent")

	// Signal handling: gracefully quit on SIGINT (Ctrl+C) or SIGTERM (pkill).
	// This ensures the systray icon is properly removed on forced exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for {
			select {
			case <-mAbout.ClickedCh:
				openBrowser(repoURL)
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			case sig := <-sigCh:
				fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
				systray.Quit()
				return
			}
		}
	}()
}

func onExit(httpServer *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(ctx)
	// Flush the daily log file so the last few lines aren't lost if the
	// user quit via the tray. Best-effort; the OS will close the FD on
	// process exit anyway.
	if s := server.GetActive(); s != nil {
		if err := s.CloseLog(); err != nil {
			fmt.Fprintf(os.Stderr, "close log: %v\n", err)
		}
	}
}

// Quit gracefully stops the systray (called from main.go for HTTP errors).
func Quit() {
	systray.Quit()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	_ = cmd.Start()
}
