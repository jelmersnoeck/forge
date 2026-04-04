package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// configPath returns the path to the MCP config file.
// If project is true, returns .forge/mcp.json relative to cwd.
// Otherwise returns ~/.forge/mcp.json.
func configPath(project bool, cwd string) (string, error) {
	if project {
		return filepath.Join(cwd, ".forge", "mcp.json"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".forge", "mcp.json"), nil
}

// loadConfigFile reads a single config file. Returns empty config if file doesn't exist.
func loadConfigFile(path string) (MCPConfig, error) {
	cfg := MCPConfig{Servers: make(map[string]MCPServerConfig)}

	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return cfg, nil
	case err != nil:
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]MCPServerConfig)
	}

	return cfg, nil
}

// saveConfigFile writes an MCP config to disk, creating parent dirs as needed.
func saveConfigFile(path string, cfg MCPConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// AddServer adds or updates a server in the config file.
func AddServer(name string, server MCPServerConfig, project bool, cwd string) error {
	path, err := configPath(project, cwd)
	if err != nil {
		return err
	}

	cfg, err := loadConfigFile(path)
	if err != nil {
		return err
	}

	cfg.Servers[name] = server
	return saveConfigFile(path, cfg)
}

// RemoveServer removes a server from the config file.
// Returns an error if the server doesn't exist.
func RemoveServer(name string, project bool, cwd string) error {
	path, err := configPath(project, cwd)
	if err != nil {
		return err
	}

	cfg, err := loadConfigFile(path)
	if err != nil {
		return err
	}

	if _, ok := cfg.Servers[name]; !ok {
		return fmt.Errorf("server %q not found in %s", name, path)
	}

	delete(cfg.Servers, name)
	return saveConfigFile(path, cfg)
}

// ListServers returns the merged view of all configured servers.
func ListServers(cwd string) (MCPConfig, error) {
	return LoadConfig(cwd)
}
