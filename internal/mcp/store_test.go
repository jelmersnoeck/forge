package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {
	tests := map[string]struct {
		setup func(*Store)
		check func(*require.Assertions, *Store)
	}{
		"empty store": {
			setup: func(s *Store) {},
			check: func(r *require.Assertions, s *Store) {
				r.True(s.Empty())
				r.Empty(s.ListServers())
				r.Empty(s.Clients())

				_, err := s.ListTools("nope")
				r.ErrorContains(err, "unknown MCP server")

				_, err = s.GetTool("nope", "nope")
				r.ErrorContains(err, "unknown MCP server")
			},
		},
		"add server and list": {
			setup: func(s *Store) {
				client := NewClient("greendale", "http://localhost:1234")
				s.Add(client, []MCPTool{
					{Name: "enroll", Description: "Enroll a human being."},
					{Name: "expel", Description: "Expel a Chang."},
				})
			},
			check: func(r *require.Assertions, s *Store) {
				r.False(s.Empty())

				servers := s.ListServers()
				r.Len(servers, 1)
				r.Equal("greendale", servers[0].Name)
				r.Equal(2, servers[0].ToolCount)
			},
		},
		"list tools for server": {
			setup: func(s *Store) {
				client := NewClient("hawthorne", "http://localhost:5555")
				s.Add(client, []MCPTool{
					{Name: "moist_towelette", Description: "Dispense a moist towelette.", InputSchema: map[string]any{"type": "object"}},
				})
			},
			check: func(r *require.Assertions, s *Store) {
				tools, err := s.ListTools("hawthorne")
				r.NoError(err)
				r.Len(tools, 1)
				r.Equal("moist_towelette", tools[0].Name)
			},
		},
		"get specific tool": {
			setup: func(s *Store) {
				client := NewClient("city_college", "http://localhost:6666")
				s.Add(client, []MCPTool{
					{Name: "spy", Description: "Send Subway to infiltrate."},
					{Name: "attack", Description: "Paintball assault."},
				})
			},
			check: func(r *require.Assertions, s *Store) {
				tool, err := s.GetTool("city_college", "spy")
				r.NoError(err)
				r.Equal("spy", tool.Name)

				_, err = s.GetTool("city_college", "befriend")
				r.ErrorContains(err, "not found")
			},
		},
		"call tool unknown server": {
			setup: func(s *Store) {},
			check: func(r *require.Assertions, s *Store) {
				_, err := s.CallTool(context.Background(), "missing", "tool", nil)
				r.ErrorContains(err, "unknown MCP server")
			},
		},
		"clients returns all": {
			setup: func(s *Store) {
				s.Add(NewClient("a", "http://a"), nil)
				s.Add(NewClient("b", "http://b"), nil)
			},
			check: func(r *require.Assertions, s *Store) {
				clients := s.Clients()
				r.Len(clients, 2)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			s := NewStore()
			tc.setup(s)
			tc.check(r, s)
		})
	}
}
