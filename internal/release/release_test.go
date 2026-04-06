package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildBundle_CopiesReleaseArtifactsAndWritesManifest(t *testing.T) {
	sourceRoot := t.TempDir()
	outputRoot := t.TempDir()

	writeTestFile(t, filepath.Join(sourceRoot, "bin", "axiom.exe"), "binary")
	writeTestFile(t, filepath.Join(sourceRoot, "docs", "getting-started.md"), "# Getting Started")
	writeTestFile(t, filepath.Join(sourceRoot, "docs", "operations-diagnostics.md"), "# Operations")
	writeTestFile(t, filepath.Join(sourceRoot, "docker", "meeseeks.Dockerfile"), "FROM alpine:3.20")
	writeTestFile(t, filepath.Join(sourceRoot, "testdata", "fixtures", "greenfield", "README.md"), "# Greenfield")
	writeTestFile(t, filepath.Join(sourceRoot, "testdata", "fixtures", "existing-go", "go.mod"), "module example.com/existing")

	manifest, err := BuildBundle(BundleOptions{
		SourceRoot: sourceRoot,
		OutputRoot: outputRoot,
		BinaryPath: filepath.Join(sourceRoot, "bin", "axiom.exe"),
		Version:    "v1.0.0-rc1",
		GOOS:       "windows",
		GOARCH:     "amd64",
		TestMatrix: []TestSuite{
			{Name: "unit", Command: "go test ./...", Status: "passed"},
			{Name: "integration", Command: "go test ./cmd/axiom -run Phase20", Status: "passed"},
		},
	})
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}

	bundleDir := filepath.Join(outputRoot, "axiom-v1.0.0-rc1-windows-amd64")
	if manifest.BundleDir != bundleDir {
		t.Fatalf("BundleDir = %q, want %q", manifest.BundleDir, bundleDir)
	}

	for _, relPath := range []string{
		"bin/axiom.exe",
		"config/axiom.default.toml",
		"docs/getting-started.md",
		"docs/operations-diagnostics.md",
		"docker/meeseeks.Dockerfile",
		"fixtures/greenfield/README.md",
		"fixtures/existing-go/go.mod",
		"test-matrix.md",
		"release-manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(bundleDir, filepath.FromSlash(relPath))); err != nil {
			t.Fatalf("expected bundled artifact %s: %v", relPath, err)
		}
	}

	manifestBytes, err := os.ReadFile(filepath.Join(bundleDir, "release-manifest.json"))
	if err != nil {
		t.Fatalf("ReadFile manifest: %v", err)
	}

	var decoded Manifest
	if err := json.Unmarshal(manifestBytes, &decoded); err != nil {
		t.Fatalf("Unmarshal manifest: %v", err)
	}
	if decoded.Version != "v1.0.0-rc1" {
		t.Fatalf("Version = %q, want v1.0.0-rc1", decoded.Version)
	}
	if decoded.Platform != "windows/amd64" {
		t.Fatalf("Platform = %q, want windows/amd64", decoded.Platform)
	}
	if len(decoded.Docs) != 2 {
		t.Fatalf("Docs count = %d, want 2", len(decoded.Docs))
	}
	if len(decoded.Fixtures) != 2 {
		t.Fatalf("Fixtures count = %d, want 2", len(decoded.Fixtures))
	}
	if decoded.TestMatrixPath != "test-matrix.md" {
		t.Fatalf("TestMatrixPath = %q, want test-matrix.md", decoded.TestMatrixPath)
	}
}

func TestBuildBundle_ValidatesRequiredInputs(t *testing.T) {
	_, err := BuildBundle(BundleOptions{})
	if err == nil {
		t.Fatal("expected validation error for empty options")
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
