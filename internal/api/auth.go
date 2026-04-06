package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/state"
)

// GenerateToken creates a new raw token and its ID.
// The raw token has the format axm_sk_<base64-encoded-32-bytes>.
// Returns (rawToken, tokenID, error).
func GenerateToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw := "axm_sk_" + base64.RawURLEncoding.EncodeToString(b)

	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return "", "", err
	}
	id := "tok_" + hex.EncodeToString(idBytes)
	return raw, id, nil
}

// HashToken returns the SHA-256 hex digest of a raw token.
func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// TokenPrefix returns the first 14 chars of a raw token for display.
func TokenPrefix(raw string) string {
	if len(raw) > 14 {
		return raw[:14] + "..."
	}
	return raw
}

// AuthMiddleware authenticates API requests via Bearer token.
// On success, the authenticated token is stored in the request context.
// Per Section 24.3: all API requests require authentication.
func AuthMiddleware(db *state.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" {
				writeError(w, http.StatusUnauthorized, "missing Authorization header")
				return
			}

			if !strings.HasPrefix(auth, "Bearer ") {
				writeError(w, http.StatusUnauthorized, "invalid Authorization scheme")
				return
			}

			rawToken := strings.TrimPrefix(auth, "Bearer ")
			hash := HashToken(rawToken)

			token, err := db.GetAPITokenByHash(hash)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			if token.RevokedAt != nil {
				writeError(w, http.StatusUnauthorized, "token revoked")
				return
			}

			if time.Now().After(token.ExpiresAt) {
				writeError(w, http.StatusUnauthorized, "token expired")
				return
			}

			// Update last used (best effort)
			_ = db.UpdateAPITokenLastUsed(token.ID)

			ctx := context.WithValue(r.Context(), tokenContextKey{}, token)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireScope returns middleware that checks the authenticated token
// has at least the required scope level.
// full-control satisfies both scopes; read-only only satisfies read-only.
func RequireScope(required string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := TokenFromContext(r.Context())
			if token == nil {
				writeError(w, http.StatusUnauthorized, "not authenticated")
				return
			}

			if required == ScopeFullControl && token.Scope != ScopeFullControl {
				writeError(w, http.StatusForbidden, "insufficient scope: requires full-control")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// TokenFromContext extracts the authenticated token from the request context.
func TokenFromContext(ctx context.Context) *state.APIToken {
	tok, _ := ctx.Value(tokenContextKey{}).(*state.APIToken)
	return tok
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: msg, Code: code})
}
