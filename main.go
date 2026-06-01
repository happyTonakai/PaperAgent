package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/feishu"
	"github.com/happyTonakai/paperagent/internal/server"
	"github.com/happyTonakai/paperagent/internal/systray"
)

// version is set via ldflags at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

var versionFlag = flag.Bool("version", false, "Print version and exit")
var daemonFlag = flag.Bool("daemon", false, "internal: already running as background daemon")

func main() {
	flag.Parse()

	if *versionFlag {
		fmt.Printf("paperagent %s\n", version)
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if cfg.API.APIKey == "" || cfg.API.APIKey == "${OPENAI_API_KEY}" {
		fmt.Fprintln(os.Stderr, "Warning: No API key configured.")
		fmt.Fprintln(os.Stderr, "Open the Web UI settings page or run:")
		fmt.Fprintln(os.Stderr, "  export OPENAI_API_KEY=your-key-here")
	}

	os.MkdirAll(config.PapersDir(), 0755)
	os.MkdirAll(config.PromptsDir(), 0755)

	if !*daemonFlag {
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

	url := fmt.Sprintf("http://localhost:%d", actualPort)
	log.Printf("PaperAgent server starting on %s", url)

	// Start HTTP server in background goroutine
	httpErrCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			httpErrCh <- err
		}
	}()

	// Monitor HTTP server error and shut down gracefully
	go func() {
		if err := <-httpErrCh; err != nil {
			fmt.Fprintf(os.Stderr, "\nServer error: %v\n", err)
			systray.Quit()
		}
	}()

	// Auto-open browser (skip when PAPER_NO_BROWSER is set, e.g. in dev mode)
	if os.Getenv("PAPER_NO_BROWSER") == "" {
		go openBrowser(url)
	}

	// Start Feishu bot if enabled
	feishuBot := feishu.New(cfg)
	s.SetFeishuBot(feishuBot)
	if err := feishuBot.Start(); err != nil {
		log.Printf("Feishu bot start failed: %v", err)
	} else {
		defer feishuBot.Stop()
	}

	// Run systray (blocks until user quits)
	systray.Run(systray.Options{Port: actualPort, Version: version}, httpServer)

	// After systray returns (either from Quit menu or signal), do final cleanup.
	// If we exited due to an HTTP error, propagate it.
	select {
	case err := <-httpErrCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Exiting due to server error: %v\n", err)
			os.Exit(1)
		}
	default:
	}
}

// parsePortFromAddr extracts the port number from an address string like ":8686" or "localhost:8686".
// Returns 0 if parsing fails.
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

// findAvailablePort tries to listen on baseAddr. If the port is occupied,
// it increments the port number up to 100 times until it finds an open one.
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

// openBrowser opens the given URL in the default browser.
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
