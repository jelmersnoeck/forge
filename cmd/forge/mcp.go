package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jelmersnoeck/forge/internal/mcp"
)

func runMCP(args []string) int {
	if len(args) < 2 {
		printMCPHelp()
		return 1
	}

	switch args[1] {
	case "add":
		return runMCPAdd(args[1:])
	case "remove", "rm":
		return runMCPRemove(args[1:])
	case "list", "ls":
		return runMCPList(args[1:])
	case "login":
		return runMCPLogin(args[1:])
	case "help", "-h", "--help":
		printMCPHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Unknown mcp command: %s\n\n", args[1])
		printMCPHelp()
		return 1
	}
}

func runMCPAdd(args []string) int {
	fs := flag.NewFlagSet("forge mcp add", flag.ExitOnError)
	urlFlag := fs.String("url", "", "MCP server URL (required)")
	authFlag := fs.String("auth", "", "authentication method (oauth)")
	projectFlag := fs.Bool("project", false, "add to project config (.forge/mcp.json) instead of user config")
	var headerFlags headerSlice
	fs.Var(&headerFlags, "header", "HTTP header as Key=Value (repeatable)")

	// Extract positional name from anywhere in the args, pass rest to flag parser.
	// Supports both: "add datadog --url ..." and "add --url ... datadog"
	name, flagArgs := extractPositional(args[1:])
	fs.Parse(flagArgs)

	// If name wasn't before flags, check remaining args after flag parsing
	if name == "" {
		if fs.NArg() > 0 {
			name = fs.Arg(0)
		} else {
			fmt.Fprintln(os.Stderr, "Usage: forge mcp add <name> --url <url> [--auth oauth] [--header Key=Value ...]")
			return 1
		}
	}

	if *urlFlag == "" {
		fmt.Fprintln(os.Stderr, "Error: --url is required")
		return 1
	}

	server := mcp.MCPServerConfig{
		URL:  *urlFlag,
		Auth: *authFlag,
	}

	if len(headerFlags) > 0 {
		server.Headers = make(map[string]string)
		for _, h := range headerFlags {
			k, v, ok := strings.Cut(h, "=")
			if !ok {
				fmt.Fprintf(os.Stderr, "Error: invalid header format %q (expected Key=Value)\n", h)
				return 1
			}
			server.Headers[k] = v
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if err := mcp.AddServer(name, server, *projectFlag, cwd); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	scope := "user"
	if *projectFlag {
		scope = "project"
	}
	fmt.Printf("Added MCP server %q (%s) to %s config\n", name, *urlFlag, scope)

	// Run OAuth flow immediately so the user authenticates now, not later
	if *authFlag == "oauth" {
		fmt.Printf("Authenticating with %q...\n", name)
		ctx := context.Background()
		err := mcp.Authenticate(ctx, name, *urlFlag, func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: authentication failed: %v\n", err)
			fmt.Fprintf(os.Stderr, "You can retry later with: forge mcp login %s\n", name)
			return 0
		}
	}

	return 0
}

func runMCPRemove(args []string) int {
	fs := flag.NewFlagSet("forge mcp remove", flag.ExitOnError)
	projectFlag := fs.Bool("project", false, "remove from project config instead of user config")

	name, flagArgs := extractPositional(args[1:])
	fs.Parse(flagArgs)

	if name == "" {
		if fs.NArg() > 0 {
			name = fs.Arg(0)
		} else {
			fmt.Fprintln(os.Stderr, "Usage: forge mcp remove <name> [--project]")
			return 1
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if err := mcp.RemoveServer(name, *projectFlag, cwd); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Printf("Removed MCP server %q\n", name)
	return 0
}

func runMCPList(args []string) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	cfg, err := mcp.ListServers(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if len(cfg.Servers) == 0 {
		fmt.Println("No MCP servers configured.")
		fmt.Println("\nAdd one with: forge mcp add <name> --url <url>")
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tURL\tAUTH")
	for name, server := range cfg.Servers {
		auth := "-"
		switch {
		case server.Auth != "":
			auth = server.Auth
		case len(server.Headers) > 0:
			auth = "headers"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", name, server.URL, auth)
	}
	tw.Flush()

	return 0
}

func runMCPLogin(args []string) int {
	name, _ := extractPositional(args[1:])
	if name == "" {
		fmt.Fprintln(os.Stderr, "Usage: forge mcp login <name>")
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	cfg, err := mcp.ListServers(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	server, ok := cfg.Servers[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: MCP server %q not found. Run 'forge mcp list' to see configured servers.\n", name)
		return 1
	}

	if server.Auth != "oauth" {
		fmt.Fprintf(os.Stderr, "Error: MCP server %q does not use OAuth (auth=%q)\n", name, server.Auth)
		return 1
	}

	fmt.Printf("Authenticating with %q...\n", name)
	ctx := context.Background()
	err = mcp.Authenticate(ctx, name, server.URL, func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}

func printMCPHelp() {
	fmt.Println(`forge mcp — manage MCP server connections

Usage:
  forge mcp add <name> --url <url> [flags]    add a server (runs auth if --auth oauth)
  forge mcp remove <name> [--project]          remove a server
  forge mcp list                               list configured servers
  forge mcp login <name>                       re-authenticate with an OAuth server

Add flags:
  --url URL          MCP server URL (required)
  --auth METHOD      authentication: "oauth" for OAuth 2.1 + DCR
  --header Key=Val   static HTTP header (repeatable)
  --project          write to .forge/mcp.json instead of ~/.forge/mcp.json

Examples:
  forge mcp add datadog --url https://mcp.datadoghq.com/mcp --auth oauth
  forge mcp add internal --url https://tools.internal/mcp --header Authorization="Bearer sk-..."
  forge mcp add local-dev --url http://localhost:8080/mcp --project
  forge mcp login datadog
  forge mcp remove datadog
  forge mcp list`)
}

// headerSlice implements flag.Value for repeatable --header flags.
type headerSlice []string

func (h *headerSlice) String() string { return strings.Join(*h, ", ") }
func (h *headerSlice) Set(val string) error {
	*h = append(*h, val)
	return nil
}

// extractPositional pulls the first non-flag argument from args,
// returning it separately so Go's flag package can parse the rest.
// Handles: "name --flag val" and "--flag val name"
func extractPositional(args []string) (positional string, remaining []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			remaining = append(remaining, arg)
			// Skip the next arg if this flag takes a value (not a bool flag).
			// Heuristic: if next arg doesn't start with -, it's the flag's value.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.Contains(arg, "=") {
				i++
				remaining = append(remaining, args[i])
			}
			continue
		}
		if positional == "" {
			positional = arg
			continue
		}
		remaining = append(remaining, arg)
	}
	return positional, remaining
}
