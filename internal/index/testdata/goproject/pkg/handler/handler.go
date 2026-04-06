package handler

import (
	"fmt"

	"example.com/testproject/pkg/auth"
)

// Handler processes HTTP requests.
type Handler struct {
	Auth auth.Service
}

// HandleRequest processes an incoming request.
func (h *Handler) HandleRequest(path string) string {
	ok, err := h.Auth.Authenticate("test-token")
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if !ok {
		return "unauthorized"
	}
	return fmt.Sprintf("ok: %s", path)
}

// RegisterRoutes sets up routing.
func RegisterRoutes(h *Handler) {
	fmt.Println("routes registered")
}
