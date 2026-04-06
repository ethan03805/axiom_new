package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/openaxiom/axiom/internal/state"
)

// Indexer is the semantic index service per Architecture Section 17.
// It maintains a structured, queryable index of project symbols, exports,
// interfaces, and dependency relationships in SQLite.
type Indexer struct {
	db  *state.DB
	log *slog.Logger
}

// NewIndexer creates a new semantic indexer backed by the given database.
func NewIndexer(db *state.DB, log *slog.Logger) *Indexer {
	if log == nil {
		log = slog.Default()
	}
	return &Indexer{db: db, log: log}
}

// Index performs a full project index. Clears existing data first.
// Per Architecture Section 17.4: refreshed after project initialization.
func (idx *Indexer) Index(ctx context.Context, dir string) error {
	// Verify directory exists
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("index directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}

	// Clear existing index for full reindex
	if err := idx.db.ClearIndex(); err != nil {
		return fmt.Errorf("clearing index: %w", err)
	}

	// Walk directory and index all supported files
	var files []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			if excludedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(d.Name())
		if _, ok := languageByExt[ext]; ok {
			if !excludedFiles[d.Name()] {
				rel, _ := filepath.Rel(dir, path)
				rel = filepath.ToSlash(rel)
				files = append(files, rel)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking directory: %w", err)
	}

	for _, relPath := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := idx.indexFile(dir, relPath); err != nil {
			idx.log.Warn("indexing file failed", "path", relPath, "error", err)
		}
	}

	// Detect Go interface implementations by matching method sets
	idx.detectGoImplementations()

	// Build package dependency graph from imports
	idx.buildPackageGraph(dir, files)

	idx.log.Info("full index complete", "files", len(files), "dir", dir)
	return nil
}

// IndexFiles performs incremental indexing of specific files.
// Per Architecture Section 17.4: refreshed after each successful commit.
func (idx *Indexer) IndexFiles(ctx context.Context, dir string, paths []string) error {
	for _, relPath := range paths {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		absPath := filepath.Join(dir, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			// File was deleted — remove from index
			idx.db.DeleteIndexFile(relPath)
			continue
		}

		// Check if content changed
		hash := hashContent(content)
		existing, err := idx.db.GetIndexFile(relPath)
		if err == nil && existing.Hash == hash {
			// Content unchanged — skip
			continue
		}

		// Remove old data for this file
		idx.db.DeleteIndexFile(relPath)

		// Re-index the file
		if err := idx.indexFile(dir, relPath); err != nil {
			idx.log.Warn("indexing file failed", "path", relPath, "error", err)
		}
	}

	idx.log.Info("incremental index complete", "files", len(paths))
	return nil
}

// indexFile parses and stores index data for a single file.
func (idx *Indexer) indexFile(dir, relPath string) error {
	absPath := filepath.Join(dir, relPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", relPath, err)
	}

	ext := filepath.Ext(relPath)
	lang, ok := languageByExt[ext]
	if !ok {
		return nil // unsupported language
	}

	parser := getParser(lang)
	if parser == nil {
		return nil // no parser registered
	}

	hash := hashContent(content)

	// Create file entry
	fileID, err := idx.db.CreateIndexFile(&state.IndexFile{
		Path:     relPath,
		Language: lang,
		Hash:     hash,
	})
	if err != nil {
		return fmt.Errorf("creating file entry: %w", err)
	}

	// Parse file
	result, err := parser.Parse(content, relPath)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", relPath, err)
	}

	// Store symbols — first pass: create all symbols
	symbolIDs := make(map[string]int64) // "name" -> ID for parent linking
	var deferredSymbols []state.IndexSymbol

	for i := range result.Symbols {
		sym := &result.Symbols[i]
		sym.FileID = fileID

		// If this symbol has a parent reference (stored temporarily in FilePath),
		// defer it until parent symbols are created
		if sym.FilePath != "" {
			deferredSymbols = append(deferredSymbols, *sym)
			continue
		}

		id, err := idx.db.CreateIndexSymbol(sym)
		if err != nil {
			idx.log.Warn("storing symbol", "name", sym.Name, "error", err)
			continue
		}
		symbolIDs[sym.Name] = id
	}

	// Second pass: create child symbols with parent references
	for i := range deferredSymbols {
		sym := &deferredSymbols[i]
		parentName := sym.FilePath
		sym.FilePath = "" // clear temporary storage

		if parentID, ok := symbolIDs[parentName]; ok {
			sym.ParentSymbolID = &parentID
		}

		id, err := idx.db.CreateIndexSymbol(sym)
		if err != nil {
			idx.log.Warn("storing child symbol", "name", sym.Name, "error", err)
			continue
		}
		symbolIDs[sym.Name] = id
	}

	// Store imports
	for i := range result.Imports {
		imp := &result.Imports[i]
		imp.FileID = fileID
		if _, err := idx.db.CreateIndexImport(imp); err != nil {
			idx.log.Warn("storing import", "path", imp.ImportPath, "error", err)
		}
	}

	// Store references
	for i := range result.References {
		ref := &result.References[i]
		ref.FileID = fileID
		if _, err := idx.db.CreateIndexReference(ref); err != nil {
			idx.log.Warn("storing reference", "symbol", ref.SymbolName, "error", err)
		}
	}

	return nil
}

