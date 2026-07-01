// Package config loads forge-level configuration from user and project levels.
//
//	~/.forge/config.json   (user)
//	.forge/config.json     (project — overrides user)
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ForgeConfig holds forge-level configuration.
type ForgeConfig struct {
	SpecsDir string        `json:"specsDir,omitempty"` // override for specs directory (default: .forge/specs)
	RepoMap  RepoMapConfig `json:"repoMap,omitempty"`  // optional structural repo map
}

// RepoMapConfig gates and bounds the optional repo map injected into context.
type RepoMapConfig struct {
	Enabled     bool `json:"enabled,omitempty"`
	TokenBudget int  `json:"tokenBudget,omitempty"` // default 2000 when enabled
	MaxFiles    int  `json:"maxFiles,omitempty"`    // 0 = unlimited
}

// rawConfig mirrors ForgeConfig for a single decode pass, but types the repo
// map's Enabled flag as *bool so an explicit "enabled": false in a more-specific
// config can override an earlier true. A plain bool under omitempty cannot
// distinguish "false" from "unset". Consumers still see the clean bool via
// ForgeConfig; the pointer stays internal to merging.
type rawConfig struct {
	SpecsDir string `json:"specsDir"`
	RepoMap  struct {
		Enabled     *bool `json:"enabled"`
		TokenBudget int   `json:"tokenBudget"`
		MaxFiles    int   `json:"maxFiles"`
	} `json:"repoMap"`
}

// Load merges configuration from user (~/.forge/config.json) and project
// (.forge/config.json) levels. Project values override user values.
// Missing files are silently ignored.
func Load(cwd string) (ForgeConfig, error) {
	var merged ForgeConfig

	home, err := os.UserHomeDir()
	if err == nil {
		userPath := filepath.Join(home, ".forge", "config.json")
		if err := mergeFile(&merged, userPath); err != nil {
			return merged, fmt.Errorf("load user forge config: %w", err)
		}
	}

	projectPath := filepath.Join(cwd, ".forge", "config.json")
	if err := mergeFile(&merged, projectPath); err != nil {
		return merged, fmt.Errorf("load project forge config: %w", err)
	}

	return merged, nil
}

func mergeFile(dst *ForgeConfig, path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var cfg rawConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	if cfg.SpecsDir != "" {
		dst.SpecsDir = cfg.SpecsDir
	}

	if cfg.RepoMap.Enabled != nil {
		dst.RepoMap.Enabled = *cfg.RepoMap.Enabled
	}
	if cfg.RepoMap.TokenBudget != 0 {
		dst.RepoMap.TokenBudget = cfg.RepoMap.TokenBudget
	}
	if cfg.RepoMap.MaxFiles != 0 {
		dst.RepoMap.MaxFiles = cfg.RepoMap.MaxFiles
	}

	return nil
}
