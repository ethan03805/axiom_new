package index

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/openaxiom/axiom/internal/state"
)

// pythonParser extracts symbols from Python files using pattern matching.
// Designed to be replaced by tree-sitter when CGO is available.
type pythonParser struct{}

func (p *pythonParser) Language() string { return "python" }

var (
	pyFunc      = regexp.MustCompile(`(?m)^(\s*)def\s+(\w+)\s*\(`)
	pyClass     = regexp.MustCompile(`(?m)^class\s+(\w+)`)
	pyImport    = regexp.MustCompile(`(?m)^import\s+(\S+)`)
	pyFromImport = regexp.MustCompile(`(?m)^from\s+(\S+)\s+import`)
	pyConstant  = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]+)\s*=`)
	pyVariable  = regexp.MustCompile(`(?m)^([a-z_]\w*)\s*=`)
)

func (p *pythonParser) Parse(source []byte, relPath string) (*ParseResult, error) {
	result := &ParseResult{}
	lines := strings.Split(string(source), "\n")

	for i, line := range lines {
		lineNum := i + 1

		// Imports
		if m := pyFromImport.FindStringSubmatch(line); m != nil {
			result.Imports = append(result.Imports, state.IndexImport{ImportPath: m[1]})
			continue
		}
		if m := pyImport.FindStringSubmatch(line); m != nil {
			result.Imports = append(result.Imports, state.IndexImport{ImportPath: m[1]})
			continue
		}

		// Classes (always exported in Python unless _prefixed)
		if m := pyClass.FindStringSubmatch(line); m != nil {
			name := m[1]
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: name, Kind: state.SymbolType, Line: lineNum,
				Exported: !strings.HasPrefix(name, "_"),
			})
			continue
		}

		// Functions — top-level only (no indentation) or methods (indented under class)
		if m := pyFunc.FindStringSubmatch(line); m != nil {
			indent := m[1]
			name := m[2]
			kind := state.SymbolFunction
			if len(indent) > 0 {
				kind = state.SymbolMethod
			}
			exported := !strings.HasPrefix(name, "_")
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: name, Kind: kind, Line: lineNum, Exported: exported,
			})
			continue
		}

		// Constants (UPPER_CASE = ...)
		trimmed := strings.TrimSpace(line)
		if m := pyConstant.FindStringSubmatch(trimmed); m != nil {
			name := m[1]
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: name, Kind: state.SymbolConstant, Line: lineNum,
				Exported: !strings.HasPrefix(name, "_"),
			})
			continue
		}

		// Variables (lower_case = ...) — top level only
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if m := pyVariable.FindStringSubmatch(trimmed); m != nil {
				name := m[1]
				// Skip dunder names and common non-variable patterns
				if strings.HasPrefix(name, "__") || name == "self" {
					continue
				}
				// Check it's really a simple assignment (no function call on LHS etc.)
				if len(name) > 0 && unicode.IsLetter(rune(name[0])) {
					result.Symbols = append(result.Symbols, state.IndexSymbol{
						Name: name, Kind: state.SymbolVariable, Line: lineNum,
						Exported: !strings.HasPrefix(name, "_"),
					})
				}
			}
		}
	}

	return result, nil
}
