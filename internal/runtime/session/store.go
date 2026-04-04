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
// File handles are kept open for the lifetime of the session to avoid
// repeated open/close syscalls on every tool result.
type Store struct {
	baseDir string
	mu      sync.RWMutex
	files   map[string]*os.File // sessionID → open file handle
}

// NewStore creates a new session store.
func NewStore(baseDir string) *Store {
	return &Store{
		baseDir: baseDir,
		files:   make(map[string]*os.File),
	}
}

// getFile returns a persistent file handle for the session, creating it
// if necessary. Caller must hold s.mu (write lock).
func (s *Store) getFile(sessionID string) (*os.File, error) {
	if f, ok := s.files[sessionID]; ok {
		return f, nil
	}

	if err := os.MkdirAll(s.baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	path := filepath.Join(s.baseDir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}

	s.files[sessionID] = f
	return f, nil
}

// Append writes a session message to the JSONL file for the given session.
func (s *Store) Append(sessionID string, msg types.SessionMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.getFile(sessionID)
	if err != nil {
		return err
	}

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

	// Large tool results can produce long lines.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

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

// Close flushes and closes all open file handles.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	for id, f := range s.files {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(s.files, id)
	}
	return firstErr
}
