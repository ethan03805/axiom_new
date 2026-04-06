package testfixtures

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
)

// Materialize copies a named fixture repo into a temporary directory and
// initializes it as a real git repository with an initial commit on main.
func Materialize(name string) (string, error) {
	src, err := SourceDir(name)
	if err != nil {
		return "", err
	}

	tempRoot, err := os.MkdirTemp("", "axiom-fixture-*")
	if err != nil {
		return "", fmt.Errorf("creating temp fixture root: %w", err)
	}

	dest := filepath.Join(tempRoot, filepath.Base(src))
	if err := copyDir(src, dest); err != nil {
		_ = os.RemoveAll(tempRoot)
		return "", err
	}

	if err := initGitRepo(dest); err != nil {
		_ = os.RemoveAll(tempRoot)
		return "", err
	}

	return dest, nil
}

// SourceDir returns the on-disk path for a named fixture repo.
func SourceDir(name string) (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locating fixture helper source")
	}

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", name)
	info, err := os.Stat(fixtureDir)
	if err != nil {
		return "", fmt.Errorf("locating fixture %q: %w", name, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("fixture %q is not a directory", name)
	}
	return fixtureDir, nil
}

func initGitRepo(dir string) error {
	if err := git(dir, "init", "-b", "main"); err != nil {
		if fallbackErr := git(dir, "init"); fallbackErr != nil {
			return err
		}
		if err := git(dir, "branch", "-M", "main"); err != nil {
			return err
		}
	}

	if err := git(dir, "config", "user.name", "Axiom Fixtures"); err != nil {
		return err
	}
	if err := git(dir, "config", "user.email", "fixtures@axiom.local"); err != nil {
		return err
	}
	if err := git(dir, "add", "."); err != nil {
		return err
	}
	if err := git(dir, "commit", "-m", "fixture baseline"); err != nil {
		return err
	}
	return nil
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %s", args, string(out))
	}
	return nil
}

func copyDir(src, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("creating fixture destination %s: %w", dest, err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("reading fixture source %s: %w", src, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}

		srcPath := filepath.Join(src, entry.Name())
		destPath := filepath.Join(dest, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, destPath); err != nil {
				return err
			}
			continue
		}

		if err := copyFile(srcPath, destPath); err != nil {
			return err
		}
	}

	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening fixture file %s: %w", src, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating fixture parent dir %s: %w", filepath.Dir(dest), err)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating fixture file %s: %w", dest, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying fixture file %s: %w", src, err)
	}
	return out.Close()
}
