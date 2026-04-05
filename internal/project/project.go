package project

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openaxiom/axiom/internal/config"
)

const (
	AxiomDir    = ".axiom"
	ConfigFile  = "config.toml"
	SRSFile     = "srs.md"
	SRSHashFile = "srs.md.sha256"
	DBFile      = "axiom.db"
)

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// Init initializes a new Axiom project in the given directory.
func Init(dir, name string) error {
	axiomDir := filepath.Join(dir, AxiomDir)

	if _, err := os.Stat(axiomDir); err == nil {
		return fmt.Errorf("axiom project already initialized in %s", dir)
	}

	// Create .axiom/ directory structure
	dirs := []string{
		axiomDir,
		filepath.Join(axiomDir, "containers", "specs"),
		filepath.Join(axiomDir, "containers", "staging"),
		filepath.Join(axiomDir, "containers", "ipc"),
		filepath.Join(axiomDir, "validation"),
		filepath.Join(axiomDir, "eco"),
		filepath.Join(axiomDir, "logs", "prompts"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
	}

	// Generate slug from name
	slug := Slugify(name)

	// Write default config
	cfg := config.Default(name, slug)
	cfgData, err := config.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	cfgPath := filepath.Join(axiomDir, ConfigFile)
	if err := os.WriteFile(cfgPath, cfgData, 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	// Write .gitignore for ephemeral paths
	if err := writeGitignore(axiomDir); err != nil {
		return fmt.Errorf("writing .gitignore: %w", err)
	}

	// Write shipped models.json placeholder
	modelsPath := filepath.Join(axiomDir, "models.json")
	if err := os.WriteFile(modelsPath, []byte("[]\n"), 0o644); err != nil {
		return fmt.Errorf("writing models.json: %w", err)
	}

	return nil
}

func writeGitignore(axiomDir string) error {
	// Per Section 28.2: gitignored runtime state
	content := `# Axiom runtime state (gitignored)
axiom.db
axiom.db-wal
axiom.db-shm
containers/
validation/
logs/
`
	return os.WriteFile(filepath.Join(axiomDir, ".gitignore"), []byte(content), 0o644)
}

// Slugify converts a project name to a URL-safe slug.
func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "project"
	}
	return s
}

// Discover walks from dir upward to find the nearest .axiom/ directory.
// Returns the project root path or an error if not found.
func Discover(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	for {
		candidate := filepath.Join(dir, AxiomDir)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not an axiom project (no .axiom/ directory found in any parent)")
		}
		dir = parent
	}
}

// DBPath returns the path to the SQLite database for a project.
func DBPath(projectRoot string) string {
	return filepath.Join(projectRoot, AxiomDir, DBFile)
}

// ConfigPath returns the path to the project config file.
func ConfigPath(projectRoot string) string {
	return filepath.Join(projectRoot, AxiomDir, ConfigFile)
}

// WorkBranch returns the axiom work branch name for a given slug.
func WorkBranch(slug string) string {
	return "axiom/" + slug
}

// IsDirty checks if the git working tree has uncommitted changes.
// Per architecture Section 28.2: engine SHALL refuse to start axiom run on dirty tree.
func IsDirty(dir string) (bool, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("running git status: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// WriteSRS writes an SRS file and its SHA-256 hash file.
// The SRS file is set to read-only per architecture requirements.
func WriteSRS(projectRoot string, content []byte) error {
	srsPath := filepath.Join(projectRoot, AxiomDir, SRSFile)
	hashPath := filepath.Join(projectRoot, AxiomDir, SRSHashFile)

	if err := os.WriteFile(srsPath, content, 0o444); err != nil {
		return fmt.Errorf("writing SRS: %w", err)
	}

	hash := sha256.Sum256(content)
	hashStr := fmt.Sprintf("%x", hash)
	if err := os.WriteFile(hashPath, []byte(hashStr+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing SRS hash: %w", err)
	}

	return nil
}

// VerifySRS checks the SRS file integrity against its hash file.
func VerifySRS(projectRoot string) error {
	srsPath := filepath.Join(projectRoot, AxiomDir, SRSFile)
	hashPath := filepath.Join(projectRoot, AxiomDir, SRSHashFile)

	content, err := os.ReadFile(srsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No SRS yet, nothing to verify
		}
		return fmt.Errorf("reading SRS: %w", err)
	}

	storedHash, err := os.ReadFile(hashPath)
	if err != nil {
		return fmt.Errorf("reading SRS hash: %w", err)
	}

	actual := fmt.Sprintf("%x", sha256.Sum256(content))
	expected := strings.TrimSpace(string(storedHash))

	if actual != expected {
		return fmt.Errorf("SRS integrity check failed: hash mismatch (expected %s, got %s)", expected, actual)
	}

	return nil
}
