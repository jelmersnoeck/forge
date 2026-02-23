package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/engine"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/tracker"
	"github.com/jelmersnoeck/forge/pkg/config"
)

// buildEngine creates an Engine from the given configuration. It wires up
// agent backends, tracker backends, and principle stores based on config values.
func buildEngine(cfg *config.Config) (*engine.Engine, error) {
	agents, err := buildAgents(cfg)
	if err != nil {
		return nil, fmt.Errorf("building agents: %w", err)
	}

	trackers, err := buildTrackers(cfg)
	if err != nil {
		return nil, fmt.Errorf("building trackers: %w", err)
	}

	store, err := buildPrincipleStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("building principle store: %w", err)
	}

	engineCfg := &engine.EngineConfig{
		DefaultAgent:      cfg.Agent.Default,
		DefaultTracker:    cfg.Tracker.Default,
		MaxIterations:     cfg.Build.MaxIterations,
		BranchPattern:     cfg.Build.BranchPattern,
		TestCommand:       cfg.Build.TestCommand,
		RequireApproval:   cfg.Build.RequirePlanApproval,
		SeverityThreshold: cfg.Build.Review.SeverityThreshold,
		PlannerAgent:      cfg.Agent.Roles.Planner,
		CoderAgent:        cfg.Agent.Roles.Coder,
		ReviewerAgent:     cfg.Agent.Roles.Reviewer,
	}

	return engine.New(engineCfg, agents, trackers, store)
}

// buildAgents creates agent backends from configuration.
func buildAgents(cfg *config.Config) (map[string]agent.Agent, error) {
	agents := make(map[string]agent.Agent)

	for name, backend := range cfg.Agent.Backends {
		a, err := createAgent(name, backend)
		if err != nil {
			return nil, fmt.Errorf("creating agent %q: %w", name, err)
		}
		agents[name] = a
	}

	if len(agents) == 0 {
		return nil, fmt.Errorf("no agent backends configured")
	}

	return agents, nil
}

// createAgent creates a single agent backend based on its name and config.
func createAgent(name string, backend config.AgentBackend) (agent.Agent, error) {
	switch {
	case name == "claude-code" || backend.Binary == "claude":
		return agent.NewClaudeCode(backend.Binary), nil
	case name == "opencode" || backend.Binary == "opencode":
		return agent.NewOpenCode(backend.Binary), nil
	case name == "http":
		// HTTP agents use the binary field as the URL.
		return agent.NewHTTP(backend.Binary, "", 5*time.Minute), nil
	default:
		// Default: try to create as a claude-code agent with the given binary.
		if backend.Binary != "" {
			return agent.NewClaudeCode(backend.Binary), nil
		}
		return nil, fmt.Errorf("cannot determine agent type for %q", name)
	}
}

// buildTrackers creates tracker backends from configuration.
func buildTrackers(cfg *config.Config) (map[string]tracker.Tracker, error) {
	trackers := make(map[string]tracker.Tracker)

	switch cfg.Tracker.Default {
	case "github":
		if cfg.Tracker.GitHub != nil {
			trackers["github"] = tracker.NewGitHubTracker(
				cfg.Tracker.GitHub.Org,
				cfg.Tracker.GitHub.DefaultRepo,
			)
		} else {
			// Create with empty org/repo; gh CLI may infer from git remote.
			trackers["github"] = tracker.NewGitHubTracker("", "")
		}
	case "file":
		dir, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getting working directory: %w", err)
		}
		trackers["file"] = tracker.NewFileTracker(dir)
	case "jira":
		if cfg.Tracker.Jira == nil {
			return nil, fmt.Errorf("tracker default is %q but tracker.jira config is missing", cfg.Tracker.Default)
		}
		email, token, _ := strings.Cut(cfg.Tracker.Jira.Auth, ":")
		trackers["jira"] = tracker.NewJiraTracker(
			cfg.Tracker.Jira.Instance,
			cfg.Tracker.Jira.DefaultProject,
			email,
			token,
		)
	case "linear":
		if cfg.Tracker.Linear == nil {
			return nil, fmt.Errorf("tracker default is %q but tracker.linear config is missing", cfg.Tracker.Default)
		}
		trackers["linear"] = tracker.NewLinearTracker(
			cfg.Tracker.Linear.Team,
			cfg.Tracker.Linear.Auth,
		)
	default:
		return nil, fmt.Errorf("unsupported tracker type: %q", cfg.Tracker.Default)
	}

	return trackers, nil
}

// buildPrincipleStore loads principles from configured directories.
func buildPrincipleStore(cfg *config.Config) (*principles.Store, error) {
	store := principles.NewStore()

	for _, path := range cfg.Principles.Paths {
		// Silently skip missing directories — principles are optional.
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if err := store.LoadDir(path); err != nil {
			return nil, fmt.Errorf("loading principles from %s: %w", path, err)
		}
	}

	return store, nil
}

// loadConfig loads configuration from the --config flag path or by auto-discovery.
func loadConfig() (*config.Config, error) {
	cfgPath, _ := rootCmd.PersistentFlags().GetString("config")
	if cfgPath != "" {
		return config.Load(cfgPath)
	}
	return config.Discover()
}
