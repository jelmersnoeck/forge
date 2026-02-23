package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jelmersnoeck/forge/internal/server"
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
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// Override port from flag if set.
	if cmd.Flags().Changed("port") {
		cfg.Server.Port = servePort
	}

	eng, err := buildEngine(cfg)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	srv := server.New(eng, &cfg.Server, logger)

	// Set up context with signal handling for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return srv.Start(ctx)
}
