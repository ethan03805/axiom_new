// Package eco implements Engineering Change Order (ECO) validation and
// persistence for Axiom. Per Architecture Section 7.
package eco

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Valid ECO category codes per Architecture Section 7.2.
var validCategories = map[string]string{
	"ECO-DEP": "Dependency Unavailable",
	"ECO-API": "API Breaking Change",
	"ECO-SEC": "Security Vulnerability",
	"ECO-PLT": "Platform Incompatibility",
	"ECO-LIC": "License Conflict",
	"ECO-PRV": "Provider Limitation",
}

// ValidCategory returns true if the given code is a valid ECO category.
func ValidCategory(code string) bool {
	_, ok := validCategories[code]
	return ok
}

// CategoryDescription returns the human-readable description for an ECO category.
// Returns empty string if the code is invalid.
func CategoryDescription(code string) string {
	return validCategories[code]
}

// Proposal represents an ECO proposal before persistence.
type Proposal struct {
	Category       string
	AffectedRefs   string
	Description    string
	ProposedChange string
}

// ValidateProposal checks that an ECO proposal has all required fields
// and uses a valid category code.
func ValidateProposal(p Proposal) error {
	var errs []error

	if !ValidCategory(p.Category) {
		errs = append(errs, fmt.Errorf("invalid ECO category %q: must be one of ECO-DEP, ECO-API, ECO-SEC, ECO-PLT, ECO-LIC, ECO-PRV", p.Category))
	}
	if strings.TrimSpace(p.Description) == "" {
		errs = append(errs, errors.New("ECO description is required"))
	}
	if strings.TrimSpace(p.AffectedRefs) == "" {
		errs = append(errs, errors.New("ECO affected SRS refs are required"))
	}
	if strings.TrimSpace(p.ProposedChange) == "" {
		errs = append(errs, errors.New("ECO proposed change is required"))
	}

	return errors.Join(errs...)
}

// Record represents an ECO record for file persistence.
// Per Architecture Section 7.4.
type Record struct {
	ECOCode        string
	Category       string
	Status         string
	AffectedRefs   string
	Description    string
	ProposedChange string
}

// WriteECOFile writes an ECO record as a markdown file under .axiom/eco/.
// Files are append-only (new file per ECO, never overwritten).
// Format follows Architecture Section 7.4.
func WriteECOFile(projectRoot string, r Record) error {
	ecoDir := filepath.Join(projectRoot, ".axiom", "eco")

	content := formatECOMarkdown(r)
	filename := fmt.Sprintf("%s.md", r.ECOCode)
	path := filepath.Join(ecoDir, filename)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing ECO file: %w", err)
	}

	return nil
}

// ListECOFiles returns the filenames of all ECO records under .axiom/eco/,
// sorted alphabetically.
func ListECOFiles(projectRoot string) ([]string, error) {
	ecoDir := filepath.Join(projectRoot, ".axiom", "eco")

	entries, err := os.ReadDir(ecoDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading ECO directory: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e.Name())
		}
	}

	sort.Strings(files)
	return files, nil
}

// formatECOMarkdown renders an ECO record in the format specified by
// Architecture Section 7.4.
func formatECOMarkdown(r Record) string {
	now := time.Now().UTC().Format(time.RFC3339)

	var b strings.Builder
	fmt.Fprintf(&b, "## %s: [%s] %s\n\n", r.ECOCode, r.Category, CategoryDescription(r.Category))
	fmt.Fprintf(&b, "**Filed:** %s\n", now)
	fmt.Fprintf(&b, "**Status:** %s\n", r.Status)
	fmt.Fprintf(&b, "**Affected SRS Sections:** %s\n\n", r.AffectedRefs)
	fmt.Fprintf(&b, "### Environmental Issue\n%s\n\n", r.Description)
	fmt.Fprintf(&b, "### Proposed Substitute\n%s\n\n", r.ProposedChange)
	fmt.Fprintf(&b, "### Impact Assessment\n- Affected references: %s\n", r.AffectedRefs)

	return b.String()
}
