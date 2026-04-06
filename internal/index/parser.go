package index

import "github.com/openaxiom/axiom/internal/state"

// ParseResult holds the symbols, imports, and references extracted from a file.
type ParseResult struct {
	Symbols    []state.IndexSymbol
	Imports    []state.IndexImport
	References []state.IndexReference
}

// Parser extracts symbols, imports, and references from source files.
type Parser interface {
	// Parse analyzes source code and returns extracted data.
	Parse(source []byte, relPath string) (*ParseResult, error)
	// Language returns the language this parser handles.
	Language() string
}

// parserRegistry maps language names to parser implementations.
var parserRegistry = map[string]Parser{}

func registerParser(p Parser) {
	parserRegistry[p.Language()] = p
}

func init() {
	registerParser(&goParser{})
	registerParser(&typescriptParser{})
	registerParser(&pythonParser{})
	registerParser(&rustParser{})
}

// getParser returns the parser for a language, or nil if unsupported.
func getParser(lang string) Parser {
	return parserRegistry[lang]
}
