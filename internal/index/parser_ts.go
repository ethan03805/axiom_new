package index

import (
	"regexp"
	"strings"

	"github.com/openaxiom/axiom/internal/state"
)

// typescriptParser extracts symbols from TypeScript/JavaScript files using pattern matching.
// Designed to be replaced by tree-sitter when CGO is available.
type typescriptParser struct{}

func (p *typescriptParser) Language() string { return "typescript" }

var (
	tsExportFunc    = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?function\s+(\w+)\s*[(<]`)
	tsExportClass   = regexp.MustCompile(`(?m)^export\s+(?:abstract\s+)?class\s+(\w+)`)
	tsExportIface   = regexp.MustCompile(`(?m)^export\s+interface\s+(\w+)`)
	tsExportConst   = regexp.MustCompile(`(?m)^export\s+const\s+(\w+)`)
	tsExportLet     = regexp.MustCompile(`(?m)^export\s+(?:let|var)\s+(\w+)`)
	tsExportType    = regexp.MustCompile(`(?m)^export\s+type\s+(\w+)`)
	tsFunc          = regexp.MustCompile(`(?m)^(?:async\s+)?function\s+(\w+)\s*[(<]`)
	tsClass         = regexp.MustCompile(`(?m)^(?:abstract\s+)?class\s+(\w+)`)
	tsInterface     = regexp.MustCompile(`(?m)^interface\s+(\w+)`)
	tsConst         = regexp.MustCompile(`(?m)^const\s+(\w+)`)
	tsImportFrom    = regexp.MustCompile(`(?m)^import\s+.*\s+from\s+['"]([^'"]+)['"]`)
	tsImportPlain   = regexp.MustCompile(`(?m)^import\s+['"]([^'"]+)['"]`)
)

func (p *typescriptParser) Parse(source []byte, relPath string) (*ParseResult, error) {
	result := &ParseResult{}
	lines := strings.Split(string(source), "\n")

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Imports
		if m := tsImportFrom.FindStringSubmatch(trimmed); m != nil {
			result.Imports = append(result.Imports, state.IndexImport{ImportPath: m[1]})
		} else if m := tsImportPlain.FindStringSubmatch(trimmed); m != nil {
			result.Imports = append(result.Imports, state.IndexImport{ImportPath: m[1]})
		}

		// Exported declarations
		if m := tsExportFunc.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolFunction, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := tsExportClass.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolType, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := tsExportIface.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolInterface, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := tsExportType.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolType, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := tsExportConst.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolConstant, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := tsExportLet.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolVariable, Line: lineNum, Exported: true,
			})
			continue
		}

		// Non-exported declarations
		if m := tsFunc.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolFunction, Line: lineNum, Exported: false,
			})
			continue
		}
		if m := tsClass.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolType, Line: lineNum, Exported: false,
			})
			continue
		}
		if m := tsInterface.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolInterface, Line: lineNum, Exported: false,
			})
			continue
		}
		if m := tsConst.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolConstant, Line: lineNum, Exported: false,
			})
			continue
		}
	}

	return result, nil
}
