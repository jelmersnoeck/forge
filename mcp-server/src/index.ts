#!/usr/bin/env node
/**
 * STDIO entrypoint for Forge MCP server
 * 
 * Usage:
 *   node dist/index.js
 * 
 * This is used by local MCP clients (Claude Desktop, Claude Code, VS Code)
 */

import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { createServer } from "./server.js";

async function main() {
  const server = createServer();
  const transport = new StdioServerTransport();
  
  await server.connect(transport);
  
  console.error("Forge MCP server running on stdio");
}

main().catch((error) => {
  console.error("Fatal error:", error);
  process.exit(1);
});
