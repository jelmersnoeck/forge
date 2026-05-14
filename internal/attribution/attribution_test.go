package attribution

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrependAttribution(t *testing.T) {
	tests := map[string]struct {
		body      string
		sessionID string
		coAuthor  string
		enabled   bool
		want      string
	}{
		"enabled with coauthor": {
			body:      "Original PR body here.",
			sessionID: "20260514-greendale",
			coAuthor:  "Jelmer Snoeck <jelmer@siphoc.com>",
			enabled:   true,
			want: "> 🤖 This PR was opened by an autonomous Forge session.\n" +
				"> Session: `20260514-greendale`\n" +
				"> Co-authored by: Jelmer Snoeck <jelmer@siphoc.com>\n" +
				"\n---\n\n" +
				"Original PR body here.",
		},
		"enabled without coauthor": {
			body:      "Some body.",
			sessionID: "20260514-troy",
			coAuthor:  "",
			enabled:   true,
			want: "> 🤖 This PR was opened by an autonomous Forge session.\n" +
				"> Session: `20260514-troy`\n" +
				"\n---\n\n" +
				"Some body.",
		},
		"disabled": {
			body:      "Some body.",
			sessionID: "20260514-troy",
			coAuthor:  "Troy Barnes <troy@greendale.edu>",
			enabled:   false,
			want:      "Some body.",
		},
		"enabled with empty body": {
			body:      "",
			sessionID: "20260514-abed",
			coAuthor:  "",
			enabled:   true,
			want: "> 🤖 This PR was opened by an autonomous Forge session.\n" +
				"> Session: `20260514-abed`\n" +
				"\n---\n\n",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := PrependAttribution(tc.body, tc.sessionID, tc.coAuthor, tc.enabled)
			r.Equal(tc.want, got)
		})
	}
}

func TestInstallCommitHook(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	// Initialize a git repo so .git/hooks exists
	cmd := exec.Command("git", "init", dir)
	r.NoError(cmd.Run())

	r.NoError(InstallCommitHook(dir))

	hookPath := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	info, err := os.Stat(hookPath)
	r.NoError(err)
	r.True(info.Mode().Perm()&0111 != 0, "hook should be executable")

	content, err := os.ReadFile(hookPath)
	r.NoError(err)
	r.Contains(string(content), "FORGE_SESSION_ID")
	r.Contains(string(content), "git interpret-trailers")
}

func TestInstallCommitHook_backsUpExistingHook(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	cmd := exec.Command("git", "init", dir)
	r.NoError(cmd.Run())

	hooksDir := filepath.Join(dir, ".git", "hooks")
	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	backupPath := filepath.Join(hooksDir, "prepare-commit-msg.forge-backup")

	// Write a user hook
	r.NoError(os.WriteFile(hookPath, []byte("#!/bin/sh\necho user hook"), 0o755))

	r.NoError(InstallCommitHook(dir))

	// User hook backed up
	backupContent, err := os.ReadFile(backupPath)
	r.NoError(err)
	r.Contains(string(backupContent), "user hook")

	// Forge hook installed
	content, err := os.ReadFile(hookPath)
	r.NoError(err)
	r.Contains(string(content), "FORGE_SESSION_ID")
}

func TestInstallCommitHook_backupAlreadyExists(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	cmd := exec.Command("git", "init", dir)
	r.NoError(cmd.Run())

	hooksDir := filepath.Join(dir, ".git", "hooks")
	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	backupPath := filepath.Join(hooksDir, "prepare-commit-msg.forge-backup")

	// Write a user hook AND a pre-existing backup
	r.NoError(os.WriteFile(hookPath, []byte("#!/bin/sh\necho second hook"), 0o755))
	r.NoError(os.WriteFile(backupPath, []byte("#!/bin/sh\necho original backup"), 0o755))

	// Should NOT overwrite existing backup — leave user's hook alone
	err := InstallCommitHook(dir)
	r.Error(err)
	r.Contains(err.Error(), "backup already exists")

	// Original backup untouched
	backupContent, err := os.ReadFile(backupPath)
	r.NoError(err)
	r.Contains(string(backupContent), "original backup")

	// User's hook untouched
	hookContent, err := os.ReadFile(hookPath)
	r.NoError(err)
	r.Contains(string(hookContent), "second hook")
}

func TestRemoveCommitHook(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	cmd := exec.Command("git", "init", dir)
	r.NoError(cmd.Run())

	// Install then remove
	r.NoError(InstallCommitHook(dir))
	r.NoError(RemoveCommitHook(dir))

	hookPath := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	_, err := os.Stat(hookPath)
	r.True(os.IsNotExist(err), "hook should be removed")
}

func TestRemoveCommitHook_restoresBackup(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	cmd := exec.Command("git", "init", dir)
	r.NoError(cmd.Run())

	hooksDir := filepath.Join(dir, ".git", "hooks")
	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")

	// Write user hook, install forge hook, then remove
	r.NoError(os.WriteFile(hookPath, []byte("#!/bin/sh\necho user hook"), 0o755))
	r.NoError(InstallCommitHook(dir))
	r.NoError(RemoveCommitHook(dir))

	// User hook restored
	content, err := os.ReadFile(hookPath)
	r.NoError(err)
	r.Contains(string(content), "user hook")

	// Backup removed
	backupPath := filepath.Join(hooksDir, "prepare-commit-msg.forge-backup")
	_, err = os.Stat(backupPath)
	r.True(os.IsNotExist(err), "backup should be cleaned up")
}

