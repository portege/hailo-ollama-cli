package client

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// StartMockServer starts a mock Hailo-Ollama HTTP server on a random port or specified port.
// It returns the listener's address (host:port) and a cleanup function.
func StartMockServer(port string) (string, func(), error) {
	mux := http.NewServeMux()

	// 1. GET /api/tags
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ListResponse{
			Models: []LocalModel{
				{
					Name:       "llama3.2:3b",
					Model:      "llama3.2:3b",
					ModifiedAt: time.Now().Add(-2 * 24 * time.Hour),
					Size:       2026818780,
					Digest:     "sha256:0d1d29d38bb109e99eef11082ecbbf59a1cf6a5b81e8eb997bc40e8a2a4b087a",
					Details: ModelDetails{
						Format:            "gguf",
						Family:            "llama",
						Families:          []string{"llama"},
						ParameterSize:     "3.2B",
						QuantizationLevel: "Q4_0",
					},
				},
				{
					Name:       "qwen2.5:1.5b",
					Model:      "qwen2.5:1.5b",
					ModifiedAt: time.Now().Add(-1 * 24 * time.Hour),
					Size:       980000000,
					Digest:     "sha256:4b971a8a25c1a7d65bbf11e99aef59b1cf6a5b81e8eb997bc40e8a2a4b087b",
					Details: ModelDetails{
						Format:            "gguf",
						Family:            "qwen2",
						Families:          []string{"qwen2"},
						ParameterSize:     "1.5B",
						QuantizationLevel: "Q4_0",
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	// 2. GET /hailo/v1/list
	mux.HandleFunc("GET /hailo/v1/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := RemoteListResponse{
			Models: []string{
				"deepseek_r1_distill_qwen:1.5b",
				"llama3.2:3b",
				"qwen2.5-coder:1.5b",
				"qwen2.5-instruct:1.5b",
				"qwen2:1.5b",
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	// 3. GET /api/ps
	mux.HandleFunc("GET /api/ps", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ProcessResponse{
			Models: []ProcessModel{
				{
					Name:      "llama3.2:3b",
					Model:     "llama3.2:3b",
					Size:      2026818780,
					Digest:    "sha256:0d1d29d38bb109e99eef11082ecbbf59a1cf6a5b81e8eb997bc40e8a2a4b087a",
					ExpiresAt: time.Now().Add(5 * time.Minute),
					SizeVRAM:  2026818780,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	// 4. POST /api/show
	mux.HandleFunc("POST /api/show", func(w http.ResponseWriter, r *http.Request) {
		var req ShowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		resp := ShowResponse{
			License:   "Apache 2.0",
			Template:  "{{ if .System }}<|im_start|>system\n{{ .System }}<|im_end|>\n{{ end }}<|im_start|>user\n{{ .Prompt }}<|im_end|>\n<|im_start|>assistant\n",
			Modelfile: fmt.Sprintf("FROM %s\nPARAMETER temperature 0.7\nSYSTEM \"You are a helpful assistant.\"", req.Name),
			Details: ModelDetails{
				Format:        "gguf",
				Family:        "llama",
				ParameterSize: "3B",
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	// 5. DELETE /api/delete
	mux.HandleFunc("DELETE /api/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	})

	// 6. POST /api/pull
	mux.HandleFunc("POST /api/pull", func(w http.ResponseWriter, r *http.Request) {
		var req PullRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)

		steps := []string{
			"pulling manifest",
			"downloading model file",
			"verifying sha256 digest",
			"writing model layers",
			"success",
		}

		totalSize := int64(2026818780)
		for i, status := range steps {
			chunk := PullResponseChunk{
				Status: status,
			}
			if status == "downloading model file" {
				// Simulating progressive download
				for progress := int64(0); progress <= totalSize; progress += totalSize / 4 {
					chunk.Completed = progress
					chunk.Total = totalSize
					chunk.Digest = "sha256:0d1d29d38bb109e99eef11082ecbbf59a1cf6a5b81e8eb997bc40e8a2a4b087a"
					json.NewEncoder(w).Encode(chunk)
					w.Write([]byte("\n"))
					flusher.Flush()
					time.Sleep(100 * time.Millisecond)
				}
				continue
			}

			json.NewEncoder(w).Encode(chunk)
			w.Write([]byte("\n"))
			flusher.Flush()
			time.Sleep(150 * time.Millisecond)
			_ = i
		}
	})

	// Helper to split a sentence into tokens/words
	streamResponse := func(w http.ResponseWriter, model string, text string, isChat bool) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)

		words := strings.Fields(text)
		for i, word := range words {
			// Add a space to all but the last word, or just output word-by-word
			suffix := " "
			if i == len(words)-1 {
				suffix = ""
			}
			token := word + suffix

			var chunk interface{}
			if isChat {
				chunk = ChatResponseChunk{
					Model:     model,
					CreatedAt: time.Now(),
					Message: ChatMessage{
						Role:    "assistant",
						Content: token,
					},
					Done: false,
				}
			} else {
				chunk = GenerateResponseChunk{
					Model:     model,
					CreatedAt: time.Now(),
					Response:  token,
					Done:      false,
				}
			}

			json.NewEncoder(w).Encode(chunk)
			w.Write([]byte("\n"))
			flusher.Flush()
			time.Sleep(40 * time.Millisecond) // Simulates latency
		}

		// Final chunk to signify completion and stats
		var finalChunk interface{}
		if isChat {
			finalChunk = ChatResponseChunk{
				Model:              model,
				CreatedAt:          time.Now(),
				Done:               true,
				TotalDuration:      int64(2450 * time.Millisecond),
				LoadDuration:       int64(300 * time.Millisecond),
				PromptEvalCount:    10,
				PromptEvalDuration: int64(150 * time.Millisecond),
				EvalCount:          len(words),
				EvalDuration:       int64(2000 * time.Millisecond),
			}
		} else {
			finalChunk = GenerateResponseChunk{
				Model:              model,
				CreatedAt:          time.Now(),
				Done:               true,
				TotalDuration:      int64(2450 * time.Millisecond),
				LoadDuration:       int64(300 * time.Millisecond),
				PromptEvalCount:    10,
				PromptEvalDuration: int64(150 * time.Millisecond),
				EvalCount:          len(words),
				EvalDuration:       int64(2000 * time.Millisecond),
			}
		}
		json.NewEncoder(w).Encode(finalChunk)
		w.Write([]byte("\n"))
		flusher.Flush()
	}

	// 7. POST /api/chat
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		prompt := ""
		if len(req.Messages) > 0 {
			prompt = req.Messages[len(req.Messages)-1].Content
		}

		text := generateMockText(prompt)
		streamResponse(w, req.Model, text, true)
	})

	// 8. POST /api/generate
	mux.HandleFunc("POST /api/generate", func(w http.ResponseWriter, r *http.Request) {
		var req GenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		text := generateMockText(req.Prompt)
		streamResponse(w, req.Model, text, false)
	})

	// Start listener
	if port == "" {
		port = "0" // ephemeral port
	}
	addr := "127.0.0.1:" + port
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("failed to bind listener: %w", err)
	}

	actualAddr := listener.Addr().String()
	server := &http.Server{
		Handler: mux,
	}

	go func() {
		_ = server.Serve(listener)
	}()

	cleanup := func() {
		_ = server.Close()
		_ = listener.Close()
	}

	return "http://" + actualAddr, cleanup, nil
}

func generateMockText(prompt string) string {
	promptLower := strings.ToLower(prompt)
	if strings.Contains(promptLower, "hello") || strings.Contains(promptLower, "hi") {
		return "Hello! I am a simulated LLM running on the Hailo NPU mock server. How can I assist you at the edge today?"
	}
	if strings.Contains(promptLower, "story") {
		return "Once upon a time, in an engineering lab powered by Raspberry Pi 5 boards, a developer set up the Hailo-10H NPU. " +
			"Unlike standard CPUs, the NPU processed neural network weights in microseconds. The terminal interface came to life, " +
			"allowing local execution of small-footprint models with near-zero latency. And so, the edge computing revolution flourished!"
	}
	if strings.Contains(promptLower, "joke") {
		return "Why don't NPUs play hide and seek? Because they always get found in the pipeline!"
	}
	return "This is a simulated response from the Hailo-Ollama mock server. You prompted: \"" + prompt + "\". " +
		"Hailo hardware acceleration provides excellent inference performance for 1.5B to 3B models directly on your Raspberry Pi!"
}
