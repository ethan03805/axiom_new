package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/dockerassets"
)

// BundleOptions describes a release candidate bundle to assemble.
type BundleOptions struct {
	SourceRoot string
	OutputRoot string
	BinaryPath string
	Version    string
	GOOS       string
	GOARCH     string
	TestMatrix []TestSuite
}

// TestSuite records one suite in the release test matrix.
type TestSuite struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Status  string `json:"status"`
}

// Manifest summarizes the contents of a generated release bundle.
type Manifest struct {
	Version           string      `json:"version"`
	Platform          string      `json:"platform"`
	BundleDir         string      `json:"bundle_dir"`
	BinaryPath        string      `json:"binary_path"`
	DefaultConfigPath string      `json:"default_config_path"`
	Docs              []string    `json:"docs"`
	DockerAssets      []string    `json:"docker_assets"`
	Fixtures          []string    `json:"fixtures"`
	TestMatrixPath    string      `json:"test_matrix_path"`
	TestMatrix        []TestSuite `json:"test_matrix"`
}

// BuildBundle assembles a releasable artifact directory containing the built
// binary, default config template, docs, Docker assets, fixture repos, and a
// test-matrix report.
func BuildBundle(opts BundleOptions) (*Manifest, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}

	bundleDir := filepath.Join(opts.OutputRoot, fmt.Sprintf("axiom-%s-%s-%s", opts.Version, opts.GOOS, opts.GOARCH))
	if err := os.RemoveAll(bundleDir); err != nil {
		return nil, fmt.Errorf("resetting bundle dir: %w", err)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating bundle dir: %w", err)
	}

	manifest := &Manifest{
		Version:           opts.Version,
		Platform:          opts.GOOS + "/" + opts.GOARCH,
		BundleDir:         bundleDir,
		BinaryPath:        filepath.ToSlash(filepath.Join("bin", filepath.Base(opts.BinaryPath))),
		DefaultConfigPath: filepath.ToSlash(filepath.Join("config", "axiom.default.toml")),
		TestMatrixPath:    "test-matrix.md",
		TestMatrix:        append([]TestSuite(nil), opts.TestMatrix...),
	}

	if err := copyFile(opts.BinaryPath, filepath.Join(bundleDir, filepath.FromSlash(manifest.BinaryPath))); err != nil {
		return nil, err
	}

	if err := writeDefaultConfig(filepath.Join(bundleDir, filepath.FromSlash(manifest.DefaultConfigPath))); err != nil {
		return nil, err
	}
	if err := requireDockerAssets(opts.SourceRoot); err != nil {
		return nil, err
	}

	var err error
	manifest.Docs, err = copyTree(filepath.Join(opts.SourceRoot, "docs"), filepath.Join(bundleDir, "docs"))
	if err != nil {
		return nil, err
	}
	manifest.DockerAssets, err = copyTree(filepath.Join(opts.SourceRoot, "docker"), filepath.Join(bundleDir, "docker"))
	if err != nil {
		return nil, err
	}
	manifest.Fixtures, err = copyTree(filepath.Join(opts.SourceRoot, "testdata", "fixtures"), filepath.Join(bundleDir, "fixtures"))
	if err != nil {
		return nil, err
	}

	if err := writeTestMatrix(filepath.Join(bundleDir, manifest.TestMatrixPath), opts.TestMatrix); err != nil {
		return nil, err
	}

	if err := writeManifest(filepath.Join(bundleDir, "release-manifest.json"), manifest); err != nil {
		return nil, err
	}

	return manifest, nil
}

func validateOptions(opts BundleOptions) error {
	switch {
	case opts.SourceRoot == "":
		return errors.New("source root is required")
	case opts.OutputRoot == "":
		return errors.New("output root is required")
	case opts.BinaryPath == "":
		return errors.New("binary path is required")
	case opts.Version == "":
		return errors.New("version is required")
	case opts.GOOS == "":
		return errors.New("GOOS is required")
	case opts.GOARCH == "":
		return errors.New("GOARCH is required")
	}

	if _, err := os.Stat(opts.BinaryPath); err != nil {
		return fmt.Errorf("stat binary: %w", err)
	}
	if _, err := os.Stat(opts.SourceRoot); err != nil {
		return fmt.Errorf("stat source root: %w", err)
	}

	return nil
}

func requireDockerAssets(sourceRoot string) error {
	dockerDir := filepath.Join(sourceRoot, filepath.FromSlash(dockerassets.DefaultBuildContextRelPath))
	info, err := os.Stat(dockerDir)
	if err != nil {
		return fmt.Errorf("required docker asset directory missing: %s: %w", dockerDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("required docker asset path is not a directory: %s", dockerDir)
	}

	dockerfile := filepath.Join(sourceRoot, filepath.FromSlash(dockerassets.DefaultDockerfileRelPath))
	info, err = os.Stat(dockerfile)
	if err != nil {
		return fmt.Errorf("required docker asset missing: %s: %w", dockerfile, err)
	}
	if info.IsDir() {
		return fmt.Errorf("required docker asset path is a directory: %s", dockerfile)
	}

	return nil
}

func writeDefaultConfig(path string) error {
	cfg := config.Default("example-project", "example-project")
	data, err := config.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling default config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config bundle dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing default config: %w", err)
	}
	return nil
}

func writeTestMatrix(path string, suites []TestSuite) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating test matrix dir: %w", err)
	}

	var b strings.Builder
	b.WriteString("# Release Test Matrix\n\n")
	b.WriteString("| Suite | Command | Status |\n")
	b.WriteString("|-------|---------|--------|\n")
	for _, suite := range suites {
		fmt.Fprintf(&b, "| %s | `%s` | %s |\n", suite.Name, suite.Command, suite.Status)
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing test matrix: %w", err)
	}
	return nil
}

func writeManifest(path string, manifest *Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}
	return nil
}

func copyTree(src, dest string) ([]string, error) {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", src, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", src)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("creating bundle tree %s: %w", dest, err)
	}

	var copied []string
	err = filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if err := copyFile(path, target); err != nil {
			return err
		}

		copied = append(copied, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("copying tree %s: %w", src, err)
	}

	sort.Strings(copied)
	return copied, nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating parent dir for %s: %w", dest, err)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}
	return out.Close()
}
