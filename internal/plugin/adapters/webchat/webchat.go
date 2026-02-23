// Package webchat serves an embedded web chat UI connected to the agent's WebSocket.
package webchat

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

// Handler returns an HTTP handler that serves the web chat UI.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(indexHTML)
	})
}
