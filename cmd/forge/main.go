// Forge — async coding agent
//
// Usage:
//
//	forge              interactive CLI (default)
//	forge agent        run agent server
//	forge server       run gateway server
//	forge stats        show cost analytics
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
	case "help", "-h", "--help":
		printHelp()
		os.Exit(0)
	default:
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
  forge help         show this help

Examples:
  forge                              # start interactive session
  forge --server http://localhost:3000  # connect to remote server
  forge stats --month 2026-04       # show costs for April 2026
  forge agent --port 8080           # run agent on port 8080
  forge server -daemon              # run server in background`)
}
