package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthServerMetadata holds RFC 8414 OAuth Authorization Server Metadata.
type AuthServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint,omitempty"`
	ResponseTypesSupported        []string `json:"response_types_supported,omitempty"`
	GrantTypesSupported           []string `json:"grant_types_supported,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
}

// ProtectedResourceMetadata holds RFC 9728 Protected Resource Metadata.
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// OAuthClient handles the full OAuth 2.1 flow with DCR for MCP servers.
type OAuthClient struct {
	httpClient *http.Client
	store      *TokenStore
	serverName string
}

// NewOAuthClient creates an OAuth client for a specific MCP server.
func NewOAuthClient(serverName string, store *TokenStore) *OAuthClient {
	return &OAuthClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		store:      store,
		serverName: serverName,
	}
}

// EnsureValidToken returns a valid access token, performing the OAuth flow if needed.
//
// Flow:
//  1. Check stored token — return if still valid
//  2. Try refresh — return if successful
//  3. Full authorization flow (DCR + PKCE)
func (o *OAuthClient) EnsureValidToken(ctx context.Context, mcpURL string) (string, error) {
	entry, err := o.store.Get(o.serverName)
	if err != nil {
		return "", fmt.Errorf("load stored token: %w", err)
	}

	if entry != nil && !entry.IsExpired() {
		return entry.AccessToken, nil
	}

	// Try refresh if we have a refresh token
	if entry != nil && entry.RefreshToken != "" {
		metadata, err := o.fetchAuthServerMetadata(entry.AuthServerURL)
		if err == nil {
			refreshed, err := o.refreshToken(metadata, entry)
			if err == nil {
				if storeErr := o.store.Put(o.serverName, refreshed); storeErr != nil {
					return "", fmt.Errorf("store refreshed token: %w", storeErr)
				}
				return refreshed.AccessToken, nil
			}
			log.Printf("[mcp:%s] token refresh failed, starting full auth flow: %v", o.serverName, err)
		}
	}

	// Full flow: discover → DCR → authorize
	return o.fullAuthFlow(ctx, mcpURL)
}

func (o *OAuthClient) fullAuthFlow(ctx context.Context, mcpURL string) (string, error) {
	// Step 1: Discover auth server via Protected Resource Metadata
	authServerURL, err := o.discoverAuthServer(mcpURL)
	if err != nil {
		return "", fmt.Errorf("discover auth server: %w", err)
	}

	// Step 2: Fetch auth server metadata
	metadata, err := o.fetchAuthServerMetadata(authServerURL)
	if err != nil {
		return "", fmt.Errorf("fetch auth server metadata: %w", err)
	}

	// Step 3: Dynamic Client Registration (if endpoint available)
	clientID, clientSecret, err := o.registerClient(metadata)
	if err != nil {
		return "", fmt.Errorf("DCR: %w", err)
	}

	// Step 4: Authorization Code + PKCE
	entry, err := o.authorizeWithPKCE(ctx, metadata, clientID, clientSecret, authServerURL)
	if err != nil {
		return "", fmt.Errorf("PKCE authorization: %w", err)
	}

	// Store everything
	if err := o.store.Put(o.serverName, entry); err != nil {
		return "", fmt.Errorf("store token: %w", err)
	}

	return entry.AccessToken, nil
}

// discoverAuthServer gets the authorization server URL by:
//  1. Attempting to fetch the MCP server's protected resource metadata
//     from {origin}/.well-known/oauth-protected-resource{path}
//  2. Falling back to hitting the MCP URL and parsing the 401 WWW-Authenticate header
func (o *OAuthClient) discoverAuthServer(mcpURL string) (string, error) {
	parsed, err := url.Parse(mcpURL)
	if err != nil {
		return "", fmt.Errorf("parse MCP URL: %w", err)
	}

	// Try RFC 9728: Protected Resource Metadata at well-known path
	prmURL := fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource%s", parsed.Scheme, parsed.Host, parsed.Path)
	resp, err := o.httpClient.Get(prmURL)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var prm ProtectedResourceMetadata
			if err := json.NewDecoder(resp.Body).Decode(&prm); err == nil && len(prm.AuthorizationServers) > 0 {
				return prm.AuthorizationServers[0], nil
			}
		}
	}

	// Fallback: hit MCP endpoint, expect 401 with WWW-Authenticate header
	req, err := http.NewRequest("GET", mcpURL, nil)
	if err != nil {
		return "", fmt.Errorf("build probe request: %w", err)
	}

	resp, err = o.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("probe MCP endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		return "", fmt.Errorf("expected 401 from MCP endpoint, got %d", resp.StatusCode)
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	return parseResourceMetadataURL(wwwAuth)
}

// parseResourceMetadataURL extracts the resource_metadata URL from a
// WWW-Authenticate header, then fetches it to get the authorization server.
func parseResourceMetadataURL(header string) (string, error) {
	if header == "" {
		return "", fmt.Errorf("no WWW-Authenticate header in 401 response")
	}

	// Parse: Bearer resource_metadata="https://..."
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "Bearer ") {
			part = strings.TrimPrefix(part, "Bearer ")
		}
		if strings.HasPrefix(part, "resource_metadata=") {
			val := strings.TrimPrefix(part, "resource_metadata=")
			val = strings.Trim(val, "\"")
			return val, nil
		}
	}

	return "", fmt.Errorf("no resource_metadata in WWW-Authenticate header: %s", header)
}

func (o *OAuthClient) fetchAuthServerMetadata(authServerURL string) (*AuthServerMetadata, error) {
	parsed, err := url.Parse(authServerURL)
	if err != nil {
		return nil, fmt.Errorf("parse auth server URL: %w", err)
	}

	wellKnown := fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server", parsed.Scheme, parsed.Host)
	if parsed.Path != "" && parsed.Path != "/" {
		wellKnown = fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server%s", parsed.Scheme, parsed.Host, parsed.Path)
	}

	resp, err := o.httpClient.Get(wellKnown)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", wellKnown, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth server metadata returned %d: %s", resp.StatusCode, body)
	}

	var metadata AuthServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("parse auth server metadata: %w", err)
	}

	return &metadata, nil
}

// registerClient performs Dynamic Client Registration (RFC 7591).
func (o *OAuthClient) registerClient(metadata *AuthServerMetadata) (clientID, clientSecret string, err error) {
	// Check if we already have registration for this server
	entry, _ := o.store.Get(o.serverName)
	if entry != nil && entry.ClientID != "" {
		return entry.ClientID, entry.ClientSecret, nil
	}

	if metadata.RegistrationEndpoint == "" {
		return "", "", fmt.Errorf("auth server has no registration_endpoint; manual client registration required")
	}

	regReq := map[string]any{
		"client_name":                "Forge Agent",
		"redirect_uris":              []string{"http://127.0.0.1/callback"},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "client_secret_post",
	}

	body, err := json.Marshal(regReq)
	if err != nil {
		return "", "", fmt.Errorf("marshal DCR request: %w", err)
	}

	resp, err := o.httpClient.Post(metadata.RegistrationEndpoint, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return "", "", fmt.Errorf("DCR request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("DCR returned %d: %s", resp.StatusCode, respBody)
	}

	var regResp struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return "", "", fmt.Errorf("parse DCR response: %w", err)
	}

	if regResp.ClientID == "" {
		return "", "", fmt.Errorf("DCR returned empty client_id")
	}

	return regResp.ClientID, regResp.ClientSecret, nil
}

// authorizeWithPKCE runs the Authorization Code + PKCE flow.
// Starts a local callback server, prints the auth URL, waits for the callback.
func (o *OAuthClient) authorizeWithPKCE(ctx context.Context, metadata *AuthServerMetadata, clientID, clientSecret, authServerURL string) (*TokenEntry, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, fmt.Errorf("generate PKCE: %w", err)
	}

	// Start local callback server on ephemeral port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start callback listener: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	state, err := generateRandomString(32)
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	// Build authorization URL
	authURL, err := url.Parse(metadata.AuthorizationEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse authorization endpoint: %w", err)
	}

	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	fmt.Fprintf(log.Writer(), "\n[mcp:%s] OAuth authorization required.\n", o.serverName)
	fmt.Fprintf(log.Writer(), "[mcp:%s] Open this URL in your browser:\n\n  %s\n\n", o.serverName, authURL.String())

	// Wait for callback
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch")
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			desc := r.URL.Query().Get("error_description")
			errCh <- fmt.Errorf("auth error: %s: %s", errParam, desc)
			fmt.Fprintf(w, "Authorization failed: %s\nYou can close this window.", desc)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code in callback")
			http.Error(w, "No code", http.StatusBadRequest)
			return
		}
		codeCh <- code
		fmt.Fprintf(w, "Authorization successful! You can close this window.")
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	defer srv.Close()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Exchange code for tokens
	return o.exchangeCode(metadata, clientID, clientSecret, code, redirectURI, verifier, authServerURL)
}

func (o *OAuthClient) exchangeCode(metadata *AuthServerMetadata, clientID, clientSecret, code, redirectURI, verifier, authServerURL string) (*TokenEntry, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"client_id":     {clientID},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}

	resp, err := o.httpClient.PostForm(metadata.TokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return &TokenEntry{
		AccessToken:          tokenResp.AccessToken,
		RefreshToken:         tokenResp.RefreshToken,
		TokenType:            tokenResp.TokenType,
		ExpiresAt:            expiresAt,
		ClientID:             clientID,
		ClientSecret:         clientSecret,
		RegistrationEndpoint: metadata.RegistrationEndpoint,
		AuthServerURL:        authServerURL,
	}, nil
}

// refreshToken exchanges a refresh token for a new access token.
func (o *OAuthClient) refreshToken(metadata *AuthServerMetadata, entry *TokenEntry) (*TokenEntry, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {entry.RefreshToken},
		"client_id":     {entry.ClientID},
	}
	if entry.ClientSecret != "" {
		form.Set("client_secret", entry.ClientSecret)
	}

	resp, err := o.httpClient.PostForm(metadata.TokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	refreshToken := tokenResp.RefreshToken
	if refreshToken == "" {
		refreshToken = entry.RefreshToken
	}

	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return &TokenEntry{
		AccessToken:          tokenResp.AccessToken,
		RefreshToken:         refreshToken,
		TokenType:            tokenResp.TokenType,
		ExpiresAt:            expiresAt,
		ClientID:             entry.ClientID,
		ClientSecret:         entry.ClientSecret,
		RegistrationEndpoint: entry.RegistrationEndpoint,
		AuthServerURL:        entry.AuthServerURL,
	}, nil
}

// generatePKCE creates a code_verifier and code_challenge (S256).
func generatePKCE() (verifier, challenge string, err error) {
	verifier, err = generateRandomString(64)
	if err != nil {
		return "", "", err
	}

	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}

// generateRandomString produces a URL-safe random string of the given byte length.
func generateRandomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
