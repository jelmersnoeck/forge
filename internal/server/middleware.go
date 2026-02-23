package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"
)

const requestIDHeader = "X-Request-ID"

// requestIDMiddleware assigns a unique ID to each request and adds it to the
// response headers and logger.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs each request with method, path, status, and duration.
func loggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration", time.Since(start).String(),
				"request_id", w.Header().Get(requestIDHeader),
			)
		})
	}
}

// panicRecovery catches panics, logs them, and returns 500 without leaking
// stack traces to clients.
func panicRecovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"error", fmt.Sprintf("%v", rec),
						"stack", string(debug.Stack()),
					)
					http.Error(w, `{"error":"internal server error","code":"INTERNAL_ERROR"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// corsMiddleware adds CORS headers for cross-origin requests. Only origins in
// the allowedOrigins list are permitted. If the list is empty, no
// Access-Control-Allow-Origin header is set, which effectively rejects
// cross-origin requests.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
				w.Header().Set("Vary", "Origin")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimiter implements a simple token-bucket rate limiter per IP.
type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int           // Max requests per window.
	window   time.Duration // Window duration.
	stop     chan struct{}  // Closed to signal cleanup goroutine to exit.
}

type visitor struct {
	tokens    int
	lastReset time.Time
}

func newRateLimiter(rate int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
		stop:     make(chan struct{}),
	}
	// Background cleanup of old entries.
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, ok := rl.visitors[ip]
	now := time.Now()
	if !ok || now.Sub(v.lastReset) > rl.window {
		rl.visitors[ip] = &visitor{tokens: rl.rate - 1, lastReset: now}
		return true
	}
	if v.tokens <= 0 {
		return false
	}
	v.tokens--
	return true
}

func (rl *rateLimiter) cleanup() {
	ticker := time.NewTicker(rl.window * 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, v := range rl.visitors {
				if now.Sub(v.lastReset) > rl.window*2 {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.stop:
			return
		}
	}
}

// Stop signals the background cleanup goroutine to exit.
func (rl *rateLimiter) Stop() {
	close(rl.stop)
}

// stripPort extracts the IP address from a RemoteAddr string, removing the
// port suffix. For addresses that cannot be parsed, the original string is
// returned as-is.
func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := stripPort(r.RemoteAddr)
		if !rl.allow(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"error":"rate limit exceeded","code":"RATE_LIMITED"}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
