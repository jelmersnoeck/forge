---
id: mcp-server-mode
status: draft
---
# Expose forge's built-in tool registry over MCP as a server

## Description
Forge is MCP-client-only today (`internal/mcp/*` connects OUT to remote servers;
`cmd/forge/mcp.go` only manages client config). This spec adds an inbound MCP
server mode — `forge mcp serve` — that exposes forge's built-in `tools.Registry`
to external MCP clients over Streamable HTTP and stdio transports. It reuses the
JSON-RPC 2.0 types already defined in `internal/mcp/client.go` and routes MCP
`tools/call` through `Registry.Execute` so permission enforcement is honored.
By default only read-only tools are exposed; mutating tools (Write, Edit, Bash)
require an explicit opt-in flag.

## Context
- `cmd/forge/mcp.go` — `runMCP` dispatch switch (add/remove/list/login). Gains a
  `serve` case and a `runMCPServe` function + help text.
- `internal/mcp/server.go` — NEW. JSON-RPC 2.0 server handling `initialize`,
  `notifications/initialized`, `tools/list`, `tools/call`. Streamable HTTP
  handler + stdio loop. Reuses `JSONRPCRequest`, `JSONRPCResponse`,
  `JSONRPCError`, `InitializeResult`, `MCPServerInfo`, `ServerCapabilities`,
  `ToolsCapability`, `MCPTool`, `MCPToolResult`, `MCPContent` from `client.go`.
- `internal/mcp/server_test.go` — NEW. Table-driven tests.
- `internal/tools/registry.go` — source of the tool catalog. `Registry.All()`
  yields `[]types.ToolDefinition` (Name, Description, InputSchema, ReadOnly);
  `Registry.Execute(name, input, ctx)` runs a tool honoring permissions.
- `internal/types/types.go` — `ToolDefinition`, `ToolResult`,
  `ToolResultContent`, `ToolContext`, `ImageSource`.
- `protocolVersion` const (`2025-03-26`) in `client.go` — server echoes it.

## Behavior
- `forge mcp serve` starts an MCP server exposing forge's built-in tools.
- Transport selection via `--transport`:
  - `--transport stdio` (default): reads JSON-RPC requests as newline-delimited
    JSON on stdin, writes responses to stdout, one JSON object per line. Logs go
    to stderr only. Runs until stdin EOF.
  - `--transport http`: listens on `--addr` (default `127.0.0.1:0`; `:0` picks a
    free port and the chosen `host:port` is printed to stderr). Serves POST at
    path `/mcp` (Streamable HTTP). Responds `application/json`.
- `--cwd DIR` sets the working directory passed to executed tools
  (`ToolContext.CWD`); defaults to the current directory.
- `--allow-mutating` includes mutating tools (non-`ReadOnly`) in the exposed
  catalog. Without it, only tools where `ToolDefinition.ReadOnly == true` are
  listed and callable.
- `--tools a,b,c` restricts the exposed set to the named tools (intersected with
  the read-only/mutating gate). Unknown names are ignored with a stderr warning.
- `initialize` returns `protocolVersion` `2025-03-26`, `serverInfo`
  `{name:"forge", version:<forge version>}`, and `capabilities.tools` present
  (`{}`), signaling tool support.
- `tools/list` returns every exposed tool as an `MCPTool{Name, Description,
  InputSchema}` sorted by name (deterministic). Tools filtered by the gate/allow
  list are absent. No pagination cursor is emitted (single page).
- `tools/call` with `params.name` + `params.arguments` invokes
  `Registry.Execute(name, arguments, ctx)`. The returned `types.ToolResult`
  maps to `MCPToolResult`: each `ToolResultContent` of type `text` → `MCPContent{
  Type:"text", Text:...}`; type `image` → `MCPContent{Type:"image",
  Data:source.Data, MimeType:source.MediaType}`. `ToolResult.IsError` maps to
  `MCPToolResult.IsError`.
- Tools not in the exposed catalog are rejected via JSON-RPC error (not silently
  run): `tools/call` for a hidden/unknown tool returns
  `JSONRPCError{Code:-32601, Message:"tool not found: <name>"}`.
