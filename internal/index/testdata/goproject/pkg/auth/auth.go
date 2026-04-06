package auth

import "fmt"

// Service defines the authentication interface.
type Service interface {
	Authenticate(token string) (bool, error)
	Authorize(userID string, resource string) bool
}

// TokenValidator validates authentication tokens.
type TokenValidator struct {
	Secret string
}

// Authenticate checks if a token is valid.
func (tv *TokenValidator) Authenticate(token string) (bool, error) {
	if token == "" {
		return false, fmt.Errorf("empty token")
	}
	return true, nil
}

// Authorize checks if a user has access to a resource.
func (tv *TokenValidator) Authorize(userID string, resource string) bool {
	return userID != ""
}

// MaxTokenAge is the maximum age of a token in seconds.
const MaxTokenAge = 3600

var defaultSecret = "changeme"
