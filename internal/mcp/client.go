package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// protocolVersion is the MCP protocol version we support.
const protocolVersion = "2025-03-26"

// JSONRPCRequest is a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"` // nil for notifications
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError is a JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// InitializeResult is the server's response to initialize.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      MCPServerInfo      `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
}

// MCPServerInfo identifies the MCP server.
type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerCapabilities describes what the server supports.
type ServerCapabilities struct {
	Tools     *ToolsCapability `json:"tools,omitempty"`
	Resources *json.RawMessage `json:"resources,omitempty"`
	Prompts   *json.RawMessage `json:"prompts,omitempty"`
}

// ToolsCapability signals the server supports tools.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// MCPTool is a tool definition from the MCP server.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// MCPToolResult is the result of a tools/call request.
type MCPToolResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// MCPContent is a content block in an MCP tool result.
type MCPContent struct {
	Type     string `json:"type"` // "text", "image", "resource"
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // base64 for images
}

// Client is an MCP client that communicates with a remote server
// over Streamable HTTP transport.
type Client struct {
	serverName string
	url        string
	httpClient *http.Client
	headers    map[string]string
	sessionID  string // Mcp-Session-Id from server
	nextID     atomic.Int64
	oauth      *OAuthClient
	mcpURL     string // original URL for OAuth discovery
}

// ClientOption configures the MCP client.
type ClientOption func(*Client)

// WithOAuth enables OAuth 2.1 authentication.
func WithOAuth(store *TokenStore) ClientOption {
	return func(c *Client) {
		c.oauth = NewOAuthClient(c.serverName, store)
	}
}

// WithHeaders sets static HTTP headers for all requests.
func WithHeaders(headers map[string]string) ClientOption {
	return func(c *Client) {
		c.headers = headers
	}
}

// NewClient creates an MCP client for the given server.
func NewClient(serverName, url string, opts ...ClientOption) *Client {
	c := &Client{
		serverName: serverName,
		url:        url,
		mcpURL:     url,
		httpClient: &http.Client{Timeout: 120 * time.Second},
		headers:    make(map[string]string),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ServerName returns the configured server name.
func (c *Client) ServerName() string {
	return c.serverName
}

// Connect performs the MCP initialize handshake.
func (c *Client) Connect(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"clientInfo": map[string]string{
			"name":    "forge",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{},
	}

	resp, err := c.sendRequest(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}

	log.Printf("[mcp:%s] connected: %s %s (protocol %s)",
		c.serverName, result.ServerInfo.Name, result.ServerInfo.Version, result.ProtocolVersion)

	// Send initialized notification (no response expected)
	return c.sendNotification(ctx, "notifications/initialized", nil)
}

// ListTools returns all tools available on the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]MCPTool, error) {
	var allTools []MCPTool
	var cursor string

	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}

		resp, err := c.sendRequest(ctx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}

		var result struct {
			Tools      []MCPTool `json:"tools"`
			NextCursor string    `json:"nextCursor,omitempty"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, fmt.Errorf("parse tools/list result: %w", err)
		}

		allTools = append(allTools, result.Tools...)

		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}

	return allTools, nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*MCPToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}

	resp, err := c.sendRequest(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", name, err)
	}

	var result MCPToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/call result: %w", err)
	}

	return &result, nil
}

// Close sends a session termination request if a session is active.
func (c *Client) Close(ctx context.Context) error {
	if c.sessionID == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "DELETE", c.url, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) sendRequest(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
	id := c.nextID.Add(1)
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	resp, err := c.doRequest(ctx, rpcReq)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp, nil
}

func (c *Client) sendNotification(ctx context.Context, method string, params any) error {
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build notification request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	defer resp.Body.Close()

	// Capture session ID
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	return nil
}

func (c *Client) doRequest(ctx context.Context, rpcReq JSONRPCRequest) (*JSONRPCResponse, error) {
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	// Add OAuth token if configured
	if c.oauth != nil {
		token, err := c.oauth.EnsureValidToken(ctx, c.mcpURL)
		if err != nil {
			return nil, fmt.Errorf("oauth: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Handle 401 with OAuth retry
	if resp.StatusCode == http.StatusUnauthorized && c.oauth != nil {
		// Clear stored token and retry
		c.oauth.store.Delete(c.serverName)
		token, err := c.oauth.EnsureValidToken(ctx, c.mcpURL)
		if err != nil {
			return nil, fmt.Errorf("oauth retry: %w", err)
		}

		req, err = http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build retry request: %w", err)
		}
		c.setHeaders(req)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("retry HTTP request: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, respBody)
	}

	// Capture session ID
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	contentType := resp.Header.Get("Content-Type")

	switch {
	case strings.HasPrefix(contentType, "text/event-stream"):
		return c.parseSSEResponse(resp.Body, rpcReq.ID)
	default:
		return c.parseJSONResponse(resp.Body)
	}
}

func (c *Client) parseJSONResponse(body io.Reader) (*JSONRPCResponse, error) {
	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("parse JSON response: %w", err)
	}
	return &rpcResp, nil
}

// parseSSEResponse reads an SSE stream looking for the JSON-RPC response
// matching the given request ID.
func (c *Client) parseSSEResponse(body io.Reader, requestID *int64) (*JSONRPCResponse, error) {
	scanner := bufio.NewScanner(body)
	var dataBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "data: "):
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			// End of event — try to parse
			if dataBuf.Len() > 0 {
				var rpcResp JSONRPCResponse
				if err := json.Unmarshal([]byte(dataBuf.String()), &rpcResp); err == nil {
					if rpcResp.ID != nil && requestID != nil && *rpcResp.ID == *requestID {
						return &rpcResp, nil
					}
				}
				dataBuf.Reset()
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE stream: %w", err)
	}

	return nil, fmt.Errorf("SSE stream ended without response for request ID %v", requestID)
}

func (c *Client) setHeaders(req *http.Request) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
}
