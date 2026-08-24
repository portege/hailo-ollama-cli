package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// PullModel must retry with stream disabled when the server rejects the
// streamed attempt with an HTTP error, and both attempts must carry the
// "name" and "model" fields.
func TestPullModelRetriesWithoutStream(t *testing.T) {
	requests := 0
	var payloads []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, _ := io.ReadAll(r.Body)
		payloads = append(payloads, string(body))
		if requests == 1 {
			// Simulate a hailo-ollama build that fails streamed pulls.
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("oatpp null pointer"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	var chunks []PullResponseChunk
	if err := c.PullModel(context.Background(), "qwen2.5-coder:1.5b", func(ch PullResponseChunk) {
		chunks = append(chunks, ch)
	}); err != nil {
		t.Fatalf("PullModel returned error: %v", err)
	}

	if requests != 2 {
		t.Fatalf("expected 2 attempts, got %d", requests)
	}
	if !strings.Contains(payloads[0], `"model":"qwen2.5-coder:1.5b"`) ||
		!strings.Contains(payloads[0], `"name":"qwen2.5-coder:1.5b"`) ||
		!strings.Contains(payloads[0], `"stream":true`) {
		t.Fatalf("first attempt payload unexpected: %s", payloads[0])
	}
	if !strings.Contains(payloads[1], `"stream":false`) {
		t.Fatalf("second attempt payload unexpected: %s", payloads[1])
	}
	if len(chunks) != 1 || chunks[0].Status != "success" {
		t.Fatalf("expected final success chunk, got %+v", chunks)
	}
}

// DeleteModel must send both "name" and "model" for old/new server builds.
func TestDeleteModelSendsBothNameAndModel(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.DeleteModel(context.Background(), "llama3.2:3b"); err != nil {
		t.Fatalf("DeleteModel returned error: %v", err)
	}
	if !strings.Contains(body, `"name":"llama3.2:3b"`) || !strings.Contains(body, `"model":"llama3.2:3b"`) {
		t.Fatalf("delete payload unexpected: %s", body)
	}
}

// ListRemoteModelsDetailed must tolerate every /hailo/v1/list response style:
// name strings or metadata objects, wrapped in {"models":[...]} or a bare
// array.
func TestListRemoteModelsFlexibleParsing(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []RemoteModel
	}{
		{
			name: "envelope with plain strings",
			body: `{"models":["m1","m2"]}`,
			want: []RemoteModel{{Name: "m1"}, {Name: "m2"}},
		},
		{
			name: "envelope with objects carrying sizes",
			body: `{"models":[{"name":"m1","size":2048},{"model":"m2","size_bytes":4096}]}`,
			want: []RemoteModel{{Name: "m1", Size: 2048}, {Name: "m2", Size: 4096}},
		},
		{
			name: "bare array of strings",
			body: `["m1"]`,
			want: []RemoteModel{{Name: "m1"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got, err := NewClient(srv.URL).ListRemoteModelsDetailed(context.Background())
			if err != nil {
				t.Fatalf("ListRemoteModelsDetailed: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// Some hailo-ollama builds close the pull connection right after the last
// progress chunk, leaving the final JSON object truncated ("unexpected EOF").
// When the model nevertheless shows up in /api/tags, PullModel must treat the
// pull as successful.
func TestPullModelToleratesTruncatedStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pull":
			w.Write([]byte(`{"status":"pulling manifest"}` + "\n"))
			w.Write([]byte(`{"status":"downloading","total":100,"completed":50}` + "\n"))
			// Truncated final object: connection dies mid-JSON.
			w.Write([]byte(`{"status":"downloa`))
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:1.5b","size":100}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	var last PullResponseChunk
	err := c.PullModel(context.Background(), "qwen2.5-coder:1.5b", func(ch PullResponseChunk) {
		last = ch
	})
	if err != nil {
		t.Fatalf("PullModel returned error: %v", err)
	}
	if last.Status != "success" {
		t.Fatalf("expected synthesized success chunk, got %+v", last)
	}
}

// Same truncation, but the model never appears in /api/tags: this time the
// error must surface instead of being masked.
func TestPullModelFailsWhenTruncatedAndModelMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pull":
			w.Write([]byte(`{"status":"pulling manifest"}` + "\n"))
			w.Write([]byte(`{"status":"downloa`))
		case "/api/tags":
			w.Write([]byte(`{"models":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	err := NewClient(srv.URL).PullModel(context.Background(), "ghost:1b", func(PullResponseChunk) {})
	if err == nil {
		t.Fatal("expected an error when the model is missing after truncation")
	}
	if !strings.Contains(err.Error(), "failed decoding stream") {
		t.Fatalf("unexpected error: %v", err)
	}
}
