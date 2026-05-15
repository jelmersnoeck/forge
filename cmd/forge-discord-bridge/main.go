// Command forge-discord-bridge connects Discord threads to Forge sessions.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jelmersnoeck/forge/internal/bridge"
	"github.com/jelmersnoeck/forge/internal/discord"
	"github.com/jelmersnoeck/forge/internal/forge"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLogLevel(getenv("BRIDGE_LOG_LEVEL", "info")),
	}))

	// Required env
	discordToken := requireEnv("DISCORD_BOT_TOKEN")
	guildID := requireEnv("DISCORD_GUILD_ID")
	forgeURL := requireEnv("FORGE_GATEWAY_URL")

	// Optional env
	listenAddr := getenv("BRIDGE_LISTEN_ADDR", ":8080")
	channelsPath := getenv("BRIDGE_CHANNELS_PATH", "/config/channels.json")
	showThinking := getenv("BRIDGE_SHOW_THINKING", "false") == "true"
	revealSession := getenv("BRIDGE_REVEAL_SESSION_ID", "false") == "true"
	adminToken := os.Getenv("BRIDGE_ADMIN_TOKEN")

	// Init config
	cfg := &bridge.Config{
		GuildID:         guildID,
		ForgeGatewayURL: forgeURL,
		ShowThinking:    showThinking,
		RevealSessionID: revealSession,
		AdminToken:      adminToken,
	}

	if err := cfg.LoadChannels(channelsPath); err != nil {
		logger.Error("failed to load channels config", "path", channelsPath, "error", err)
		os.Exit(1)
	}
	cfg.WatchConfig(channelsPath)

	// Init clients
	dc, err := discord.NewLiveClient(discordToken, guildID, logger)
	if err != nil {
		logger.Error("failed to connect to discord", "error", err)
		os.Exit(1)
	}
	defer dc.Close()

	fc := forge.NewHTTPClient(forgeURL, logger)

	// Init bridge (stateless — no database)
	b := bridge.New(fc, dc, cfg, logger)

	// Admin HTTP server
	admin := bridge.NewAdminServer(b, adminToken)

	go func() {
		logger.Info("admin server listening", "addr", listenAddr)
		if err := http.ListenAndServe(listenAddr, admin.Handler()); err != nil {
			logger.Error("admin server error", "error", err)
		}
	}()

	// Graceful shutdown with 5s grace window for retry queue
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutting down (5s grace for pending messages)")
		cancel()
		// Give the retry queue drain goroutine its 5s grace period
		time.Sleep(6 * time.Second)
		os.Exit(0)
	}()

	admin.SetReady()
	logger.Info("bridge starting",
		"guild", guildID,
		"forge_url", forgeURL,
		"listen", listenAddr)

	if err := b.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("bridge error", "error", err)
		os.Exit(1)
	}
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("required env var not set", "key", key)
		os.Exit(1)
	}
	return v
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
