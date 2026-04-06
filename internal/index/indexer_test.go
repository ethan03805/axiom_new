package index

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/openaxiom/axiom/internal/state"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := state.Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testdataDir returns the absolute path to the testdata directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	// Get the directory of this test file
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	return filepath.Join(filepath.Dir(filename), "testdata")
}

func testIndexer(t *testing.T) (*Indexer, *state.DB) {
	t.Helper()
	db := testDB(t)
	idx := NewIndexer(db, testLogger())
	return idx, db
}

// --- Full index of Go project ---

func TestIndexGoProject(t *testing.T) {
	idx, db := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	err := idx.Index(context.Background(), dir)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Should have indexed Go files but not .axiom/
	files, err := db.ListIndexFiles()
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range files {
		if filepath.Base(filepath.Dir(f.Path)) == ".axiom" || f.Path == ".axiom/config.toml" {
			t.Errorf("indexed .axiom file: %s", f.Path)
		}
	}

	if len(files) == 0 {
		t.Fatal("expected at least one indexed file")
	}

	// Verify Go files were found
	goFileCount := 0
	for _, f := range files {
		if f.Language == "go" {
			goFileCount++
		}
	}
	if goFileCount < 3 {
		t.Errorf("expected at least 3 Go files, got %d", goFileCount)
	}
}

func TestIndexExcludesAxiomDir(t *testing.T) {
	idx, db := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	files, _ := db.ListIndexFiles()
	for _, f := range files {
		if f.Path == ".axiom/config.toml" {
			t.Error("should not index .axiom/ files")
		}
	}
}

func TestIndexExcludesGoMod(t *testing.T) {
	idx, db := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	files, _ := db.ListIndexFiles()
	for _, f := range files {
		if filepath.Base(f.Path) == "go.mod" || filepath.Base(f.Path) == "go.sum" {
			t.Errorf("should not index %s", f.Path)
		}
	}
}

// --- Incremental reindex ---

func TestIncrementalIndex(t *testing.T) {
	idx, db := testIndexer(t)

	// Create a temp directory with a Go file
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() string {
	return "hello"
}
`)

	// Full index
	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	results, _ := idx.LookupSymbol(context.Background(), "Hello", "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for Hello, got %d", len(results))
	}

	// Modify the file — add a new function
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() string {
	return "hello"
}

func Goodbye() string {
	return "bye"
}
`)

	// Incremental reindex of just the changed file
	err := idx.IndexFiles(context.Background(), dir, []string{"main.go"})
	if err != nil {
		t.Fatalf("IndexFiles: %v", err)
	}

	// Both symbols should now exist
	results, _ = idx.LookupSymbol(context.Background(), "Hello", "")
	if len(results) != 1 {
		t.Errorf("Hello: expected 1, got %d", len(results))
	}
	results, _ = idx.LookupSymbol(context.Background(), "Goodbye", "")
	if len(results) != 1 {
		t.Errorf("Goodbye: expected 1, got %d", len(results))
	}

	// Should only have 1 file entry (same path, updated)
	files, _ := db.ListIndexFiles()
	if len(files) != 1 {
		t.Errorf("expected 1 indexed file, got %d", len(files))
	}
}

