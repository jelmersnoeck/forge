package mcp

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTokenStore(t *testing.T) {
	tests := map[string]struct {
		setup func(s *TokenStore)
		check func(r *require.Assertions, s *TokenStore)
	}{
		"get returns nil for nonexistent server": {
			check: func(r *require.Assertions, s *TokenStore) {
				entry, err := s.Get("senor-chang")
				r.NoError(err)
				r.Nil(entry)
			},
		},
		"put and get round-trip": {
			setup: func(s *TokenStore) {
				entry := &TokenEntry{
					AccessToken:  "troy-and-abed-in-the-morning",
					RefreshToken: "cool-cool-cool",
					TokenType:    "Bearer",
					ExpiresAt:    time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC),
					ClientID:     "greendale-client",
					ClientSecret: "e-pluribus-anus",
				}
				if err := s.Put("greendale", entry); err != nil {
					panic(err)
				}
			},
			check: func(r *require.Assertions, s *TokenStore) {
				entry, err := s.Get("greendale")
				r.NoError(err)
				r.NotNil(entry)
				r.Equal("troy-and-abed-in-the-morning", entry.AccessToken)
				r.Equal("cool-cool-cool", entry.RefreshToken)
				r.Equal("greendale-client", entry.ClientID)
				r.Equal("e-pluribus-anus", entry.ClientSecret)
			},
		},
		"delete removes entry": {
			setup: func(s *TokenStore) {
				entry := &TokenEntry{
					AccessToken: "temporary",
					TokenType:   "Bearer",
					ExpiresAt:   time.Now().Add(time.Hour),
				}
				if err := s.Put("city-college", entry); err != nil {
					panic(err)
				}
			},
			check: func(r *require.Assertions, s *TokenStore) {
				r.NoError(s.Delete("city-college"))
				entry, err := s.Get("city-college")
				r.NoError(err)
				r.Nil(entry)
			},
		},
		"multiple servers coexist": {
			setup: func(s *TokenStore) {
				_ = s.Put("greendale", &TokenEntry{AccessToken: "go-human-beings", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)})
				_ = s.Put("city-college", &TokenEntry{AccessToken: "go-bulldogs", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)})
			},
			check: func(r *require.Assertions, s *TokenStore) {
				g, err := s.Get("greendale")
				r.NoError(err)
				r.Equal("go-human-beings", g.AccessToken)

				c, err := s.Get("city-college")
				r.NoError(err)
				r.Equal("go-bulldogs", c.AccessToken)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir := t.TempDir()
			store := NewTokenStoreAt(filepath.Join(dir, "tokens.json"))

			if tc.setup != nil {
				tc.setup(store)
			}

			// Create a fresh store instance to test persistence
			store2 := NewTokenStoreAt(filepath.Join(dir, "tokens.json"))
			tc.check(r, store2)
		})
	}
}

func TestTokenEntry_IsExpired(t *testing.T) {
	tests := map[string]struct {
		expiresAt time.Time
		want      bool
	}{
		"future token is not expired": {
			expiresAt: time.Now().Add(5 * time.Minute),
			want:      false,
		},
		"past token is expired": {
			expiresAt: time.Now().Add(-5 * time.Minute),
			want:      true,
		},
		"within 30s grace is expired": {
			expiresAt: time.Now().Add(15 * time.Second),
			want:      true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			entry := &TokenEntry{ExpiresAt: tc.expiresAt}
			r.Equal(tc.want, entry.IsExpired())
		})
	}
}
