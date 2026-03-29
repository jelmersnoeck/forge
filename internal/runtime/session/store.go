// Package session provides JSONL-based session persistence.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Store persists session messages to JSONL files.
type Store struct {
	baseDir string
	mu      sync.RWMutex
}

// NewStore creates a new session store.
func NewStore(baseDir string) *Store {
	return &Store{
		baseDir: baseDir,
	}
}

// Append writes a session message to the JSONL file for the given session.
func (s *Store) Append(sessionID string, msg types.SessionMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure base directory exists
	if err := os.MkdirAll(s.baseDir, 0755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	path := filepath.Join(s.baseDir, sessionID+".jsonl")

	// Open file in append mode
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}
	defer f.Close()

	// Write JSON line
	if err := json.NewEncoder(f).Encode(msg); err != nil {
		return fmt.Errorf("encode session message: %w", err)
	}

	return nil
}

// Load reads all session messages from the JSONL file for the given session.
// Returns an empty slice if the session file does not exist.
func (s *Store) Load(sessionID string) ([]types.SessionMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.baseDir, sessionID+".jsonl")

	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return []types.SessionMessage{}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}
	defer f.Close()

	var messages []types.SessionMessage
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		var msg types.SessionMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			return nil, fmt.Errorf("decode session message: %w", err)
		}
		messages = append(messages, msg)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan session file: %w", err)
	}

	return messages, nil
}
