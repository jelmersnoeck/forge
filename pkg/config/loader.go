package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

const (
	// ForgeDir is the directory name for Forge configuration.
	ForgeDir = ".forge"
	// ConfigFileName is the default config file name.
	ConfigFileName = "config"
	// ConfigFileType is the default config file type.
	ConfigFileType = "yaml"
	// ConfigFile is the full config file name.
	ConfigFile = "config.yaml"
)

// Load reads configuration from a specific file path.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	cfg := Default()
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	expandEnvVars(cfg)

	return cfg, nil
}

// Discover finds .forge/config.yaml by walking up from the current working
// directory. It returns the parsed config from the first match found.
func Discover() (*Config, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	return DiscoverFrom(dir)
}

// DiscoverFrom finds .forge/config.yaml by walking up from the given directory.
func DiscoverFrom(startDir string) (*Config, error) {
	dir := startDir
	for {
		configPath := filepath.Join(dir, ForgeDir, ConfigFile)
		if _, err := os.Stat(configPath); err == nil {
			return Load(configPath)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding config.
			return nil, fmt.Errorf("no %s/%s found (searched from %s to /)", ForgeDir, ConfigFile, startDir)
		}
		dir = parent
	}
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Agent: AgentConfig{
			Default: "claude-code",
			Backends: map[string]AgentBackend{
				"claude-code": {
					Binary: "claude",
				},
			},
			Roles: AgentRoles{
				Planner:  "claude-code",
				Coder:    "claude-code",
				Reviewer: "claude-code",
			},
		},
		Tracker: TrackerConfig{
			Default: "github",
		},
		Principles: PrincipleConfig{
			Paths: []string{".forge/principles"},
		},
		Build: BuildConfig{
			MaxIterations:           3,
			BranchPattern:           "forge/{{.Tracker}}-{{.IssueID}}",
			WorkstreamBranchPattern: "forge/ws-{{.WorkstreamID}}",
			RequirePlanApproval:     true,
			Review: ReviewConfig{
				ParallelReviewers: 1,
				SeverityThreshold: "warning",
			},
		},
		Server: ServerConfig{
			Port: 8080,
		},
	}
}

// Validate checks that a Config has all required fields and that values are
// within acceptable ranges.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if cfg.Agent.Default == "" {
		return fmt.Errorf("agent.default is required")
	}

	if cfg.Tracker.Default == "" {
		return fmt.Errorf("tracker.default is required")
	}

	if cfg.Build.MaxIterations < 1 {
		return fmt.Errorf("build.max_iterations must be >= 1, got %d", cfg.Build.MaxIterations)
	}

	if cfg.Build.BranchPattern == "" {
		return fmt.Errorf("build.branch_pattern is required")
	}

	validSeverities := map[string]bool{
		"info": true, "warning": true, "critical": true,
	}
	if cfg.Build.Review.SeverityThreshold != "" && !validSeverities[cfg.Build.Review.SeverityThreshold] {
		return fmt.Errorf("build.review.severity_threshold must be one of info, warning, critical; got %q", cfg.Build.Review.SeverityThreshold)
	}

	if cfg.Server.Port < 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 0 and 65535, got %d", cfg.Server.Port)
	}

	return nil
}

// expandEnvVars expands environment variable references in auth-related
// fields. Values prefixed with "env:" are replaced with the corresponding
// environment variable value (e.g., "env:GITHUB_TOKEN" -> os.Getenv("GITHUB_TOKEN")).
func expandEnvVars(cfg *Config) {
	if cfg.Tracker.Jira != nil {
		cfg.Tracker.Jira.Auth = expandEnvValue(cfg.Tracker.Jira.Auth)
	}
	if cfg.Tracker.Linear != nil {
		cfg.Tracker.Linear.Auth = expandEnvValue(cfg.Tracker.Linear.Auth)
	}
	if cfg.Server.Webhooks.GitHub != nil {
		cfg.Server.Webhooks.GitHub.Secret = expandEnvValue(cfg.Server.Webhooks.GitHub.Secret)
	}
	if cfg.Server.Webhooks.Jira != nil {
		cfg.Server.Webhooks.Jira.Auth = expandEnvValue(cfg.Server.Webhooks.Jira.Auth)
	}
}

// expandEnvValue expands a single value. If the value starts with "env:",
// the remainder is treated as an environment variable name.
func expandEnvValue(val string) string {
	if strings.HasPrefix(val, "env:") {
		envName := strings.TrimPrefix(val, "env:")
		return os.Getenv(envName)
	}
	return val
}
