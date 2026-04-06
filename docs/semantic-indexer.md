# Semantic Indexer Reference

The semantic indexer maintains a structured, queryable index of project code symbols, exports, interfaces, and dependency relationships. It enables precise context construction for TaskSpecs without giving agents raw filesystem access. Per Architecture Section 17.

## Architecture

```
                    ┌──────────────┐
                    │  Engine      │
                    │  IndexService│
                    └──────┬───────┘
                           │
                    ┌──────┴───────┐
                    │IndexerAdapter│ (engine_adapter.go)
                    └──────┬───────┘
                           │
                    ┌──────┴───────┐
                    │   Indexer    │ (indexer.go)
                    └──┬───────┬──┘
                       │       │
              ┌────────┴──┐  ┌─┴─────────┐
              │  Parsers  │  │ Query API  │
              │ (per-lang)│  │ (query.go) │
              └────────┬──┘  └─┬──────────┘
                       │       │
                    ┌──┴───────┴──┐
                    │   state.DB  │
                    │ (index.go)  │
                    └─────────────┘
```

## Supported Languages

| Language | Parser | Strategy |
|----------|--------|----------|
| Go | `go/parser` + `go/ast` (stdlib) | Full AST analysis — signatures, receivers, fields, interface methods |
| TypeScript | Regex patterns | Export/declaration extraction — functions, classes, interfaces, types, const |
| JavaScript | Reuses TypeScript | Same declaration patterns |
| Python | Regex patterns | Indentation-aware — classes, functions/methods, constants, variables |
| Rust | Regex patterns | `pub`/private — fn, struct, trait, enum, const, impl, use |

Go uses the stdlib parser for superior analysis quality. Non-Go languages use regex patterns designed for drop-in replacement with tree-sitter when a C compiler is available.

## Index Contents

Per Architecture Section 17.3, the index stores:

| Entry Type | SQLite Table | Data |
|------------|-------------|------|
| Functions | `index_symbols` | Name, file, line, signature, return type, exported |
| Types/Structs | `index_symbols` | Name, file, line, fields (child symbols), methods (child symbols) |
| Interfaces | `index_symbols` | Name, file, line, method signatures (child symbols) |
| Constants/Variables | `index_symbols` | Name, file, line, kind, exported |
| Fields/Methods | `index_symbols` | Name, file, line, parent_symbol_id link |
| Imports | `index_imports` | File, imported package/module, alias |
| References | `index_references` | File, symbol name, line, usage type (call/reference/implementation) |
| Packages | `index_packages` | Package path, directory |
| Dependencies | `index_package_deps` | Package-to-package edges |

Content hashes (`index_files.hash`) enable incremental reindexing by detecting unchanged files.

## Refresh Cycle

Per Architecture Section 17.4:

| Trigger | Method | Behavior |
|---------|--------|----------|
| Project initialization | `Index(ctx, dir)` | Full index — clears all data, walks directory, parses all files |
| After merge queue commit | `IndexFiles(ctx, dir, paths)` | Incremental — only re-parses files with changed content hashes |
| On demand (`axiom index refresh`) | `Index(ctx, dir)` | Full reindex |

### Incremental Indexing

`IndexFiles` computes SHA-256 hashes of each file and compares against stored hashes. Unchanged files are skipped entirely. Changed files have their old data deleted (cascading to symbols, imports, references) and are re-parsed fresh.

## Excluded Paths

The indexer automatically excludes:

| Directory | Reason |
|-----------|--------|
| `.axiom/` | Runtime state — Architecture Section 2.8 |
| `.git/` | Git internals |
| `node_modules/` | Dependencies |
| `vendor/` | Go vendored dependencies |
| `.venv/` | Python virtual environments |
| `__pycache__/` | Python bytecode cache |
| `target/` | Rust build output |
| `dist/`, `build/` | Build output |

Non-source files (`go.mod`, `go.sum`, `.DS_Store`) are also excluded.

## Typed Query API

Per Architecture Section 17.5, all queries use structured types. Natural language queries are NOT supported.

### `LookupSymbol`

Find symbols by name with optional kind filter.

```go
results, err := indexer.LookupSymbol(ctx, "HandleAuth", "function")
// Returns: []SymbolResult with Name, Kind, FilePath, Line, Signature, Exported
```

Parameters:
- `name` — exact symbol name
- `kind` — optional filter: `function`, `type`, `interface`, `constant`, `variable`, `field`, `method`; empty string matches all kinds

### `ReverseDependencies`

Find all files/symbols that reference a given symbol.

```go
refs, err := indexer.ReverseDependencies(ctx, "HandleAuth")
// Returns: []ReferenceResult with FilePath, Line, SymbolName, UsageType
```

Usage types: `call` (function invocation), `reference` (type/value usage), `implementation` (interface implementation).

### `ListExports`

List all exported symbols in a package directory.

```go
exports, err := indexer.ListExports(ctx, "pkg/auth")
// Returns: []SymbolResult — only symbols where Exported == true
```

The `packagePath` is a directory path relative to the project root (e.g., `pkg/auth`, `cmd/server`).

### `FindImplementations`

Find types that implement a given interface.

```go
impls, err := indexer.FindImplementations(ctx, "Service")
// Returns: []ReferenceResult with FilePath, Line, SymbolName="Service", UsageType="implementation"
```

