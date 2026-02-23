package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const searchTimeout = 10 * time.Second

// WebSearchTool searches the web and returns relevant results.
type WebSearchTool struct {
	provider   string
	apiKey     string
	maxResults int
	client     *http.Client
	// braveURL and tavilyURL override the default API endpoints (used in tests).
	braveURL  string
	tavilyURL string
}

// NewWebSearchTool creates a new WebSearchTool.
// provider: "brave" (default) or "tavily"; apiKey: the API key for the provider.
func NewWebSearchTool(provider, apiKey string) *WebSearchTool {
	if provider == "" {
		provider = "brave"
	}
	return &WebSearchTool{
		provider:   provider,
		apiKey:     apiKey,
		maxResults: 5,
		client:     &http.Client{Timeout: searchTimeout},
	}
}

func (t *WebSearchTool) Name() string { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "Search the web and return a list of relevant results with titles, URLs, and snippets"
}

func (t *WebSearchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query"},
			"num_results": {"type": "integer", "description": "Number of results to return (default 5)"}
		},
		"required": ["query"]
	}`)
}

type webSearchParams struct {
	Query      string `json:"query"`
	NumResults int    `json:"num_results,omitempty"`
}

func (t *WebSearchTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p webSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	if p.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	n := p.NumResults
	if n <= 0 {
		n = t.maxResults
	}

	switch t.provider {
	case "brave":
		return t.searchBrave(ctx, p.Query, n)
	case "tavily":
		return t.searchTavily(ctx, p.Query, n)
	default:
		return "", fmt.Errorf("web_search: unknown provider %q", t.provider)
	}
}

func (t *WebSearchTool) searchBrave(ctx context.Context, query string, n int) (string, error) {
	if t.apiKey == "" {
		return "web_search: API key missing or invalid", nil
	}

	base := t.braveURL
	if base == "" {
		base = "https://api.search.brave.com/res/v1/web/search"
	}
	endpoint := fmt.Sprintf("%s?q=%s&count=%d", base, url.QueryEscape(query), n)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}
	req.Header.Set("X-Subscription-Token", t.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Sprintf("web_search: request failed: %v", err), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "web_search: API key missing or invalid", nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return "", fmt.Errorf("web_search: reading response: %w", err)
	}

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("web_search: parsing response: %w", err)
	}

	if len(result.Web.Results) == 0 {
		return "web_search: no results found", nil
	}

	var out strings.Builder
	for i, r := range result.Web.Results {
		fmt.Fprintf(&out, "## Result %d\nTitle: %s\nURL: %s\nSnippet: %s\n\n",
			i+1, r.Title, r.URL, r.Description)
	}
	return strings.TrimSpace(out.String()), nil
}

func (t *WebSearchTool) searchTavily(ctx context.Context, query string, n int) (string, error) {
	if t.apiKey == "" {
		return "web_search: API key missing or invalid", nil
	}

	payload, err := json.Marshal(map[string]interface{}{
		"api_key":     t.apiKey,
		"query":       query,
		"max_results": n,
	})
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}

	base := t.tavilyURL
	if base == "" {
		base = "https://api.tavily.com/search"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", base, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Sprintf("web_search: request failed: %v", err), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "web_search: API key missing or invalid", nil
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return "", fmt.Errorf("web_search: reading response: %w", err)
	}

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("web_search: parsing response: %w", err)
	}

	if len(result.Results) == 0 {
		return "web_search: no results found", nil
	}

	var out strings.Builder
	for i, r := range result.Results {
		fmt.Fprintf(&out, "## Result %d\nTitle: %s\nURL: %s\nSnippet: %s\n\n",
			i+1, r.Title, r.URL, r.Content)
	}
	return strings.TrimSpace(out.String()), nil
}
