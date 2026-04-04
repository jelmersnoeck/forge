/**
 * Forge MCP Server
 * 
 * Exposes Forge's agent capabilities as MCP tools, allowing other MCP clients
 * (Claude Desktop, Claude Code, VS Code, etc.) to use Forge's tools.
 * 
 * This server connects to a running Forge agent (either local subprocess or remote)
 * and proxies tool calls through the Forge HTTP API.
 */

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
  ListResourcesRequestSchema,
  ReadResourceRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { spawn, type ChildProcess } from "node:child_process";
import * as http from "node:http";

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const FORGE_BIN = process.env.FORGE_BIN ?? "forge";
const FORGE_SERVER_URL = process.env.FORGE_SERVER_URL;
const WORKSPACE_DIR = process.env.WORKSPACE_DIR ?? process.cwd();

// ---------------------------------------------------------------------------
// Forge Agent Management
// ---------------------------------------------------------------------------

interface ForgeAgentConnection {
  type: "local" | "remote";
  url: string;
  process?: ChildProcess;
}

let agentConnection: ForgeAgentConnection | null = null;

/**
 * Start a local forge agent subprocess or connect to remote server
 */
async function connectToForgeAgent(): Promise<ForgeAgentConnection> {
  if (agentConnection) {
    return agentConnection;
  }

  // Remote server mode
  if (FORGE_SERVER_URL) {
    agentConnection = {
      type: "remote",
      url: FORGE_SERVER_URL,
    };
    return agentConnection;
  }

  // Local subprocess mode - spawn `forge agent --port 0`
  return new Promise((resolve, reject) => {
    const proc = spawn(FORGE_BIN, ["agent", "--port", "0"], {
      cwd: WORKSPACE_DIR,
      env: { ...process.env },
      stdio: ["pipe", "pipe", "inherit"],
    });

    let stdoutBuffer = "";

    proc.stdout?.on("data", (chunk) => {
      stdoutBuffer += chunk.toString();
      // Look for {"port": 12345} in stdout
      const match = stdoutBuffer.match(/\{"port":\s*(\d+)\}/);
      if (match) {
        const port = parseInt(match[1], 10);
        agentConnection = {
          type: "local",
          url: `http://localhost:${port}`,
          process: proc,
        };
        resolve(agentConnection);
      }
    });

    proc.on("error", (err) => {
      reject(new Error(`Failed to spawn forge agent: ${err.message}`));
    });

    proc.on("exit", (code) => {
      if (!agentConnection) {
        reject(
          new Error(`Forge agent exited with code ${code} before emitting port`)
        );
      }
      agentConnection = null;
    });

    // Timeout after 10s
    setTimeout(() => {
      if (!agentConnection) {
        proc.kill();
        reject(new Error("Timeout waiting for forge agent to start"));
      }
    }, 10000);
  });
}

/**
 * Cleanup agent on exit
 */
function cleanupAgent() {
  if (agentConnection?.process) {
    agentConnection.process.kill();
    agentConnection = null;
  }
}

process.on("exit", cleanupAgent);
process.on("SIGINT", () => {
  cleanupAgent();
  process.exit(0);
});
process.on("SIGTERM", () => {
  cleanupAgent();
  process.exit(0);
});

// ---------------------------------------------------------------------------
// Forge HTTP API Client
// ---------------------------------------------------------------------------

/**
 * Fetch tool schemas from forge agent
 */
async function fetchForgeTools(): Promise<any[]> {
  const conn = await connectToForgeAgent();
  
  return new Promise((resolve, reject) => {
    const url = new URL("/health", conn.url);
    const req = http.get(url, (res) => {
      if (res.statusCode !== 200) {
        reject(new Error(`Health check failed: ${res.statusCode}`));
        return;
      }

      // For now, return hardcoded tool list based on Forge's current tools
      // TODO: Add endpoint to forge agent to list available tools
      resolve([
        {
          name: "Read",
          description: "Read a file from the filesystem with line numbers",
          inputSchema: {
            type: "object",
            properties: {
              file_path: { type: "string", description: "Absolute path to the file" },
              limit: { type: "number", description: "Number of lines to read (default 2000)" },
              offset: { type: "number", description: "Starting line number (1-based, default 1)" },
            },
            required: ["file_path"],
          },
        },
        {
          name: "Write",
          description: "Write content to a file, creating parent directories if needed",
          inputSchema: {
            type: "object",
            properties: {
              file_path: { type: "string", description: "Absolute path to the file" },
              content: { type: "string", description: "Content to write" },
            },
            required: ["file_path", "content"],
          },
        },
        {
          name: "Edit",
          description: "Perform exact string replacement in a file",
          inputSchema: {
            type: "object",
            properties: {
              file_path: { type: "string", description: "Absolute path to the file" },
              old_string: { type: "string", description: "Text to replace" },
              new_string: { type: "string", description: "Replacement text" },
              replace_all: { type: "boolean", description: "Replace all occurrences (default false)" },
            },
            required: ["file_path", "old_string", "new_string"],
          },
        },
        {
          name: "Bash",
          description: "Execute a bash command",
          inputSchema: {
            type: "object",
            properties: {
              command: { type: "string", description: "The command to execute" },
              timeout: { type: "number", description: "Timeout in milliseconds (default 120000, max 600000)" },
            },
            required: ["command"],
          },
        },
        {
          name: "Grep",
          description: "Search for patterns using ripgrep",
          inputSchema: {
            type: "object",
            properties: {
              pattern: { type: "string", description: "Regular expression pattern" },
              path: { type: "string", description: "File or directory to search" },
              glob: { type: "string", description: "Glob pattern to filter files" },
              "-i": { type: "boolean", description: "Case insensitive search" },
              output_mode: { 
                type: "string", 
                enum: ["files_with_matches", "content", "count"],
                description: "Output mode"
              },
            },
            required: ["pattern"],
          },
        },
        {
          name: "Glob",
          description: "Fast file pattern matching using glob patterns",
          inputSchema: {
            type: "object",
            properties: {
              pattern: { type: "string", description: "Glob pattern (e.g., **/*.go)" },
              path: { type: "string", description: "Base directory to search" },
            },
            required: ["pattern"],
          },
        },
        {
          name: "Reflect",
          description: "Reflect on the session and capture learnings",
          inputSchema: {
            type: "object",
            properties: {
              summary: { type: "string", description: "Brief summary of what was accomplished" },
              mistakes: { 
                type: "array",
                items: { type: "string" },
                description: "List of mistakes or things that could have been done better"
              },
              successes: { 
                type: "array",
                items: { type: "string" },
                description: "List of patterns that worked well"
              },
              suggestions: { 
                type: "array",
                items: { type: "string" },
                description: "Ideas for future improvement"
              },
            },
            required: ["summary"],
          },
        },
      ]);
    });

    req.on("error", reject);
  });
}

