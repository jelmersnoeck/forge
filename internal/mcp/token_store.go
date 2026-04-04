package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenEntry holds OAuth credentials and DCR registration for a single MCP server.
type TokenEntry struct {
	AccessToken          string    `json:"access_token"`
	RefreshToken         string    `json:"refresh_token,omitempty"`
	TokenType            string    `json:"token_type"`
	ExpiresAt            time.Time `json:"expires_at"`
	ClientID             string    `json:"client_id"`
	ClientSecret         string    `json:"client_secret,omitempty"`
	RegistrationEndpoint string    `json:"registration_endpoint,omitempty"`
	AuthServerURL        string    `json:"auth_server_url,omitempty"`
}

// IsExpired reports whether the token has expired (with 30s grace period).
func (e *TokenEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt.Add(-30 * time.Second))
}

// tokenStoreData is the on-disk JSON structure.
type tokenStoreData struct {
	Servers map[string]*TokenEntry `json:"servers"`
}

// TokenStore manages OAuth tokens for MCP servers.
// Tokens are persisted to ~/.forge/mcp-tokens.json.
type TokenStore struct {
	mu   sync.Mutex
	path string
}

// NewTokenStore creates a token store at the default location (~/.forge/mcp-tokens.json).
func NewTokenStore() (*TokenStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	dir := filepath.Join(home, ".forge")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create forge dir: %w", err)
	}

	return &TokenStore{
		path: filepath.Join(dir, "mcp-tokens.json"),
	}, nil
}

// NewTokenStoreAt creates a token store at a specific path (useful for testing).
func NewTokenStoreAt(path string) *TokenStore {
	return &TokenStore{path: path}
}

func (s *TokenStore) load() (*tokenStoreData, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &tokenStoreData{Servers: make(map[string]*TokenEntry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read token store: %w", err)
	}

	var store tokenStoreData
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parse token store: %w", err)
	}
	if store.Servers == nil {
		store.Servers = make(map[string]*TokenEntry)
	}

	return &store, nil
}

func (s *TokenStore) save(store *tokenStoreData) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token store: %w", err)
	}

	return os.WriteFile(s.path, data, 0o600)
}

// Get retrieves the token entry for a server. Returns nil if not found.
func (s *TokenStore) Get(serverName string) (*TokenEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.load()
	if err != nil {
		return nil, err
	}

	return store.Servers[serverName], nil
}

// Put stores a token entry for a server.
func (s *TokenStore) Put(serverName string, entry *TokenEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.load()
	if err != nil {
		return err
	}

	store.Servers[serverName] = entry
	return s.save(store)
}

// Delete removes a token entry for a server.
func (s *TokenStore) Delete(serverName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.load()
	if err != nil {
		return err
	}

	delete(store.Servers, serverName)
	return s.save(store)
}
