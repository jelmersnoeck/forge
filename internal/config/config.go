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
	SpecsDir string `json:"specsDir,omitempty"` // override for specs directory (default: .forge/specs)
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

	var cfg ForgeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	if cfg.SpecsDir != "" {
		dst.SpecsDir = cfg.SpecsDir
	}

	return nil
}
