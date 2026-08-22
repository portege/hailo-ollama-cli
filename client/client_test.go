package client

import (
	"context"
	"testing"
)

func TestClientAndMockServer(t *testing.T) {
	// 1. Start mock server
	addr, cleanup, err := StartMockServer("")
	if err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}
	defer cleanup()

	// 2. Initialize Client
	c := NewClient(addr)

	// 3. Test ListLocalModels
	ctx := context.Background()
	localModels, err := c.ListLocalModels(ctx)
	if err != nil {
		t.Errorf("ListLocalModels failed: %v", err)
	}
	if len(localModels) != 2 || localModels[0].Name != "llama3.2:3b" {
		t.Errorf("Unexpected local models: %+v", localModels)
	}

	// 4. Test ListRemoteModels
	remoteModels, err := c.ListRemoteModels(ctx)
	if err != nil {
		t.Errorf("ListRemoteModels failed: %v", err)
	}
	if len(remoteModels) != 5 || remoteModels[0] != "deepseek_r1_distill_qwen:1.5b" {
		t.Errorf("Unexpected remote models: %+v", remoteModels)
	}

	// 5. Test ShowModel
	showResp, err := c.ShowModel(ctx, "llama3.2:3b")
	if err != nil {
		t.Errorf("ShowModel failed: %v", err)
	}
	if showResp.License != "Apache 2.0" {
		t.Errorf("Unexpected show response: %+v", showResp)
	}

	// 6. Test ListRunningModels
	running, err := c.ListRunningModels(ctx)
	if err != nil {
		t.Errorf("ListRunningModels failed: %v", err)
	}
	if len(running) != 1 || running[0].Name != "llama3.2:3b" {
		t.Errorf("Unexpected running models: %+v", running)
	}

	// 7. Test DeleteModel
	err = c.DeleteModel(ctx, "llama3.2:3b")
	if err != nil {
		t.Errorf("DeleteModel failed: %v", err)
	}

	// 8. Test PullModel (streaming)
	var pullStatuses []string
	err = c.PullModel(ctx, "llama3.2:3b", func(chunk PullResponseChunk) {
		pullStatuses = append(pullStatuses, chunk.Status)
	})
	if err != nil {
		t.Errorf("PullModel failed: %v", err)
	}
	if len(pullStatuses) == 0 || pullStatuses[len(pullStatuses)-1] != "success" {
		t.Errorf("Unexpected pull statuses: %v", pullStatuses)
	}

	// 9. Test ChatStream (streaming)
	var chatTokens []string
	req := ChatRequest{
		Model: "llama3.2:3b",
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
		},
	}
	err = c.ChatStream(ctx, req, func(chunk ChatResponseChunk) {
		if chunk.Message.Content != "" {
			chatTokens = append(chatTokens, chunk.Message.Content)
		}
	})
	if err != nil {
		t.Errorf("ChatStream failed: %v", err)
	}
	if len(chatTokens) == 0 {
		t.Errorf("ChatStream received zero tokens")
	}

	// 10. Test GenerateStream (streaming)
	var genTokens []string
	genReq := GenerateRequest{
		Model:  "llama3.2:3b",
		Prompt: "write a story",
	}
	err = c.GenerateStream(ctx, genReq, func(chunk GenerateResponseChunk) {
		if chunk.Response != "" {
			genTokens = append(genTokens, chunk.Response)
		}
	})
	if err != nil {
		t.Errorf("GenerateStream failed: %v", err)
	}
	if len(genTokens) == 0 {
		t.Errorf("GenerateStream received zero tokens")
	}
}