// buildPackageGraph constructs the package/module dependency graph
// from import declarations.
func (idx *Indexer) buildPackageGraph(dir string, files []string) {
	// Group files by directory (package)
	// Use path.Dir (not filepath.Dir) since stored paths use forward slashes.
	pkgDirs := make(map[string]bool)
	for _, f := range files {
		d := pathpkg.Dir(f)
		if d == "." {
			d = ""
		}
		pkgDirs[d] = true
	}

	// Detect Go module path for resolving import paths
	goModPath := detectGoModulePath(dir)

	// Create package entries
	pkgIDs := make(map[string]int64)
	for d := range pkgDirs {
		pkgPath := d
		if pkgPath == "" {
			pkgPath = "."
		}
		id, err := idx.db.CreateIndexPackage(&state.IndexPackage{
			Path: pkgPath,
			Dir:  pkgPath,
		})
		if err != nil {
			continue
		}
		pkgIDs[pkgPath] = id

		// Also register with full Go module path if available
		if goModPath != "" && pkgPath != "." {
			fullPath := goModPath + "/" + pkgPath
			pkgIDs[fullPath] = id
		}
	}

	// Build edges from import declarations
	for _, f := range files {
		d := pathpkg.Dir(f)
		if d == "." {
			d = ""
		}
		srcPkg := d
		if srcPkg == "" {
			srcPkg = "."
		}
		srcID, ok := pkgIDs[srcPkg]
		if !ok {
			continue
		}

		fileEntry, err := idx.db.GetIndexFile(f)
		if err != nil {
			continue
		}

		imports, err := idx.db.ListImportsByFile(fileEntry.ID)
		if err != nil {
			continue
		}

		for _, imp := range imports {
			// Try to find the target package in our index
			if depID, ok := pkgIDs[imp.ImportPath]; ok && depID != srcID {
				idx.db.AddPackageDep(srcID, depID)
			}
		}
	}
}

// detectGoModulePath reads the go.mod file to determine the module path.
func detectGoModulePath(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

// detectGoImplementations scans indexed Go symbols to find struct types whose
// method sets match interface method sets, and records implementation references.
// This is a post-index pass since implementations span multiple symbols.
func (idx *Indexer) detectGoImplementations() {
	// Find all interfaces
	ifaceRows, err := idx.db.Query(`SELECT s.id, s.name, s.file_id, f.path
		FROM index_symbols s JOIN index_files f ON s.file_id = f.id
		WHERE s.kind = 'interface' AND f.language = 'go'`)
	if err != nil {
		return
	}
	defer ifaceRows.Close()

	type ifaceInfo struct {
		id      int64
		name    string
		fileID  int64
		dir     string
		methods []string
	}

	var ifaces []ifaceInfo
	for ifaceRows.Next() {
		var info ifaceInfo
		var filePath string
		if err := ifaceRows.Scan(&info.id, &info.name, &info.fileID, &filePath); err != nil {
			continue
		}
		info.dir = filepath.Dir(filePath)
		ifaces = append(ifaces, info)
	}

	// Get methods for each interface (child symbols)
	for i := range ifaces {
		methRows, err := idx.db.Query(`SELECT name FROM index_symbols
			WHERE parent_symbol_id = ? AND kind = 'method'`, ifaces[i].id)
		if err != nil {
			continue
		}
		for methRows.Next() {
			var name string
			if err := methRows.Scan(&name); err != nil {
				continue
			}
			ifaces[i].methods = append(ifaces[i].methods, name)
		}
		methRows.Close()
	}

	// Find types with methods that match (same package directory)
	for _, iface := range ifaces {
		if len(iface.methods) == 0 {
			continue
		}

		// Find all type symbols in the same package directory
		typeRows, err := idx.db.Query(`SELECT s.id, s.name, s.file_id, f.path
			FROM index_symbols s JOIN index_files f ON s.file_id = f.id
			WHERE s.kind = 'type' AND f.language = 'go'`)
		if err != nil {
			continue
		}

		for typeRows.Next() {
			var typeID int64
			var typeName string
			var typeFileID int64
			var typeFilePath string
			if err := typeRows.Scan(&typeID, &typeName, &typeFileID, &typeFilePath); err != nil {
				continue
			}

			// Check if this type has all the interface methods
			// Look for methods with parent_symbol_id = typeID
			methRows, err := idx.db.Query(`SELECT name FROM index_symbols
				WHERE parent_symbol_id = ? AND kind = 'method'`, typeID)
			if err != nil {
				continue
			}
			typeMethods := make(map[string]bool)
			for methRows.Next() {
				var name string
				methRows.Scan(&name)
				typeMethods[name] = true
			}
			methRows.Close()

			// Also check for methods with receiver matching the type name
			// (methods without parent_symbol_id linked)
			recvRows, err := idx.db.Query(`SELECT s.name FROM index_symbols s
				JOIN index_files f ON s.file_id = f.id
				WHERE s.kind = 'method' AND f.language = 'go'
				AND s.parent_symbol_id = ?`, typeID)
			if err == nil {
				for recvRows.Next() {
					var name string
					recvRows.Scan(&name)
					typeMethods[name] = true
				}
				recvRows.Close()
			}

			// Check if all interface methods are present
			allMatch := true
			for _, m := range iface.methods {
				if !typeMethods[m] {
					allMatch = false
					break
				}
			}

			if allMatch && typeName != iface.name {
				// Record implementation reference
				idx.db.CreateIndexReference(&state.IndexReference{
					FileID:     typeFileID,
					SymbolName: iface.name,
					Line:       0, // line of type declaration (we don't have it easily here)
					UsageType:  state.UsageImplementation,
				})
			}
		}
		typeRows.Close()
	}
}

func hashContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
