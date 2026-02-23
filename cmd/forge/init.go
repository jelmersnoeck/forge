package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a .forge/ directory with default configuration",
	Long: `Scaffolds a .forge/ directory in the current working directory with:
  - config.yaml with commented defaults
  - principles/ directory for governance principle sets
  - prompts/ directory for custom prompt templates`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing .forge/ configuration")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	return runInitInDir(dir, initForce)
}

func runInitInDir(dir string, force bool) error {
	forgeDir := filepath.Join(dir, ".forge")

	// Check for existing .forge directory.
	if _, err := os.Stat(forgeDir); err == nil && !force {
		return fmt.Errorf(".forge/ already exists (use --force to overwrite)")
	}

	// Create directory structure.
	dirs := []string{
		forgeDir,
		filepath.Join(forgeDir, "principles"),
		filepath.Join(forgeDir, "prompts"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
		fmt.Printf("  created %s/\n", relPath(dir, d))
	}

	// Write config.yaml.
	configPath := filepath.Join(forgeDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0644); err != nil {
		return fmt.Errorf("writing config.yaml: %w", err)
	}
	fmt.Printf("  created %s\n", relPath(dir, configPath))

	fmt.Println("\nForge initialized. Edit .forge/config.yaml to configure your project.")
	return nil
}

// relPath returns a path relative to base, or the absolute path on error.
func relPath(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

const defaultConfigYAML = `# Forge Configuration
# See https://github.com/jelmersnoeck/forge for documentation.

# Agent configuration — which LLM backends to use.
agent:
  # Default agent backend for all roles.
  default: claude-code
  backends:
    claude-code:
      binary: claude
      # model: opus  # Optional model override.
  # Map operational roles to agent backends.
  roles:
    planner: claude-code
    coder: claude-code
    reviewer: claude-code

# Tracker configuration — where issues and PRs live.
tracker:
  default: github
  # github:
  #   org: your-org
  #   default_repo: your-repo
  # jira:
  #   instance: https://your-org.atlassian.net
  #   default_project: PROJ
  #   auth: "env:JIRA_API_TOKEN"  # Use env: prefix for environment variables.
  # linear:
  #   team: TEAM
  #   auth: "env:LINEAR_API_KEY"

# Principles — governance rules agents must follow.
principles:
  paths:
    - .forge/principles
  # active:
  #   - security
  #   - architecture

# Build loop configuration.
build:
  max_iterations: 3
  branch_pattern: "forge/{{.Tracker}}-{{.IssueID}}"
  workstream_branch_pattern: "forge/ws-{{.WorkstreamID}}"
  # test_command: "make test"
  require_plan_approval: true
  review:
    parallel_reviewers: 1
    severity_threshold: warning

# Server configuration (for forge serve).
server:
  port: 8080
  # webhooks:
  #   github:
  #     secret: "env:GITHUB_WEBHOOK_SECRET"
  #     triggers:
  #       - event: issues.labeled
  #         label: forge-build
  #         action: build
`
