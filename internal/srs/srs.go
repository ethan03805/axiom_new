// Package srs implements SRS (Software Requirements Specification) validation,
// bootstrap context building, and draft persistence for Axiom.
// Per Architecture Sections 6 and 8.7.
package srs

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Required top-level sections per Architecture Section 6.1.
var requiredSections = []string{
	"## 1. Architecture",
	"## 2. Requirements & Constraints",
	"## 3. Test Strategy",
	"## 4. Acceptance Criteria",
}

// ValidateStructure checks that the SRS content contains the required sections
// per Architecture Section 6.1. It does not validate section contents, only
// that the expected structure is present.
func ValidateStructure(content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("SRS content is empty")
	}

	// Check for SRS title
	if !strings.Contains(content, "# SRS:") {
		return errors.New("SRS must start with a title in the format: # SRS: <Project Name>")
	}

	var missing []string
	for _, section := range requiredSections {
		if !strings.Contains(content, section) {
			// Extract the section name for the error message
			name := strings.TrimPrefix(section, "## ")
			// Remove the number prefix for readability
			parts := strings.SplitN(name, ". ", 2)
			if len(parts) == 2 {
				name = parts[1]
			}
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("SRS is missing required sections: %s", strings.Join(missing, ", "))
	}

	return nil
}

// BootstrapContext holds the context provided to the orchestrator during
// SRS generation, per Architecture Section 8.7.
type BootstrapContext struct {
	ProjectRoot string
	IsGreenfield bool
	RepoMap     string // file listing of existing project (empty for greenfield)
}

// BuildBootstrapContext assembles the bootstrap context for SRS generation.
// For greenfield projects, only the project root is provided.
// For existing projects, a read-only repo-map is built (excluding .axiom/).
// Per Architecture Section 8.7.
func BuildBootstrapContext(projectRoot string, isGreenfield bool) (*BootstrapContext, error) {
	ctx := &BootstrapContext{
		ProjectRoot:  projectRoot,
		IsGreenfield: isGreenfield,
	}

	if isGreenfield {
		return ctx, nil
	}

	// Build repo-map for existing projects
	var files []string
	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}

		rel, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return nil
		}

		// Normalize to forward slashes for consistency
		rel = filepath.ToSlash(rel)

		// Exclude .axiom/ and other internal directories per Architecture Section 2.2
		if info.IsDir() {
			if rel == ".axiom" || rel == ".git" || rel == "node_modules" || rel == ".axiom/" {
				return filepath.SkipDir
			}
			return nil
		}

		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("building repo map: %w", err)
	}

	ctx.RepoMap = strings.Join(files, "\n")
	return ctx, nil
}

// draftPath returns the file path for a pending SRS draft.
func draftPath(projectRoot, runID string) string {
	return filepath.Join(projectRoot, ".axiom", fmt.Sprintf("srs-draft-%s.md", runID))
}

// WriteDraft persists a pending SRS draft for a run.
func WriteDraft(projectRoot, runID, content string) error {
	path := draftPath(projectRoot, runID)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing SRS draft: %w", err)
	}
	return nil
}

// ReadDraft reads a pending SRS draft for a run.
func ReadDraft(projectRoot, runID string) (string, error) {
	path := draftPath(projectRoot, runID)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading SRS draft: %w", err)
	}
	return string(data), nil
}

// DeleteDraft removes a pending SRS draft. No error if the draft doesn't exist.
func DeleteDraft(projectRoot, runID string) error {
	path := draftPath(projectRoot, runID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting SRS draft: %w", err)
	}
	return nil
}

// ComputeHash returns the hex-encoded SHA-256 hash of the given content.
func ComputeHash(content []byte) string {
	h := sha256.Sum256(content)
	return fmt.Sprintf("%x", h)
}
