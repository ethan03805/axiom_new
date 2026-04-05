package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBitNet_Complete_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %s", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		// Verify grammar constraints are forwarded
		if _, ok := req["grammar"]; !ok {
			t.Error("expected grammar field in request")
		}

		resp := chatCompletionResponse{
			ID:    "local-1",
			Model: "bitnet/falcon3-1b",
			Choices: []chatCompletionChoice{
				{
					Message:      chatMessage{Role: "assistant", Content: `{"key": "value"}`},
					FinishReason: "stop",
				},
			},
			Usage: chatCompletionUsage{
				PromptTokens:     20,
				CompletionTokens: 10,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewBitNetProvider(server.URL)
	grammar := `root ::= "{" ws "}" ws`
	result, err := provider.Complete(context.Background(), ProviderRequest{
		Model:              "bitnet/falcon3-1b",
		Messages:           []Message{{Role: "user", Content: "Generate JSON"}},
		MaxTokens:          512,
		Temperature:        0.1,
		GrammarConstraints: &grammar,
	})

	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if result.Content != `{"key": "value"}` {
		t.Errorf("unexpected content: %q", result.Content)
	}
	if result.InputTokens != 20 {
		t.Errorf("expected 20 input tokens, got %d", result.InputTokens)
	}
	if result.OutputTokens != 10 {
		t.Errorf("expected 10 output tokens, got %d", result.OutputTokens)
	}
}

func TestBitNet_Complete_NoGrammar(t *testing.T) {
	var receivedGrammar bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		_, receivedGrammar = req["grammar"]

		resp := chatCompletionResponse{
			ID:    "local-1",
			Model: "bitnet/falcon3-1b",
			Choices: []chatCompletionChoice{
				{Message: chatMessage{Role: "assistant", Content: "hello"}, FinishReason: "stop"},
			},
			Usage: chatCompletionUsage{PromptTokens: 5, CompletionTokens: 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewBitNetProvider(server.URL)
	_, err := provider.Complete(context.Background(), ProviderRequest{
		Model:    "bitnet/falcon3-1b",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if receivedGrammar {
		t.Error("grammar field should not be sent when nil")
	}
}

func TestBitNet_Name(t *testing.T) {
	provider := NewBitNetProvider("http://unused")
	if provider.Name() != "bitnet" {
		t.Errorf("expected 'bitnet', got %q", provider.Name())
	}
}

func TestBitNet_Available_Up(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// BitNet health endpoint
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := NewBitNetProvider(server.URL)
	if !provider.Available(context.Background()) {
		t.Error("expected provider to be available")
	}
}

func TestBitNet_Available_Down(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	provider := NewBitNetProvider(server.URL)
	if provider.Available(context.Background()) {
		t.Error("expected provider to be unavailable")
	}
}

func TestBitNet_Complete_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := NewBitNetProvider(server.URL)
	_, err := provider.Complete(context.Background(), ProviderRequest{
		Model:    "bitnet/falcon3-1b",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestBitNet_Complete_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatCompletionResponse{
			ID:      "local-1",
			Choices: []chatCompletionChoice{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewBitNetProvider(server.URL)
	_, err := provider.Complete(context.Background(), ProviderRequest{
		Model:    "bitnet/falcon3-1b",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}