For Go, implementations are detected by matching struct method sets against interface method sets after all files are indexed. For Rust, `impl Trait for Type` declarations are directly parsed.

### `ModuleGraph`

Return the package dependency graph.

```go
// Full project graph
graph, err := indexer.ModuleGraph(ctx, "")

// Subgraph rooted at a package
graph, err := indexer.ModuleGraph(ctx, "cmd/server")
```

Returns `ModuleGraphResult` with:
- `Packages` — list of `PackageNode{Path, Dir}`
- `Edges` — list of `PackageEdge{From, To}` where From depends on To

Rooted queries use BFS traversal to return only reachable packages.

## Implementation Detection

### Go Interface Implementations

After indexing all files, the indexer performs a post-parse analysis:

1. Collects all `interface` symbols and their declared methods (child symbols)
2. Collects all `type` symbols and their methods (child symbols via `parent_symbol_id`)
3. For each type, checks if its method set is a superset of each interface's method set
4. Records matches as `implementation` references in `index_references`

This enables `FindImplementations("Service")` to return `TokenValidator` when `TokenValidator` has all methods declared by `Service`.

### Rust Trait Implementations

`impl Trait for Type` declarations are directly parsed by the Rust parser and stored as `implementation` references.

## Engine Integration

### IndexService Interface

```go
type IndexService interface {
    Index(ctx context.Context, dir string) error
    IndexFiles(ctx context.Context, dir string, paths []string) error
    LookupSymbol(ctx context.Context, name, kind string) ([]SymbolResult, error)
    ReverseDependencies(ctx context.Context, symbolName string) ([]ReferenceResult, error)
    ListExports(ctx context.Context, packagePath string) ([]SymbolResult, error)
    FindImplementations(ctx context.Context, interfaceName string) ([]ReferenceResult, error)
    ModuleGraph(ctx context.Context, rootPackage string) (*ModuleGraphResult, error)
}
```

### Engine-Level Types

```go
type SymbolResult struct {
    Name      string
    Kind      string   // function, type, interface, constant, variable, field, method
    FilePath  string
    Line      int
    Signature string
    Exported  bool
}

type ReferenceResult struct {
    FilePath   string
    Line       int
    SymbolName string
    UsageType  string   // call, reference, implementation
}

type ModuleGraphResult struct {
    Packages []PackageNode
    Edges    []PackageEdge
}
```

### Adapter

`IndexerAdapter` in `index/engine_adapter.go` bridges the indexer package types to engine-level types with a compile-time interface assertion:

```go
var _ engine.IndexService = (*IndexerAdapter)(nil)
```

## Parser Interface

```go
type Parser interface {
    Parse(source []byte, relPath string) (*ParseResult, error)
    Language() string
}

type ParseResult struct {
    Symbols    []state.IndexSymbol
    Imports    []state.IndexImport
    References []state.IndexReference
}
```

Parsers are registered in `init()` and looked up by language. The Go parser produces the richest results (full signatures, receiver types, parent linking). Non-Go parsers extract top-level declarations and exports.

## Go Parser Details

The Go parser uses `go/parser.ParseFile` with `go/ast` traversal:

- **Functions** — Extracted from `*ast.FuncDecl`. Method vs function distinguished by presence of receiver list. Full signature formatted including receiver type, parameters, and return types.
- **Types/Structs** — Extracted from `*ast.TypeSpec`. Interface vs struct distinguished by underlying type. Struct fields and interface methods are stored as child symbols with `parent_symbol_id` linking.
- **Constants/Variables** — Extracted from `*ast.GenDecl` with `token.CONST` vs `token.VAR`.
- **Imports** — Extracted from `*ast.ImportSpec` with alias support.
- **References** — `ast.Inspect` walk detects `*ast.CallExpr` for function calls and selector expressions.
- **Export detection** — Uses `ast.IsExported()` (first character uppercase).

## Database Schema

Migration: `internal/state/migrations/004_semantic_index.sql`

6 tables with ON DELETE CASCADE from `index_files` to child tables, ensuring file deletion cleanly removes all associated data. 11 performance indexes cover the primary query patterns.

See [Database Schema Reference](database-schema.md) for full table definitions.

## Test Coverage

46 tests total (22 state layer + 24 indexer service):

| Category | Tests | What's Tested |
|----------|-------|---------------|
| State CRUD | 22 | File create/get/delete/update/list, cascade delete, symbol CRUD, import CRUD, reference CRUD, package CRUD, package deps, exported symbol queries, implementation queries, clear index |
| Full indexing | 3 | Go project indexing, .axiom/ exclusion, go.mod exclusion |
| Incremental indexing | 2 | Changed file reindex, unchanged file skip |
| lookup_symbol | 6 | By function/type/interface/constant/variable, without kind filter |
| reverse_dependencies | 1 | Cross-file function call references |
| list_exports | 2 | Package exports, empty package |
| find_implementations | 1 | Go interface implementation detection |
| module_graph | 2 | Full graph, rooted subgraph |
| Multi-language | 4 | TypeScript/Python/Rust symbol extraction, language detection |
| Edge cases | 3 | Empty directory, nonexistent directory, full reindex clears old data |

Test fixtures in `internal/index/testdata/`:
- `goproject/` — 3-package Go project (auth, handler, server) with interfaces, implementations, imports, and constants
- `multilang/` — TypeScript, Python, and Rust sample files with classes, functions, traits, and constants
