package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ModelDetails holds information about the architecture and quantization.
type ModelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

// LocalModel represents a model downloaded locally.
type LocalModel struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    ModelDetails `json:"details"`
}

// ListResponse is the JSON schema returned by GET /api/tags
type ListResponse struct {
	Models []LocalModel `json:"models"`
}

// RemoteListResponse is the schema for /hailo/v1/list
type RemoteListResponse struct {
	Models []string `json:"models"`
}

// ChatMessage represents a single message in a chat history.
// Thinking holds any reasoning/thinking delta for assistant messages returned
// by thinking-capable models (e.g. the Qwen family). It is omitted when the
// model does not report separate reasoning.
type ChatMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
}

// ChatRequest represents a POST payload to /api/chat
type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []ChatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
	// Think requests a separate reasoning/thinking stream. It can be a boolean
	// (true/false) or a thinking-effort string ("low", "medium", "high", "max")
	// on servers that support it. Omitted when unset.
	Think any `json:"think,omitempty"`
}

// ChatResponseChunk represents a single streamed line from /api/chat
type ChatResponseChunk struct {
	Model     string      `json:"model"`
	CreatedAt time.Time   `json:"created_at"`
	Message   ChatMessage `json:"message"`
	Done      bool        `json:"done"`

	// Stats
	TotalDuration      int64 `json:"total_duration,omitempty"`
	LoadDuration       int64 `json:"load_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}

// GenerateRequest represents a POST payload to /api/generate
type GenerateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	System  string         `json:"system,omitempty"`
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options,omitempty"`
}

