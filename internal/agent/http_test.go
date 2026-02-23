package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTP_RunSuccess(t *testing.T) {
	want := Response{
		Output:   "generated code",
		ExitCode: 0,
		Cost: &Cost{
			InputTokens:  200,
			OutputTokens: 100,
			TotalCost:    0.01,
		},
		Files: []string{"main.go"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request.
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.Prompt != "implement feature" {
			t.Errorf("Prompt = %q, want %q", req.Prompt, "implement feature")
		}
		if req.Mode != ModeCode {
			t.Errorf("Mode = %q, want %q", req.Mode, ModeCode)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()

	agent := NewHTTP(server.URL, "", 5*time.Second)

	resp, err := agent.Run(context.Background(), Request{
		Prompt: "implement feature",
		Mode:   ModeCode,
	})
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if resp.Output != want.Output {
		t.Errorf("Output = %q, want %q", resp.Output, want.Output)
	}
	if resp.Cost == nil {
		t.Fatal("Cost is nil, expected non-nil")
	}
	if resp.Cost.InputTokens != 200 {
		t.Errorf("InputTokens = %d, want 200", resp.Cost.InputTokens)
	}
	if len(resp.Files) != 1 || resp.Files[0] != "main.go" {
		t.Errorf("Files = %v, want [main.go]", resp.Files)
	}
}

func TestHTTP_RunWithAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Response{Output: "ok"})
	}))
	defer server.Close()

	agent := NewHTTP(server.URL, "Bearer test-token", 5*time.Second)

	resp, err := agent.Run(context.Background(), Request{
		Prompt: "test",
		Mode:   ModePlan,
	})
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if resp.Output != "ok" {
		t.Errorf("Output = %q, want %q", resp.Output, "ok")
	}
}

func TestHTTP_RunServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	agent := NewHTTP(server.URL, "", 5*time.Second)

	resp, err := agent.Run(context.Background(), Request{
		Prompt: "test",
		Mode:   ModeCode,
	})
	if err == nil {
		t.Fatal("Run() expected error for 500 response, got nil")
	}
	if resp == nil {
		t.Fatal("Run() expected non-nil response even on error")
	}
	if resp.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", resp.ExitCode)
	}
}

func TestHTTP_RunInvalidURL(t *testing.T) {
	agent := NewHTTP("http://localhost:0/nonexistent", "", 1*time.Second)

	_, err := agent.Run(context.Background(), Request{
		Prompt: "test",
	})
	if err == nil {
		t.Fatal("Run() expected error for invalid URL, got nil")
	}
}

func TestHTTP_RunContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // Delay to trigger context cancellation.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Response{Output: "late"})
	}))
	defer server.Close()

	agent := NewHTTP(server.URL, "", 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := agent.Run(ctx, Request{Prompt: "test"})
	if err == nil {
		t.Fatal("Run() expected error for cancelled context, got nil")
	}
}

func TestHTTP_RunInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	agent := NewHTTP(server.URL, "", 5*time.Second)

	_, err := agent.Run(context.Background(), Request{Prompt: "test"})
	if err == nil {
		t.Fatal("Run() expected error for invalid JSON response, got nil")
	}
}

// Verify HTTP implements Agent interface.
var _ Agent = (*HTTP)(nil)
