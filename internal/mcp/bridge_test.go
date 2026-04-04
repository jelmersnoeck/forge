package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRegisterMCPTools(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	client := NewClient("greendale", srv.URL)
	r.NoError(client.Connect(context.Background()))

	registry := tools.NewRegistry()
	r.NoError(RegisterMCPTools(registry, client))

	// Should have registered both tools with namespaced names
	all := registry.All()
	r.Len(all, 2)

	names := make(map[string]bool)
	for _, tool := range all {
		names[tool.Name] = true
	}

	r.True(names["mcp__greendale__paintball_launcher"])
	r.True(names["mcp__greendale__blanket_fort"])
}

func TestMCPToolExecution(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	client := NewClient("greendale", srv.URL)
	r.NoError(client.Connect(context.Background()))

	registry := tools.NewRegistry()
	r.NoError(RegisterMCPTools(registry, client))

	// Execute the paintball tool through the registry
	toolCtx := types.ToolContext{
		Ctx:       context.Background(),
		CWD:       t.TempDir(),
		SessionID: "paintball-episode",
	}

	result, err := registry.Execute("mcp__greendale__paintball_launcher", map[string]any{
		"target": "Troy Barnes",
	}, toolCtx)
	r.NoError(err)
	r.False(result.IsError)
	r.Len(result.Content, 1)
	r.Contains(result.Content[0].Text, "Troy Barnes")
}

func TestMCPToolErrorResult(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	client := NewClient("greendale", srv.URL)
	r.NoError(client.Connect(context.Background()))

	// Call an unknown tool directly
	result, err := client.CallTool(context.Background(), "nonexistent", map[string]any{})
	r.NoError(err)
	r.True(result.IsError)
}

func TestToolPrefix(t *testing.T) {
	tests := map[string]struct {
		serverName string
		toolName   string
		want       string
	}{
		"basic": {
			serverName: "greendale",
			toolName:   "paintball",
			want:       "mcp__greendale__paintball",
		},
		"with hyphens": {
			serverName: "city-college",
			toolName:   "evil-plan",
			want:       "mcp__city-college__evil-plan",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, ToolPrefix(tc.serverName, tc.toolName))
		})
	}
}

func TestConnectAndRegister(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	registry := tools.NewRegistry()
	cfg := MCPServerConfig{
		URL: srv.URL,
	}

	client, err := ConnectAndRegister(context.Background(), registry, "greendale", cfg, nil)
	r.NoError(err)
	r.NotNil(client)
	defer client.Close(context.Background())

	// Verify tools registered
	all := registry.All()
	r.Len(all, 2)
}

func TestConnectAndRegisterWithHeaders(t *testing.T) {
	r := require.New(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("X-Dean-Secret")

		var rpcReq JSONRPCRequest
		json.NewDecoder(req.Body).Decode(&rpcReq)

		if rpcReq.ID == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch rpcReq.Method {
		case "initialize":
			result, _ := json.Marshal(InitializeResult{
				ProtocolVersion: protocolVersion,
				ServerInfo:      MCPServerInfo{Name: "Header Test"},
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
			})
			json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(result),
			})
		case "tools/list":
			result, _ := json.Marshal(map[string]any{"tools": []MCPTool{}})
			json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(result),
			})
		}
	}))
	defer srv.Close()

	registry := tools.NewRegistry()
	cfg := MCPServerConfig{
		URL:     srv.URL,
		Headers: map[string]string{"X-Dean-Secret": "dean-dean-dean"},
	}

	client, err := ConnectAndRegister(context.Background(), registry, "secret", cfg, nil)
	r.NoError(err)
	defer client.Close(context.Background())

	r.Equal("dean-dean-dean", gotAuth)
}

func TestConnectAndRegisterWithOAuthRequiresStore(t *testing.T) {
	r := require.New(t)

	registry := tools.NewRegistry()
	cfg := MCPServerConfig{
		URL:  "https://example.com/mcp",
		Auth: "oauth",
	}

	_, err := ConnectAndRegister(context.Background(), registry, "test", cfg, nil)
	r.Error(err)
	r.Contains(err.Error(), "token store")
}

func TestMCPToolWithImageContent(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var rpcReq JSONRPCRequest
		json.NewDecoder(req.Body).Decode(&rpcReq)

		if rpcReq.ID == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch rpcReq.Method {
		case "initialize":
			result, _ := json.Marshal(InitializeResult{
				ProtocolVersion: protocolVersion,
				ServerInfo:      MCPServerInfo{Name: "Image Server"},
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
			})
			json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(result),
			})
		case "tools/list":
			result, _ := json.Marshal(map[string]any{
				"tools": []MCPTool{{
					Name:        "yearbook_photo",
					Description: "Take a yearbook photo",
					InputSchema: map[string]any{"type": "object"},
				}},
			})
			json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(result),
			})
		case "tools/call":
			result, _ := json.Marshal(MCPToolResult{
				Content: []MCPContent{
					{Type: "text", Text: "Here's your photo:"},
					{Type: "image", MimeType: "image/png", Data: "aGVsbG8="},
				},
			})
			json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(result),
			})
		}
	}))
	defer srv.Close()

	client := NewClient("yearbook", srv.URL)
	r.NoError(client.Connect(context.Background()))

	registry := tools.NewRegistry()
	r.NoError(RegisterMCPTools(registry, client))

	toolCtx := types.ToolContext{
		Ctx:       context.Background(),
		CWD:       t.TempDir(),
		SessionID: "yearbook-session",
	}

	result, err := registry.Execute("mcp__yearbook__yearbook_photo", map[string]any{}, toolCtx)
	r.NoError(err)
	r.Len(result.Content, 2)
	r.Equal("text", result.Content[0].Type)
	r.Equal("image", result.Content[1].Type)
	r.Equal("image/png", result.Content[1].Source.MediaType)
	r.Equal("aGVsbG8=", result.Content[1].Source.Data)

	fmt.Println("yearbook photo test passed — streets ahead!")
}
