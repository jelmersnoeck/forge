package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/config"
	"github.com/jelmersnoeck/forge/internal/credentials"
	"github.com/jelmersnoeck/forge/internal/envutil"
)

func runAgent(args []string) int {
	fs := flag.NewFlagSet("forge agent", flag.ExitOnError)
	port := fs.Int("port", 8080, "HTTP port to listen on (0 for random free port)")
	cwd := fs.String("cwd", ".", "working directory for the agent")
	sessionID := fs.String("session-id", "", "session ID (required)")
	sessionsDir := fs.String("sessions-dir", defaultSessionsDir, "directory for session JSONL files")
	mode := fs.String("mode", "", "agent mode: swe (default), spec, code, review")
	specPath := fs.String("spec", "", "path to spec file (used by coder phase)")
	modelFlag := fs.String("model", "", "model to use (overrides settings)")
	issueURLFlag := fs.String("issue-url", "", "GitHub issue URL for PR linking")
	_ = fs.Parse(args[1:])

	if err := os.Chdir(*cwd); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: chdir %s: %v\n", *cwd, err)
		os.Exit(1)
	}
	envutil.LoadEnv(*cwd)

	// Install the credential resolver from user config before any provider
	// construction. Defaults to env-only when [credentials] is unset.
	if userCfg, err := config.LoadUserConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: load user config for credentials: %v\n", err)
	} else {
		credentials.SetDefault(credentials.Resolve(userCfg.Credentials.Sources))
	}

	if warning := agent.StartupKeyWarning(); warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "fatal: --session-id is required")
		os.Exit(1)
	}

	// Default to SWE mode (spec → code → review) when no mode is specified.
	agentMode := *mode
	if agentMode == "" {
		agentMode = "swe"
	}

	cfg := agent.Config{
		Port:        *port,
		CWD:         *cwd,
		SessionID:   *sessionID,
		SessionsDir: *sessionsDir,
		Mode:        agentMode,
		SpecPath:    *specPath,
		Model:       *modelFlag,
		IssueURL:    *issueURLFlag,
	}

	if err := agent.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		return 1
	}

	return 0
}