func TestIncrementalIndexSkipsUnchangedFiles(t *testing.T) {
	idx, db := testIndexer(t)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Foo() {}
`)

	// Full index
	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	// Get the initial indexed_at time
	f1, _ := db.GetIndexFile("main.go")
	firstIndexedAt := f1.IndexedAt

	// Re-index same file without changes — should skip
	if err := idx.IndexFiles(context.Background(), dir, []string{"main.go"}); err != nil {
		t.Fatal(err)
	}

	f2, _ := db.GetIndexFile("main.go")
	if f2.Hash != f1.Hash {
		t.Error("hash should not change for unchanged file")
	}
	// The indexed_at should remain the same since content didn't change
	if !f2.IndexedAt.Equal(firstIndexedAt) {
		t.Error("indexed_at should not change for unchanged file")
	}
}

// --- Typed Query API: lookup_symbol ---

func TestLookupSymbolFunction(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	results, err := idx.LookupSymbol(context.Background(), "RegisterRoutes", "function")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Exported {
		t.Error("RegisterRoutes should be exported")
	}
	if results[0].FilePath == "" {
		t.Error("FilePath should be populated")
	}
}

func TestLookupSymbolType(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	results, err := idx.LookupSymbol(context.Background(), "TokenValidator", "type")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Exported {
		t.Error("TokenValidator should be exported")
	}
}

func TestLookupSymbolInterface(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	results, err := idx.LookupSymbol(context.Background(), "Service", "interface")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for Service interface, got %d", len(results))
	}
}

func TestLookupSymbolConstant(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	results, err := idx.LookupSymbol(context.Background(), "MaxTokenAge", "constant")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Exported {
		t.Error("MaxTokenAge should be exported")
	}
}

func TestLookupSymbolVariable(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	results, err := idx.LookupSymbol(context.Background(), "defaultSecret", "variable")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Exported {
		t.Error("defaultSecret should not be exported")
	}
}

func TestLookupSymbolNoKindFilter(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	// "Handler" exists as both a type and as part of method names
	results, err := idx.LookupSymbol(context.Background(), "Handler", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected results for Handler without kind filter")
	}
}

// --- Typed Query API: reverse_dependencies ---

func TestReverseDependencies(t *testing.T) {
	idx, _ := testIndexer(t)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.go"), `package main

func Helper() string {
	return "help"
}
`)
	writeFile(t, filepath.Join(dir, "caller.go"), `package main

func UsesHelper() {
	Helper()
}
`)

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	refs, err := idx.ReverseDependencies(context.Background(), "Helper")
	if err != nil {
		t.Fatal(err)
	}
	// Should find at least the call in caller.go
	found := false
	for _, r := range refs {
		if r.FilePath == "caller.go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reference to Helper in caller.go, got %+v", refs)
	}
}

// --- Typed Query API: list_exports ---

func TestListExports(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	exports, err := idx.ListExports(context.Background(), "pkg/auth")
	if err != nil {
		t.Fatal(err)
	}

	if len(exports) == 0 {
		t.Fatal("expected exports in pkg/auth")
	}

	// Verify all returned symbols are exported
	for _, s := range exports {
		if !s.Exported {
			t.Errorf("symbol %s should be exported", s.Name)
		}
	}

	// Should include known exports
	names := make(map[string]bool)
	for _, s := range exports {
		names[s.Name] = true
	}
	for _, expected := range []string{"Service", "TokenValidator", "MaxTokenAge", "Authenticate", "Authorize"} {
		if !names[expected] {
			t.Errorf("missing expected export: %s", expected)
		}
	}
}

func TestListExportsEmpty(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	exports, err := idx.ListExports(context.Background(), "nonexistent/pkg")
	if err != nil {
		t.Fatal(err)
	}
	if len(exports) != 0 {
		t.Errorf("expected 0 exports, got %d", len(exports))
	}
}

// --- Typed Query API: find_implementations ---

func TestFindImplementations(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	impls, err := idx.FindImplementations(context.Background(), "Service")
	if err != nil {
		t.Fatal(err)
	}

	// TokenValidator implements Service
	if len(impls) == 0 {
		t.Fatal("expected at least 1 implementation of Service")
	}
}

// --- Typed Query API: module_graph ---

func TestModuleGraph(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	graph, err := idx.ModuleGraph(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}

	if graph == nil {
		t.Fatal("expected non-nil graph")
	}
	if len(graph.Packages) == 0 {
		t.Fatal("expected at least 1 package in graph")
	}

	// Verify edges exist
	if len(graph.Edges) == 0 {
		t.Error("expected at least 1 dependency edge")
	}
}

func TestModuleGraphRootFilter(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "goproject")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	// Get graph rooted at a specific package
	graph, err := idx.ModuleGraph(context.Background(), "cmd/server")
	if err != nil {
		t.Fatal(err)
	}

	if graph == nil {
		t.Fatal("expected non-nil graph")
	}
}

// --- Multi-language indexing ---

func TestIndexMultiLanguage(t *testing.T) {
	idx, db := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "multilang")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	files, _ := db.ListIndexFiles()
	languages := make(map[string]bool)
	for _, f := range files {
		languages[f.Language] = true
	}

	for _, lang := range []string{"typescript", "python", "rust"} {
		if !languages[lang] {
			t.Errorf("expected %s files to be indexed", lang)
		}
	}
}

func TestIndexTypeScriptSymbols(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "multilang")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	// Look for exported TypeScript class
	results, err := idx.LookupSymbol(context.Background(), "AppServer", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected to find AppServer in TypeScript file")
	}
}

func TestIndexPythonSymbols(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "multilang")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	// Look for Python class
	results, err := idx.LookupSymbol(context.Background(), "UserService", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected to find UserService in Python file")
	}
}

func TestIndexRustSymbols(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := filepath.Join(testdataDir(t), "multilang")

	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	// Look for Rust struct
	results, err := idx.LookupSymbol(context.Background(), "MemoryStore", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected to find MemoryStore in Rust file")
	}
}

// --- Edge cases ---

func TestIndexEmptyDirectory(t *testing.T) {
	idx, _ := testIndexer(t)
	dir := t.TempDir()

	err := idx.Index(context.Background(), dir)
	if err != nil {
		t.Fatalf("indexing empty dir should not error: %v", err)
	}
}

func TestIndexNonexistentDirectory(t *testing.T) {
	idx, _ := testIndexer(t)

	err := idx.Index(context.Background(), "/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestIndexClearsBeforeFullReindex(t *testing.T) {
	idx, db := testIndexer(t)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), `package main
func A() {}
`)

	// First index
	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	results, _ := idx.LookupSymbol(context.Background(), "A", "")
	if len(results) != 1 {
		t.Fatalf("expected A, got %d results", len(results))
	}

	// Remove file and add a different one
	os.Remove(filepath.Join(dir, "a.go"))
	writeFile(t, filepath.Join(dir, "b.go"), `package main
func B() {}
`)

	// Full reindex
	if err := idx.Index(context.Background(), dir); err != nil {
		t.Fatal(err)
	}

	// A should be gone
	results, _ = idx.LookupSymbol(context.Background(), "A", "")
	if len(results) != 0 {
		t.Error("A should not exist after reindex")
	}

	// B should exist
	results, _ = idx.LookupSymbol(context.Background(), "B", "")
	if len(results) != 1 {
		t.Error("B should exist after reindex")
	}

	files, _ := db.ListIndexFiles()
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}
}

// --- Helper ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
