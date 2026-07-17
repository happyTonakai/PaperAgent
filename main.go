package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/feishu"
	"github.com/happyTonakai/paperagent/internal/server"
	"github.com/happyTonakai/paperagent/internal/systray"
	"github.com/happyTonakai/paperagent/internal/update"
)

// version is set via ldflags at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	// Handle subcommands before flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "update" {
		update.Run(version)
		return
	}

	var showVersion bool
	pflag.BoolVar(&showVersion, "version", false, "Print version and exit")
	pflag.Parse()

	if showVersion {
		fmt.Printf("paperagent %s\n", version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		if !strings.Contains(err.Error(), "env var not set") &&
			!strings.Contains(err.Error(), "unresolved env vars") {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "⚠️  Config warning: %v\n", err)
		fmt.Fprintln(os.Stderr, "   API calls will fail until the keys are configured.")
		fmt.Fprintln(os.Stderr, "   Open the Web UI settings page, or set the environment variables.")
	}

	if cfg.API.APIKey == "" {
		fmt.Fprintln(os.Stderr, "Warning: No API key configured.")
		fmt.Fprintln(os.Stderr, "Open the Web UI settings page or run:")
		fmt.Fprintln(os.Stderr, "  export OPENAI_API_KEY=your-key-here")
	}

	os.MkdirAll(config.PapersDir(), 0755)
	os.MkdirAll(config.PromptsDir(), 0755)

	// daemonize() is a no-op on Windows (process lifecycle managed by systray).
	// It is also skipped when PAPER_DAEMONIZED=1 (child process already forked).
	if os.Getenv("PAPER_DAEMONIZED") == "" {
		daemonize()
	}

	runSystray(cfg)
}

func runSystray(cfg *config.Config) {
	s := server.New(cfg)

	startPort := 8686
	if v := os.Getenv("PAPER_ADDR"); v != "" {
		if p := parsePortFromAddr(v); p > 0 {
			startPort = p
		}
	}

	ln, actualPort, err := findAvailablePort(fmt.Sprintf(":%d", startPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to find available port: %v\n", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Handler: s.Handler(),
	}

	// Wire up the /api/shutdown endpoint so an update process can request
	// a graceful shutdown of the running daemon.
	// We only call systray.Quit here — the actual httpServer.Shutdown is
	// handled by systray's onExit callback, which runs when the fyne event
	// loop returns.  Having both paths call Shutdown would be harmless but
	// redundant: the second call would return http.ErrServerClosed silently.
	s.ShutdownFunc = func() {
		systray.Quit()
	}

	url := fmt.Sprintf("http://localhost:%d", actualPort)
	log.Printf("PaperAgent server starting on %s", url)

	httpErrCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			httpErrCh <- err
		}
	}()

	go func() {
		if err := <-httpErrCh; err != nil {
			fmt.Fprintf(os.Stderr, "\nServer error: %v\n", err)
			systray.Quit()
		}
	}()

	if os.Getenv("PAPER_NO_BROWSER") == "" {
		go openBrowser(url)
	}

	feishuBot := feishu.New(cfg)
	s.SetFeishuBot(feishuBot)
	if err := feishuBot.Start(); err != nil {
		log.Printf("Feishu bot start failed: %v", err)
	} else {
		defer feishuBot.Stop()
	}

	systray.Run(systray.Options{Port: actualPort, Version: version}, httpServer)

	select {
	case err := <-httpErrCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Exiting due to server error: %v\n", err)
			os.Exit(1)
		}
	default:
	}
}

func parsePortFromAddr(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

func findAvailablePort(baseAddr string) (net.Listener, int, error) {
	host, portStr, err := net.SplitHostPort(baseAddr)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid address %q: %w", baseAddr, err)
	}

	startPort, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid port in %q: %w", baseAddr, err)
	}

	for port := startPort; port < startPort+100; port++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		ln, listenErr := net.Listen("tcp", addr)
		if listenErr == nil {
			return ln, port, nil
		}
	}

	return nil, 0, fmt.Errorf("no available port found starting from %d", startPort)
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
