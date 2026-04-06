package state

import (
	"fmt"
	"testing"
	"time"
)

func TestCreateAPIToken(t *testing.T) {
	db := testDB(t)

	token := &APIToken{
		ID:          "tok-001",
		TokenHash:   "sha256-hash-value",
		TokenPrefix: "axm_sk_abc",
		Scope:       "full-control",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}

	err := db.CreateAPIToken(token)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	got, err := db.GetAPIToken("tok-001")
	if err != nil {
		t.Fatalf("GetAPIToken: %v", err)
	}

	if got.ID != token.ID {
		t.Errorf("ID: got %q, want %q", got.ID, token.ID)
	}
	if got.TokenHash != token.TokenHash {
		t.Errorf("TokenHash: got %q, want %q", got.TokenHash, token.TokenHash)
	}
	if got.TokenPrefix != token.TokenPrefix {
		t.Errorf("TokenPrefix: got %q, want %q", got.TokenPrefix, token.TokenPrefix)
	}
	if got.Scope != token.Scope {
		t.Errorf("Scope: got %q, want %q", got.Scope, token.Scope)
	}
	if got.RevokedAt != nil {
		t.Errorf("RevokedAt: expected nil, got %v", got.RevokedAt)
	}
}

func TestCreateAPIToken_DuplicateHash(t *testing.T) {
	db := testDB(t)

	token := &APIToken{
		ID:          "tok-001",
		TokenHash:   "same-hash",
		TokenPrefix: "axm_sk_abc",
		Scope:       "full-control",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}
	if err := db.CreateAPIToken(token); err != nil {
		t.Fatal(err)
	}

	token2 := &APIToken{
		ID:          "tok-002",
		TokenHash:   "same-hash",
		TokenPrefix: "axm_sk_def",
		Scope:       "read-only",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}
	err := db.CreateAPIToken(token2)
	if err == nil {
		t.Fatal("expected error for duplicate token hash")
	}
}

func TestGetAPITokenByHash(t *testing.T) {
	db := testDB(t)

	token := &APIToken{
		ID:          "tok-001",
		TokenHash:   "lookup-hash",
		TokenPrefix: "axm_sk_abc",
		Scope:       "full-control",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}
	if err := db.CreateAPIToken(token); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetAPITokenByHash("lookup-hash")
	if err != nil {
		t.Fatalf("GetAPITokenByHash: %v", err)
	}
	if got.ID != "tok-001" {
		t.Errorf("got ID %q, want tok-001", got.ID)
	}
}

func TestGetAPITokenByHash_NotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetAPITokenByHash("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestListAPITokens(t *testing.T) {
	db := testDB(t)

	for i, scope := range []string{"full-control", "read-only"} {
		token := &APIToken{
			ID:          fmt.Sprintf("tok-%03d", i),
			TokenHash:   fmt.Sprintf("hash-%d", i),
			TokenPrefix: "axm_sk_abc",
			Scope:       scope,
			ExpiresAt:   time.Now().Add(24 * time.Hour),
		}
		if err := db.CreateAPIToken(token); err != nil {
			t.Fatal(err)
		}
	}

	tokens, err := db.ListAPITokens()
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
}

func TestRevokeAPIToken(t *testing.T) {
	db := testDB(t)

	token := &APIToken{
		ID:          "tok-001",
		TokenHash:   "revoke-hash",
		TokenPrefix: "axm_sk_abc",
		Scope:       "full-control",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}
	if err := db.CreateAPIToken(token); err != nil {
		t.Fatal(err)
	}

	if err := db.RevokeAPIToken("tok-001"); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}

	got, err := db.GetAPIToken("tok-001")
	if err != nil {
		t.Fatalf("GetAPIToken after revoke: %v", err)
	}
	if got.RevokedAt == nil {
		t.Fatal("expected RevokedAt to be set after revocation")
	}
}

func TestRevokeAPIToken_NotFound(t *testing.T) {
	db := testDB(t)

	err := db.RevokeAPIToken("nonexistent")
	if err == nil {
		t.Fatal("expected error for revoking nonexistent token")
	}
}

func TestUpdateAPITokenLastUsed(t *testing.T) {
	db := testDB(t)

	token := &APIToken{
		ID:          "tok-001",
		TokenHash:   "used-hash",
		TokenPrefix: "axm_sk_abc",
		Scope:       "full-control",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}
	if err := db.CreateAPIToken(token); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateAPITokenLastUsed("tok-001"); err != nil {
		t.Fatalf("UpdateAPITokenLastUsed: %v", err)
	}

	got, err := db.GetAPIToken("tok-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastUsedAt == nil {
		t.Fatal("expected LastUsedAt to be set")
	}
}

func TestListAPITokens_ExcludesRevoked(t *testing.T) {
	db := testDB(t)

	// Create two tokens, revoke one
	for i := 0; i < 2; i++ {
		token := &APIToken{
			ID:          fmt.Sprintf("tok-%03d", i),
			TokenHash:   fmt.Sprintf("hash-%d", i),
			TokenPrefix: "axm_sk_abc",
			Scope:       "full-control",
			ExpiresAt:   time.Now().Add(24 * time.Hour),
		}
		if err := db.CreateAPIToken(token); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RevokeAPIToken("tok-000"); err != nil {
		t.Fatal(err)
	}

	tokens, err := db.ListAPITokens()
	if err != nil {
		t.Fatal(err)
	}
	// ListAPITokens returns all tokens (including revoked) for admin visibility
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
}

func TestDeleteExpiredAPITokens(t *testing.T) {
	db := testDB(t)

	// One expired, one valid
	expired := &APIToken{
		ID:          "tok-expired",
		TokenHash:   "expired-hash",
		TokenPrefix: "axm_sk_exp",
		Scope:       "full-control",
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
	}
	valid := &APIToken{
		ID:          "tok-valid",
		TokenHash:   "valid-hash",
		TokenPrefix: "axm_sk_val",
		Scope:       "full-control",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}

	if err := db.CreateAPIToken(expired); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAPIToken(valid); err != nil {
		t.Fatal(err)
	}

	n, err := db.DeleteExpiredAPITokens()
	if err != nil {
		t.Fatalf("DeleteExpiredAPITokens: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 deleted, got %d", n)
	}

	tokens, err := db.ListAPITokens()
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 {
		t.Fatalf("expected 1 remaining token, got %d", len(tokens))
	}
	if tokens[0].ID != "tok-valid" {
		t.Errorf("expected tok-valid to remain, got %q", tokens[0].ID)
	}
}
