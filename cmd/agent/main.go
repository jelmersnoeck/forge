// forge agent — single-session agent process with its own HTTP server,
// conversation loop, and tool execution.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/server/backend"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port to listen on (0 for random free port)")
	cwd := flag.String("cwd", ".", "working directory for the agent")
	sessionID := flag.String("session-id", "", "session ID (required)")
	sessionsDir := flag.String("sessions-dir", "/tmp/forge/sessions", "directory for session JSONL files")
	noWorktree := flag.Bool("no-worktree", false, "disable automatic git worktree isolation (not recommended)")
	flag.Parse()

	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "fatal: --session-id is required")
		os.Exit(1)
	}

	// Resolve absolute path for CWD
	absCwd, err := filepath.Abs(*cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: resolve cwd %s: %v\n", *cwd, err)
		os.Exit(1)
	}

	// Setup git worktree isolation unless explicitly disabled
	worktreePath := absCwd
	var worktreeMgr *backend.WorktreeManager
	
	if !*noWorktree {
		worktreeDir := filepath.Join(filepath.Dir(absCwd), "forge-worktrees")
		worktreeMgr = backend.NewWorktreeManager(absCwd, worktreeDir)
		
		isolatedPath, err := worktreeMgr.EnsureWorktree(*sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fatal: create worktree: %v\n", err)
			os.Exit(1)
		}
		worktreePath = isolatedPath
		
		// Register cleanup on exit
		defer func() {
			if err := worktreeMgr.RemoveWorktree(*sessionID); err != nil {
				fmt.Fprintf(os.Stderr, "warning: cleanup worktree: %v\n", err)
			}
		}()
	}

	// Change to the working directory and load .env from there.
	if err := os.Chdir(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: chdir %s: %v\n", worktreePath, err)
		os.Exit(1)
	}
	loadEnv(worktreePath)

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "warning: ANTHROPIC_API_KEY not set — agent will start but cannot connect to Anthropic")
	}

	cfg := agent.Config{
		Port:        *port,
		CWD:         worktreePath,
		SessionID:   *sessionID,
		SessionsDir: *sessionsDir,
	}

	if err := agent.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// loadEnv reads a .env file from the given directory (falling back to the
// binary's directory). Existing env vars are not overridden.
func loadEnv(dir string) {
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
		defer f.Close()

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
				os.Setenv(key, val)
			}
		}
		return
	}
}
