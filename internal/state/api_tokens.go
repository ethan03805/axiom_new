package state

import (
	"fmt"
	"time"
)

// APIToken represents an API authentication token (Section 24.3).
type APIToken struct {
	ID          string
	TokenHash   string
	TokenPrefix string
	Scope       string // "read-only" or "full-control"
	CreatedAt   time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	LastUsedAt  *time.Time
}

// CreateAPIToken inserts a new API token.
func (d *DB) CreateAPIToken(t *APIToken) error {
	_, err := d.Exec(`INSERT INTO api_tokens
		(id, token_hash, token_prefix, scope, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.TokenHash, t.TokenPrefix, t.Scope,
		t.ExpiresAt.UTC().Format(sqliteTimeFormat))
	if err != nil {
		return fmt.Errorf("creating api token: %w", err)
	}
	return nil
}

// GetAPIToken retrieves a token by ID.
func (d *DB) GetAPIToken(id string) (*APIToken, error) {
	var t APIToken
	var createdAt, expiresAt string
	var revokedAt, lastUsedAt *string

	err := d.QueryRow(`SELECT id, token_hash, token_prefix, scope,
		created_at, expires_at, revoked_at, last_used_at
		FROM api_tokens WHERE id = ?`, id).
		Scan(&t.ID, &t.TokenHash, &t.TokenPrefix, &t.Scope,
			&createdAt, &expiresAt, &revokedAt, &lastUsedAt)
	if err != nil {
		return nil, fmt.Errorf("getting api token: %w", err)
	}

	t.CreatedAt = parseTime(createdAt)
	t.ExpiresAt = parseTime(expiresAt)
	t.RevokedAt = parseNullTime(revokedAt)
	t.LastUsedAt = parseNullTime(lastUsedAt)
	return &t, nil
}

// GetAPITokenByHash retrieves a token by its SHA-256 hash.
func (d *DB) GetAPITokenByHash(hash string) (*APIToken, error) {
	var t APIToken
	var createdAt, expiresAt string
	var revokedAt, lastUsedAt *string

	err := d.QueryRow(`SELECT id, token_hash, token_prefix, scope,
		created_at, expires_at, revoked_at, last_used_at
		FROM api_tokens WHERE token_hash = ?`, hash).
		Scan(&t.ID, &t.TokenHash, &t.TokenPrefix, &t.Scope,
			&createdAt, &expiresAt, &revokedAt, &lastUsedAt)
	if err != nil {
		return nil, fmt.Errorf("getting api token by hash: %w", err)
	}

	t.CreatedAt = parseTime(createdAt)
	t.ExpiresAt = parseTime(expiresAt)
	t.RevokedAt = parseNullTime(revokedAt)
	t.LastUsedAt = parseNullTime(lastUsedAt)
	return &t, nil
}

// ListAPITokens returns all tokens ordered by creation date.
func (d *DB) ListAPITokens() ([]APIToken, error) {
	rows, err := d.Query(`SELECT id, token_hash, token_prefix, scope,
		created_at, expires_at, revoked_at, last_used_at
		FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing api tokens: %w", err)
	}
	defer rows.Close()

	var tokens []APIToken
	for rows.Next() {
		var t APIToken
		var createdAt, expiresAt string
		var revokedAt, lastUsedAt *string

		if err := rows.Scan(&t.ID, &t.TokenHash, &t.TokenPrefix, &t.Scope,
			&createdAt, &expiresAt, &revokedAt, &lastUsedAt); err != nil {
			return nil, fmt.Errorf("scanning api token: %w", err)
		}

		t.CreatedAt = parseTime(createdAt)
		t.ExpiresAt = parseTime(expiresAt)
		t.RevokedAt = parseNullTime(revokedAt)
		t.LastUsedAt = parseNullTime(lastUsedAt)
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// RevokeAPIToken marks a token as revoked.
func (d *DB) RevokeAPIToken(id string) error {
	res, err := d.Exec(`UPDATE api_tokens SET revoked_at = CURRENT_TIMESTAMP WHERE id = ? AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("revoking api token: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("revoking api token: %w", ErrNotFound)
	}
	return nil
}

// UpdateAPITokenLastUsed updates the last_used_at timestamp.
func (d *DB) UpdateAPITokenLastUsed(id string) error {
	_, err := d.Exec(`UPDATE api_tokens SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("updating api token last used: %w", err)
	}
	return nil
}

// DeleteExpiredAPITokens removes tokens past their expiration time.
// Returns the number of deleted tokens.
func (d *DB) DeleteExpiredAPITokens() (int64, error) {
	res, err := d.Exec(`DELETE FROM api_tokens WHERE expires_at < CURRENT_TIMESTAMP`)
	if err != nil {
		return 0, fmt.Errorf("deleting expired api tokens: %w", err)
	}
	return res.RowsAffected()
}
