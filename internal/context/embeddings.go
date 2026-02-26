package context

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Embedder generates vector embeddings from text.
type Embedder interface {
	// Embed returns a vector embedding for the given text.
	Embed(text string) ([]float32, error)

	// Dimensions returns the embedding vector size.
	Dimensions() int
}

// --- OpenAI Embedder ---

// OpenAIEmbedder calls the OpenAI embeddings API.
type OpenAIEmbedder struct {
	apiKey string
	model  string
	dims   int
}

// embeddingClient is a dedicated HTTP client with a timeout.
// Using http.DefaultClient risks hanging forever on slow API responses.
var embeddingClient = &http.Client{Timeout: 30 * time.Second}

// NewOpenAIEmbedder creates an embedder using OpenAI's API.
// apiKeyEnv is the environment variable name containing the API key.
func NewOpenAIEmbedder(apiKeyEnv string) (*OpenAIEmbedder, error) {
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("environment variable %s is not set", apiKeyEnv)
	}
	return &OpenAIEmbedder{
		apiKey: key,
		model:  "text-embedding-3-small",
		dims:   1536,
	}, nil
}

type openAIEmbedRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (e *OpenAIEmbedder) Embed(text string) ([]float32, error) {
	reqBody, _ := json.Marshal(openAIEmbedRequest{
		Input: text,
		Model: e.model,
	})

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("creating embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := embeddingClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling embedding API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading embedding response: %w", err)
	}

	var result openAIEmbedResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing embedding response: %w", err)
	}

	if result.Error != nil {
		// Don't forward raw API errors — they may contain sensitive info (#16).
		// Log the category only; the full message is not included.
		return nil, fmt.Errorf("embedding API returned an error (check your API key and quota)")
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("embedding API returned no data")
	}

	return result.Data[0].Embedding, nil
}

func (e *OpenAIEmbedder) Dimensions() int {
	return e.dims
}

// --- Ollama Embedder ---

// OllamaEmbedder calls the local Ollama embeddings API.
type OllamaEmbedder struct {
	baseURL string
	model   string
	mu      sync.RWMutex
	dims    int
}

// ollamaBaseURL normalizes an Ollama base URL so IPv4 is used (127.0.0.1).
// Ollama often binds to 127.0.0.1 only; using "localhost" can resolve to IPv6 (::1) and fail with "connection refused".
func ollamaBaseURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "http://127.0.0.1:11434"
	}
	// Prefer 127.0.0.1 so we connect to the same address Ollama listens on (avoids IPv6 connection refused).
	if strings.Contains(raw, "//localhost") {
		raw = strings.Replace(raw, "//localhost", "//127.0.0.1", 1)
	}
	return raw
}

// NewOllamaEmbedder creates an Ollama embedder.
func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder {
	baseURL = ollamaBaseURL(baseURL)
	if strings.TrimSpace(model) == "" {
		model = "nomic-embed-text"
	}
	return &OllamaEmbedder{
		baseURL: baseURL,
		model:   model,
		dims:    768, // nomic-embed-text default dimensions
	}
}

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Embedding  []float32   `json:"embedding"`
	Error      string      `json:"error,omitempty"`
}

func (e *OllamaEmbedder) Embed(text string) ([]float32, error) {
	body, _ := json.Marshal(ollamaEmbedRequest{
		Model: e.model,
		Input: text,
	})

	req, err := http.NewRequest("POST", e.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating ollama embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := embeddingClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling ollama embedding API: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading ollama embedding response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ollama embedding API error (HTTP %d)", resp.StatusCode)
	}

	var parsed ollamaEmbedResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parsing ollama embedding response: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("ollama embedding API returned an error: %s", parsed.Error)
	}

	var vec []float32
	if len(parsed.Embeddings) > 0 {
		vec = parsed.Embeddings[0]
	} else if len(parsed.Embedding) > 0 {
		vec = parsed.Embedding
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("ollama embedding API returned no vector")
	}

	e.mu.Lock()
	e.dims = len(vec)
	e.mu.Unlock()
	return vec, nil
}

func (e *OllamaEmbedder) Dimensions() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dims
}

// --- NoOp Embedder ---

// NoOpEmbedder is a fallback that disables vector search gracefully.
// Used when no embedding API key is configured.
type NoOpEmbedder struct{}

func NewNoOpEmbedder() *NoOpEmbedder {
	return &NoOpEmbedder{}
}

func (e *NoOpEmbedder) Embed(text string) ([]float32, error) {
	return nil, nil
}

func (e *NoOpEmbedder) Dimensions() int {
	return 0
}
