// Package attribution handles autonomous-agent attribution on commits and PRs.
//
// It provides:
//   - A prepare-commit-msg git hook that appends Co-authored-by and Generated-by trailers
//   - Environment variable helpers for the hook
//   - PR body attribution prefix
package attribution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

ARGS=""

if [ -n "$FORGE_COAUTHOR" ]; then
  ARGS="$ARGS --trailer \"Co-authored-by: $FORGE_COAUTHOR\""
fi

if [ -n "$FORGE_GENERATED_BY" ]; then
  ARGS="$ARGS --trailer \"Generated-by: $FORGE_GENERATED_BY session=$FORGE_SESSION_ID\""
fi

if [ -n "$ARGS" ]; then
  eval git interpret-trailers --in-place $ARGS "$COMMIT_MSG_FILE"
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
			return fmt.Errorf("prepare-commit-msg backup already exists at %s — not overwriting", backupPath)
		}
		// Back up the existing hook.
		if err := os.Rename(hookPath, backupPath); err != nil {
			return fmt.Errorf("backup existing hook: %w", err)
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

	if cfg.CoAuthor != "" {
		envs = append(envs, "FORGE_COAUTHOR="+cfg.CoAuthor)
	}

	generatedBy := cfg.Attribution.GeneratedBy
	if generatedBy == "" {
		generatedBy = "forge"
	}
	envs = append(envs, "FORGE_GENERATED_BY="+generatedBy)

	return envs
}

// PrependAttribution prepends an attribution block to a PR body.
// Returns body unchanged if enabled is false.
func PrependAttribution(body, sessionID, coAuthor string, enabled bool) string {
	if !enabled {
		return body
	}

	var b strings.Builder
	b.WriteString("> 🤖 This PR was opened by an autonomous Forge session.\n")
	b.WriteString(fmt.Sprintf("> Session: `%s`\n", sessionID))
	if coAuthor != "" {
		b.WriteString(fmt.Sprintf("> Co-authored by: %s\n", coAuthor))
	}
	b.WriteString("\n---\n\n")
	b.WriteString(body)

	return b.String()
}
