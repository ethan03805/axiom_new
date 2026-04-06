package state

import (
	"database/sql"
	"fmt"
)

// --- IndexFile operations ---

// CreateIndexFile inserts a new file entry into the semantic index.
func (d *DB) CreateIndexFile(f *IndexFile) (int64, error) {
	res, err := d.Exec(`INSERT INTO index_files (path, language, hash) VALUES (?, ?, ?)`,
		f.Path, f.Language, f.Hash)
	if err != nil {
		return 0, fmt.Errorf("creating index file %s: %w", f.Path, err)
	}
	return res.LastInsertId()
}

// GetIndexFile retrieves an indexed file by path. Returns ErrNotFound if absent.
func (d *DB) GetIndexFile(path string) (*IndexFile, error) {
	var f IndexFile
	var indexedAt string
	err := d.QueryRow(`SELECT id, path, language, hash, indexed_at FROM index_files WHERE path = ?`, path).
		Scan(&f.ID, &f.Path, &f.Language, &f.Hash, &indexedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting index file %s: %w", path, err)
	}
	f.IndexedAt = parseTime(indexedAt)
	return &f, nil
}

// DeleteIndexFile removes a file and its associated symbols, imports, and references
// via ON DELETE CASCADE.
func (d *DB) DeleteIndexFile(path string) error {
	_, err := d.Exec(`DELETE FROM index_files WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("deleting index file %s: %w", path, err)
	}
	return nil
}

// UpdateIndexFileHash updates the content hash and timestamp for an indexed file.
func (d *DB) UpdateIndexFileHash(path, hash string) error {
	_, err := d.Exec(`UPDATE index_files SET hash = ?, indexed_at = CURRENT_TIMESTAMP WHERE path = ?`,
		hash, path)
	if err != nil {
		return fmt.Errorf("updating index file hash %s: %w", path, err)
	}
	return nil
}

// ListIndexFiles returns all indexed files ordered by path.
func (d *DB) ListIndexFiles() ([]IndexFile, error) {
	rows, err := d.Query(`SELECT id, path, language, hash, indexed_at FROM index_files ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("listing index files: %w", err)
	}
	defer rows.Close()

	var files []IndexFile
	for rows.Next() {
		var f IndexFile
		var indexedAt string
		if err := rows.Scan(&f.ID, &f.Path, &f.Language, &f.Hash, &indexedAt); err != nil {
			return nil, fmt.Errorf("scanning index file: %w", err)
		}
		f.IndexedAt = parseTime(indexedAt)
		files = append(files, f)
	}
	return files, rows.Err()
}

// ClearIndex removes all data from semantic index tables.
func (d *DB) ClearIndex() error {
	tables := []string{
		"index_package_deps", "index_packages",
		"index_references", "index_imports", "index_symbols", "index_files",
	}
	for _, table := range tables {
		if _, err := d.Exec("DELETE FROM " + table); err != nil {
			return fmt.Errorf("clearing %s: %w", table, err)
		}
	}
	return nil
}

// --- IndexSymbol operations ---

