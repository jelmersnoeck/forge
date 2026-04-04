package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jelmersnoeck/forge/internal/agent"
)

func runAgent(args []string) int {
	fs := flag.NewFlagSet("forge agent", flag.ExitOnError)
	port := fs.Int("port", 8080, "HTTP port to listen on (0 for random free port)")
	cwd := fs.String("cwd", ".", "working directory for the agent")
	sessionID := fs.String("session-id", "", "session ID (required)")
	sessionsDir := fs.String("sessions-dir", "/tmp/forge/sessions", "directory for session JSONL files")
	_ = fs.Parse(args[1:])

	// Change to the working directory and load .env from there.
	if err := os.Chdir(*cwd); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: chdir %s: %v\n", *cwd, err)
		os.Exit(1)
	}
	loadAgentEnv(*cwd)

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "warning: ANTHROPIC_API_KEY not set — agent will start but cannot connect to Anthropic")
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "fatal: --session-id is required")
		os.Exit(1)
	}

	cfg := agent.Config{
		Port:        *port,
		CWD:         *cwd,
		SessionID:   *sessionID,
		SessionsDir: *sessionsDir,
	}

	if err := agent.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		return 1
	}

	return 0
}

// loadAgentEnv reads a .env file from the given directory (falling back to the
// binary's directory). Existing env vars are not overridden.
func loadAgentEnv(dir string) {
	candidates := []string{
		filepath.Join(dir, ".env"),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}

	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer func() { _ = f.Close() }()

		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq < 0 {
				continue
			}
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, val)
			}
		}
		return
	}
}
