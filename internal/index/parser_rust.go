package index

import (
	"regexp"
	"strings"

	"github.com/openaxiom/axiom/internal/state"
)

// rustParser extracts symbols from Rust files using pattern matching.
// Designed to be replaced by tree-sitter when CGO is available.
type rustParser struct{}

func (p *rustParser) Language() string { return "rust" }

var (
	rsPubFn       = regexp.MustCompile(`(?m)^(?:\s*)pub(?:\s*\([^)]*\))?\s+(?:async\s+)?fn\s+(\w+)`)
	rsPrivFn      = regexp.MustCompile(`(?m)^(?:\s*)fn\s+(\w+)`)
	rsPubStruct   = regexp.MustCompile(`(?m)^pub\s+struct\s+(\w+)`)
	rsPrivStruct  = regexp.MustCompile(`(?m)^struct\s+(\w+)`)
	rsPubTrait    = regexp.MustCompile(`(?m)^pub\s+trait\s+(\w+)`)
	rsPrivTrait   = regexp.MustCompile(`(?m)^trait\s+(\w+)`)
	rsPubConst    = regexp.MustCompile(`(?m)^pub\s+const\s+(\w+)`)
	rsPrivConst   = regexp.MustCompile(`(?m)^const\s+(\w+)`)
	rsPubStatic   = regexp.MustCompile(`(?m)^pub\s+static\s+(\w+)`)
	rsPubEnum     = regexp.MustCompile(`(?m)^pub\s+enum\s+(\w+)`)
	rsPrivEnum    = regexp.MustCompile(`(?m)^enum\s+(\w+)`)
	rsPubType     = regexp.MustCompile(`(?m)^pub\s+type\s+(\w+)`)
	rsUse         = regexp.MustCompile(`(?m)^use\s+([^;{]+)`)
	rsImplFor     = regexp.MustCompile(`(?m)^impl\s+(\w+)\s+for\s+(\w+)`)
)

func (p *rustParser) Parse(source []byte, relPath string) (*ParseResult, error) {
	result := &ParseResult{}
	lines := strings.Split(string(source), "\n")

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Use statements as imports
		if m := rsUse.FindStringSubmatch(trimmed); m != nil {
			importPath := strings.TrimSpace(m[1])
			result.Imports = append(result.Imports, state.IndexImport{ImportPath: importPath})
			continue
		}

		// impl Trait for Type — record as implementation reference
		if m := rsImplFor.FindStringSubmatch(trimmed); m != nil {
			traitName := m[1]
			result.References = append(result.References, state.IndexReference{
				SymbolName: traitName,
				Line:       lineNum,
				UsageType:  state.UsageImplementation,
			})
			continue
		}

		// Pub declarations
		if m := rsPubFn.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolFunction, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := rsPubStruct.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolType, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := rsPubTrait.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolInterface, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := rsPubConst.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolConstant, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := rsPubStatic.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolVariable, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := rsPubEnum.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolType, Line: lineNum, Exported: true,
			})
			continue
		}
		if m := rsPubType.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolType, Line: lineNum, Exported: true,
			})
			continue
		}

		// Private declarations
		if m := rsPrivFn.FindStringSubmatch(trimmed); m != nil {
			// Check it's not inside an impl block (indented) — still track it
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolFunction, Line: lineNum, Exported: false,
			})
			continue
		}
		if m := rsPrivStruct.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolType, Line: lineNum, Exported: false,
			})
			continue
		}
		if m := rsPrivTrait.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolInterface, Line: lineNum, Exported: false,
			})
			continue
		}
		if m := rsPrivConst.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolConstant, Line: lineNum, Exported: false,
			})
			continue
		}
		if m := rsPrivEnum.FindStringSubmatch(trimmed); m != nil {
			result.Symbols = append(result.Symbols, state.IndexSymbol{
				Name: m[1], Kind: state.SymbolType, Line: lineNum, Exported: false,
			})
			continue
		}
	}

	return result, nil
}
