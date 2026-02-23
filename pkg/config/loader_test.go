package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	yaml := `
agent:
  default: claude-code
  backends:
    claude-code:
      binary: claude
      model: opus
  roles:
    planner: claude-code
    coder: claude-code
    reviewer: claude-code
tracker:
  default: github
  github:
    org: myorg
    default_repo: myrepo
principles:
  paths:
    - .forge/principles
  active:
    - security
build:
  max_iterations: 5
  branch_pattern: "forge/{{.Tracker}}-{{.IssueID}}"
  test_command: "make test"
  require_plan_approval: true
  review:
    parallel_reviewers: 2
    severity_threshold: critical
server:
  port: 9090
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Agent.Default != "claude-code" {
		t.Errorf("Agent.Default = %q, want %q", cfg.Agent.Default, "claude-code")
	}
	if cfg.Tracker.Default != "github" {
		t.Errorf("Tracker.Default = %q, want %q", cfg.Tracker.Default, "github")
	}
	if cfg.Tracker.GitHub == nil {
		t.Fatal("Tracker.GitHub is nil")
	}
	if cfg.Tracker.GitHub.Org != "myorg" {
		t.Errorf("Tracker.GitHub.Org = %q, want %q", cfg.Tracker.GitHub.Org, "myorg")
	}
	if cfg.Tracker.GitHub.DefaultRepo != "myrepo" {
		t.Errorf("Tracker.GitHub.DefaultRepo = %q, want %q", cfg.Tracker.GitHub.DefaultRepo, "myrepo")
	}
	if cfg.Build.MaxIterations != 5 {
		t.Errorf("Build.MaxIterations = %d, want %d", cfg.Build.MaxIterations, 5)
	}
	if cfg.Build.TestCommand != "make test" {
		t.Errorf("Build.TestCommand = %q, want %q", cfg.Build.TestCommand, "make test")
	}
	if cfg.Build.Review.ParallelReviewers != 2 {
		t.Errorf("Build.Review.ParallelReviewers = %d, want %d", cfg.Build.Review.ParallelReviewers, 2)
	}
	if cfg.Build.Review.SeverityThreshold != "critical" {
		t.Errorf("Build.Review.SeverityThreshold = %q, want %q", cfg.Build.Review.SeverityThreshold, "critical")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 9090)
	}
	backend, ok := cfg.Agent.Backends["claude-code"]
	if !ok {
		t.Fatal("Agent.Backends missing claude-code")
	}
	if backend.Model != "opus" {
		t.Errorf("Agent.Backends[claude-code].Model = %q, want %q", backend.Model, "opus")
	}

	if len(cfg.Principles.Active) != 1 || cfg.Principles.Active[0] != "security" {
		t.Errorf("Principles.Active = %v, want [security]", cfg.Principles.Active)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(cfgPath, []byte("{{not valid yaml"), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("Load() expected error for invalid YAML, got nil")
	}
}

func TestLoad_NonExistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("Load() expected error for missing file, got nil")
	}
}

func TestDiscoverFrom_WalksUp(t *testing.T) {
	// Create directory tree: root/.forge/config.yaml and root/a/b/c/
	root := t.TempDir()
	forgeDir := filepath.Join(root, ".forge")
	if err := os.MkdirAll(forgeDir, 0755); err != nil {
		t.Fatalf("creating .forge dir: %v", err)
	}

	yaml := `
agent:
  default: claude-code
tracker:
  default: github
build:
  max_iterations: 3
  branch_pattern: "forge/{{.Tracker}}-{{.IssueID}}"