// CreateIndexSymbol inserts a symbol into the index.
func (d *DB) CreateIndexSymbol(s *IndexSymbol) (int64, error) {
	res, err := d.Exec(`INSERT INTO index_symbols
		(file_id, name, kind, line, signature, return_type, exported, parent_symbol_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.FileID, s.Name, string(s.Kind), s.Line,
		s.Signature, s.ReturnType,
		boolToInt(s.Exported), s.ParentSymbolID)
	if err != nil {
		return 0, fmt.Errorf("creating index symbol %s: %w", s.Name, err)
	}
	return res.LastInsertId()
}

// ListSymbolsByFile returns all symbols in a given file.
func (d *DB) ListSymbolsByFile(fileID int64) ([]IndexSymbol, error) {
	rows, err := d.Query(`SELECT id, file_id, name, kind, line, signature, return_type, exported, parent_symbol_id
		FROM index_symbols WHERE file_id = ? ORDER BY line`, fileID)
	if err != nil {
		return nil, fmt.Errorf("listing symbols for file %d: %w", fileID, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// LookupSymbol finds symbols by name, optionally filtered by kind.
// Returns results with joined file paths. Per Architecture Section 17.5.
func (d *DB) LookupSymbol(name, kind string) ([]IndexSymbol, error) {
	var rows *sql.Rows
	var err error
	if kind != "" {
		rows, err = d.Query(`SELECT s.id, s.file_id, s.name, s.kind, s.line,
			s.signature, s.return_type, s.exported, s.parent_symbol_id, f.path
			FROM index_symbols s JOIN index_files f ON s.file_id = f.id
			WHERE s.name = ? AND s.kind = ? ORDER BY f.path, s.line`, name, kind)
	} else {
		rows, err = d.Query(`SELECT s.id, s.file_id, s.name, s.kind, s.line,
			s.signature, s.return_type, s.exported, s.parent_symbol_id, f.path
			FROM index_symbols s JOIN index_files f ON s.file_id = f.id
			WHERE s.name = ? ORDER BY f.path, s.line`, name)
	}
	if err != nil {
		return nil, fmt.Errorf("looking up symbol %s: %w", name, err)
	}
	defer rows.Close()
	return scanSymbolsWithPath(rows)
}

// ListExportedSymbolsByPackageDir returns exported symbols whose files are in
// the given directory prefix. Per Architecture Section 17.5 list_exports.
func (d *DB) ListExportedSymbolsByPackageDir(dir string) ([]IndexSymbol, error) {
	pattern := dir + "/%"
	rows, err := d.Query(`SELECT s.id, s.file_id, s.name, s.kind, s.line,
		s.signature, s.return_type, s.exported, s.parent_symbol_id, f.path
		FROM index_symbols s JOIN index_files f ON s.file_id = f.id
		WHERE s.exported = 1 AND (f.path LIKE ? OR f.path LIKE ?)
		ORDER BY f.path, s.line`, pattern, dir+"/%")
	if err != nil {
		return nil, fmt.Errorf("listing exports for %s: %w", dir, err)
	}
	defer rows.Close()
	return scanSymbolsWithPath(rows)
}

// FindImplementations returns references with usage_type='implementation'
// for the given interface name, joined with file paths.
// Per Architecture Section 17.5 find_implementations.
func (d *DB) FindImplementations(interfaceName string) ([]IndexReference, error) {
	rows, err := d.Query(`SELECT r.id, r.file_id, r.symbol_name, r.line, r.usage_type, f.path
		FROM index_references r JOIN index_files f ON r.file_id = f.id
		WHERE r.symbol_name = ? AND r.usage_type = 'implementation'
		ORDER BY f.path, r.line`, interfaceName)
	if err != nil {
		return nil, fmt.Errorf("finding implementations of %s: %w", interfaceName, err)
	}
	defer rows.Close()
	return scanRefsWithPath(rows)
}

// --- IndexImport operations ---

// CreateIndexImport inserts an import declaration.
func (d *DB) CreateIndexImport(imp *IndexImport) (int64, error) {
	res, err := d.Exec(`INSERT INTO index_imports (file_id, import_path, alias) VALUES (?, ?, ?)`,
		imp.FileID, imp.ImportPath, imp.Alias)
	if err != nil {
		return 0, fmt.Errorf("creating index import: %w", err)
	}
	return res.LastInsertId()
}

// ListImportsByFile returns all imports for a file.
func (d *DB) ListImportsByFile(fileID int64) ([]IndexImport, error) {
	rows, err := d.Query(`SELECT id, file_id, import_path, alias FROM index_imports WHERE file_id = ? ORDER BY import_path`, fileID)
	if err != nil {
		return nil, fmt.Errorf("listing imports for file %d: %w", fileID, err)
	}
	defer rows.Close()

	var imports []IndexImport
	for rows.Next() {
		var imp IndexImport
		if err := rows.Scan(&imp.ID, &imp.FileID, &imp.ImportPath, &imp.Alias); err != nil {
			return nil, fmt.Errorf("scanning import: %w", err)
		}
		imports = append(imports, imp)
	}
	return imports, rows.Err()
}

// ListImporterFiles returns file paths that import the given path.
func (d *DB) ListImporterFiles(importPath string) ([]string, error) {
	rows, err := d.Query(`SELECT DISTINCT f.path
		FROM index_imports i JOIN index_files f ON i.file_id = f.id
		WHERE i.import_path = ? ORDER BY f.path`, importPath)
	if err != nil {
		return nil, fmt.Errorf("listing importers of %s: %w", importPath, err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scanning importer path: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// --- IndexReference operations ---

// CreateIndexReference inserts a symbol reference.
func (d *DB) CreateIndexReference(ref *IndexReference) (int64, error) {
	res, err := d.Exec(`INSERT INTO index_references (file_id, symbol_name, line, usage_type) VALUES (?, ?, ?, ?)`,
		ref.FileID, ref.SymbolName, ref.Line, string(ref.UsageType))
	if err != nil {
		return 0, fmt.Errorf("creating index reference: %w", err)
	}
	return res.LastInsertId()
}

// ListReferencesBySymbol returns all references to a symbol name, joined with file paths.
// Per Architecture Section 17.5 reverse_dependencies.
func (d *DB) ListReferencesBySymbol(symbolName string) ([]IndexReference, error) {
	rows, err := d.Query(`SELECT r.id, r.file_id, r.symbol_name, r.line, r.usage_type, f.path
		FROM index_references r JOIN index_files f ON r.file_id = f.id
		WHERE r.symbol_name = ? ORDER BY f.path, r.line`, symbolName)
	if err != nil {
		return nil, fmt.Errorf("listing references for %s: %w", symbolName, err)
	}
	defer rows.Close()
	return scanRefsWithPath(rows)
}

// --- IndexPackage operations ---

// CreateIndexPackage inserts a package entry.
func (d *DB) CreateIndexPackage(pkg *IndexPackage) (int64, error) {
	res, err := d.Exec(`INSERT INTO index_packages (path, dir) VALUES (?, ?)`,
		pkg.Path, pkg.Dir)
	if err != nil {
		return 0, fmt.Errorf("creating index package %s: %w", pkg.Path, err)
	}
	return res.LastInsertId()
}

// GetIndexPackage retrieves a package by import path. Returns ErrNotFound if absent.
func (d *DB) GetIndexPackage(path string) (*IndexPackage, error) {
	var pkg IndexPackage
	err := d.QueryRow(`SELECT id, path, dir FROM index_packages WHERE path = ?`, path).
		Scan(&pkg.ID, &pkg.Path, &pkg.Dir)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting index package %s: %w", path, err)
	}
	return &pkg, nil
}

// AddPackageDep records a dependency edge. Idempotent.
func (d *DB) AddPackageDep(pkgID, depID int64) error {
	_, err := d.Exec(`INSERT OR IGNORE INTO index_package_deps (package_id, depends_on_id) VALUES (?, ?)`,
		pkgID, depID)
	if err != nil {
		return fmt.Errorf("adding package dep %d -> %d: %w", pkgID, depID, err)
	}
	return nil
}

// ListPackageDeps returns the packages that pkgID depends on.
func (d *DB) ListPackageDeps(pkgID int64) ([]IndexPackage, error) {
	rows, err := d.Query(`SELECT p.id, p.path, p.dir
		FROM index_packages p JOIN index_package_deps d ON p.id = d.depends_on_id
		WHERE d.package_id = ? ORDER BY p.path`, pkgID)
	if err != nil {
		return nil, fmt.Errorf("listing package deps for %d: %w", pkgID, err)
	}
	defer rows.Close()

	var pkgs []IndexPackage
	for rows.Next() {
		var pkg IndexPackage
		if err := rows.Scan(&pkg.ID, &pkg.Path, &pkg.Dir); err != nil {
			return nil, fmt.Errorf("scanning package dep: %w", err)
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, rows.Err()
}

// --- scan helpers ---

func scanSymbols(rows *sql.Rows) ([]IndexSymbol, error) {
	var syms []IndexSymbol
	for rows.Next() {
		var s IndexSymbol
		var kind string
		var exported int
		if err := rows.Scan(&s.ID, &s.FileID, &s.Name, &kind, &s.Line,
			&s.Signature, &s.ReturnType, &exported, &s.ParentSymbolID); err != nil {
			return nil, fmt.Errorf("scanning symbol: %w", err)
		}
		s.Kind = SymbolKind(kind)
		s.Exported = exported != 0
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

func scanSymbolsWithPath(rows *sql.Rows) ([]IndexSymbol, error) {
	var syms []IndexSymbol
	for rows.Next() {
		var s IndexSymbol
		var kind string
		var exported int
		if err := rows.Scan(&s.ID, &s.FileID, &s.Name, &kind, &s.Line,
			&s.Signature, &s.ReturnType, &exported, &s.ParentSymbolID, &s.FilePath); err != nil {
			return nil, fmt.Errorf("scanning symbol with path: %w", err)
		}
		s.Kind = SymbolKind(kind)
		s.Exported = exported != 0
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

func scanRefsWithPath(rows *sql.Rows) ([]IndexReference, error) {
	var refs []IndexReference
	for rows.Next() {
		var r IndexReference
		var usageType string
		if err := rows.Scan(&r.ID, &r.FileID, &r.SymbolName, &r.Line, &usageType, &r.FilePath); err != nil {
			return nil, fmt.Errorf("scanning reference with path: %w", err)
		}
		r.UsageType = UsageType(usageType)
		refs = append(refs, r)
	}
	return refs, rows.Err()
}
