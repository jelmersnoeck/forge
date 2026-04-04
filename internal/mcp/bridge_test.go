package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectAndStore(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	store := NewStore()
	cfg := MCPServerConfig{
		URL: srv.URL,
	}

	client, err := ConnectAndStore(context.Background(), store, "greendale", cfg, nil)
	r.NoError(err)
	r.NotNil(client)
	defer client.Close(context.Background())

	// Tools should be in the store, not a registry
	r.False(store.Empty())
	tools, err := store.ListTools("greendale")
	r.NoError(err)
	r.Len(tools, 2)

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}
	r.True(names["paintball_launcher"])
	r.True(names["blanket_fort"])
}

func TestConnectAndStoreCallTool(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	store := NewStore()
	cfg := MCPServerConfig{URL: srv.URL}

	client, err := ConnectAndStore(context.Background(), store, "greendale", cfg, nil)
	r.NoError(err)
	defer client.Close(context.Background())

	// Call tool through the store
	result, err := store.CallTool(context.Background(), "greendale", "paintball_launcher", map[string]any{
		"target": "Troy Barnes",
	})
	r.NoError(err)
	r.False(result.IsError)
	r.Len(result.Content, 1)
	r.Contains(result.Content[0].Text, "Troy Barnes")
}

func TestConnectAndStoreWithHeaders(t *testing.T) {
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

	store := NewStore()
	cfg := MCPServerConfig{
		URL:     srv.URL,
		Headers: map[string]string{"X-Dean-Secret": "dean-dean-dean"},
	}

	client, err := ConnectAndStore(context.Background(), store, "secret", cfg, nil)
	r.NoError(err)
	defer client.Close(context.Background())

	r.Equal("dean-dean-dean", gotAuth)
}

func TestConnectAndStoreWithOAuthRequiresStore(t *testing.T) {
	r := require.New(t)

	store := NewStore()
	cfg := MCPServerConfig{
		URL:  "https://example.com/mcp",
		Auth: "oauth",
	}

	_, err := ConnectAndStore(context.Background(), store, "test", cfg, nil)
	r.Error(err)
	r.Contains(err.Error(), "token store")
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
