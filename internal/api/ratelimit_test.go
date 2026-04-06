package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter(10) // 10 RPM

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want %d", i, rr.Code, http.StatusOK)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(3) // 3 RPM

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Use up the budget
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want %d", i, rr.Code, http.StatusOK)
		}
	}

	// This one should be rate limited
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("got %d, want %d", rr.Code, http.StatusTooManyRequests)
	}

	// Check Retry-After header
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestRateLimiter_SeparateByToken(t *testing.T) {
	rl := NewRateLimiter(2)

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Two requests from "token-a"
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.Header.Set("Authorization", "Bearer token-a")
		req.RemoteAddr = "127.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("token-a request %d: got %d", i, rr.Code)
		}
	}

	// Should be blocked for token-a
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer token-a")
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("token-a over limit: got %d, want 429", rr.Code)
	}

	// token-b should still be allowed
	req = httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer token-b")
	req.RemoteAddr = "127.0.0.1:5678"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("token-b: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRateLimiter_ZeroDisabled(t *testing.T) {
	rl := NewRateLimiter(0) // disabled

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: got %d", i, rr.Code)
		}
	}
}

func TestIPAllowlist_Allowed(t *testing.T) {
	handler := IPAllowlist([]string{"127.0.0.1", "192.168.1.0/24"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	tests := []struct {
		addr string
		want int
	}{
		{"127.0.0.1:1234", http.StatusOK},
		{"192.168.1.50:1234", http.StatusOK},
		{"10.0.0.1:1234", http.StatusForbidden},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = tt.addr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != tt.want {
			t.Errorf("addr %s: got %d, want %d", tt.addr, rr.Code, tt.want)
		}
	}
}

func TestIPAllowlist_Empty(t *testing.T) {
	// Empty list means allow all
	handler := IPAllowlist(nil)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("empty allowlist: got %d, want %d", rr.Code, http.StatusOK)
	}
}
