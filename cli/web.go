package cli

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"hailo-ollama-cli/client"
)

//go:embed static/*
var webStaticFS embed.FS

// webChatRequest is the JSON body accepted by the browser's POST /api/chat.
type webChatRequest struct {
	Model    string               `json:"model"`
	Messages []client.ChatMessage `json:"messages"`
	// System is an optional system instruction sent as the first chat message.
	System string `json:"system,omitempty"`
	// Think requests a separate reasoning/thinking stream from thinking-capable
	// models (e.g. the Qwen family). Boolean or effort string, forwarded as-is.
	Think any `json:"think,omitempty"`
}

// RunWebUI serves a ChatGPT-style browser chat interface that streams model
// responses token-by-token over the given bind address (e.g. ":8080").
//
// metricsPath optionally points at a hailo-monitor --json JSONL file. When
// empty it falls back to HAILO_NPU_METRICS, then to auto-spawning a local
// hailo-monitor writing into the user cache directory (see cli/npu.go).
func RunWebUI(ctx context.Context, apiCli *client.Client, addr, metricsPath string) error {
	staticContent, err := fs.Sub(webStaticFS, "static")
	if err != nil {
		return fmt.Errorf("failed to load embedded web assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticContent)))
	mux.HandleFunc("GET /api/models", handleListModels(apiCli))
	mux.HandleFunc("GET /api/models/remote", handleListRemoteModels(apiCli))
	mux.HandleFunc("POST /api/models/pull", handlePullModel(apiCli))
	mux.HandleFunc("POST /api/models/remove", handleRemoveModel(apiCli))
	mux.HandleFunc("POST /api/chat", handleWebChat(apiCli))

	// Saved conversations (server-side SQLite, see cli/chatstore.go).
	chatStore, err := OpenChatStore(DefaultChatDBPath())
	if err != nil {
		return fmt.Errorf("failed to open chat database: %w", err)
	}
	defer chatStore.Close()
	mux.HandleFunc("GET /api/chats", handleListChats(chatStore))
	mux.HandleFunc("POST /api/chats", handleSaveChat(chatStore))
	mux.HandleFunc("GET /api/chats/{id}", handleGetChat(chatStore))
	mux.HandleFunc("DELETE /api/chats/{id}", handleDeleteChat(chatStore))

	// NPU telemetry: tail hailo-monitor output and expose API/SSE endpoints.
	npu := startNPUSource(ctx, apiCli, metricsPath)
	mux.HandleFunc("GET /api/npu/metrics", npu.handleMetrics)
	mux.HandleFunc("GET /api/npu/history", npu.handleHistory)
	mux.HandleFunc("GET /api/npu/stream", npu.handleStream)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		server.Close()
	}()

	fmt.Printf("Web chat UI available at: http://%s\n", displayAddr(addr))
	fmt.Println("Press Ctrl+C to stop.")

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// localModelInfo is one entry of GET /api/models. Size is in bytes and may be
// 0 when the inference server does not report it.
type localModelInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size,omitempty"`
}

func handleListModels(apiCli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		models, err := apiCli.ListLocalModels(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		infos := make([]localModelInfo, 0, len(models))
		for _, m := range models {
			infos = append(infos, localModelInfo{Name: m.Name, Size: m.Size})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(infos)
	}
}

// modelNameRequest is the JSON body for the model management endpoints.
type modelNameRequest struct {
	Name string `json:"name"`
}

// handleListRemoteModels serves GET /api/models/remote with the models
// available for download from the Hailo Model Zoo (GET /hailo/v1/list).
// Sizes are included when the server reports them (bytes; 0 = unknown).
func handleListRemoteModels(apiCli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		models, err := apiCli.ListRemoteModelsDetailed(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models)
	}
}

// handlePullModel serves POST /api/models/pull {"name": "..."}. The download
// can take minutes, so progress chunks from POST /api/pull are forwarded to
// the browser as newline-delimited JSON. Each line is a PullResponseChunk;
// the terminal line is {"done":true} on success or {"error":"..."} on failure.
func handlePullModel(apiCli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req modelNameRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
			http.Error(w, "model name is required", http.StatusBadRequest)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)

		enc := json.NewEncoder(w)
		err := apiCli.PullModel(r.Context(), strings.TrimSpace(req.Name), func(chunk client.PullResponseChunk) {
			enc.Encode(chunk)
			flusher.Flush()
		})
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		} else {
			json.NewEncoder(w).Encode(map[string]bool{"done": true})
		}
		flusher.Flush()
	}
}

// handleRemoveModel serves POST /api/models/remove {"name": "..."} and deletes
// an installed model from the inference server (DELETE /api/delete).
func handleRemoveModel(apiCli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req modelNameRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
			http.Error(w, "model name is required", http.StatusBadRequest)
			return
		}
		if err := apiCli.DeleteModel(r.Context(), strings.TrimSpace(req.Name)); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

// metricsMarker separates the streamed response text from the trailing JSON
// metrics payload. It must never appear in normal model output.
const metricsMarker = "@@HAILO-METRICS:"

