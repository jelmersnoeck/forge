package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratePKCE(t *testing.T) {
	r := require.New(t)

	verifier, challenge, err := generatePKCE()
	r.NoError(err)

	// Verifier should be base64url encoded 64 random bytes
	r.Greater(len(verifier), 40)

	// Challenge should be S256(verifier)
	h := sha256.Sum256([]byte(verifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(h[:])
	r.Equal(wantChallenge, challenge)
}

func TestGenerateRandomString(t *testing.T) {
	r := require.New(t)

	s1, err := generateRandomString(32)
	r.NoError(err)
	r.Greater(len(s1), 0)

	s2, err := generateRandomString(32)
	r.NoError(err)
	r.NotEqual(s1, s2, "two random strings should differ (probabilistically)")
}

func TestParseResourceMetadataURL(t *testing.T) {
	tests := map[string]struct {
		header  string
		want    string
		wantErr bool
	}{
		"standard header": {
			header: `Bearer resource_metadata="https://auth.greendale.edu/.well-known/oauth-protected-resource"`,
			want:   "https://auth.greendale.edu/.well-known/oauth-protected-resource",
		},
		"with additional params": {
			header: `Bearer realm="Greendale", resource_metadata="https://auth.greendale.edu/prm"`,
			want:   "https://auth.greendale.edu/prm",
		},
		"empty header": {
			header:  "",
			wantErr: true,
		},
		"no resource_metadata": {
			header:  `Bearer realm="Greendale"`,
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got, err := parseResourceMetadataURL(tc.header)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

func TestDiscoverAuthServer(t *testing.T) {
	r := require.New(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(AuthServerMetadata{
				Issuer:                "https://auth.greendale.edu",
				AuthorizationEndpoint: "https://auth.greendale.edu/authorize",
				TokenEndpoint:         "https://auth.greendale.edu/token",
				RegistrationEndpoint:  "https://auth.greendale.edu/register",
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer authServer.Close()

	// Use a closure var so the handler can reference its own URL
	var mcpURL string
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			json.NewEncoder(w).Encode(ProtectedResourceMetadata{
				Resource:             mcpURL + "/mcp",
				AuthorizationServers: []string{authServer.URL},
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer mcpServer.Close()
	mcpURL = mcpServer.URL

	store := NewTokenStoreAt(t.TempDir() + "/tokens.json")
	oauth := NewOAuthClient("greendale", store)

	gotURL, err := oauth.discoverAuthServer(mcpServer.URL + "/mcp")
	r.NoError(err)
	r.Equal(authServer.URL, gotURL)
}

func TestDiscoverAuthServerViaWWWAuthenticate(t *testing.T) {
	r := require.New(t)

	// Use closure var for self-referential handler
	var mcpURL string
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			// PRM not available — fall through to 401 path
			http.NotFound(w, req)
		case "/mcp":
			w.Header().Set("WWW-Authenticate",
				`Bearer resource_metadata="`+mcpURL+`/auth-metadata"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/auth-metadata":
			// The resource_metadata URL points here; return the auth server URL
			w.Header().Set("Content-Type", "application/json")
			// parseResourceMetadataURL returns this URL directly
		default:
			http.NotFound(w, req)
		}
	}))
	defer mcpServer.Close()
	mcpURL = mcpServer.URL

	store := NewTokenStoreAt(t.TempDir() + "/tokens.json")
	oauth := NewOAuthClient("greendale", store)

	// This test verifies the fallback path: PRM 404 → probe endpoint → parse WWW-Authenticate
	gotURL, err := oauth.discoverAuthServer(mcpServer.URL + "/mcp")
	r.NoError(err)
	r.Equal(mcpURL+"/auth-metadata", gotURL)
}

func TestRegisterClient(t *testing.T) {
	r := require.New(t)

	regServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.Equal("POST", req.Method)
		r.Equal("application/json", req.Header.Get("Content-Type"))

		var body map[string]any
		json.NewDecoder(req.Body).Decode(&body)
		r.Equal("Forge Agent", body["client_name"])

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"client_id":     "human-being-mascot",
			"client_secret": "six-seasons-and-a-movie",
		})
	}))
	defer regServer.Close()

	store := NewTokenStoreAt(t.TempDir() + "/tokens.json")
	oauth := NewOAuthClient("greendale", store)

	metadata := &AuthServerMetadata{
		RegistrationEndpoint: regServer.URL,
	}

	clientID, clientSecret, err := oauth.registerClient(metadata, "http://127.0.0.1:12345/callback")
	r.NoError(err)
	r.Equal("human-being-mascot", clientID)
	r.Equal("six-seasons-and-a-movie", clientSecret)
}

func TestRefreshToken(t *testing.T) {
	r := require.New(t)

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.Equal("POST", req.Method)
		r.NoError(req.ParseForm())
		r.Equal("refresh_token", req.FormValue("grant_type"))
		r.Equal("old-refresh-token", req.FormValue("refresh_token"))
		r.Equal("study-group-7", req.FormValue("client_id"))

		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	store := NewTokenStoreAt(t.TempDir() + "/tokens.json")
	oauth := NewOAuthClient("greendale", store)

	metadata := &AuthServerMetadata{
		TokenEndpoint: tokenServer.URL,
	}

	entry := &TokenEntry{
		RefreshToken: "old-refresh-token",
		ClientID:     "study-group-7",
	}

	got, err := oauth.refreshToken(metadata, entry)
	r.NoError(err)
	r.Equal("new-access-token", got.AccessToken)
	r.Equal("new-refresh-token", got.RefreshToken)
	r.Equal("Bearer", got.TokenType)
	r.False(got.IsExpired())
}
