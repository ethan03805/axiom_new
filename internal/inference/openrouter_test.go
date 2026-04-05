package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRouter_Complete_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request shape
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}

		// Verify request body
		body, _ := io.ReadAll(r.Body)
		var req chatCompletionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		if req.Model != "anthropic/claude-4-sonnet" {
			t.Errorf("expected model anthropic/claude-4-sonnet, got %s", req.Model)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(req.Messages))
		}
		if req.MaxTokens != 1024 {
			t.Errorf("expected max_tokens 1024, got %d", req.MaxTokens)
		}

		// Return valid response
		resp := chatCompletionResponse{
			ID:    "gen-123",
			Model: "anthropic/claude-4-sonnet",
			Choices: []chatCompletionChoice{
				{
					Index: 0,
					Message: chatMessage{
						Role:    "assistant",
						Content: "Hello, world!",
					},
					FinishReason: "stop",
				},
			},
			Usage: chatCompletionUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	result, err := provider.Complete(context.Background(), ProviderRequest{
		Model: "anthropic/claude-4-sonnet",
		Messages: []Message{
			{Role: "user", Content: "Say hello"},
		},
		MaxTokens:   1024,
		Temperature: 0.2,
	})

	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if result.Content != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", result.Content)
	}
	if result.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", result.InputTokens)
	}
	if result.OutputTokens != 5 {
		t.Errorf("expected 5 output tokens, got %d", result.OutputTokens)
	}
	if result.FinishReason != "stop" {
		t.Errorf("expected finish_reason stop, got %s", result.FinishReason)
	}
	if result.Model != "anthropic/claude-4-sonnet" {
		t.Errorf("expected model anthropic/claude-4-sonnet, got %s", result.Model)
	}
}

func TestOpenRouter_Complete_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    429,
				"message": "rate limited",
			},
		})
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	_, err := provider.Complete(context.Background(), ProviderRequest{
		Model:    "anthropic/claude-4-sonnet",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected error to contain status code, got: %v", err)
	}
}

func TestOpenRouter_Complete_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	_, err := provider.Complete(context.Background(), ProviderRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestOpenRouter_Complete_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatCompletionResponse{
			ID:      "gen-123",
			Choices: []chatCompletionChoice{},
			Usage:   chatCompletionUsage{PromptTokens: 5, CompletionTokens: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	_, err := provider.Complete(context.Background(), ProviderRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestOpenRouter_Complete_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	_, err := provider.Complete(context.Background(), ProviderRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestOpenRouter_Complete_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block forever — context should cancel
		<-r.Context().Done()
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := provider.Complete(ctx, ProviderRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestOpenRouter_Name(t *testing.T) {
	provider := NewOpenRouterProvider("http://unused", "key")
	if provider.Name() != "openrouter" {
		t.Errorf("expected 'openrouter', got %q", provider.Name())
	}
}

func TestOpenRouter_Available_Up(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	if !provider.Available(context.Background()) {
		t.Error("expected provider to be available")
	}
}

func TestOpenRouter_Available_Down(t *testing.T) {
	// Point to a closed server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	if provider.Available(context.Background()) {
		t.Error("expected provider to be unavailable for closed server")
	}
}

func TestOpenRouter_Complete_PaymentRequired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    402,
				"message": "insufficient credits",
			},
		})
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	_, err := provider.Complete(context.Background(), ProviderRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 402 response")
	}
	if !strings.Contains(err.Error(), "402") {
		t.Errorf("expected error to contain 402, got: %v", err)
	}
}

func TestOpenRouter_Complete_TemperatureZero(t *testing.T) {
	var sentTemp float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		if t, ok := req["temperature"].(float64); ok {
			sentTemp = t
		}

		resp := chatCompletionResponse{
			ID: "gen-1",
			Choices: []chatCompletionChoice{
				{Message: chatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: chatCompletionUsage{PromptTokens: 1, CompletionTokens: 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(server.URL, "test-key")
	provider.Complete(context.Background(), ProviderRequest{
		Model:       "test-model",
		Messages:    []Message{{Role: "user", Content: "hi"}},
		Temperature: 0,
		MaxTokens:   100,
	})

	if sentTemp != 0 {
		t.Errorf("expected temperature 0, got %f", sentTemp)
	}
}