// reasoningMarker separates the streamed response text from a JSON-encoded
// reasoning/thinking payload captured from a thinking-capable model. The value
// is a single JSON string so arbitrary reasoning text (including newlines) is
// escaped and cannot corrupt the plain-text stream format.
const reasoningMarker = "@@HAILO-REASONING:"

// webChatMetrics is the JSON payload appended to the end of a completed chat
// stream. Durations are in milliseconds. Token counts and inference timings
// come from the final stream chunk reported by the inference server; when the
// server does not report them, token count and speed are approximated by
// counting streamed content chunks against the measured wall clock.
type webChatMetrics struct {
	TotalMS         float64 `json:"total_ms"`
	FirstTokenMS    float64 `json:"first_token_ms,omitempty"`
	ResponseChars   int     `json:"response_chars,omitempty"`
	PromptTokens    int     `json:"prompt_tokens,omitempty"`
	PromptEvalMS    float64 `json:"prompt_eval_ms,omitempty"`
	OutputTokens    int     `json:"output_tokens,omitempty"`
	EvalMS          float64 `json:"eval_ms,omitempty"`
	TokensPerSecond float64 `json:"tokens_per_second,omitempty"`
	LoadMS          float64 `json:"load_ms,omitempty"`
}

func handleWebChat(apiCli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req webChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Model) == "" {
			http.Error(w, "model is required", http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)

		start := time.Now()
		var firstTokenAt time.Time
		var finalChunk client.ChatResponseChunk
		contentChunks := 0
		responseChars := 0

		// Prepend an optional system instruction ahead of the chat history.
		messages := req.Messages
		if strings.TrimSpace(req.System) != "" {
			messages = append([]client.ChatMessage{{Role: "system", Content: req.System}}, messages...)
		}

		chatReq := client.ChatRequest{Model: req.Model, Messages: messages, Think: req.Think}
		var thinkingBuilder strings.Builder
		streamErr := apiCli.ChatStream(r.Context(), chatReq, func(chunk client.ChatResponseChunk) {
			if chunk.Done {
				finalChunk = chunk
			}
			// Reasoning/thinking deltas arrive on a separate field from the
			// answer text; keep them out of the plain-text content stream and
			// emit them after the stream completes.
			if chunk.Message.Thinking != "" {
				thinkingBuilder.WriteString(chunk.Message.Thinking)
			}
			if chunk.Message.Content != "" {
				if firstTokenAt.IsZero() {
					firstTokenAt = time.Now()
				}
				contentChunks++
				responseChars += len([]rune(chunk.Message.Content))
				fmt.Fprint(w, chunk.Message.Content)
				flusher.Flush()
			}
		})
		if streamErr != nil {
			fmt.Fprintf(w, "\n[error: %v]", streamErr)
			flusher.Flush()
			return
		}

		if thinkingBuilder.Len() > 0 {
			if payload, err := json.Marshal(thinkingBuilder.String()); err == nil {
				fmt.Fprint(w, "\n"+reasoningMarker+string(payload))
			}
		}

		metrics := collectMetrics(finalChunk, start, firstTokenAt, time.Since(start), contentChunks, responseChars)
		if payload, err := json.Marshal(metrics); err == nil {
			fmt.Fprint(w, metricsMarker+string(payload))
			flusher.Flush()
		}
	}
}

// collectMetrics merges server-measured wall-clock timings with the stats
// reported by the inference server on the final stream chunk.
func collectMetrics(final client.ChatResponseChunk, start, firstTokenAt time.Time, total time.Duration, contentChunks, responseChars int) webChatMetrics {
	m := webChatMetrics{
		TotalMS:       durationMS(total),
		ResponseChars: responseChars,
	}
	if !firstTokenAt.IsZero() {
		m.FirstTokenMS = durationMS(firstTokenAt.Sub(start))
	}

	outputTokens := final.EvalCount
	if outputTokens <= 0 {
		outputTokens = contentChunks // approximation when no counts are reported
	}
	if outputTokens > 0 {
		m.OutputTokens = outputTokens
	}
	if final.PromptEvalCount > 0 {
		m.PromptTokens = final.PromptEvalCount
	}

	if evalDur := time.Duration(final.EvalDuration); evalDur > 0 {
		m.EvalMS = durationMS(evalDur)
		if final.EvalCount > 0 {
			m.TokensPerSecond = float64(final.EvalCount) / evalDur.Seconds()
		}
	}
	if m.TokensPerSecond == 0 && outputTokens > 0 && !firstTokenAt.IsZero() {
		// Fall back to wall clock: generation starts once the first token lands.
		if genWindow := total - firstTokenAt.Sub(start); genWindow > 0 {
			m.TokensPerSecond = float64(outputTokens) / genWindow.Seconds()
		}
	}
	if d := time.Duration(final.PromptEvalDuration); d > 0 {
		m.PromptEvalMS = durationMS(d)
	}
	if d := time.Duration(final.LoadDuration); d > 0 {
		m.LoadMS = durationMS(d)
	}
	return m
}

func durationMS(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*100) / 100
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
