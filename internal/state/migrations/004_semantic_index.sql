-- Semantic index tables per Architecture Section 17.3.
-- Stores parsed symbols, imports, references, and package dependencies
-- for the typed query API (Section 17.5).

-- Tracked source files with content hashes for incremental reindexing.
CREATE TABLE IF NOT EXISTS index_files (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    path       TEXT    NOT NULL UNIQUE,
    language   TEXT    NOT NULL,
    hash       TEXT    NOT NULL,
    indexed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Symbols: functions, types, interfaces, constants, variables.
CREATE TABLE IF NOT EXISTS index_symbols (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id          INTEGER NOT NULL REFERENCES index_files(id) ON DELETE CASCADE,
    name             TEXT    NOT NULL,
    kind             TEXT    NOT NULL CHECK(kind IN ('function','type','interface','constant','variable','field','method')),
    line             INTEGER NOT NULL,
    signature        TEXT,
    return_type      TEXT,
    exported         INTEGER NOT NULL DEFAULT 0,
    parent_symbol_id INTEGER REFERENCES index_symbols(id) ON DELETE CASCADE
);

-- Import declarations per file.
CREATE TABLE IF NOT EXISTS index_imports (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id     INTEGER NOT NULL REFERENCES index_files(id) ON DELETE CASCADE,
    import_path TEXT    NOT NULL,
    alias       TEXT
);

-- Symbol references for reverse-dependency queries.
CREATE TABLE IF NOT EXISTS index_references (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id     INTEGER NOT NULL REFERENCES index_files(id) ON DELETE CASCADE,
    symbol_name TEXT    NOT NULL,
    line        INTEGER NOT NULL,
    usage_type  TEXT    NOT NULL CHECK(usage_type IN ('call','reference','implementation'))
);

-- Package/module identity.
CREATE TABLE IF NOT EXISTS index_packages (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT    NOT NULL UNIQUE,
    dir  TEXT    NOT NULL
);

-- Package dependency edges.
CREATE TABLE IF NOT EXISTS index_package_deps (
    package_id    INTEGER NOT NULL REFERENCES index_packages(id) ON DELETE CASCADE,
    depends_on_id INTEGER NOT NULL REFERENCES index_packages(id) ON DELETE CASCADE,
    PRIMARY KEY (package_id, depends_on_id)
);

-- Query performance indexes.
CREATE INDEX IF NOT EXISTS idx_index_symbols_name   ON index_symbols(name);
CREATE INDEX IF NOT EXISTS idx_index_symbols_kind   ON index_symbols(kind);
CREATE INDEX IF NOT EXISTS idx_index_symbols_file   ON index_symbols(file_id);
CREATE INDEX IF NOT EXISTS idx_index_symbols_parent ON index_symbols(parent_symbol_id);
CREATE INDEX IF NOT EXISTS idx_index_imports_file   ON index_imports(file_id);
CREATE INDEX IF NOT EXISTS idx_index_imports_path   ON index_imports(import_path);
CREATE INDEX IF NOT EXISTS idx_index_refs_symbol    ON index_references(symbol_name);
CREATE INDEX IF NOT EXISTS idx_index_refs_file      ON index_references(file_id);
CREATE INDEX IF NOT EXISTS idx_index_files_path     ON index_files(path);
CREATE INDEX IF NOT EXISTS idx_index_packages_path  ON index_packages(path);
