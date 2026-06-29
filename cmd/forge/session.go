package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

func createSession(gatewayURL, cwd string) (string, error) {
	body, _ := json.Marshal(map[string]string{"cwd": cwd})
	resp, err := http.Post(gatewayURL+"/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.SessionID, nil
}

// isDefaultBranch returns true for branches that should trigger ephemeral
// worktree mode rather than branch-reuse mode (main, master, HEAD/detached).
func isDefaultBranch(branch string) bool {
	switch branch {
	case "main", "master", "HEAD":
		return true
	}
	return false
}

// isInWorktree checks if the current directory is inside a git worktree.
// Returns false if not in a git repo or if in the main repo.
func isInWorktree(dir string) bool {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	// In a worktree, .git is a file (pointing to the real git dir)
	// In the main repo, .git is a directory
	return !info.IsDir()
}

// spawnLocalAgent starts a forge agent subprocess and returns (sessionID, serverURL, worktreePath, worktreeBranch, cleanup, error).
// The agent runs on a random port and auto-terminates when cleanup is called.
// If skipWorktree is false and in a git repo (and not already in a worktree), creates a temporary worktree for the session.
// If branchName is set, reuses an existing worktree for that branch or creates one.
// initialPrompt, when non-empty, is used to generate a human-readable session name via Haiku.
func spawnLocalAgent(cwd string, skipWorktree bool, branchName string, initialPrompt string, mode string, specPath string, modelName string, namingHint string, issueNum int, issueURL string) (string, string, string, string, func(), error) {
	// Find forge binary (prefer same dir as CLI, fallback to PATH)
	forgeBin := "forge"
	if exe, err := os.Executable(); err == nil {
		// If we're already the forge binary, use ourselves
		if filepath.Base(exe) == "forge" || strings.HasPrefix(filepath.Base(exe), "forge.") {
			forgeBin = exe
		} else {
			// Look for forge in same directory
			candidate := filepath.Join(filepath.Dir(exe), "forge")
			if _, err := os.Stat(candidate); err == nil {
				forgeBin = candidate
			}
		}
	}

	// Generate session ID with a readable name.
	// If we have a naming hint (e.g. issue title), prefer that for a short slug.
	// Otherwise use the full initial prompt. Falls back to random adjective-noun.
	nameSource := namingHint
	if nameSource == "" {
		nameSource = initialPrompt
	}
	slug := generateSessionName(newLightweightProvider(), nameSource)
	// Inject issue number into the session ID for branch traceability.
	// Result: "20260628-42-fix-auth-timeout" instead of "20260628-fix-auth-timeout".
	// Only when --branch is not explicitly set (the user's branch name takes precedence).
	datePart := time.Now().Format("20060102")
	sessionID := datePart + "-" + slug
	if issueNum > 0 && branchName == "" {
		sessionID = fmt.Sprintf("%s-%d-%s", datePart, issueNum, slug)
	}

	// Check if we're in a git repo and should create a worktree
	var worktreePath string
	var worktreeBranch string
	var repoRoot string
	worktreeBase := filepath.Join(os.TempDir(), "forge", "worktrees")

	// explicitBranch tracks whether --branch was passed by the user (reuse ok)
	// vs auto-detected from the current checkout (always fresh worktree).
	explicitBranch := branchName != ""

	// Auto-detect branch: if no --branch flag, not skipping worktrees, and not
	// already in a worktree, check the current branch. If it's a feature branch
	// (not main/master/HEAD), create a fresh worktree branched off it instead
	// of an ephemeral one from HEAD.
	var detectedBranch string
	if branchName == "" && !skipWorktree && !isInWorktree(cwd) {
		if root := findRepoRoot(cwd); root != "" {
			cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
			cmd.Dir = root
			if out, err := cmd.Output(); err == nil {
				detected := strings.TrimSpace(string(out))
				if !isDefaultBranch(detected) {
					detectedBranch = detected
					fmt.Fprintln(os.Stderr, dimStyle.Render("  detected branch: "+detectedBranch))
				}
			}
		}
	}

	if explicitBranch {
		// Explicit --branch: find or create worktree for the named branch.
		// Reuses existing worktrees (intentional resume).
		repoRoot = findRepoRoot(cwd)
		if repoRoot == "" {
			return "", "", "", "", nil, fmt.Errorf("not in a git repo")
		}

		wtPath, err := findWorktreeForBranch(repoRoot, branchName)
		if err != nil {
			return "", "", "", "", nil, fmt.Errorf("listing worktrees: %w", err)
		}

		if wtPath != "" {
			worktreePath = wtPath
			worktreeBranch = branchName
			cwd = worktreePath
			if info, err := readSessionFile(wtPath); err == nil {
				sessionID = info.SessionID
				fmt.Fprintln(os.Stderr, dimStyle.Render("  resuming session: "+sessionID))

				// Warn if session JSONL is missing (conversation history lost)
				if jsonlPath, err := sessionFilePath(sessionID); err == nil {
					switch _, err := os.Stat(jsonlPath); {
					case os.IsNotExist(err):
						fmt.Fprintln(os.Stderr, errorStyle.Render("  warning: session history unavailable, conversation will start fresh"))
					case err != nil:
						fmt.Fprintf(os.Stderr, "  warning: could not check session history: %v\n", err)
					}
				}

				// Warn if the persisted routing state predates the current
				// worktree HEAD — the agent will resume conversation history
				// that no longer matches the working tree (issue #211).
				warnIfStateStale(wtPath)
			}
			fmt.Fprintln(os.Stderr, dimStyle.Render("  reusing worktree: "+worktreePath))
			fmt.Fprintln(os.Stderr, dimStyle.Render("  branch: "+branchName))
		} else {
			// No existing worktree — create one
			worktreePath = filepath.Join(worktreeBase, sessionID)
			if err := os.MkdirAll(worktreeBase, 0o755); err != nil {
				return "", "", "", "", nil, fmt.Errorf("create worktree dir: %w", err)
			}

			// Try checking out existing branch first; if that fails, create it
			cmd := exec.Command("git", "worktree", "add", worktreePath, branchName)
			cmd.Dir = repoRoot
			if out, err := cmd.CombinedOutput(); err != nil {
				cmd = exec.Command("git", "worktree", "add", "-b", branchName, worktreePath, "HEAD")
				cmd.Dir = repoRoot
				if out2, err2 := cmd.CombinedOutput(); err2 != nil {
					return "", "", "", "", nil, fmt.Errorf("git worktree add: %s\n%s", err, string(append(out, out2...)))
				}
			}

			worktreeBranch = branchName
			cwd = worktreePath
			fmt.Fprintln(os.Stderr, dimStyle.Render("  created worktree: "+worktreePath))
			fmt.Fprintln(os.Stderr, dimStyle.Render("  branch: "+branchName))
		}
	} else if !skipWorktree && !isInWorktree(cwd) {
		// Fresh worktree mode: create a new branch from either the detected
		// feature branch or the current branch.
		repoRoot = findRepoRoot(cwd)
		if repoRoot != "" {
			baseBranch := detectedBranch
			if baseBranch == "" {
				cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
				cmd.Dir = repoRoot
				if branchOut, err := cmd.Output(); err == nil {
					baseBranch = strings.TrimSpace(string(branchOut))
				}
			}

			if baseBranch != "" {
				worktreePath = filepath.Join(worktreeBase, sessionID)
				if err := os.MkdirAll(worktreeBase, 0o755); err == nil {
					newBranch := fmt.Sprintf("jelmer/%s", sessionID)
					cmd := exec.Command("git", "worktree", "add", "-b", newBranch, worktreePath, baseBranch)
					cmd.Dir = repoRoot
					if err := cmd.Run(); err == nil {
						worktreeBranch = newBranch
						fmt.Fprintln(os.Stderr, dimStyle.Render("  created worktree: "+worktreePath))
						fmt.Fprintln(os.Stderr, dimStyle.Render("  branch: "+newBranch+" (from "+baseBranch+")"))
						cwd = worktreePath
					}
				}
			}
		}
	}

	// Write .forge-session metadata for resume
	if worktreePath != "" && repoRoot != "" {
		_ = writeSessionFile(worktreePath, SessionInfo{
			SessionID: sessionID,
			Branch:    worktreeBranch,
			RepoRoot:  repoRoot,
			CreatedAt: time.Now(),
		})
	}

	// Spawn agent subcommand on random port (0 = OS picks)
	agentArgs := []string{"agent",
		"--port", "0",
		"--cwd", cwd,
		"--session-id", sessionID,
	}
	if mode != "" {
		agentArgs = append(agentArgs, "--mode", mode)
	}
	if specPath != "" {
		agentArgs = append(agentArgs, "--spec", specPath)
	}
	if modelName != "" {
		agentArgs = append(agentArgs, "--model", modelName)
	}
	if issueURL != "" {
		agentArgs = append(agentArgs, "--issue-url", issueURL)
	}
	cmd := exec.Command(forgeBin, agentArgs...)

	// Capture stdout to read the port
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", "", "", nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Send stderr to /dev/null (agent logs are noise in interactive mode)
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return "", "", "", "", nil, fmt.Errorf("start agent: %w", err)
	}

	// Read port from first line of stdout (JSON: {"port": 12345})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		_ = cmd.Process.Kill()
		return "", "", "", "", nil, fmt.Errorf("agent did not emit port")
	}

	var portMsg struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &portMsg); err != nil {
		_ = cmd.Process.Kill()
		return "", "", "", "", nil, fmt.Errorf("parse agent port: %w", err)
	}

	serverURL := fmt.Sprintf("http://localhost:%d", portMsg.Port)

	// Wait for agent to be ready (health check with retries)
	ready := false
	for i := 0; i < 10; i++ {
		resp, err := http.Get(serverURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !ready {
		_ = cmd.Process.Kill()
		return "", "", "", "", nil, fmt.Errorf("agent did not become healthy")
	}

	// Track whether cleanup has been called to avoid double-cleanup
	var cleanupCalled bool
	var cleanupMutex sync.Mutex

	cleanup := func() {
		cleanupMutex.Lock()
		defer cleanupMutex.Unlock()

		if cleanupCalled {
			return
		}
		cleanupCalled = true

		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}

		// Print resume hint if worktree is preserved
		if worktreePath != "" && worktreeBranch != "" {
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, dimStyle.Render("  worktree preserved: "+worktreePath))
			fmt.Fprintln(os.Stderr, dimStyle.Render("  resume: forge --branch "+worktreeBranch))
		}
	}

	return sessionID, serverURL, worktreePath, worktreeBranch, cleanup, nil
}

