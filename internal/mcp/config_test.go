package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	tests := map[string]struct {
		userConfig    *MCPConfig
		projectConfig *MCPConfig
		wantServers   map[string]MCPServerConfig
	}{
		"no config files": {
			wantServers: map[string]MCPServerConfig{},
		},
		"user config only": {
			userConfig: &MCPConfig{
				Servers: map[string]MCPServerConfig{
					"greendale": {
						URL:  "https://greendale.edu/mcp",
						Auth: "oauth",
					},
				},
			},
			wantServers: map[string]MCPServerConfig{
				"greendale": {
					URL:  "https://greendale.edu/mcp",
					Auth: "oauth",
				},
			},
		},
		"project config only": {
			projectConfig: &MCPConfig{
				Servers: map[string]MCPServerConfig{
					"city-college": {
						URL:     "https://city-college.edu/mcp",
						Headers: map[string]string{"Authorization": "Bearer dean-pelton-rocks"},
					},
				},
			},
			wantServers: map[string]MCPServerConfig{
				"city-college": {
					URL:     "https://city-college.edu/mcp",
					Headers: map[string]string{"Authorization": "Bearer dean-pelton-rocks"},
				},
			},
		},
		"project overrides user for same server name": {
			userConfig: &MCPConfig{
				Servers: map[string]MCPServerConfig{
					"greendale": {
						URL:  "https://greendale.edu/mcp",
						Auth: "oauth",
					},
				},
			},
			projectConfig: &MCPConfig{
				Servers: map[string]MCPServerConfig{
					"greendale": {
						URL:     "https://greendale.edu/v2/mcp",
						Headers: map[string]string{"X-Study-Group": "7"},
					},
				},
			},
			wantServers: map[string]MCPServerConfig{
				"greendale": {
					URL:     "https://greendale.edu/v2/mcp",
					Headers: map[string]string{"X-Study-Group": "7"},
				},
			},
		},
		"merge different servers from both levels": {
			userConfig: &MCPConfig{
				Servers: map[string]MCPServerConfig{
					"greendale": {URL: "https://greendale.edu/mcp"},
				},
			},
			projectConfig: &MCPConfig{
				Servers: map[string]MCPServerConfig{
					"city-college": {URL: "https://city-college.edu/mcp"},
				},
			},
			wantServers: map[string]MCPServerConfig{
				"greendale":    {URL: "https://greendale.edu/mcp"},
				"city-college": {URL: "https://city-college.edu/mcp"},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir := t.TempDir()

			// Override HOME for user config
			origHome := os.Getenv("HOME")
			t.Setenv("HOME", dir)
			defer func() { _ = os.Setenv("HOME", origHome) }()

			projectDir := filepath.Join(dir, "project")
			r.NoError(os.MkdirAll(filepath.Join(dir, ".forge"), 0o755))
			r.NoError(os.MkdirAll(filepath.Join(projectDir, ".forge"), 0o755))

			if tc.userConfig != nil {
				data, err := json.Marshal(tc.userConfig)
				r.NoError(err)
				r.NoError(os.WriteFile(filepath.Join(dir, ".forge", "mcp.json"), data, 0o644))
			}

			if tc.projectConfig != nil {
				data, err := json.Marshal(tc.projectConfig)
				r.NoError(err)
				r.NoError(os.WriteFile(filepath.Join(projectDir, ".forge", "mcp.json"), data, 0o644))
			}

			cfg, err := LoadConfig(projectDir)
			r.NoError(err)
			r.Equal(tc.wantServers, cfg.Servers)
		})
	}
}
