// Package attribution handles autonomous-agent attribution on commits and PRs.
//
// It provides:
//   - A prepare-commit-msg git hook that appends Co-authored-by and Generated-by trailers
//   - Environment variable helpers for the hook
//   - PR body attribution prefix
package attribution

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CommitConfig holds commit attribution settings.
type CommitConfig struct {
	CoAuthor    string            // "Name <email>"
	Attribution AttributionConfig // Generated-by control
}

// AttributionConfig controls the Generated-by trailer.
type AttributionConfig struct {
	Enabled     bool   // default true — master switch for all trailers
	GeneratedBy string // default "forge"
}

// PRConfig holds PR attribution settings.
type PRConfig struct {
	Attribution AttributionConfig
}

// hookMarker identifies Forge-installed hooks for safe removal.
const hookMarker = "# FORGE-ATTRIBUTION-HOOK"

// hookScript is the prepare-commit-msg hook content.
// It reads FORGE_SESSION_ID, FORGE_COAUTHOR, and FORGE_GENERATED_BY from env
// and appends trailers via git interpret-trailers.
const hookScript = `#!/bin/sh
` + hookMarker + `
# Installed by Forge — do not edit. Restored on session teardown.
#
# Appends Co-authored-by and Generated-by trailers to commit messages
# when running inside a Forge session.

COMMIT_MSG_FILE="$1"

# Guard: only run inside a Forge session.
if [ -z "$FORGE_SESSION_ID" ]; then
  exit 0
fi

if [ -n "$FORGE_COAUTHOR" ]; then
  git interpret-trailers --in-place --trailer "Co-authored-by: $FORGE_COAUTHOR" "$COMMIT_MSG_FILE"
fi

if [ -n "$FORGE_GENERATED_BY" ]; then
  git interpret-trailers --in-place --trailer "Generated-by: $FORGE_GENERATED_BY session=$FORGE_SESSION_ID" "$COMMIT_MSG_FILE"
fi
`

// InstallCommitHook writes a prepare-commit-msg hook into the worktree's
// .git/hooks dir. The hook reads FORGE_SESSION_ID, FORGE_COAUTHOR, and
// FORGE_GENERATED_BY env vars and appends trailers to the commit message.
func InstallCommitHook(worktreeDir string) error {
	hooksDir := filepath.Join(worktreeDir, ".git", "hooks")
	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	backupPath := filepath.Join(hooksDir, "prepare-commit-msg.forge-backup")

	// Ensure hooks directory exists.
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	// Check for existing hook.
	if _, err := os.Stat(hookPath); err == nil {
		// Hook exists — check if backup already exists.
		if _, err := os.Stat(backupPath); err == nil {
			// Backup already exists. The original user hook is already saved.
			// Just overwrite the current hook with ours — whether it's a stale
			// forge hook or a user hook installed after our backup was created.
		} else {
			// No backup yet — back up the existing hook.
			if err := os.Rename(hookPath, backupPath); err != nil {
				return fmt.Errorf("backup existing hook: %w", err)
			}
		}
	}

	// Write the Forge hook.
	if err := os.WriteFile(hookPath, []byte(hookScript), 0o755); err != nil {
		return fmt.Errorf("write hook: %w", err)
	}

	return nil
}

// RemoveCommitHook removes the Forge prepare-commit-msg hook and restores
// any backed-up user hook.
func RemoveCommitHook(worktreeDir string) error {
	hooksDir := filepath.Join(worktreeDir, ".git", "hooks")
	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	backupPath := filepath.Join(hooksDir, "prepare-commit-msg.forge-backup")

	// Only remove if it's our hook.
	content, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to remove
		}
		return fmt.Errorf("read hook: %w", err)
	}

	if !strings.Contains(string(content), hookMarker) {
		return nil // not our hook — leave it alone
	}

	// Remove the Forge hook.
	if err := os.Remove(hookPath); err != nil {
		return fmt.Errorf("remove hook: %w", err)
	}

	// Restore backup if it exists.
	if _, err := os.Stat(backupPath); err == nil {
		if err := os.Rename(backupPath, hookPath); err != nil {
			return fmt.Errorf("restore backup hook: %w", err)
		}
	}

	return nil
}

