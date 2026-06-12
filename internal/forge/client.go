// Package forge provides an HTTP client for the Forge gateway API.
package forge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Client is the Forge gateway operations interface used by the bridge.
type Client interface {
	CreateSession(ctx context.Context, cwd string, metadata map[string]any) (sessionID string, err error)
	SendMessage(ctx context.Context, sessionID, text string) error
	Interrupt(ctx context.Context, sessionID string) error
	SubscribeEvents(ctx context.Context, sessionID string) (<-chan types.OutboundEvent, error)
	Healthy(ctx context.Context) bool
}

// HTTPClient implements Client against a real Forge gateway.
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
	// sseClient is a long-lived client for SSE subscriptions. It shares a
	// connection pool across all sessions and has no overall request timeout
	// (SSE streams are long-lived by definition). We keep it as a struct field
	// instead of constructing one per call so we don't leak a fresh
	// http.Transport (and its socket pool) on every reconnect attempt — that
	// was the root cause of the 17k-TCP-conn incident on 2026-06-11.
	sseClient *http.Client
	logger    *slog.Logger
}

// NewHTTPClient creates a Forge gateway client.
func NewHTTPClient(baseURL string, logger *slog.Logger) *HTTPClient {
	sseTransport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		// Aggressive keep-alive: SSE connections are long-lived but when they
		// drop we want the idle ones cleaned up promptly, not lingering for
		// the OS default (~2h on macOS).
		DisableKeepAlives: false,
	}
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		sseClient: &http.Client{
			Transport: sseTransport,
			// No Timeout — SSE streams are long-lived. The request-level ctx
			// passed to SubscribeEvents handles cancellation.
		},
		logger: logger,
	}
}

func (c *HTTPClient) CreateSession(ctx context.Context, cwd string, metadata map[string]any) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"cwd":      cwd,
		"metadata": metadata,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/sessions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session: HTTP %d: %s", resp.StatusCode, b)
	}

	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode create session response: %w", err)
	}
	return result.SessionID, nil
}

func (c *HTTPClient) SendMessage(ctx context.Context, sessionID, text string) error {
	body, _ := json.Marshal(map[string]any{
		"text":   text,
		"source": "discord",
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/sessions/%s/messages", c.baseURL, sessionID),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send message: HTTP %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (c *HTTPClient) Interrupt(ctx context.Context, sessionID string) error {
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/sessions/%s/interrupt", c.baseURL, sessionID), nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("interrupt: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *HTTPClient) SubscribeEvents(ctx context.Context, sessionID string) (<-chan types.OutboundEvent, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/sessions/%s/events", c.baseURL, sessionID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	// Reuse the long-lived SSE client (shared transport, shared connection
	// pool). DO NOT construct a fresh http.Client here — see comment on
	// HTTPClient.sseClient.
	resp, err := c.sseClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscribe events: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("subscribe events: HTTP %d", resp.StatusCode)
	}

	ch := make(chan types.OutboundEvent, 64)
	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()
		c.readSSE(ctx, resp.Body, ch)
	}()

	return ch, nil
}

func (c *HTTPClient) readSSE(ctx context.Context, r io.Reader, ch chan<- types.OutboundEvent) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		var evt types.OutboundEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			c.logger.Warn("malformed SSE data", "error", err, "data", data)
			continue
		}

		select {
		case ch <- evt:
		case <-ctx.Done():
			return
		}
	}
}

func (c *HTTPClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
