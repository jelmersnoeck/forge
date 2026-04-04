package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/mcp"
	"github.com/stretchr/testify/require"
)

func TestExtractPositional(t *testing.T) {
	tests := map[string]struct {
		args         []string
		wantPos      string
		wantRemCount int
	}{
		"name first": {
			args:         []string{"datadog", "--url", "https://example.com", "--auth", "oauth"},
			wantPos:      "datadog",
			wantRemCount: 4,
		},
		"name last": {
			args:         []string{"--url", "https://example.com", "--auth", "oauth", "datadog"},
			wantPos:      "datadog",
			wantRemCount: 4,
		},
		"name middle": {
			args:         []string{"--url", "https://example.com", "datadog", "--auth", "oauth"},
			wantPos:      "datadog",
			wantRemCount: 4,
		},
		"no positional": {
			args:         []string{"--url", "https://example.com"},
			wantPos:      "",
			wantRemCount: 2,
		},
		"bool flag project": {
			args:         []string{"greendale", "--project"},
			wantPos:      "greendale",
			wantRemCount: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			pos, rem := extractPositional(tc.args)
			r.Equal(tc.wantPos, pos)
			r.Len(rem, tc.wantRemCount)
		})
	}
}

func TestMCPAddIntegration(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cwd := filepath.Join(dir, "project")
	r.NoError(os.MkdirAll(cwd, 0o755))

	// Add via the management API (same path the CLI uses)
	r.NoError(mcp.AddServer("greendale", mcp.MCPServerConfig{
		URL:  "https://greendale.edu/mcp",
		Auth: "oauth",
	}, false, cwd))

	r.NoError(mcp.AddServer("hawthorne", mcp.MCPServerConfig{
		URL:     "https://hawthorne-wipes.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer wipes"},
	}, false, cwd))

	// Verify file contents
	data, err := os.ReadFile(filepath.Join(dir, ".forge", "mcp.json"))
	r.NoError(err)

	var cfg mcp.MCPConfig
	r.NoError(json.Unmarshal(data, &cfg))
	r.Len(cfg.Servers, 2)
	r.Equal("https://greendale.edu/mcp", cfg.Servers["greendale"].URL)
	r.Equal("oauth", cfg.Servers["greendale"].Auth)
	r.Equal("Bearer wipes", cfg.Servers["hawthorne"].Headers["Authorization"])

	// Remove one
	r.NoError(mcp.RemoveServer("greendale", false, cwd))

	data, err = os.ReadFile(filepath.Join(dir, ".forge", "mcp.json"))
	r.NoError(err)
	var cfg2 mcp.MCPConfig
	r.NoError(json.Unmarshal(data, &cfg2))
	r.Len(cfg2.Servers, 1)
	r.Contains(cfg2.Servers, "hawthorne")
}
