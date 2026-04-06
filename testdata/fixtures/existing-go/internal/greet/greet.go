package greet

// Message returns a small deterministic greeting for fixture flows.
func Message(name string) string {
	if name == "" {
		name = "world"
	}
	return "hello, " + name
}