/**
 * Execute a tool via forge agent HTTP API
 */
async function executeForgeToolViaHTTP(
  toolName: string,
  input: any
): Promise<any> {
  const conn = await connectToForgeAgent();

  return new Promise((resolve, reject) => {
    const data = JSON.stringify({
      role: "user",
      content: [
        {
          type: "tool_result",
          tool_use_id: "mcp-proxy",
          content: `Executing ${toolName} via MCP proxy - NOT IMPLEMENTED YET`,
        },
      ],
    });

    const url = new URL("/messages", conn.url);
    const req = http.request(
      url,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Content-Length": Buffer.byteLength(data),
        },
      },
      (res) => {
        let body = "";
        res.on("data", (chunk) => (body += chunk));
        res.on("end", () => {
          if (res.statusCode !== 200) {
            reject(new Error(`Tool execution failed: ${res.statusCode} ${body}`));
            return;
          }

          try {
            resolve(JSON.parse(body));
          } catch (err) {
            reject(new Error(`Invalid JSON response: ${err}`));
          }
        });
      }
    );

    req.on("error", reject);
    req.write(data);
    req.end();
  });
}

// ---------------------------------------------------------------------------
// MCP Server
// ---------------------------------------------------------------------------

export function createServer(): Server {
  const server = new Server(
    { name: "forge", version: "0.1.0" },
    {
      capabilities: {
        tools: {},
        resources: {},
      },
    }
  );

  // ---- Resources ---------------------------------------------------------

  server.setRequestHandler(ListResourcesRequestSchema, async () => ({
    resources: [
      {
        uri: "forge://readme",
        name: "Forge README",
        description: "Project documentation and architecture overview",
        mimeType: "text/markdown",
      },
      {
        uri: "forge://claude",
        name: "Project Instructions",
        description: "CLAUDE.md project-level agent instructions",
        mimeType: "text/markdown",
      },
    ],
  }));

  server.setRequestHandler(
    ReadResourceRequestSchema,
    async (request: { params: { uri: string } }) => {
      const { uri } = request.params;

      if (uri === "forge://readme") {
        // TODO: Read from actual README.md
        return {
          contents: [
            {
              uri,
              mimeType: "text/markdown",
              text: "# Forge\n\nAsync coding agent — headless Claude Code behind a platform-agnostic HTTP API.",
            },
          ],
        };
      }

      if (uri === "forge://claude") {
        // TODO: Read from actual CLAUDE.md
        return {
          contents: [
            {
              uri,
              mimeType: "text/markdown",
              text: "# Project Instructions\n\nSee CLAUDE.md in the forge repository.",
            },
          ],
        };
      }

      throw new Error(`Unknown resource: ${uri}`);
    }
  );

  // ---- Tools -------------------------------------------------------------

  server.setRequestHandler(ListToolsRequestSchema, async () => {
    const tools = await fetchForgeTools();
    return { tools };
  });

  server.setRequestHandler(
    CallToolRequestSchema,
    async (request: { params: { name: string; arguments?: any } }) => {
      const { name, arguments: args } = request.params;

      try {
        // NOTE: This is a simplified implementation
        // Real implementation would need to properly proxy tool execution
        // through the Forge agent's conversation loop
        const result = await executeForgeToolViaHTTP(name, args ?? {});

        return {
          content: [
            {
              type: "text",
              text: JSON.stringify(result, null, 2),
            },
          ],
        };
      } catch (error) {
        return {
          content: [
            {
              type: "text",
              text: `Error executing tool ${name}: ${error}`,
            },
          ],
          isError: true,
        };
      }
    }
  );

  return server;
}
