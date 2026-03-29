package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Config holds agent server settings.
type Config struct {
	Port        int
	CWD         string
	SessionID   string
	SessionsDir string
}

// Start creates a Hub, starts the Worker in a background goroutine,
// and runs the HTTP server. It blocks until the server exits.
func Start(cfg Config) error {
	hub := NewHub()

	worker := NewWorker(hub, cfg.SessionID, cfg.CWD, cfg.SessionsDir)
	go worker.Run(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(cfg.SessionID))
	mux.HandleFunc("POST /messages", handleMessages(hub, cfg.SessionID))
	mux.HandleFunc("POST /interrupt", handleInterrupt(hub))
	mux.HandleFunc("GET /events", handleSSE(hub))

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("[agent] listening on %s (session=%s)", addr, cfg.SessionID)
	return http.ListenAndServe(addr, mux)
}

func handleHealth(sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":    "ok",
			"sessionId": sessionID,
		})
	}
}

func handleMessages(hub *Hub, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text   string `json:"text"`
			User   string `json:"user"`
			Source string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if body.Text == "" {
			http.Error(w, `{"error":"text is required"}`, http.StatusBadRequest)
			return
		}
		if body.User == "" {
			body.User = "anonymous"
		}
		if body.Source == "" {
			body.Source = "api"
		}

		immediate := hub.PushMessage(types.InboundMessage{
			SessionID: sessionID,
			Text:      body.Text,
			User:      body.User,
			Source:    body.Source,
			Timestamp: time.Now().UnixMilli(),
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		
		status := "queued"
		if immediate {
			status = "processing"
		}
		json.NewEncoder(w).Encode(map[string]string{"status": status})
	}
}

func handleInterrupt(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hub.TriggerInterrupt()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "interrupted"})
	}
}

func handleSSE(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher.Flush()

		events, unsub := hub.Subscribe()
		defer unsub()

		for {
			select {
			case event, ok := <-events:
				if !ok {
					return
				}
				data, _ := json.Marshal(event)
				fmt.Fprintf(w, "id: %s\ndata: %s\n\n", event.ID, data)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}
