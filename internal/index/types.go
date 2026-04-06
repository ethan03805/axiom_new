package index

import "github.com/openaxiom/axiom/internal/state"

// SymbolResult is the query result for symbol lookups.
// Per Architecture Section 17.5.
type SymbolResult = state.IndexSymbol

// ReferenceResult is the query result for reverse-dependency lookups.
type ReferenceResult = state.IndexReference

// ModuleGraphResult holds the package dependency graph.
// Per Architecture Section 17.5 module_graph.
type ModuleGraphResult struct {
	Packages []PackageNode
	Edges    []PackageEdge
}

// PackageNode represents a package in the dependency graph.
type PackageNode struct {
	Path string
	Dir  string
}

// PackageEdge represents a dependency between two packages.
type PackageEdge struct {
	From string // package path
	To   string // package path
}

// excludedDirs lists directories that should never be indexed.
// Per Architecture Section 17.4 and 2.8.
var excludedDirs = map[string]bool{
	".axiom":       true,
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"__pycache__":  true,
	"target":       true, // Rust build output
	"dist":         true,
	"build":        true,
}

// excludedFiles lists file patterns that should never be indexed.
var excludedFiles = map[string]bool{
	"go.mod":  true,
	"go.sum":  true,
	".DS_Store": true,
}

// languageByExt maps file extensions to language names.
var languageByExt = map[string]string{
	".go":   "go",
	".ts":   "typescript",
	".tsx":  "typescript",
	".js":   "javascript",
	".jsx":  "javascript",
	".py":   "python",
	".rs":   "rust",
}
