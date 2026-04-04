package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeMCPServer simulates a minimal MCP server for testing.
type fakeMCPServer struct {
	t         *testing.T
	sessionID string
	requestID atomic.Int64
}

func newFakeMCPServer(t *testing.T) *fakeMCPServer {
	return &fakeMCPServer{
		t:         t,
		sessionID: "study-room-f",
	}
}

func (s *fakeMCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", s.sessionID)

	switch req.Method {
	case "initialize":
		s.writeResponse(w, req.ID, InitializeResult{
			ProtocolVersion: protocolVersion,
			ServerInfo:      MCPServerInfo{Name: "Greendale MCP", Version: "1.0.0"},
			Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
		})

	case "notifications/initialized":
		w.WriteHeader(http.StatusOK)

	case "tools/list":
		s.writeResponse(w, req.ID, map[string]any{
			"tools": []MCPTool{
				{
					Name:        "paintball_launcher",
					Description: "Fires paintballs at rival study groups",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"target": map[string]any{"type": "string", "description": "Who to hit"},
							"color":  map[string]any{"type": "string", "description": "Paintball color"},
						},
						"required": []string{"target"},
					},
				},
				{
					Name:        "blanket_fort",
					Description: "Constructs a blanket fort of specified dimensions",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"width":  map[string]any{"type": "number"},
							"height": map[string]any{"type": "number"},
						},
					},
				},
			},
		})

	case "tools/call":
		params, _ := json.Marshal(req.Params)
		var callParams struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		json.Unmarshal(params, &callParams)

		switch callParams.Name {
		case "paintball_launcher":
			target, _ := callParams.Arguments["target"].(string)
			s.writeResponse(w, req.ID, MCPToolResult{
				Content: []MCPContent{
					{Type: "text", Text: fmt.Sprintf("Direct hit on %s!", target)},
				},
			})
		case "blanket_fort":
			s.writeResponse(w, req.ID, MCPToolResult{
				Content: []MCPContent{
					{Type: "text", Text: "Blanket fort constructed. It's beautiful."},
				},
			})
		default:
			s.writeResponse(w, req.ID, MCPToolResult{
				Content: []MCPContent{{Type: "text", Text: "unknown tool"}},
				IsError: true,
			})
		}

	default:
		s.writeError(w, req.ID, -32601, "method not found")
	}
}

func (s *fakeMCPServer) writeResponse(w http.ResponseWriter, id *int64, result any) {
	data, _ := json.Marshal(result)
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  json.RawMessage(data),
	})
}

func (s *fakeMCPServer) writeError(w http.ResponseWriter, id *int64, code int, msg string) {
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: msg},
	})
}

func TestClientConnect(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	client := NewClient("greendale", srv.URL)
	err := client.Connect(context.Background())
	r.NoError(err)
	r.Equal("study-room-f", client.sessionID)
}

func TestClientListTools(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	client := NewClient("greendale", srv.URL)
	r.NoError(client.Connect(context.Background()))

	tools, err := client.ListTools(context.Background())
	r.NoError(err)
	r.Len(tools, 2)
	r.Equal("paintball_launcher", tools[0].Name)
	r.Equal("blanket_fort", tools[1].Name)
}

func TestClientCallTool(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(newFakeMCPServer(t))
	defer srv.Close()

	client := NewClient("greendale", srv.URL)
	r.NoError(client.Connect(context.Background()))

	result, err := client.CallTool(context.Background(), "paintball_launcher", map[string]any{
		"target": "Señor Chang",
		"color":  "blue",
	})
	r.NoError(err)
	r.Len(result.Content, 1)
	r.Contains(result.Content[0].Text, "Señor Chang")
	r.False(result.IsError)
}

func TestClientSSEResponse(t *testing.T) {
	r := require.New(t)

	// Server that responds with SSE
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var rpcReq JSONRPCRequest
		json.NewDecoder(req.Body).Decode(&rpcReq)

		if rpcReq.ID == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		switch rpcReq.Method {
		case "initialize":
			result, _ := json.Marshal(InitializeResult{
				ProtocolVersion: protocolVersion,
				ServerInfo:      MCPServerInfo{Name: "SSE Server"},
				Capabilities:    ServerCapabilities{},
			})
			resp, _ := json.Marshal(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(result),
			})
			fmt.Fprintf(w, "data: %s\n\n", resp)
		case "tools/list":
			result, _ := json.Marshal(map[string]any{"tools": []MCPTool{}})
			resp, _ := json.Marshal(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      rpcReq.ID,
				Result:  json.RawMessage(result),
			})
			fmt.Fprintf(w, "data: %s\n\n", resp)
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	client := NewClient("sse-test", srv.URL)
	r.NoError(client.Connect(context.Background()))

	tools, err := client.ListTools(context.Background())
	r.NoError(err)
	r.Empty(tools)
}

func TestClientWithHeaders(t *testing.T) {
	r := require.New(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")

		var rpcReq JSONRPCRequest
		json.NewDecoder(req.Body).Decode(&rpcReq)

		if rpcReq.ID == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		result, _ := json.Marshal(InitializeResult{
			ProtocolVersion: protocolVersion,
			ServerInfo:      MCPServerInfo{Name: "Auth Server"},
			Capabilities:    ServerCapabilities{},
		})
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      rpcReq.ID,
			Result:  json.RawMessage(result),
		})
	}))
	defer srv.Close()

	client := NewClient("auth-test", srv.URL, WithHeaders(map[string]string{
		"Authorization": "Bearer dean-peltons-diary",
	}))
	r.NoError(client.Connect(context.Background()))
	r.Equal("Bearer dean-peltons-diary", gotAuth)
}

func TestClientClose(t *testing.T) {
	r := require.New(t)

	var gotDelete bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "DELETE" {
			gotDelete = true
			w.WriteHeader(http.StatusOK)
			return
		}

		var rpcReq JSONRPCRequest
		json.NewDecoder(req.Body).Decode(&rpcReq)

		if rpcReq.ID == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-123")
		result, _ := json.Marshal(InitializeResult{
			ProtocolVersion: protocolVersion,
			ServerInfo:      MCPServerInfo{Name: "Close Test"},
			Capabilities:    ServerCapabilities{},
		})
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      rpcReq.ID,
			Result:  json.RawMessage(result),
		})
	}))
	defer srv.Close()

	client := NewClient("close-test", srv.URL)
	r.NoError(client.Connect(context.Background()))
	r.NoError(client.Close(context.Background()))
	r.True(gotDelete)
}
