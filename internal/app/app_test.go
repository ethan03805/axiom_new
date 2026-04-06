package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/testfixtures"
)

func TestOpenDiscoversProjectFromSubdirectoryAndRunsRecovery(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	if err := project.Init(repoDir, "fixture-app"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	db, err := state.Open(project.DBPath(repoDir), slog.Default())
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stagingDir := filepath.Join(repoDir, ".axiom", "containers", "staging", "stale-attempt")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "partial.diff"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	subdir := filepath.Join(repoDir, "cmd", "service")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}

	withWorkingDir(t, subdir, func() {
		application, err := Open(slog.Default())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer application.Close()

		if application.ProjectRoot != repoDir {
			t.Fatalf("ProjectRoot = %q, want %q", application.ProjectRoot, repoDir)
		}
		if application.Engine == nil {
			t.Fatal("expected engine to be initialized")
		}
		if application.Registry == nil {
			t.Fatal("expected model registry to be initialized")
		}
		if application.BitNet == nil {
			t.Fatal("expected BitNet service to be initialized")
		}
	})

	entries, err := os.ReadDir(filepath.Join(repoDir, ".axiom", "containers", "staging"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries after recovery = %d, want 0", len(entries))
	}
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	fn()
}
