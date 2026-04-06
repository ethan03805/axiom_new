// Package manifest implements parsing and validation of Meeseeks output
// manifests per Architecture Sections 10.4 and 14.1–14.2.
//
// Every Meeseeks emits a manifest.json alongside its output files in
// /workspace/staging/. The engine validates this manifest before any
// output enters the validation sandbox or reviewer pipeline.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Manifest represents the manifest.json emitted by a Meeseeks.
// Per Architecture Section 10.4.
type Manifest struct {
	TaskID       string        `json:"task_id"`
	BaseSnapshot string        `json:"base_snapshot"`
	Files        ManifestFiles `json:"files"`
}

// ManifestFiles groups file operations declared in the manifest.
type ManifestFiles struct {
	Added    []FileEntry   `json:"added"`
	Modified []FileEntry   `json:"modified"`
	Deleted  []string      `json:"deleted"`
	Renamed  []RenameEntry `json:"renamed"`
}

// FileEntry describes an added or modified file.
// Binary files must include SizeBytes.
type FileEntry struct {
	Path      string `json:"path"`
	Binary    bool   `json:"binary"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// RenameEntry describes a file rename operation.
// Per Architecture Section 10.4: renames are first-class operations
// and SHALL NOT be degraded into delete-plus-add pairs.
type RenameEntry struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ValidationConfig controls manifest validation thresholds.
type ValidationConfig struct {
	MaxFileSizeBytes int64 // 0 means no limit
}

// DefaultValidationConfig returns the default manifest validation config.
func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{
		MaxFileSizeBytes: 50 * 1024 * 1024, // 50 MB
	}
}

// ParseManifest parses a manifest.json byte slice into a Manifest.
// Returns an error if the JSON is malformed or required fields are missing.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest JSON: %w", err)
	}
	if m.TaskID == "" {
		return nil, fmt.Errorf("manifest missing required field: task_id")
	}
	if m.BaseSnapshot == "" {
		return nil, fmt.Errorf("manifest missing required field: base_snapshot")
	}
	return &m, nil
}

// ValidateManifest checks a parsed manifest against the staging directory.
// Per Architecture Section 14.2, Stage 1:
//   - All files listed in manifest exist in staging
//   - No files in staging are unlisted in manifest
//   - All paths are canonicalized (no traversal, no symlinks)
//   - No oversized files
//   - All paths within expected output scope (if scope is non-nil)
//   - No duplicate paths
//
// Returns a slice of validation errors; empty slice means valid.
func ValidateManifest(m *Manifest, stagingDir string, allowedScope []string, cfg ValidationConfig) []error {
	var errs []error

	// Collect all output paths (files that should exist in staging)
	outputPaths := m.AllOutputPaths()

	// Check for duplicate paths
	seen := make(map[string]bool)
	for _, p := range outputPaths {
		if seen[p] {
			errs = append(errs, fmt.Errorf("duplicate path in manifest: %q", p))
		}
		seen[p] = true
	}

	// Validate all referenced paths (including deleted and rename "from" paths)
	allPaths := m.AllReferencedPaths()
	for _, p := range allPaths {
		if perrs := validatePath(p); len(perrs) > 0 {
			errs = append(errs, perrs...)
		}
	}

	// Scope enforcement: check all referenced paths against allowed scope
	if allowedScope != nil {
		for _, p := range allPaths {
			if !isInScope(p, allowedScope) {
				errs = append(errs, fmt.Errorf("path %q is outside allowed scope %v", p, allowedScope))
			}
		}
	}

	// Check that all output files exist in staging
	for _, p := range outputPaths {
		fullPath := filepath.Join(stagingDir, filepath.FromSlash(p))
		info, err := os.Lstat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("manifest lists %q but file not found in staging", p))
			} else {
				errs = append(errs, fmt.Errorf("checking %q: %w", p, err))
			}
			continue
		}

		// Reject symlinks
		if info.Mode()&fs.ModeSymlink != 0 {
			errs = append(errs, fmt.Errorf("path %q is a symlink; symlinks are not allowed", p))
		}

		// Reject non-regular files (device files, FIFOs, etc.)
		if !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
			errs = append(errs, fmt.Errorf("path %q is not a regular file (mode %s)", p, info.Mode()))
		}

		// File size check
		if cfg.MaxFileSizeBytes > 0 && info.Size() > cfg.MaxFileSizeBytes {
			errs = append(errs, fmt.Errorf("file %q exceeds max size (%d > %d bytes)",
				p, info.Size(), cfg.MaxFileSizeBytes))
		}
	}

	// Check for unlisted files in staging
	if uerrs := checkUnlistedFiles(stagingDir, seen); len(uerrs) > 0 {
		errs = append(errs, uerrs...)
	}

	return errs
}

// AllOutputPaths returns all file paths that should exist in the staging directory.
// This includes added, modified, and rename "to" paths.
func (m *Manifest) AllOutputPaths() []string {
	var paths []string
	for _, f := range m.Files.Added {
		paths = append(paths, f.Path)
	}
	for _, f := range m.Files.Modified {
		paths = append(paths, f.Path)
	}
	for _, r := range m.Files.Renamed {
		paths = append(paths, r.To)
	}
	return paths
}

// AllReferencedPaths returns every path referenced in the manifest,
// including deleted paths and rename "from" paths.
func (m *Manifest) AllReferencedPaths() []string {
	var paths []string
	for _, f := range m.Files.Added {
		paths = append(paths, f.Path)
	}
	for _, f := range m.Files.Modified {
		paths = append(paths, f.Path)
	}
	paths = append(paths, m.Files.Deleted...)
	for _, r := range m.Files.Renamed {
		paths = append(paths, r.From)
		paths = append(paths, r.To)
	}
	return paths
}

// ArtifactRecord holds computed artifact data for persistence.
// Fields map directly to state.TaskArtifact but are package-local
// to avoid a dependency cycle.
type ArtifactRecord struct {
	AttemptID    int64
	Operation    string
	PathFrom     *string
	PathTo       *string
	SHA256Before *string
	SHA256After  *string
	SizeBefore   *int64
	SizeAfter    *int64
}

// ComputeArtifacts computes artifact records from a validated manifest.
// For add/modify/rename, it reads files from stagingDir to compute SHA256
// hashes and sizes. attemptID is stored for later persistence.
func ComputeArtifacts(m *Manifest, stagingDir string, attemptID int64) ([]ArtifactRecord, error) {
	var arts []ArtifactRecord

	for _, f := range m.Files.Added {
		hash, size, err := hashFile(filepath.Join(stagingDir, filepath.FromSlash(f.Path)))
		if err != nil {
			return nil, fmt.Errorf("hashing added file %q: %w", f.Path, err)
		}
		p := f.Path
		arts = append(arts, ArtifactRecord{
			AttemptID:   attemptID,
			Operation:   "add",
			PathTo:      &p,
			SHA256After: &hash,
			SizeAfter:   &size,
		})
	}

	for _, f := range m.Files.Modified {
		hash, size, err := hashFile(filepath.Join(stagingDir, filepath.FromSlash(f.Path)))
		if err != nil {
			return nil, fmt.Errorf("hashing modified file %q: %w", f.Path, err)
		}
		p := f.Path
		arts = append(arts, ArtifactRecord{
			AttemptID:   attemptID,
			Operation:   "modify",
			PathTo:      &p,
			SHA256After: &hash,
			SizeAfter:   &size,
		})
	}

	for _, d := range m.Files.Deleted {
		p := d
		arts = append(arts, ArtifactRecord{
			AttemptID: attemptID,
			Operation: "delete",
			PathFrom:  &p,
		})
	}

	for _, r := range m.Files.Renamed {
		hash, size, err := hashFile(filepath.Join(stagingDir, filepath.FromSlash(r.To)))
		if err != nil {
			return nil, fmt.Errorf("hashing renamed file %q: %w", r.To, err)
		}
		from := r.From
		to := r.To
		arts = append(arts, ArtifactRecord{
			AttemptID:   attemptID,
			Operation:   "rename",
			PathFrom:    &from,
			PathTo:      &to,
			SHA256After: &hash,
			SizeAfter:   &size,
		})
	}

	return arts, nil
}

// --- internal helpers ---

// validatePath checks a single path for safety violations.
func validatePath(p string) []error {
	var errs []error

	// Reject absolute paths
	if filepath.IsAbs(p) || (len(p) > 0 && p[0] == '/') {
		errs = append(errs, fmt.Errorf("path %q is absolute; only relative paths are allowed", p))
	}

	// Reject path traversal
	cleaned := filepath.ToSlash(filepath.Clean(p))
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/../") {
		errs = append(errs, fmt.Errorf("path %q escapes the staging directory (traversal detected)", p))
	}

	return errs
}

// isInScope checks whether a path falls under at least one of the allowed scope prefixes.
func isInScope(p string, scopes []string) bool {
	normalized := filepath.ToSlash(p)
	for _, scope := range scopes {
		scope = filepath.ToSlash(scope)
		if strings.HasPrefix(normalized, scope) {
			return true
		}
	}
	return false
}

// checkUnlistedFiles walks the staging directory and reports any files
// not declared in the manifest's output paths.
func checkUnlistedFiles(stagingDir string, declaredOutputs map[string]bool) []error {
	var errs []error

	err := filepath.WalkDir(stagingDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Skip manifest.json itself
		rel, relErr := filepath.Rel(stagingDir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "manifest.json" {
			return nil
		}

		if !declaredOutputs[rel] {
			errs = append(errs, fmt.Errorf("file %q in staging is not listed in manifest", rel))
		}
		return nil
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("walking staging directory: %w", err))
	}

	return errs
}

// hashFile computes the SHA256 hash and size of a file using streaming
// to avoid loading large files (up to 50 MB) entirely into memory.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
