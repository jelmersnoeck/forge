#!/usr/bin/env node
/**
 * HTTP entrypoint for Forge MCP server
 * 
 * Supports both Streamable HTTP and legacy SSE transports.
 * 
 * Usage:
 *   PORT=3000 node dist/http.js
 *   MCP_API_KEY=secret PORT=3000 node dist/http.js
 */

import { SSEServerTransport } from "@modelcontextprotocol/sdk/server/sse.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import * as http from "node:http";
import { createServer as createMCPServer } from "./server.js";

const PORT = parseInt(process.env.PORT ?? "3000", 10);
const API_KEY = process.env.MCP_API_KEY;

function createHTTPServer() {
  const server = http.createServer(async (req, res) => {
    // Health check
    if (req.url === "/health") {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ status: "ok", service: "forge-mcp" }));
      return;
    }

    // Auth check
    if (API_KEY) {
      const auth = req.headers.authorization;
      if (auth !== `Bearer ${API_KEY}`) {
        res.writeHead(401, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: "Unauthorized" }));
        return;
      }
    }

    // Streamable HTTP transport (modern)
    if (req.url === "/mcp" && (req.method === "POST" || req.method === "GET")) {
      const mcpServer = createMCPServer();
      const transport = new StreamableHTTPServerTransport({
        sessionIdGenerator: () => `forge-${Date.now()}`,
      });
      await mcpServer.connect(transport);
      await transport.handleRequest(req, res);
      return;
    }

    // Legacy SSE transport
    if (req.url === "/sse" && req.method === "GET") {
      const mcpServer = createMCPServer();
      const transport = new SSEServerTransport("/messages", res);
      await mcpServer.connect(transport);
      return;
    }

    if (req.url === "/messages" && req.method === "POST") {
      // SSE POST endpoint handled by SSEServerTransport
      // This is a placeholder - actual handling is done by the transport
      res.writeHead(200);
      res.end();
      return;
    }

    // 404
    res.writeHead(404, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ error: "Not found" }));
  });

  return server;
}

async function main() {
  const server = createHTTPServer();
  
  server.listen(PORT, "0.0.0.0", () => {
    console.error(`Forge MCP server listening on http://0.0.0.0:${PORT}`);
    console.error(`  Streamable HTTP: http://localhost:${PORT}/mcp`);
    console.error(`  Legacy SSE:      http://localhost:${PORT}/sse`);
    console.error(`  Health check:    http://localhost:${PORT}/health`);
    if (API_KEY) {
      console.error("  Auth:            Bearer token required");
    }
  });
}

main().catch((error) => {
  console.error("Fatal error:", error);
  process.exit(1);
});
