package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	servePort   int
	serveConfig string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Forge HTTP server",
	Long: `Start the Forge HTTP server for webhook-driven builds and API access.

The server exposes REST endpoints for triggering builds, viewing status,
and receiving webhooks from GitHub, Jira, and Linear. It also provides
SSE endpoints for real-time build progress streaming.

This feature is planned for Phase 3. See the architecture documentation
for the full server design.

Example:
  forge serve --port 8080
  forge serve --port 9090 --config /path/to/config.yaml`,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 8080, "HTTP server port")
	serveCmd.Flags().StringVar(&serveConfig, "serve-config", "", "Config file path (overrides --config)")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	fmt.Printf("Server mode not yet implemented (port: %d).\n", servePort)
	fmt.Println("This feature is planned for Phase 3. See forge-architecture-v0.md for details.")
	return nil
}