func TestEnvForCommit(t *testing.T) {
	tests := map[string]struct {
		sessionID   string
		coAuthor    string
		enabled     bool
		generatedBy string
		wantKeys    []string
		wantAbsent  []string
	}{
		"all set": {
			sessionID:   "20260514-greendale",
			coAuthor:    "Troy Barnes <troy@greendale.edu>",
			enabled:     true,
			generatedBy: "forge",
			wantKeys: []string{
				"FORGE_SESSION_ID=20260514-greendale",
				"FORGE_COAUTHOR=Troy Barnes <troy@greendale.edu>",
				"FORGE_GENERATED_BY=forge",
			},
		},
		"attribution disabled": {
			sessionID:   "20260514-greendale",
			coAuthor:    "Troy Barnes <troy@greendale.edu>",
			enabled:     false,
			generatedBy: "forge",
			wantAbsent:  []string{"FORGE_SESSION_ID", "FORGE_COAUTHOR", "FORGE_GENERATED_BY"},
		},
		"no coauthor": {
			sessionID:   "20260514-greendale",
			coAuthor:    "",
			enabled:     true,
			generatedBy: "forge",
			wantKeys: []string{
				"FORGE_SESSION_ID=20260514-greendale",
				"FORGE_GENERATED_BY=forge",
			},
			wantAbsent: []string{"FORGE_COAUTHOR"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			cfg := CommitConfig{
				CoAuthor: tc.coAuthor,
				Attribution: AttributionConfig{
					Enabled:     tc.enabled,
					GeneratedBy: tc.generatedBy,
				},
			}
			envs := EnvForCommit(tc.sessionID, cfg)
			for _, want := range tc.wantKeys {
				r.Contains(envs, want)
			}
			for _, absent := range tc.wantAbsent {
				for _, env := range envs {
					r.NotContains(env, absent)
				}
			}
		})
	}
}

func TestCommitHookIntegration(t *testing.T) {
	// Integration test: create a repo, install hook, make a commit, verify trailers.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	r := require.New(t)
	dir := t.TempDir()

	// Init repo with initial commit
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Troy Barnes",
			"GIT_AUTHOR_EMAIL=troy@greendale.edu",
			"GIT_COMMITTER_NAME=Troy Barnes",
			"GIT_COMMITTER_EMAIL=troy@greendale.edu",
		)
		out, err := cmd.CombinedOutput()
		r.NoError(err, "git %v: %s", args, out)
	}

	run("init", dir)
	run("-C", dir, "commit", "--allow-empty", "-m", "initial")

	r.NoError(InstallCommitHook(dir))

	// Set the env vars the hook needs
	t.Setenv("FORGE_SESSION_ID", "20260514-paintball")
	t.Setenv("FORGE_COAUTHOR", "Jelmer Snoeck <jelmer@siphoc.com>")
	t.Setenv("FORGE_GENERATED_BY", "forge")

	// Create a file and commit
	r.NoError(os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644))
	run("-C", dir, "add", "test.txt")
	run("-C", dir, "commit", "-m", "Add test file")

	// Check the commit message
	cmd := exec.Command("git", "log", "-1", "--format=%B")
	cmd.Dir = dir
	out, err := cmd.Output()
	r.NoError(err)
	msg := string(out)

	r.Contains(msg, "Co-authored-by: Jelmer Snoeck <jelmer@siphoc.com>")
	r.Contains(msg, "Generated-by: forge session=20260514-paintball")
}

func TestCommitHookIntegration_noEnvVars(t *testing.T) {
	// Hook should be a no-op when FORGE_SESSION_ID is not set.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	r := require.New(t)
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Troy Barnes",
			"GIT_AUTHOR_EMAIL=troy@greendale.edu",
			"GIT_COMMITTER_NAME=Troy Barnes",
			"GIT_COMMITTER_EMAIL=troy@greendale.edu",
		)
		out, err := cmd.CombinedOutput()
		r.NoError(err, "git %v: %s", args, out)
	}

	run("init", dir)
	run("-C", dir, "commit", "--allow-empty", "-m", "initial")

	r.NoError(InstallCommitHook(dir))

	// Ensure FORGE env vars are NOT set
	t.Setenv("FORGE_SESSION_ID", "")
	t.Setenv("FORGE_COAUTHOR", "")
	t.Setenv("FORGE_GENERATED_BY", "")

	r.NoError(os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644))
	run("-C", dir, "add", "test.txt")
	run("-C", dir, "commit", "-m", "Add test file without attribution")

	cmd := exec.Command("git", "log", "-1", "--format=%B")
	cmd.Dir = dir
	out, err := cmd.Output()
	r.NoError(err)
	msg := string(out)

	r.NotContains(msg, "Co-authored-by:")
	r.NotContains(msg, "Generated-by:")
}
