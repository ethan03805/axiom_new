package state

import (
	"testing"
)

// --- Index tables exist after migration ---

func TestIndexTablesExist(t *testing.T) {
	db := testDB(t)

	tables := []string{
		"index_files", "index_symbols", "index_imports",
		"index_references", "index_packages", "index_package_deps",
	}
	for _, table := range tables {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil {
			t.Errorf("table %s should exist: %v", table, err)
		}
	}
}

// --- IndexFile CRUD ---

func TestCreateAndGetIndexFile(t *testing.T) {
	db := testDB(t)

	f := &IndexFile{
		Path:     "src/main.go",
		Language: "go",
		Hash:     "abc123",
	}
	id, err := db.CreateIndexFile(f)
	if err != nil {
		t.Fatalf("CreateIndexFile: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	got, err := db.GetIndexFile("src/main.go")
	if err != nil {
		t.Fatalf("GetIndexFile: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
	if got.Path != "src/main.go" {
		t.Errorf("Path = %q", got.Path)
	}
	if got.Language != "go" {
		t.Errorf("Language = %q", got.Language)
	}
	if got.Hash != "abc123" {
		t.Errorf("Hash = %q", got.Hash)
	}
}

func TestGetIndexFileNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetIndexFile("nonexistent.go")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateIndexFileDuplicatePath(t *testing.T) {
	db := testDB(t)

	f := &IndexFile{Path: "src/main.go", Language: "go", Hash: "abc"}
	if _, err := db.CreateIndexFile(f); err != nil {
		t.Fatal(err)
	}

	// Duplicate path should fail
	_, err := db.CreateIndexFile(f)
	if err == nil {
		t.Fatal("expected error on duplicate path")
	}
}

func TestDeleteIndexFile(t *testing.T) {
	db := testDB(t)

	f := &IndexFile{Path: "src/main.go", Language: "go", Hash: "abc"}
	if _, err := db.CreateIndexFile(f); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteIndexFile("src/main.go"); err != nil {
		t.Fatalf("DeleteIndexFile: %v", err)
	}

	_, err := db.GetIndexFile("src/main.go")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteIndexFileCascadesSymbols(t *testing.T) {
	db := testDB(t)

	fileID := seedIndexFile(t, db, "src/main.go", "go")

	// Add a symbol
	sym := &IndexSymbol{
		FileID:   fileID,
		Name:     "main",
		Kind:     SymbolFunction,
		Line:     10,
		Exported: false,
	}
	if _, err := db.CreateIndexSymbol(sym); err != nil {
		t.Fatal(err)
	}

	// Delete the file — symbol should cascade
	if err := db.DeleteIndexFile("src/main.go"); err != nil {
		t.Fatal(err)
	}

	syms, err := db.ListSymbolsByFile(fileID)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 0 {
		t.Errorf("expected 0 symbols after cascade delete, got %d", len(syms))
	}
}

func TestUpdateIndexFileHash(t *testing.T) {
	db := testDB(t)

	f := &IndexFile{Path: "src/main.go", Language: "go", Hash: "old"}
	if _, err := db.CreateIndexFile(f); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateIndexFileHash("src/main.go", "new"); err != nil {
		t.Fatalf("UpdateIndexFileHash: %v", err)
	}

	got, err := db.GetIndexFile("src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash != "new" {
		t.Errorf("Hash = %q, want %q", got.Hash, "new")
	}
}

func TestListIndexFiles(t *testing.T) {
	db := testDB(t)

	seedIndexFile(t, db, "a.go", "go")
	seedIndexFile(t, db, "b.py", "python")
	seedIndexFile(t, db, "c.ts", "typescript")

	files, err := db.ListIndexFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}
}

func TestClearIndex(t *testing.T) {
	db := testDB(t)

	fileID := seedIndexFile(t, db, "a.go", "go")
	db.CreateIndexSymbol(&IndexSymbol{FileID: fileID, Name: "Foo", Kind: SymbolFunction, Line: 1, Exported: true})
	db.CreateIndexImport(&IndexImport{FileID: fileID, ImportPath: "fmt"})
	db.CreateIndexReference(&IndexReference{FileID: fileID, SymbolName: "Foo", Line: 5, UsageType: UsageCall})
	db.CreateIndexPackage(&IndexPackage{Path: "example.com/pkg", Dir: "pkg"})

	if err := db.ClearIndex(); err != nil {
		t.Fatal(err)
	}

	files, _ := db.ListIndexFiles()
	if len(files) != 0 {
		t.Errorf("expected 0 files after clear, got %d", len(files))
	}
}

// --- IndexSymbol CRUD ---

func TestCreateAndListSymbols(t *testing.T) {
	db := testDB(t)
	fileID := seedIndexFile(t, db, "src/handler.go", "go")

	syms := []IndexSymbol{
		{FileID: fileID, Name: "HandleRequest", Kind: SymbolFunction, Line: 10, Signature: strPtr("func HandleRequest(w http.ResponseWriter, r *http.Request)"), ReturnType: nil, Exported: true},
		{FileID: fileID, Name: "handler", Kind: SymbolType, Line: 5, Exported: false},
		{FileID: fileID, Name: "MaxRetries", Kind: SymbolConstant, Line: 3, Exported: true},
	}

	for i := range syms {
		id, err := db.CreateIndexSymbol(&syms[i])
		if err != nil {
			t.Fatalf("CreateIndexSymbol[%d]: %v", i, err)
		}
		if id <= 0 {
			t.Fatalf("expected positive ID for symbol %d", i)
		}
	}

	got, err := db.ListSymbolsByFile(fileID)
	if err != nil {
		t.Fatalf("ListSymbolsByFile: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 symbols, got %d", len(got))
	}
}

func TestLookupSymbolByName(t *testing.T) {
	db := testDB(t)
	fileID := seedIndexFile(t, db, "src/auth.go", "go")

	db.CreateIndexSymbol(&IndexSymbol{
		FileID: fileID, Name: "Authenticate", Kind: SymbolFunction,
		Line: 20, Exported: true,
	})
	db.CreateIndexSymbol(&IndexSymbol{
		FileID: fileID, Name: "Authenticate", Kind: SymbolType,
		Line: 50, Exported: true,
	})

	// Lookup by name only
	results, err := db.LookupSymbol("Authenticate", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Lookup by name and kind
	results, err = db.LookupSymbol("Authenticate", string(SymbolFunction))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind != SymbolFunction {
		t.Errorf("kind = %s, want function", results[0].Kind)
	}
}

func TestLookupSymbolNotFound(t *testing.T) {
	db := testDB(t)

	results, err := db.LookupSymbol("NonExistent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSymbolWithParent(t *testing.T) {
	db := testDB(t)
	fileID := seedIndexFile(t, db, "src/user.go", "go")

	// Create a type
	typeID, err := db.CreateIndexSymbol(&IndexSymbol{
		FileID: fileID, Name: "User", Kind: SymbolType, Line: 5, Exported: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a method on that type
	methodID, err := db.CreateIndexSymbol(&IndexSymbol{
		FileID: fileID, Name: "Validate", Kind: SymbolMethod, Line: 15,
		Exported: true, ParentSymbolID: &typeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if methodID <= 0 {
		t.Fatal("expected positive method ID")
	}

	// List symbols for file and verify parent relationship
	syms, _ := db.ListSymbolsByFile(fileID)
	var method *IndexSymbol
	for i := range syms {
		if syms[i].Name == "Validate" {
			method = &syms[i]
			break
		}
	}
	if method == nil {
		t.Fatal("method not found")
	}
	if method.ParentSymbolID == nil || *method.ParentSymbolID != typeID {
		t.Errorf("ParentSymbolID = %v, want %d", method.ParentSymbolID, typeID)
	}
}

// --- IndexImport CRUD ---

func TestCreateAndListImports(t *testing.T) {
	db := testDB(t)
	fileID := seedIndexFile(t, db, "src/main.go", "go")

	imports := []IndexImport{
		{FileID: fileID, ImportPath: "fmt"},
		{FileID: fileID, ImportPath: "net/http", Alias: strPtr("http")},
		{FileID: fileID, ImportPath: "github.com/example/pkg"},
	}

	for i := range imports {
		id, err := db.CreateIndexImport(&imports[i])
		if err != nil {
			t.Fatalf("CreateIndexImport[%d]: %v", i, err)
		}
		if id <= 0 {
			t.Fatalf("expected positive ID for import %d", i)
		}
	}

	got, err := db.ListImportsByFile(fileID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(got))
	}
}

func TestListImporterFiles(t *testing.T) {
	db := testDB(t)
	f1 := seedIndexFile(t, db, "src/a.go", "go")
	f2 := seedIndexFile(t, db, "src/b.go", "go")

	db.CreateIndexImport(&IndexImport{FileID: f1, ImportPath: "fmt"})
	db.CreateIndexImport(&IndexImport{FileID: f2, ImportPath: "fmt"})
	db.CreateIndexImport(&IndexImport{FileID: f2, ImportPath: "os"})

	files, err := db.ListImporterFiles("fmt")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files importing fmt, got %d", len(files))
	}
}

// --- IndexReference CRUD ---

func TestCreateAndListReferences(t *testing.T) {
	db := testDB(t)
	fileID := seedIndexFile(t, db, "src/routes.go", "go")

	refs := []IndexReference{
		{FileID: fileID, SymbolName: "HandleAuth", Line: 45, UsageType: UsageCall},
		{FileID: fileID, SymbolName: "HandleAuth", Line: 78, UsageType: UsageReference},
	}

	for i := range refs {
		id, err := db.CreateIndexReference(&refs[i])
		if err != nil {
			t.Fatalf("CreateIndexReference[%d]: %v", i, err)
		}
		if id <= 0 {
			t.Fatalf("expected positive ID for reference %d", i)
		}
	}

	got, err := db.ListReferencesBySymbol("HandleAuth")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 references, got %d", len(got))
	}
}

func TestListReferencesBySymbolIncludesFilePath(t *testing.T) {
	db := testDB(t)
	fileID := seedIndexFile(t, db, "src/api.go", "go")

	db.CreateIndexReference(&IndexReference{
		FileID: fileID, SymbolName: "RegisterRoutes", Line: 12, UsageType: UsageCall,
	})

	refs, err := db.ListReferencesBySymbol("RegisterRoutes")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatal("expected 1 reference")
	}
	if refs[0].FilePath != "src/api.go" {
		t.Errorf("FilePath = %q, want %q", refs[0].FilePath, "src/api.go")
	}
}

// --- IndexPackage CRUD ---

func TestCreateAndGetPackage(t *testing.T) {
	db := testDB(t)

	pkg := &IndexPackage{Path: "github.com/example/pkg", Dir: "pkg"}
	id, err := db.CreateIndexPackage(pkg)
	if err != nil {
		t.Fatalf("CreateIndexPackage: %v", err)
	}
	if id <= 0 {
		t.Fatal("expected positive ID")
	}

	got, err := db.GetIndexPackage("github.com/example/pkg")
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != "pkg" {
		t.Errorf("Dir = %q", got.Dir)
	}
}

func TestGetPackageNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetIndexPackage("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAddAndListPackageDeps(t *testing.T) {
	db := testDB(t)

	// Create packages
	pkgA, _ := db.CreateIndexPackage(&IndexPackage{Path: "a", Dir: "a"})
	pkgB, _ := db.CreateIndexPackage(&IndexPackage{Path: "b", Dir: "b"})
	pkgC, _ := db.CreateIndexPackage(&IndexPackage{Path: "c", Dir: "c"})

	// A depends on B and C
	if err := db.AddPackageDep(pkgA, pkgB); err != nil {
		t.Fatal(err)
	}
	if err := db.AddPackageDep(pkgA, pkgC); err != nil {
		t.Fatal(err)
	}

	deps, err := db.ListPackageDeps(pkgA)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 {
		t.Errorf("expected 2 deps, got %d", len(deps))
	}
}

func TestAddPackageDepDuplicate(t *testing.T) {
	db := testDB(t)

	pkgA, _ := db.CreateIndexPackage(&IndexPackage{Path: "a", Dir: "a"})
	pkgB, _ := db.CreateIndexPackage(&IndexPackage{Path: "b", Dir: "b"})

	if err := db.AddPackageDep(pkgA, pkgB); err != nil {
		t.Fatal(err)
	}
	// Duplicate should not error (idempotent)
	if err := db.AddPackageDep(pkgA, pkgB); err != nil {
		t.Fatalf("duplicate dep should be idempotent: %v", err)
	}
}

// --- Exported symbol queries ---

func TestListExportedSymbolsByPackageDir(t *testing.T) {
	db := testDB(t)

	f1 := seedIndexFile(t, db, "pkg/handler.go", "go")
	f2 := seedIndexFile(t, db, "pkg/util.go", "go")
	f3 := seedIndexFile(t, db, "other/other.go", "go")

	db.CreateIndexSymbol(&IndexSymbol{FileID: f1, Name: "HandleRequest", Kind: SymbolFunction, Line: 10, Exported: true})
	db.CreateIndexSymbol(&IndexSymbol{FileID: f1, Name: "helper", Kind: SymbolFunction, Line: 20, Exported: false})
	db.CreateIndexSymbol(&IndexSymbol{FileID: f2, Name: "FormatResponse", Kind: SymbolFunction, Line: 5, Exported: true})
	db.CreateIndexSymbol(&IndexSymbol{FileID: f3, Name: "OtherExport", Kind: SymbolFunction, Line: 1, Exported: true})

	exports, err := db.ListExportedSymbolsByPackageDir("pkg")
	if err != nil {
		t.Fatal(err)
	}
	if len(exports) != 2 {
		t.Fatalf("expected 2 exported symbols in pkg/, got %d", len(exports))
	}
	for _, s := range exports {
		if !s.Exported {
			t.Errorf("symbol %s should be exported", s.Name)
		}
	}
}

// --- Implementation query ---

func TestFindImplementations(t *testing.T) {
	db := testDB(t)

	fileID := seedIndexFile(t, db, "src/service.go", "go")

	// A type that implements an interface
	db.CreateIndexSymbol(&IndexSymbol{
		FileID: fileID, Name: "UserService", Kind: SymbolType, Line: 10, Exported: true,
	})
	db.CreateIndexReference(&IndexReference{
		FileID: fileID, SymbolName: "Service", Line: 10, UsageType: UsageImplementation,
	})

	impls, err := db.FindImplementations("Service")
	if err != nil {
		t.Fatal(err)
	}
	if len(impls) != 1 {
		t.Fatalf("expected 1 implementation, got %d", len(impls))
	}
	if impls[0].FilePath != "src/service.go" {
		t.Errorf("FilePath = %q", impls[0].FilePath)
	}
}

// --- Test helpers ---

func seedIndexFile(t *testing.T, db *DB, path, lang string) int64 {
	t.Helper()
	id, err := db.CreateIndexFile(&IndexFile{
		Path: path, Language: lang, Hash: "seed-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func strPtr(s string) *string { return &s }
