package cli

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"hailo-ollama-cli/client"
)

//go:embed static/*
var webStaticFS embed.FS

// webChatRequest is the JSON body accepted by the browser's POST /api/chat.
type webChatRequest struct {
	Model    string               `json:"model"`
	Messages []client.ChatMessage `json:"messages"`
}

// RunWebUI serves a ChatGPT-style browser chat interface that streams model
// responses token-by-token over the given bind address (e.g. ":8080").
func RunWebUI(ctx context.Context, apiCli *client.Client, addr string) error {
	staticContent, err := fs.Sub(webStaticFS, "static")
	if err != nil {
		return fmt.Errorf("failed to load embedded web assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticContent)))
	mux.HandleFunc("GET /api/models", handleListModels(apiCli))
	mux.HandleFunc("POST /api/chat", handleWebChat(apiCli))

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

func handleListModels(apiCli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		models, err := apiCli.ListLocalModels(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		names := make([]string, 0, len(models))
		for _, m := range models {
			names = append(names, m.Name)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(names)
	}
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

		chatReq := client.ChatRequest{Model: req.Model, Messages: req.Messages}
		streamErr := apiCli.ChatStream(r.Context(), chatReq, func(chunk client.ChatResponseChunk) {
			if chunk.Message.Content != "" {
				fmt.Fprint(w, chunk.Message.Content)
				flusher.Flush()
			}
		})
		if streamErr != nil {
			fmt.Fprintf(w, "\n[error: %v]", streamErr)
			flusher.Flush()
		}
	}
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
