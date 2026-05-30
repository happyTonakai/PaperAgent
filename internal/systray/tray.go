package systray

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"fyne.io/systray"
)

const repoURL = "https://github.com/happyTonakai/PaperAgent"

// Options configures the systray behavior.
type Options struct {
	Port int
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

	mAbout := systray.AddMenuItem("关于 PaperAgent", "AI 论文阅读助手")
	mQuit := systray.AddMenuItem("退出", "退出 PaperAgent")

	go func() {
		for {
			select {
			case <-mAbout.ClickedCh:
				openBrowser(repoURL)
			case <-mQuit.ClickedCh:
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