// GenerateResponseChunk represents a single streamed line from /api/generate
type GenerateResponseChunk struct {
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	Response  string    `json:"response"`
	Done      bool      `json:"done"`

	// Stats
	TotalDuration      int64 `json:"total_duration,omitempty"`
	LoadDuration       int64 `json:"load_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}

// PullRequest represents a POST payload to /api/pull.
//
// Both Name (legacy ollama field) and Model (current ollama field) are sent
// with the same value: hailo-ollama servers built on newer ollama DTOs read
// "model" and crash with a 500 ("Error. Null pointer.") when it is missing,
// while older builds only know "name".
type PullRequest struct {
	Name   string `json:"name"`
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// PullResponseChunk represents progress on a model download stream
type PullResponseChunk struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// ProcessModel represents an active running model in memory
type ProcessModel struct {
	Name      string    `json:"name"`
	Model     string    `json:"model"`
	Size      int64     `json:"size"`
	Digest    string    `json:"digest"`
	ExpiresAt time.Time `json:"expires_at"`
	SizeVRAM  int64     `json:"size_vram"`
}

// ProcessResponse is the schema returned by GET /api/ps
type ProcessResponse struct {
	Models []ProcessModel `json:"models"`
}

// ShowRequest represents a POST payload to /api/show
type ShowRequest struct {
	Name string `json:"name"`
}

// ShowResponse represents detailed model metadata
type ShowResponse struct {
	License    string         `json:"license,omitempty"`
	Modelfile  string         `json:"modelfile,omitempty"`
	Parameters string         `json:"parameters,omitempty"`
	Template   string         `json:"template,omitempty"`
	Details    ModelDetails   `json:"details,omitempty"`
	ModelInfo  map[string]any `json:"model_info,omitempty"`
}

// Client facilitates communication with the hailo-ollama REST API
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient creates a new Client instance.
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:8000" // Hailo-Ollama default port
	}
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 0, // No timeout to allow long streams
		},
	}
}

// NewRequest creates an HTTP request with context and headers set
func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	u, err := url.JoinPath(c.BaseURL, path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// ListLocalModels calls GET /api/tags
func (c *Client) ListLocalModels(ctx context.Context) ([]LocalModel, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	var lr ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return lr.Models, nil
}

// RemoteModel describes a model available for download from the Hailo Model
// Zoo. Size is in bytes; it stays 0 (unknown) when the server only reports
// names without metadata.
type RemoteModel struct {
	Name string `json:"name"`
	Size int64  `json:"size,omitempty"`
}

// ListRemoteModels calls GET /hailo/v1/list and returns just the model names.
func (c *Client) ListRemoteModels(ctx context.Context) ([]string, error) {
	models, err := c.ListRemoteModelsDetailed(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	return names, nil
}

// ListRemoteModelsDetailed calls GET /hailo/v1/list and tolerates every
// response style observed across hailo-ollama builds: the envelope may be
// {"models":[...]} or a bare array, and each entry may be a plain name string
// or an object carrying optional metadata ("name"/"model", "size"/"size_bytes").
func (c *Client) ListRemoteModelsDetailed(ctx context.Context) ([]RemoteModel, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/hailo/v1/list", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading response: %w", err)
	}

	var raw []json.RawMessage
	var envelope struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Models != nil {
		raw = envelope.Models
	} else if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed decoding model list: %w", err)
	}

	models := make([]RemoteModel, 0, len(raw))
	for _, entry := range raw {
		var name string
		if err := json.Unmarshal(entry, &name); err == nil {
			if name != "" {
				models = append(models, RemoteModel{Name: name})
			}
			continue
		}
		var obj struct {
			Name      string `json:"name"`
			Model     string `json:"model"`
			Size      int64  `json:"size"`
			SizeBytes int64  `json:"size_bytes"`
		}
		if err := json.Unmarshal(entry, &obj); err == nil {
			n := obj.Name
			if n == "" {
				n = obj.Model
			}
			if n != "" {
				size := obj.Size
				if size == 0 {
					size = obj.SizeBytes
				}
				models = append(models, RemoteModel{Name: n, Size: size})
			}
		}
	}
	return models, nil
}

// ShowModel calls POST /api/show
func (c *Client) ShowModel(ctx context.Context, name string) (*ShowResponse, error) {
	reqPayload := ShowRequest{Name: name}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/show", reqPayload)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	var sr ShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &sr, nil
}

// ListRunningModels calls GET /api/ps
func (c *Client) ListRunningModels(ctx context.Context) ([]ProcessModel, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/ps", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	var pr ProcessResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return pr.Models, nil
}

// DeleteModel calls DELETE /api/delete.
//
// Like PullRequest, both "name" and "model" are sent for compatibility with
// older and newer hailo-ollama server builds.
func (c *Client) DeleteModel(ctx context.Context, name string) error {
	reqPayload := map[string]string{"name": name, "model": name}
	req, err := c.newRequest(ctx, http.MethodDelete, "/api/delete", reqPayload)
	if err != nil {
		return err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// PullModel calls POST /api/pull and streams progress updates.
//
// A streamed pull is attempted first. If the server rejects it with an HTTP
// error (some hailo-ollama builds return 500 for the streaming code path),
// the pull is retried once with stream disabled; the server then answers with
// a single final JSON chunk instead.
func (c *Client) PullModel(ctx context.Context, name string, onProgress func(PullResponseChunk)) error {
	doPull := func(stream bool) (*http.Response, error) {
		reqPayload := PullRequest{Name: name, Model: name, Stream: stream}
		req, err := c.newRequest(ctx, http.MethodPost, "/api/pull", reqPayload)
		if err != nil {
			return nil, err
		}
		return c.HTTPClient.Do(req)
	}

	resp, err := doPull(true)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		return c.decodePullStream(resp.Body, name, onProgress)
	}

	// Capture why the streamed attempt failed so it can be included in the
	// final error when the retry fails as well.
	streamedErr := "unknown error"
	if err != nil {
		streamedErr = err.Error()
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		streamedErr = fmt.Sprintf("server error (%d): %s", resp.StatusCode, string(body))
	}

	// Retry without streaming.
	resp, err = doPull(false)
	if err != nil {
		return fmt.Errorf("pull request failed: %w (streamed attempt: %s)", err, streamedErr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error (%d): %s (streamed attempt: %s)",
			resp.StatusCode, string(body), streamedErr)
	}
	var chunk PullResponseChunk
	if err := json.NewDecoder(resp.Body).Decode(&chunk); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) && c.pullModelInstalled(name) {
			onProgress(PullResponseChunk{Status: "success"})
			return nil
		}
		return fmt.Errorf("failed decoding pull response: %w", err)
	}
	onProgress(chunk)
	return nil
}

func (c *Client) decodePullStream(body io.Reader, name string, onProgress func(PullResponseChunk)) error {
	decoder := json.NewDecoder(body)
	for {
		var chunk PullResponseChunk
		if err := decoder.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// Some hailo-ollama builds close the connection right after the
			// last progress chunk, cutting the final JSON object short
			// ("unexpected EOF"). That does not necessarily mean the pull
			// failed: check whether the model actually landed locally before
			// reporting an error.
			if errors.Is(err, io.ErrUnexpectedEOF) && c.pullModelInstalled(name) {
				onProgress(PullResponseChunk{Status: "success"})
				return nil
			}
			return fmt.Errorf("failed decoding stream: %w", err)
		}
		onProgress(chunk)
	}
	return nil
}

// pullModelInstalled reports whether name is present in the server's local
// model list (GET /api/tags).
func (c *Client) pullModelInstalled(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := c.ListLocalModels(ctx)
	if err != nil {
		return false
	}
	for _, m := range models {
		if m.Name == name || m.Model == name {
			return true
		}
	}
	return false
}

// ChatStream calls POST /api/chat and streams output tokens
func (c *Client) ChatStream(ctx context.Context, reqPayload ChatRequest, onChunk func(ChatResponseChunk)) error {
	reqPayload.Stream = true
	if len(reqPayload.Options) == 0 {
		reqPayload.Options = nil
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/chat", reqPayload)
	if err != nil {
		return err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk ChatResponseChunk
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed decoding stream: %w", err)
		}
		onChunk(chunk)
	}

	return nil
}

// GenerateStream calls POST /api/generate and streams output tokens
func (c *Client) GenerateStream(ctx context.Context, reqPayload GenerateRequest, onChunk func(GenerateResponseChunk)) error {
	reqPayload.Stream = true
	if len(reqPayload.Options) == 0 {
		reqPayload.Options = nil
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/generate", reqPayload)
	if err != nil {
		return err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk GenerateResponseChunk
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed decoding stream: %w", err)
		}
		onChunk(chunk)
	}

	return nil
}
