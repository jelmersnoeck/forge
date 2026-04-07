package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/envutil"
)

func runAgent(args []string) int {
	fs := flag.NewFlagSet("forge agent", flag.ExitOnError)
	port := fs.Int("port", 8080, "HTTP port to listen on (0 for random free port)")
	cwd := fs.String("cwd", ".", "working directory for the agent")
	sessionID := fs.String("session-id", "", "session ID (required)")
	sessionsDir := fs.String("sessions-dir", "/tmp/forge/sessions", "directory for session JSONL files")
	_ = fs.Parse(args[1:])

	if err := os.Chdir(*cwd); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: chdir %s: %v\n", *cwd, err)
		os.Exit(1)
	}
	envutil.LoadEnv(*cwd)

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
