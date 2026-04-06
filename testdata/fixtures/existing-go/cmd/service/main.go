package main

import (
	"fmt"

	"example.com/existing-go/internal/greet"
)

func main() {
	fmt.Println(greet.Message("axiom"))
}
