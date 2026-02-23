package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCorsMiddleware_AllowedOrigin(t *testing.T) {
	handler := corsMiddleware([]string{"https://allowed.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://allowed.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "https://allowed.com" {
		t.Errorf("expected Access-Control-Allow-Origin to be https://allowed.com, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Errorf("expected Vary: Origin header, got %q", w.Header().Get("Vary"))
	}
}

func TestCorsMiddleware_DisallowedOrigin(t *testing.T) {
	handler := corsMiddleware([]string{"https://allowed.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no Access-Control-Allow-Origin for disallowed origin, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCorsMiddleware_NoOrigins(t *testing.T) {
	handler := corsMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://any.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no Access-Control-Allow-Origin when no origins configured, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCorsMiddleware_Preflight(t *testing.T) {
	handler := corsMiddleware([]string{"https://allowed.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called for OPTIONS preflight")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "https://allowed.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "https://allowed.com" {
		t.Errorf("expected CORS header on preflight, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCorsMiddleware_MultipleOrigins(t *testing.T) {
	handler := corsMiddleware([]string{"https://a.com", "https://b.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		origin string
		want   string
	}{
		{"https://a.com", "https://a.com"},
		{"https://b.com", "https://b.com"},
		{"https://c.com", ""},
	}

	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Origin", tt.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			got := w.Header().Get("Access-Control-Allow-Origin")
			if got != tt.want {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRateLimiter_UsesRemoteAddr(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	defer rl.Stop()

	handler := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Make 2 requests from same IP (should succeed).
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, w.Code)
		}
	}

	// Third request should be rate limited.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestRateLimiter_IgnoresXForwardedFor(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	defer rl.Stop()

	handler := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request from IP.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Second request from same IP with a different X-Forwarded-For should
	// still be rate limited (X-Forwarded-For must be ignored).
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "10.0.0.1:5001"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 (X-Forwarded-For should be ignored), got %d", w.Code)
	}
}

func TestRateLimiter_DifferentIPsNotLimited(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	defer rl.Stop()

	handler := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = fmt.Sprintf("10.0.0.%d:5000", i+1)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request from 10.0.0.%d: expected 200, got %d", i+1, w.Code)
		}
	}
}

func TestRateLimiter_Stop(t *testing.T) {
	rl := newRateLimiter(10, 50*time.Millisecond)

	// Stop should not panic and the goroutine should terminate.
	rl.Stop()

	// Allow should still work after stop (just no background cleanup).
	if !rl.allow("1.2.3.4") {
		t.Error("allow should still work after Stop")
	}
}

func TestStripPort(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"192.168.1.1:8080", "192.168.1.1"},
		{"[::1]:8080", "::1"},
		{"10.0.0.1:0", "10.0.0.1"},
		{"bare-address", "bare-address"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := stripPort(tt.addr)
			if got != tt.want {
				t.Errorf("stripPort(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}
