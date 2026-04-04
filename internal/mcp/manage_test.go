package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddServer(t *testing.T) {
	tests := map[string]struct {
		existing *MCPConfig
		name     string
		server   MCPServerConfig
		project  bool
		want     map[string]MCPServerConfig
	}{
		"add to empty config": {
			name:   "datadog",
			server: MCPServerConfig{URL: "https://mcp.datadoghq.com/mcp", Auth: "oauth"},
			want: map[string]MCPServerConfig{
				"datadog": {URL: "https://mcp.datadoghq.com/mcp", Auth: "oauth"},
			},
		},
		"add alongside existing": {
			existing: &MCPConfig{Servers: map[string]MCPServerConfig{
				"greendale": {URL: "https://greendale.edu/mcp"},
			}},
			name:   "city-college",
			server: MCPServerConfig{URL: "https://city-college.edu/mcp"},
			want: map[string]MCPServerConfig{
				"greendale":    {URL: "https://greendale.edu/mcp"},
				"city-college": {URL: "https://city-college.edu/mcp"},
			},
		},
		"overwrite existing server": {
			existing: &MCPConfig{Servers: map[string]MCPServerConfig{
				"greendale": {URL: "https://greendale.edu/old"},
			}},
			name:   "greendale",
			server: MCPServerConfig{URL: "https://greendale.edu/v2/mcp", Auth: "oauth"},
			want: map[string]MCPServerConfig{
				"greendale": {URL: "https://greendale.edu/v2/mcp", Auth: "oauth"},
			},
		},
		"add with headers": {
			name: "hawthorne",
			server: MCPServerConfig{
				URL:     "https://hawthorne-wipes.com/mcp",
				Headers: map[string]string{"Authorization": "Bearer pierce-was-here"},
			},
			want: map[string]MCPServerConfig{
				"hawthorne": {
					URL:     "https://hawthorne-wipes.com/mcp",
					Headers: map[string]string{"Authorization": "Bearer pierce-was-here"},
				},
			},
		},
		"add to project config": {
			name:    "local-tool",
			server:  MCPServerConfig{URL: "http://localhost:8080/mcp"},
			project: true,
			want: map[string]MCPServerConfig{
				"local-tool": {URL: "http://localhost:8080/mcp"},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			dir := t.TempDir()
			t.Setenv("HOME", dir)

			cwd := filepath.Join(dir, "project")
			r.NoError(os.MkdirAll(cwd, 0o755))

			// Seed existing config if provided
			if tc.existing != nil {
				path := filepath.Join(dir, ".forge", "mcp.json")
				if tc.project {
					path = filepath.Join(cwd, ".forge", "mcp.json")
				}
				r.NoError(os.MkdirAll(filepath.Dir(path), 0o755))
				data, _ := json.MarshalIndent(tc.existing, "", "  ")
				r.NoError(os.WriteFile(path, data, 0o644))
			}

			r.NoError(AddServer(tc.name, tc.server, tc.project, cwd))

			// Read back and verify
			path := filepath.Join(dir, ".forge", "mcp.json")
			if tc.project {
				path = filepath.Join(cwd, ".forge", "mcp.json")
			}
			data, err := os.ReadFile(path)
			r.NoError(err)

			var got MCPConfig
			r.NoError(json.Unmarshal(data, &got))
			r.Equal(tc.want, got.Servers)
		})
	}
}

func TestRemoveServer(t *testing.T) {
	tests := map[string]struct {
		existing MCPConfig
		name     string
		wantErr  bool
		want     map[string]MCPServerConfig
	}{
		"remove existing": {
			existing: MCPConfig{Servers: map[string]MCPServerConfig{
				"greendale":    {URL: "https://greendale.edu/mcp"},
				"city-college": {URL: "https://city-college.edu/mcp"},
			}},
			name: "greendale",
			want: map[string]MCPServerConfig{
				"city-college": {URL: "https://city-college.edu/mcp"},
			},
		},
		"remove nonexistent": {
			existing: MCPConfig{Servers: map[string]MCPServerConfig{
				"greendale": {URL: "https://greendale.edu/mcp"},
			}},
			name:    "subway",
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			dir := t.TempDir()
			t.Setenv("HOME", dir)

			path := filepath.Join(dir, ".forge", "mcp.json")
			r.NoError(os.MkdirAll(filepath.Dir(path), 0o755))
			data, _ := json.MarshalIndent(tc.existing, "", "  ")
			r.NoError(os.WriteFile(path, data, 0o644))

			err := RemoveServer(tc.name, false, dir)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)

			readBack, err := os.ReadFile(path)
			r.NoError(err)
			var got MCPConfig
			r.NoError(json.Unmarshal(readBack, &got))
			r.Equal(tc.want, got.Servers)
		})
	}
}