`
	cfgPath := filepath.Join(forgeDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Create a deeply nested subdirectory.
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}

	cfg, err := DiscoverFrom(nested)
	if err != nil {
		t.Fatalf("DiscoverFrom() error: %v", err)
	}

	if cfg.Agent.Default != "claude-code" {
		t.Errorf("Agent.Default = %q, want %q", cfg.Agent.Default, "claude-code")
	}
}

func TestDiscoverFrom_NotFound(t *testing.T) {
	// Use a temp dir with no .forge directory anywhere.
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}

	_, err := DiscoverFrom(nested)
	if err == nil {
		t.Fatal("DiscoverFrom() expected error when no config found, got nil")
	}
}

func TestDefault_ReturnsValidConfig(t *testing.T) {
	cfg := Default()

	if err := Validate(cfg); err != nil {
		t.Fatalf("Default() config failed validation: %v", err)
	}

	if cfg.Agent.Default != "claude-code" {
		t.Errorf("Default Agent.Default = %q, want %q", cfg.Agent.Default, "claude-code")
	}
	if cfg.Tracker.Default != "github" {
		t.Errorf("Default Tracker.Default = %q, want %q", cfg.Tracker.Default, "github")
	}
	if cfg.Build.MaxIterations != 3 {
		t.Errorf("Default Build.MaxIterations = %d, want %d", cfg.Build.MaxIterations, 3)
	}
	if cfg.Build.BranchPattern != "forge/{{.Tracker}}-{{.IssueID}}" {
		t.Errorf("Default Build.BranchPattern = %q, want %q", cfg.Build.BranchPattern, "forge/{{.Tracker}}-{{.IssueID}}")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Default Server.Port = %d, want %d", cfg.Server.Port, 8080)
	}
	if cfg.Build.RequirePlanApproval != true {
		t.Errorf("Default Build.RequirePlanApproval = %v, want true", cfg.Build.RequirePlanApproval)
	}
}

func TestValidate_MissingAgentDefault(t *testing.T) {
	cfg := Default()
	cfg.Agent.Default = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for missing agent.default")
	}
}

func TestValidate_MissingTrackerDefault(t *testing.T) {
	cfg := Default()
	cfg.Tracker.Default = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for missing tracker.default")
	}
}

func TestValidate_InvalidMaxIterations(t *testing.T) {
	cfg := Default()
	cfg.Build.MaxIterations = 0
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for max_iterations=0")
	}
}

func TestValidate_MissingBranchPattern(t *testing.T) {
	cfg := Default()
	cfg.Build.BranchPattern = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for missing branch_pattern")
	}
}

func TestValidate_InvalidSeverityThreshold(t *testing.T) {
	cfg := Default()
	cfg.Build.Review.SeverityThreshold = "bogus"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for invalid severity_threshold")
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	cfg := Default()
	cfg.Server.Port = 99999
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for invalid port")
	}
}

func TestValidate_NilConfig(t *testing.T) {
	err := Validate(nil)
	if err == nil {
		t.Fatal("Validate() expected error for nil config")
	}
}

func TestExpandEnvVars(t *testing.T) {
	const testToken = "my-secret-token-12345"
	t.Setenv("FORGE_TEST_TOKEN", testToken)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	yaml := `
agent:
  default: claude-code
tracker:
  default: jira
  jira:
    instance: https://myorg.atlassian.net
    default_project: PROJ
    auth: "env:FORGE_TEST_TOKEN"
build:
  max_iterations: 3
  branch_pattern: "forge/{{.Tracker}}-{{.IssueID}}"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Tracker.Jira == nil {
		t.Fatal("Tracker.Jira is nil")
	}
	if cfg.Tracker.Jira.Auth != testToken {
		t.Errorf("Tracker.Jira.Auth = %q, want %q (expanded from env:FORGE_TEST_TOKEN)", cfg.Tracker.Jira.Auth, testToken)
	}
}

func TestExpandEnvVars_NoPrefix(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	yaml := `
agent:
  default: claude-code
tracker:
  default: jira
  jira:
    instance: https://myorg.atlassian.net
    default_project: PROJ
    auth: "literal-token-value"
build:
  max_iterations: 3
  branch_pattern: "forge/{{.Tracker}}-{{.IssueID}}"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Tracker.Jira.Auth != "literal-token-value" {
		t.Errorf("Tracker.Jira.Auth = %q, want %q (should not be expanded)", cfg.Tracker.Jira.Auth, "literal-token-value")
	}
}

func TestExpandEnvVars_LinearAuth(t *testing.T) {
	const testToken = "lin-token-abc"
	t.Setenv("LINEAR_API_KEY", testToken)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	yaml := `
agent:
  default: claude-code
tracker:
  default: linear
  linear:
    team: TEAM
    auth: "env:LINEAR_API_KEY"
build:
  max_iterations: 3
  branch_pattern: "forge/{{.Tracker}}-{{.IssueID}}"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Tracker.Linear == nil {
		t.Fatal("Tracker.Linear is nil")
	}
	if cfg.Tracker.Linear.Auth != testToken {
		t.Errorf("Tracker.Linear.Auth = %q, want %q", cfg.Tracker.Linear.Auth, testToken)
	}
}