// EnvForCommit returns the env vars to inject when spawning the agent's
// shell tool so the hook picks them up. Returns nil if attribution is disabled.
func EnvForCommit(sessionID string, cfg CommitConfig) []string {
	if !cfg.Attribution.Enabled {
		return nil
	}

	var envs []string
	envs = append(envs, "FORGE_SESSION_ID="+sessionID)

	coAuthor := cfg.CoAuthor
	if coAuthor == "" {
		coAuthor = "Forge <forge@noreply.invalid>"
	}
	envs = append(envs, "FORGE_COAUTHOR="+coAuthor)

	generatedBy := cfg.Attribution.GeneratedBy
	if generatedBy == "" {
		generatedBy = "forge"
	}
	envs = append(envs, "FORGE_GENERATED_BY="+generatedBy)

	return envs
}

// PrependAttribution prepends an attribution block to a PR body.
// Returns body unchanged if enabled is false.
//
// The block format (from spec):
//
//	> 🤖 This PR was opened by a Forge session acting on behalf of @<author>.
//	> Session: `<session-id>`
//	> Co-authored by: <coAuthor>
//
// @<author> is resolved from the GitHub login associated with the commit
// author email (best-effort; falls back to the bare email).
func PrependAttribution(body, sessionID, coAuthor string, enabled bool) string {
	if !enabled {
		return body
	}

	author := resolveAuthor()

	var b strings.Builder
	fmt.Fprintf(&b, "> 🤖 This PR was opened by a Forge session acting on behalf of @%s.\n", author)
	fmt.Fprintf(&b, "> Session: `%s`\n", sessionID)
	if coAuthor != "" {
		fmt.Fprintf(&b, "> Co-authored by: %s\n", coAuthor)
	}
	b.WriteString("\n---\n\n")
	b.WriteString(body)

	return b.String()
}

// resolveAuthor attempts to resolve the current git user's GitHub login.
// Falls back to git user.email, then "unknown".
func resolveAuthor() string {
	// Get the git author email.
	email := gitConfigValue("user.email")
	if email == "" {
		return "unknown"
	}

	// Best-effort: try to resolve GitHub login via gh api.
	if login := ghLoginForEmail(email); login != "" {
		return login
	}

	return email
}

// gitConfigValue returns a git config value, or "" on error.
func gitConfigValue(key string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "config", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ghLoginForEmail attempts to resolve a GitHub login from an email address
// using `gh api /search/users`. Returns "" if not found or on error.
//
// Retries once (2 attempts total, 1s delay) to handle transient failures.
// Each attempt has a 5s timeout. Failures are logged for diagnostics.
func ghLoginForEmail(email string) string {
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}

	const maxAttempts = 2
	const retryDelay = 1 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		out, err := exec.CommandContext(ctx, "gh", "api",
			fmt.Sprintf("/search/users?q=%s+in:email", email),
			"--jq", ".items[0].login",
		).Output()
		cancel()

		if err != nil {
			log.Printf("attribution: ghLoginForEmail(%q) attempt %d/%d failed: %v", email, attempt, maxAttempts, err)
			if attempt < maxAttempts {
				time.Sleep(retryDelay)
			}
			continue
		}

		login := strings.TrimSpace(string(out))
		if login == "" || login == "null" {
			log.Printf("attribution: ghLoginForEmail(%q) attempt %d/%d returned empty/null login", email, attempt, maxAttempts)
			return ""
		}
		return login
	}

	log.Printf("attribution: ghLoginForEmail(%q) failed after %d attempts, falling back", email, maxAttempts)
	return ""
}
