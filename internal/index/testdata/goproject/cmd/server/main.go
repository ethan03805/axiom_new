package main

import (
	"fmt"

	"example.com/testproject/pkg/auth"
	"example.com/testproject/pkg/handler"
)

func main() {
	validator := &auth.TokenValidator{Secret: "secret"}
	h := &handler.Handler{Auth: validator}
	handler.RegisterRoutes(h)
	fmt.Println("server started")
}
