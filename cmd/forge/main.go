// Forge — async coding agent
//
// Usage:
//
//	forge              interactive CLI (default)
//	forge agent        run agent server
//	forge server       run gateway server
//	forge stats        show cost analytics
//	forge mcp          manage MCP server connections
package main

import (
	"fmt"
	"os"
)

func main() {
	// Default to interactive mode if no args
	if len(os.Args) == 1 {
		os.Exit(runCLI(os.Args))
	}

	// Parse subcommand
	cmd := os.Args[1]
	switch cmd {
	case "agent":
		os.Exit(runAgent(os.Args[1:]))
	case "server":
		os.Exit(runServer(os.Args[1:]))
	case "stats":
		os.Exit(runStats(os.Args[1:]))
	case "mcp":
		os.Exit(runMCP(os.Args[1:]))
	case "help", "-h", "--help":
		printHelp()
		os.Exit(0)
	default:
		// If first arg starts with -, it's a flag for interactive mode
		if len(cmd) > 0 && cmd[0] == '-' {
			os.Exit(runCLI(os.Args))
		}
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`forge — async coding agent

Usage:
  forge              interactive CLI (default)
  forge agent        run agent server
  forge server       run gateway server  
  forge stats        show cost analytics
  forge mcp          manage MCP server connections
  forge help         show this help

Flags (interactive mode):
  --server URL             connect to remote forge server
  --resume SESSION_ID      resume a session
  --skip-worktree          skip git worktree creation

Examples:
  forge                                        # start interactive session
  forge --skip-worktree                        # run in current directory
  forge --server http://localhost:3000         # connect to remote server
  forge stats --month 2026-04                  # show costs for April 2026
  forge mcp add datadog --url https://mcp.datadoghq.com/mcp --auth oauth
  forge mcp list                               # list MCP servers`)
}
