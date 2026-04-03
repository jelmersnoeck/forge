// Package mcp implements a Model Context Protocol client.
//
// The MCP client allows Forge to connect to external MCP servers and use their
// tools, resources, and prompts.
//
// This is a minimal JSON-RPC 2.0 implementation targeting the MCP specification.
// See: https://spec.modelcontextprotocol.io/
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Client represents an MCP client connection to a server.
type Client struct {
	name      string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	reader    *bufio.Reader
	requestID atomic.Int64
	mu        sync.Mutex
	pending   map[int64]chan *JSONRPCResponse
	closed    bool
}

// JSONRPCRequest represents a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// ServerInfo holds MCP server metadata.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerCapabilities describes what the server supports.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// InitializeResult is returned from the initialize handshake.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
}

// Tool represents an MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolResult represents the result of calling a tool.
type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock represents a piece of content (text or image).
type ContentBlock struct {
	Type     string `json:"type"` // "text" or "image"
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`     // base64 for images
	MimeType string `json:"mimeType,omitempty"` // for images
}

// Resource represents an MCP resource.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ConnectSTDIO connects to an MCP server via STDIO subprocess.
//
// command is the executable (e.g., "node")
// args are the command arguments (e.g., ["path/to/server.js"])
func ConnectSTDIO(name, command string, args []string, env []string) (*Client, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = append(cmd.Env, env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start MCP server: %w", err)
	}

	client := &Client{
		name:    name,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		reader:  bufio.NewReader(stdout),
		pending: make(map[int64]chan *JSONRPCResponse),
	}

	// Start reading responses in the background
	go client.readLoop()

	return client, nil
}

// Initialize performs the MCP handshake.
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]string{
			"name":    "forge",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{},
	}

	var result InitializeResult
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return nil, err
	}

	// Send initialized notification (no response expected)
	if err := c.notify("notifications/initialized", nil); err != nil {
		return nil, fmt.Errorf("failed to send initialized notification: %w", err)
	}

	return &result, nil
}

// ListTools retrieves available tools from the server.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool invokes a tool on the server.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}

	var result ToolResult
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ListResources retrieves available resources from the server.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	var result struct {
		Resources []Resource `json:"resources"`
	}
	if err := c.call(ctx, "resources/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Resources, nil
}

// ReadResource reads a resource from the server.
func (c *Client) ReadResource(ctx context.Context, uri string) ([]ContentBlock, error) {
	params := map[string]any{
		"uri": uri,
	}

	var result struct {
		Contents []ContentBlock `json:"contents"`
	}
	if err := c.call(ctx, "resources/read", params, &result); err != nil {
		return nil, err
	}
	return result.Contents, nil
}

// Close terminates the connection and kills the subprocess.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	// Close all pending requests
	for _, ch := range c.pending {
		close(ch)
	}
	c.pending = nil

	// Close pipes
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.stdout != nil {
		c.stdout.Close()
	}
	if c.stderr != nil {
		c.stderr.Close()
	}

	// Kill the subprocess
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}

	return nil
}

// call sends a JSON-RPC request and waits for the response.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	id := c.requestID.Add(1)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	// Register pending request
	ch := make(chan *JSONRPCResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("client closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	// Send request
	data, err := json.Marshal(req)
	if err != nil {
		c.removePending(id)
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		c.removePending(id)
		return fmt.Errorf("failed to write request: %w", err)
	}

	// Wait for response
	select {
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return fmt.Errorf("client closed while waiting for response")
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("failed to unmarshal result: %w", err)
			}
		}
		return nil
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(method string, params any) error {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("client closed")
	}

	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write notification: %w", err)
	}

	return nil
}

// readLoop continuously reads JSON-RPC responses from stdout.
func (c *Client) readLoop() {
	for {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			// Connection closed or error
			c.Close()
			return
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Invalid JSON - skip
			continue
		}

		// Dispatch to waiting caller
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()

		if ok {
			ch <- &resp
			close(ch)
		}
	}
}

// removePending removes a pending request from the map.
func (c *Client) removePending(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.pending[id]; ok {
		close(ch)
		delete(c.pending, id)
	}
}