// findRepoRoot returns the git repository root for the given directory, or ""
// if the directory is not inside a git repo.
func findRepoRoot(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// findWorktreeForBranch parses `git worktree list --porcelain` and returns the
// worktree path whose checked-out branch matches the given name, or "" if none.
//
//	worktree /tmp/forge/worktrees/cli-20260406-183659
//	HEAD abc123
//	branch refs/heads/jelmer/cli-20260406-183659
//	<blank line>
func findWorktreeForBranch(repoRoot, branch string) (string, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	target := "refs/heads/" + branch
	var currentPath string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			currentPath = strings.TrimPrefix(line, "worktree ")
		case strings.TrimSpace(line) == "":
			currentPath = ""
		case line == "branch "+target:
			return currentPath, nil
		}
	}
	return "", nil
}

// prURLRe matches GitHub pull request URLs in text.
var prURLRe = regexp.MustCompile(`https://github\.com/[^\s/]+/[^\s/]+/pull/\d+`)

// extractPRURL finds the first GitHub PR URL in text, or returns "".
func extractPRURL(text string) string {
	return prURLRe.FindString(text)
}

// detectCurrentPR checks if the current branch has an open PR on GitHub.
// Returns the PR URL or "" if none found (no gh, no repo, no PR — all silent).
func detectCurrentPR(cwd string) string {
	cmd := exec.Command("gh", "pr", "view", "--json", "url", "--jq", ".url")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
