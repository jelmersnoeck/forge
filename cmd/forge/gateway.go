package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/envutil"
	"github.com/jelmersnoeck/forge/internal/server/backend"
	"github.com/jelmersnoeck/forge/internal/server/gateway"
)

func runGateway(args []string) int {
	fs := flag.NewFlagSet("forge gateway", flag.ExitOnError)
	daemon := fs.Bool("daemon", false, "run in background and write PID file")
	pidFile := fs.String("pid-file", "", "path to PID file (default: $FORGE_RUN_DIR/forge.pid or $SESSIONS_DIR/forge.pid)")
	logFile := fs.String("log-file", "", "path to log file (default: $FORGE_RUN_DIR/forge.log or $SESSIONS_DIR/forge.log)")
	_ = fs.Parse(args[1:])

	envutil.LoadEnv(".")

	port := envInt("GATEWAY_PORT", 3000)
	host := envStr("GATEWAY_HOST", "0.0.0.0")
	workspaceDir := envStr("WORKSPACE_DIR", "/tmp/forge/workspace")
	sessionsDir := envStr("SESSIONS_DIR", defaultSessionsDir)
	forgeBin := envStr("FORGE_BIN", "forge")

	// Resolve PID/log file defaults: -flag > FORGE_RUN_DIR > SESSIONS_DIR
	resolvedPidFile := resolveDaemonPath(*pidFile, "forge.pid", sessionsDir)
	resolvedLogFile := resolveDaemonPath(*logFile, "forge.log", sessionsDir)

	// Handle daemon mode by re-executing in background
	if *daemon {
		if err := daemonize(args, resolvedPidFile, resolvedLogFile); err != nil {
			log.Printf("daemon error: %v", err)
			return 1
		}
		return 0
	}

	_ = os.MkdirAll(workspaceDir, 0o755)
	_ = os.MkdirAll(sessionsDir, 0o755)

	// Write PID file if -pid-file was provided (foreground mode) or if
	// we were spawned as a daemon child.
	if *pidFile != "" || os.Getenv("FORGE_DAEMON_CHILD") == "1" {
		if err := writePIDFile(resolvedPidFile); err != nil {
			log.Fatalf("failed to write PID file: %v", err)
		}
		defer func() { _ = os.Remove(resolvedPidFile) }()
	}

	gatewayID := uuid.New().String()[:8]
	be := backend.NewTmux(forgeBin, gatewayID, workspaceDir)

	// Clean up agent sessions on shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("received %s, stopping agents...", sig)
		_ = be.Close()
		if *pidFile != "" || os.Getenv("FORGE_DAEMON_CHILD") == "1" {
			_ = os.Remove(resolvedPidFile)
		}
		os.Exit(0)
	}()

	log.Println("forge gateway starting...")
	log.Printf("  gateway id: %s", gatewayID)
	log.Printf("  workspace: %s", workspaceDir)
	log.Printf("  sessions:  %s", sessionsDir)
	log.Printf("  forge bin: %s", forgeBin)

	cfg := gateway.Config{
		Port:         port,
		Host:         host,
		WorkspaceDir: workspaceDir,
		SessionsDir:  sessionsDir,
		Backend:      be,
	}

	if err := gateway.Start(cfg); err != nil {
		_ = be.Close()
		log.Printf("fatal: %v", err)
		return 1
	}

	return 0
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// resolveDaemonPath resolves a daemon artifact path (PID or log file).
// Priority: explicit flag value > $FORGE_RUN_DIR/<name> > <sessionsDir>/<name>.
func resolveDaemonPath(flagValue, filename, sessionsDir string) string {
	if flagValue != "" {
		return flagValue
	}
	if runDir := os.Getenv("FORGE_RUN_DIR"); runDir != "" {
		return filepath.Join(runDir, filename)
	}
	return filepath.Join(sessionsDir, filename)
}

// buildChildArgs builds the argument list for the daemon child process.
// It takes the original args (e.g. ["gateway", "-daemon", "-port", "4000"])
// and strips the "-daemon" flag to prevent infinite forking.
func buildChildArgs(args []string) []string {
	var child []string
	for _, a := range args {
		if a == "-daemon" || a == "--daemon" {
			continue
		}
		child = append(child, a)
	}
	return child
}

// checkPIDFile checks whether a PID file already exists and whether the
// process it references is still alive. Returns nil if no PID file exists
// or the referenced process is dead (stale file removed). Returns an error
// if a live process is found.
func checkPIDFile(pidFile string) error {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no PID file — proceed
		}
		return fmt.Errorf("reading PID file %s: %w", pidFile, err)
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		// Corrupt PID file — remove and proceed
		_ = os.Remove(pidFile)
		return nil
	}

	// Check if process is alive (signal 0 doesn't kill, just checks)
	if err := syscall.Kill(pid, 0); err == nil {
		return fmt.Errorf("gateway already running (pid %d, see %s)", pid, pidFile)
	}

	// Process is dead — stale PID file
	_ = os.Remove(pidFile)
	return nil
}

// daemonize forks the process into the background and exits the parent.
// The child process is launched with the same arguments minus -daemon,
// so it runs runGateway in foreground mode.
func daemonize(args []string, pidFile, logFile string) error {
	// Check for existing live process
	if err := checkPIDFile(pidFile); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Ensure parent directories exist
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		return fmt.Errorf("failed to create PID directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Open log file for child process
	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer func() { _ = lf.Close() }()

	// Re-execute with the same args minus -daemon.
	// The child sees "gateway -pid-file ... -log-file ..." and runs in foreground.
	childArgs := buildChildArgs(args)
	// Ensure -pid-file and -log-file are passed so the child writes them
	childArgs = appendIfMissing(childArgs, "-pid-file", pidFile)
	childArgs = appendIfMissing(childArgs, "-log-file", logFile)

	cmd := exec.Command(exe, childArgs...)
	cmd.Env = append(os.Environ(), "FORGE_DAEMON_CHILD=1")
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	fmt.Printf("forge gateway started in background (PID: %d)\n", cmd.Process.Pid)
	fmt.Printf("  log file: %s\n", logFile)
	fmt.Printf("  pid file: %s\n", pidFile)
	fmt.Printf("\nTo stop:\n  kill $(cat %s)\n", pidFile)

	return nil
}

// appendIfMissing appends "-flag value" to args if flag is not already present.
func appendIfMissing(args []string, flag, value string) []string {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return args
		}
	}
	return append(args, flag, value)
}

// writePIDFile writes the current process ID to a file.
func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating PID directory: %w", err)
	}
	pid := os.Getpid()
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0o644)
}
