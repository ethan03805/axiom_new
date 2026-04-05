package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	s := String()
	if !strings.Contains(s, "axiom") {
		t.Errorf("version string should contain 'axiom', got: %s", s)
	}
	if !strings.Contains(s, runtime.GOOS) {
		t.Errorf("version string should contain OS, got: %s", s)
	}
}

func TestStringWithValues(t *testing.T) {
	old := Version
	oldCommit := GitCommit
	oldDate := BuildDate
	defer func() {
		Version = old
		GitCommit = oldCommit
		BuildDate = oldDate
	}()

	Version = "1.0.0"
	GitCommit = "abc1234"
	BuildDate = "2026-01-01"

	s := String()
	if !strings.Contains(s, "1.0.0") {
		t.Errorf("expected version 1.0.0 in: %s", s)
	}
	if !strings.Contains(s, "abc1234") {
		t.Errorf("expected commit abc1234 in: %s", s)
	}
	if !strings.Contains(s, "2026-01-01") {
		t.Errorf("expected build date in: %s", s)
	}
}
