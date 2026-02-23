package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	fetchTimeout   = 10 * time.Second
	fetchMaxSize   = 500 * 1024 // 500KB
	fetchUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

var (
	htmlTagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlSpaceRe = regexp.MustCompile(`\s+`)
)

// WebFetchTool fetches URL content via HTTP GET.
type WebFetchTool struct {
	client *http.Client
	mu     sync.RWMutex
	// browserFallback is optional and used when a dynamic page needs JS rendering.
	browserFallback Tool
}

func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{Timeout: fetchTimeout},
	}
}

// SetBrowserFallback configures an optional browser tool fallback.
func (t *WebFetchTool) SetBrowserFallback(browser Tool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.browserFallback = browser
}

func (t *WebFetchTool) Name() string { return "web_fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch raw content from a URL via HTTP GET. Best for static pages/APIs; it does not execute JavaScript. For dynamic sites (Google Flights, Skyscanner, Kayak, etc.), use the browser tool instead."
}
func (t *WebFetchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "URL to fetch"}
		},
		"required": ["url"]
	}`)
}

type webFetchParams struct {
	URL string `json:"url"`
}

func (t *WebFetchTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p webFetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	if p.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if looksLikeDynamicFlightURL(p.URL) {
		if out, ok := t.tryBrowserFallback(ctx, p.URL); ok {
			return out, nil
		}
		return dynamicPageHint(0, p.URL, "URL appears to be a dynamic flight search page"), nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", fetchUserAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching URL: %w", err)
	}
	defer resp.Body.Close()

	// Read with size limit
	limited := io.LimitReader(resp.Body, fetchMaxSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if len(body) > fetchMaxSize {
		body = body[:fetchMaxSize]
		return fmt.Sprintf("[HTTP %d — truncated to 500KB]\n%s", resp.StatusCode, string(body)), nil
	}
	content := string(body)
	if looksLikeBlockedPage(resp.StatusCode, content) {
		if out, ok := t.tryBrowserFallback(ctx, p.URL); ok {
			return out, nil
		}
		return dynamicPageHint(resp.StatusCode, p.URL, "site blocked or challenged this non-browser request"), nil
	}
	if looksLikeJSRenderedPage(resp.Header.Get("Content-Type"), content) {
		if out, ok := t.tryBrowserFallback(ctx, p.URL); ok {
			return out, nil
		}
		return dynamicPageHint(resp.StatusCode, p.URL, "page appears to rely on JavaScript-rendered content"), nil
	}

	return fmt.Sprintf("[HTTP %d]\n%s", resp.StatusCode, content), nil
}

func (t *WebFetchTool) tryBrowserFallback(ctx context.Context, rawURL string) (string, bool) {
	t.mu.RLock()
	fallback := t.browserFallback
	t.mu.RUnlock()
	if fallback == nil || fallback.Name() != "browser" {
		return "", false
	}

	payload := fmt.Sprintf(`{"action":"browse","url":%q}`, strings.TrimSpace(rawURL))
	out, err := fallback.Execute(ctx, json.RawMessage(payload))
	if err != nil {
		return dynamicPageHint(0, rawURL, "browser fallback failed: "+err.Error()), true
	}
	return "[web_fetch dynamic fallback -> browser]\n" + out, true
}

func looksLikeDynamicFlightURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	path := strings.ToLower(u.Path)
	full := strings.ToLower(rawURL)

	if strings.Contains(host, "google.") && strings.Contains(path, "/flights") {
		return true
	}
	switch {
	case strings.Contains(host, "skyscanner."),
		strings.Contains(host, "kayak."),
		strings.Contains(host, "momondo."),
		strings.Contains(host, "expedia."),
		strings.Contains(host, "priceline."),
		strings.Contains(host, "cheapflights."):
		return true
	}
	return strings.Contains(full, "flight") && strings.Contains(full, "search")
}

func looksLikeBlockedPage(status int, body string) bool {
	if status == http.StatusForbidden || status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		return true
	}
	lower := strings.ToLower(body)
	markers := []string{
		"captcha",
		"cloudflare",
		"cf-chl",
		"verify you are human",
		"enable javascript",
		"access denied",
		"unusual traffic",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func looksLikeJSRenderedPage(contentType, body string) bool {
	if contentType != "" && !strings.Contains(strings.ToLower(contentType), "html") {
		return false
	}
	lower := strings.ToLower(body)
	scriptCount := strings.Count(lower, "<script")
	if scriptCount < 8 {
		return false
	}
	text := htmlTagRe.ReplaceAllString(lower, " ")
	text = htmlSpaceRe.ReplaceAllString(text, " ")
	visible := strings.TrimSpace(text)
	return len(visible) < 400
}

func dynamicPageHint(status int, targetURL, reason string) string {
	statusLine := ""
	if status > 0 {
		statusLine = fmt.Sprintf("[HTTP %d]\n", status)
	}
	return statusLine +
		"[web_fetch limitation] " + reason + ".\n" +
		"web_fetch only retrieves raw HTML and does not execute JavaScript.\n" +
		"Use the browser tool for this URL (action=\"browse\", url=\"" + targetURL + "\")\n" +
		"or use web_search for lightweight result snippets."
}
