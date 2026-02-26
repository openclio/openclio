package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// RunOpenAIOAuthLogin runs the OAuth flow in the terminal: starts a local server,
// prints the authorization URL so the user can open it in a browser, then exchanges
// the callback code for a token and saves it to dataDir. Like OpenClaw/Cline CLI login.
func RunOpenAIOAuthLogin(dataDir, authorizationURL, tokenURL, clientID, clientSecret, scope string, openBrowser bool) error {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return fmt.Errorf("pkce: %w", err)
	}
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)
	StoreOAuthState(state, verifier)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	if scope == "" {
		scope = "openid profile"
	}
	authURL := BuildOpenAIOAuthStartURL(authorizationURL, clientID, redirectURI, scope, state, challenge)

	done := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		stateIn := strings.TrimSpace(r.URL.Query().Get("state"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if code == "" || stateIn == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<h1>Missing code or state</h1><p>Close this window and try again.</p>"))
			done <- fmt.Errorf("missing code or state in callback")
			return
		}
		verifierIn := ConsumeOAuthState(stateIn)
		if verifierIn == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<h1>Invalid or expired state</h1><p>Close this window and try again.</p>"))
			done <- fmt.Errorf("invalid or expired state")
			return
		}
		tok, err := ExchangeOpenAIOAuthCode(r.Context(), tokenURL, clientID, clientSecret, redirectURI, code, verifierIn)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<h1>Token exchange failed</h1><p>" + err.Error() + "</p><p>Close this window and try again.</p>"))
			done <- err
			return
		}
		if err := WriteOpenAIOAuthToken(dataDir, tok); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("<h1>Failed to save token</h1><p>" + err.Error() + "</p>"))
			done <- err
			return
		}
		_, _ = w.Write([]byte("<h1>Success!</h1><p>You can close this window and return to the terminal.</p>"))
		done <- nil
	})
	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(listener)
	}()

	fmt.Println()
	fmt.Println("  Open this URL in your browser to sign in with OpenAI:")
	fmt.Println()
	fmt.Printf("  %s\n", authURL)
	fmt.Println()
	fmt.Println("  Waiting for callback...")
	if openBrowser {
		openURL(authURL)
	}
	fmt.Println()

	select {
	case err := <-done:
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		return err
	case <-time.After(5 * time.Minute):
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		return fmt.Errorf("timed out waiting for callback (5 minutes)")
	}
}

func openURL(u string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{u}
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", u}
	default:
		cmd, args = "xdg-open", []string{u}
	}
	_ = exec.Command(cmd, args...).Start()
}
