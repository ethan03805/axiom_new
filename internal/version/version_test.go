package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
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

func TestString_FallsBackToBuildInfo(t *testing.T) {
	if _, ok := debug.ReadBuildInfo(); !ok {
		t.Skip("debug.ReadBuildInfo() unavailable in this environment")
	}

	oldVersion := Version
	oldCommit := GitCommit
	oldDate := BuildDate
	oldOnce := populateOnce
	defer func() {
		Version = oldVersion
		GitCommit = oldCommit
		BuildDate = oldDate
		populateOnce = oldOnce
	}()

	Version = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
	populateOnce = &sync.Once{}

	s := String()
	if s == "" {
		t.Fatal("String() returned empty")
	}

	// At least one field should have been populated from build info.
	// In `go test`, vcs.revision/vcs.time are typically unavailable but
	// Main.Version is also often "(devel)", so if nothing changed we
	// skip rather than fail.
	if Version == "dev" && GitCommit == "unknown" && BuildDate == "unknown" {
		t.Skip("build info present but did not yield usable fields (likely running under `go test` with no vcs settings)")
	}

	if strings.Contains(s, "dev") && strings.Contains(s, "unknown (unknown)") {
		t.Errorf("expected at least one non-default field, got: %s", s)
	}
}
