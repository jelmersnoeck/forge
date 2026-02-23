package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// HTTP is a generic HTTP-based agent that sends requests to a remote endpoint.
type HTTP struct {
	// URL is the endpoint to POST agent requests to.
	URL string

	// AuthHeader is an optional Authorization header value (e.g., "Bearer <token>").
	AuthHeader string

	// Timeout is the HTTP client timeout. Zero means no timeout (context controls cancellation).
	Timeout time.Duration

	client *http.Client
}

// NewHTTP creates a new HTTP agent with the given configuration.
func NewHTTP(url, authHeader string, timeout time.Duration) *HTTP {
	return &HTTP{
		URL:        url,
		AuthHeader: authHeader,
		Timeout:    timeout,
		client:     &http.Client{Timeout: timeout},
	}
}

// Run executes a prompt by sending an HTTP POST request to the configured URL.
func (h *HTTP) Run(ctx context.Context, req Request) (*Response, error) {
	slog.InfoContext(ctx, "running http agent",
		"url", h.URL,
		"mode", req.Mode,
	)

	start := time.Now()

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("http agent: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http agent: create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if h.AuthHeader != "" {
		httpReq.Header.Set("Authorization", h.AuthHeader)
	}

	httpResp, err := h.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("http agent: %w", ctx.Err())
		}
		return nil, fmt.Errorf("http agent: send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("http agent: read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return &Response{
			Output:   string(respBody),
			ExitCode: 1,
			Duration: time.Since(start).Seconds(),
			Error:    fmt.Sprintf("http agent: status %d: %s", httpResp.StatusCode, string(respBody)),
		}, fmt.Errorf("http agent: unexpected status %d", httpResp.StatusCode)
	}

	var resp Response
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("http agent: parse response: %w", err)
	}

	resp.Duration = time.Since(start).Seconds()

	return &resp, nil
}