- Permission enforcement: the server builds the registry via
  `Registry.WithPermissions(allow, nil)` where `allow` is the exposed tool names
  (or `["*"]` semantics for mutating+all). A denied tool reaching `Execute`
  round-trips as an `IsError` `MCPToolResult`, not a JSON-RPC error, matching the
  registry's existing behavior.
- `notifications/initialized` (a notification, `id` absent) is accepted and
  produces no response.
- `forge mcp` help lists `serve` with its flags and examples.

## Constraints
- Must NOT expose mutating tools (`ReadOnly == false`) unless `--allow-mutating`
  is passed.
- Must NOT run a tool whose name is absent from the exposed catalog — reject with
  JSON-RPC `-32601` before calling `Execute`.
- Must NOT emit non-protocol output to stdout in stdio mode (no banners, no
  prompts). All human-facing/log output goes to stderr.
- Must reuse the existing JSON-RPC/MCP types in `internal/mcp/client.go`; do not
  duplicate `JSONRPCRequest`/`JSONRPCResponse`/`MCPTool`/etc.
- Must NOT add OAuth/auth to the server in this spec — bind HTTP to loopback
  (`127.0.0.1`) by default. Auth is a future extension point.
- Must respond to a request `id` with the same `id`; notifications (nil `id`)
  get no response.
- Must NOT depend on a live LLM provider or network — the server only lists and
  executes local tools.

## Interfaces
```go
// internal/mcp/server.go

// Server serves forge's tool registry to MCP clients over JSON-RPC 2.0.
type Server struct {
    registry *tools.Registry     // pre-filtered to the exposed tool set
    exposed  map[string]bool     // tool names allowed for list/call
    cwd      string
    info     MCPServerInfo
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// NewServer builds a Server exposing the given registry's tools, filtered by
// the read-only/allow-mutating/name-list policy applied by the caller.
func NewServer(registry *tools.Registry, cwd string, opts ...ServerOption) *Server

// Handle processes a single decoded JSON-RPC request and returns the response
// to send, or (nil, nil) for notifications that need no reply.
func (s *Server) Handle(ctx context.Context, req JSONRPCRequest) (*JSONRPCResponse, error)

// ServeHTTP implements http.Handler for the Streamable HTTP transport (POST /mcp).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)

// ServeStdio runs the newline-delimited JSON-RPC loop over the given reader/writer
// until EOF. Used by `forge mcp serve --transport stdio`.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error
```
```go
// cmd/forge/mcp.go
func runMCPServe(args []string) int   // flags: --transport, --addr, --cwd,
                                       // --allow-mutating, --tools
```

## Edge Cases
- Unknown method (e.g. `resources/list`): return
  `JSONRPCError{Code:-32601, Message:"method not found: resources/list"}`.
- Malformed JSON on the wire (http body or a stdio line): HTTP returns 400 with a
  `-32700` parse-error JSON-RPC envelope (id null); stdio writes the same envelope
  to stdout and continues to the next line rather than crashing.
- `tools/call` missing `params.name`: `JSONRPCError{Code:-32602,
  Message:"invalid params: name is required"}`.
- `tools/call` with `arguments` absent or null: treated as empty map, tool runs
  with `{}` (matches client-side behavior where args default to empty).
- Empty exposed catalog (e.g. `--tools nonexistent`): `tools/list` returns an
  empty `tools` array; server still initializes normally.
- Tool handler returns a Go error from `Execute` (not an `IsError` result):
  surface as `JSONRPCError{Code:-32603, Message:"internal error: <err>"}` so the
  client sees a protocol error rather than a hung stream.
- Concurrent HTTP requests: each `ServeHTTP` call is independent; `Registry` is
  already `sync.RWMutex`-guarded, so `Execute`/`All` are safe. No shared mutable
  server state beyond the immutable `exposed`/`registry`.
- stdio: a blank line on stdin is skipped (no parse error, no response).
- `:0` HTTP addr: the actual bound `host:port` must be printed to stderr so a
  supervising process can discover it (mirrors `forge agent` port emission).
