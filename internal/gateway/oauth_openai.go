package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	oauthStateTTL     = 10 * time.Minute
	oauthTokenFile    = "openai_oauth_token.json"
	oauthCodeVerifierLen = 32
)

// openAIOAuthState holds PKCE state for one OAuth flow.
type openAIOAuthState struct {
	CodeVerifier string
	CreatedAt    time.Time
}

var (
	oauthStateMu sync.Mutex
	oauthStates  = make(map[string]openAIOAuthState)
)

func init() {
	go func() {
		tick := time.NewTicker(2 * time.Minute)
		defer tick.Stop()
		for range tick.C {
			oauthStateMu.Lock()
			now := time.Now()
			for s, v := range oauthStates {
				if now.Sub(v.CreatedAt) > oauthStateTTL {
					delete(oauthStates, s)
				}
			}
			oauthStateMu.Unlock()
		}
	}()
}

// OpenAIOAuthToken is the stored OAuth token set.
type OpenAIOAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type,omitempty"`
}

// GeneratePKCE returns code_verifier and code_challenge (S256) for PKCE.
func GeneratePKCE() (verifier, challenge string, err error) {
	b := make([]byte, oauthCodeVerifierLen)
	if _, err = io.ReadFull(rand.Reader, b); err != nil {
		return "", "", fmt.Errorf("pkce verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])
	return verifier, challenge, nil
}

// StoreOAuthState saves state -> code_verifier for callback validation.
func StoreOAuthState(state, codeVerifier string) {
	oauthStateMu.Lock()
	defer oauthStateMu.Unlock()
	oauthStates[state] = openAIOAuthState{CodeVerifier: codeVerifier, CreatedAt: time.Now()}
}

// ConsumeOAuthState returns the code_verifier for state and removes it. Returns "" if invalid/expired.
func ConsumeOAuthState(state string) string {
	oauthStateMu.Lock()
	defer oauthStateMu.Unlock()
	s, ok := oauthStates[state]
	if !ok {
		return ""
	}
	if time.Since(s.CreatedAt) > oauthStateTTL {
		delete(oauthStates, state)
		return ""
	}
	delete(oauthStates, state)
	return s.CodeVerifier
}

// OpenAIOAuthTokenPath returns the path to the stored token file.
func OpenAIOAuthTokenPath(dataDir string) string {
	return filepath.Join(dataDir, oauthTokenFile)
}

// ReadOpenAIOAuthToken reads the stored token from dataDir. Returns nil if missing or invalid.
func ReadOpenAIOAuthToken(dataDir string) (*OpenAIOAuthToken, error) {
	path := OpenAIOAuthTokenPath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var t OpenAIOAuthToken
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	if t.AccessToken == "" {
		return nil, nil
	}
	return &t, nil
}

// WriteOpenAIOAuthToken writes the token to dataDir with 0600.
func WriteOpenAIOAuthToken(dataDir string, t *OpenAIOAuthToken) error {
	if t == nil {
		return fmt.Errorf("token is nil")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	path := OpenAIOAuthTokenPath(dataDir)
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// ClearOpenAIOAuthToken removes the stored token file.
func ClearOpenAIOAuthToken(dataDir string) error {
	path := OpenAIOAuthTokenPath(dataDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// GetValidOpenAIOAuthAccessToken returns a valid access token, refreshing if needed. Returns "" if none or invalid.
func GetValidOpenAIOAuthAccessToken(dataDir string, cfg *OpenAIOAuthConfig, refreshTokenURL string) string {
	t, err := ReadOpenAIOAuthToken(dataDir)
	if err != nil || t == nil {
		return ""
	}
	// Consider expired 60s before actual expiry to avoid race.
	if time.Until(t.ExpiresAt) > 60*time.Second {
		return t.AccessToken
	}
	if t.RefreshToken == "" || refreshTokenURL == "" {
		return ""
	}
	newT, err := refreshOpenAIOAuthToken(refreshTokenURL, cfg.ClientID, cfg.ClientSecret, t.RefreshToken)
	if err != nil || newT == nil {
		return t.AccessToken // best effort: use old token
	}
	_ = WriteOpenAIOAuthToken(dataDir, newT)
	return newT.AccessToken
}

// ExchangeOpenAIOAuthCode exchanges the authorization code for tokens (PKCE).
func ExchangeOpenAIOAuthCode(ctx context.Context, tokenURL, clientID, clientSecret, redirectURI, code, codeVerifier string) (*OpenAIOAuthToken, error) {
	body := url.Values{}
	body.Set("grant_type", "authorization_code")
	body.Set("code", code)
	body.Set("redirect_uri", redirectURI)
	body.Set("client_id", clientID)
	body.Set("code_verifier", codeVerifier)
	if clientSecret != "" {
		body.Set("client_secret", clientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}

	expiresAt := time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return &OpenAIOAuthToken{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		ExpiresAt:    expiresAt,
		TokenType:    out.TokenType,
	}, nil
}

// OpenAIOAuthConfig is the OAuth config for OpenAI (mirrors config.OpenAIOAuthConfig for gateway use).
type OpenAIOAuthConfig struct {
	Enabled          bool
	ClientID         string
	ClientSecret     string
	AuthorizationURL string
	TokenURL         string
	Scope            string
}

// BuildOpenAIOAuthStartURL builds the authorization URL for the OAuth start step.
func BuildOpenAIOAuthStartURL(authURL, clientID, redirectURI, scope, state, codeChallenge string) string {
	u, _ := url.Parse(authURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	if scope != "" {
		q.Set("scope", scope)
	} else {
		q.Set("scope", "openid profile")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// DecodeTokenResponse reads a JSON token response (e.g. from token endpoint) into OpenAIOAuthToken.
func DecodeTokenResponse(r io.Reader) (*OpenAIOAuthToken, error) {
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.AccessToken == "" {
		return nil, fmt.Errorf("response missing access_token")
	}
	expiresAt := time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second)
	return &OpenAIOAuthToken{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresAt:    expiresAt,
		TokenType:    raw.TokenType,
	}, nil
}

// RefreshOpenAIOAuthToken refreshes using the token URL (many providers use same URL for token and refresh).
func RefreshOpenAIOAuthToken(dataDir, tokenURL, clientID, clientSecret string) (*OpenAIOAuthToken, error) {
	t, err := ReadOpenAIOAuthToken(dataDir)
	if err != nil || t == nil || t.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token")
	}
	return refreshOpenAIOAuthToken(tokenURL, clientID, clientSecret, t.RefreshToken)
}

func refreshOpenAIOAuthToken(refreshURL, clientID, clientSecret, refreshToken string) (*OpenAIOAuthToken, error) {
	body := url.Values{}
	body.Set("grant_type", "refresh_token")
	body.Set("client_id", clientID)
	body.Set("refresh_token", refreshToken)
	if clientSecret != "" {
		body.Set("client_secret", clientSecret)
	}

	req, err := http.NewRequest(http.MethodPost, refreshURL, bytes.NewReader([]byte(body.Encode())))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh failed (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	tok, err := DecodeTokenResponse(resp.Body)
	if err != nil {
		return nil, err
	}
	tok.RefreshToken = refreshToken
	return tok, nil
}
