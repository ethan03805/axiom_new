package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/state"
)

func testDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := state.Open(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedToken(t *testing.T, db *state.DB, rawToken, scope string, expiresIn time.Duration) string {
	t.Helper()
	h := sha256.Sum256([]byte(rawToken))
	hash := hex.EncodeToString(h[:])
	id := "tok-" + rawToken[:6]
	token := &state.APIToken{
		ID:          id,
		TokenHash:   hash,
		TokenPrefix: rawToken[:10],
		Scope:       scope,
		ExpiresAt:   time.Now().Add(expiresIn),
	}
	if err := db.CreateAPIToken(token); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	db := testDB(t)
	rawToken := "axm_sk_test1234567890abcdefghij"
	seedToken(t, db, rawToken, ScopeFullControl, 24*time.Hour)

	handler := AuthMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := TokenFromContext(r.Context())
		if tok == nil {
			t.Fatal("expected token in context")
		}
		if tok.Scope != ScopeFullControl {
			t.Errorf("scope: got %q, want %q", tok.Scope, ScopeFullControl)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	db := testDB(t)

	handler := AuthMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	db := testDB(t)

	handler := AuthMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer axm_sk_invalid_token_here")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	db := testDB(t)
	rawToken := "axm_sk_expired1234567890abcdef"
	seedToken(t, db, rawToken, ScopeFullControl, -1*time.Hour)

	handler := AuthMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_RevokedToken(t *testing.T) {
	db := testDB(t)
	rawToken := "axm_sk_revoked1234567890abcde"
	id := seedToken(t, db, rawToken, ScopeFullControl, 24*time.Hour)
	if err := db.RevokeAPIToken(id); err != nil {
		t.Fatal(err)
	}

	handler := AuthMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_BadPrefix(t *testing.T) {
	db := testDB(t)

	handler := AuthMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestRequireScope_ReadOnly(t *testing.T) {
	db := testDB(t)
	rawToken := "axm_sk_readonly1234567890abcd"
	seedToken(t, db, rawToken, ScopeReadOnly, 24*time.Hour)

	inner := RequireScope(ScopeFullControl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	handler := AuthMiddleware(db)(inner)

	req := httptest.NewRequest("POST", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireScope_FullControl(t *testing.T) {
	db := testDB(t)
	rawToken := "axm_sk_fullctrl1234567890abcd"
	seedToken(t, db, rawToken, ScopeFullControl, 24*time.Hour)

	inner := RequireScope(ScopeFullControl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	handler := AuthMiddleware(db)(inner)

	req := httptest.NewRequest("POST", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestGenerateToken(t *testing.T) {
	raw, id, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	if len(raw) < 20 {
		t.Errorf("raw token too short: %d", len(raw))
	}
	if id == "" {
		t.Error("token ID is empty")
	}
	if raw[:7] != "axm_sk_" {
		t.Errorf("token prefix: got %q, want axm_sk_ prefix", raw[:7])
	}
}

func TestHashToken(t *testing.T) {
	raw := "axm_sk_test1234567890abcdefghij"
	hash := HashToken(raw)

	if hash == "" {
		t.Fatal("hash is empty")
	}
	if hash == raw {
		t.Fatal("hash should not equal raw token")
	}

	// Same input gives same hash
	hash2 := HashToken(raw)
	if hash != hash2 {
		t.Error("hash is not deterministic")
	}
}
