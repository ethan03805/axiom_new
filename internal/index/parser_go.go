package index

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/openaxiom/axiom/internal/state"
)

// goParser extracts symbols from Go source files using go/parser.
type goParser struct{}

func (p *goParser) Language() string { return "go" }

func (p *goParser) Parse(source []byte, relPath string) (*ParseResult, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, parser.ParseComments)
	if err != nil {
		// Return partial results on parse errors
		return &ParseResult{}, nil
	}

	result := &ParseResult{}

	// Extract imports
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		var alias *string
		if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
			a := imp.Name.Name
			alias = &a
		}
		result.Imports = append(result.Imports, state.IndexImport{
			ImportPath: importPath,
			Alias:      alias,
		})
	}

	// Walk the AST for declarations
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			p.extractFunc(fset, d, result)
		case *ast.GenDecl:
			p.extractGenDecl(fset, d, result)
		}
	}

	// Extract references (function calls and identifiers)
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if ident, ok := node.Fun.(*ast.Ident); ok {
				result.References = append(result.References, state.IndexReference{
					SymbolName: ident.Name,
					Line:       fset.Position(ident.Pos()).Line,
					UsageType:  state.UsageCall,
				})
			}
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				result.References = append(result.References, state.IndexReference{
					SymbolName: sel.Sel.Name,
					Line:       fset.Position(sel.Sel.Pos()).Line,
					UsageType:  state.UsageCall,
				})
			}
		}
		return true
	})

	return result, nil
}

func (p *goParser) extractFunc(fset *token.FileSet, d *ast.FuncDecl, result *ParseResult) {
	name := d.Name.Name
	exported := ast.IsExported(name)
	line := fset.Position(d.Pos()).Line

	kind := state.SymbolFunction
	if d.Recv != nil {
		kind = state.SymbolMethod
	}

	sig := formatFuncSignature(d)
	retType := formatReturnType(d)

	sym := state.IndexSymbol{
		Name:      name,
		Kind:      kind,
		Line:      line,
		Signature: strPtrIfNonEmpty(sig),
		ReturnType: strPtrIfNonEmpty(retType),
		Exported:  exported,
	}

	// For methods, we record the receiver type name for parent linking
	// The indexer will resolve parent_symbol_id after all symbols are created
	if d.Recv != nil && len(d.Recv.List) > 0 {
		if recvTypeName := extractRecvTypeName(d.Recv.List[0].Type); recvTypeName != "" {
			sym.FilePath = recvTypeName // temporarily store receiver type name
		}
	}

	result.Symbols = append(result.Symbols, sym)
}

func (p *goParser) extractGenDecl(fset *token.FileSet, d *ast.GenDecl, result *ParseResult) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			p.extractTypeSpec(fset, s, result)
		case *ast.ValueSpec:
			p.extractValueSpec(fset, s, d.Tok, result)
		}
	}
}

func (p *goParser) extractTypeSpec(fset *token.FileSet, s *ast.TypeSpec, result *ParseResult) {
	name := s.Name.Name
	exported := ast.IsExported(name)
	line := fset.Position(s.Pos()).Line

	var kind state.SymbolKind
	switch s.Type.(type) {
	case *ast.InterfaceType:
		kind = state.SymbolInterface
	default:
		kind = state.SymbolType
	}

	result.Symbols = append(result.Symbols, state.IndexSymbol{
		Name:     name,
		Kind:     kind,
		Line:     line,
		Exported: exported,
	})

	// If it's an interface, extract method signatures
	if iface, ok := s.Type.(*ast.InterfaceType); ok && iface.Methods != nil {
		for _, method := range iface.Methods.List {
			if len(method.Names) > 0 {
				mName := method.Names[0].Name
				result.Symbols = append(result.Symbols, state.IndexSymbol{
					Name:     mName,
					Kind:     state.SymbolMethod,
					Line:     fset.Position(method.Pos()).Line,
					Exported: ast.IsExported(mName),
					FilePath: name, // temporarily store parent type name
				})
			}
		}
	}

	// If it's a struct, extract fields
	if structType, ok := s.Type.(*ast.StructType); ok && structType.Fields != nil {
		for _, field := range structType.Fields.List {
			for _, fieldName := range field.Names {
				result.Symbols = append(result.Symbols, state.IndexSymbol{
					Name:     fieldName.Name,
					Kind:     state.SymbolField,
					Line:     fset.Position(field.Pos()).Line,
					Exported: ast.IsExported(fieldName.Name),
					FilePath: name, // temporarily store parent type name
				})
			}
		}
	}

	// Record implementations: check if any struct type in the same file
	// implements an interface (Go compiler-verified, so we look for method sets)
}

func (p *goParser) extractValueSpec(fset *token.FileSet, s *ast.ValueSpec, tok token.Token, result *ParseResult) {
	for _, name := range s.Names {
		kind := state.SymbolVariable
		if tok == token.CONST {
			kind = state.SymbolConstant
		}

		result.Symbols = append(result.Symbols, state.IndexSymbol{
			Name:     name.Name,
			Kind:     kind,
			Line:     fset.Position(name.Pos()).Line,
			Exported: ast.IsExported(name.Name),
		})
	}
}

// --- formatting helpers ---

func formatFuncSignature(d *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")
	if d.Recv != nil && len(d.Recv.List) > 0 {
		b.WriteString("(")
		b.WriteString(formatExpr(d.Recv.List[0].Type))
		b.WriteString(") ")
	}
	b.WriteString(d.Name.Name)
	b.WriteString("(")
	if d.Type.Params != nil {
		b.WriteString(formatFieldList(d.Type.Params))
	}
	b.WriteString(")")
	if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		b.WriteString(" ")
		if len(d.Type.Results.List) > 1 || len(d.Type.Results.List[0].Names) > 0 {
			b.WriteString("(")
			b.WriteString(formatFieldList(d.Type.Results))
			b.WriteString(")")
		} else {
			b.WriteString(formatExpr(d.Type.Results.List[0].Type))
		}
	}
	return b.String()
}

func formatReturnType(d *ast.FuncDecl) string {
	if d.Type.Results == nil || len(d.Type.Results.List) == 0 {
		return ""
	}
	var parts []string
	for _, field := range d.Type.Results.List {
		parts = append(parts, formatExpr(field.Type))
	}
	return strings.Join(parts, ", ")
}

func formatFieldList(fl *ast.FieldList) string {
	var parts []string
	for _, field := range fl.List {
		typStr := formatExpr(field.Type)
		if len(field.Names) > 0 {
			for _, name := range field.Names {
				parts = append(parts, name.Name+" "+typStr)
			}
		} else {
			parts = append(parts, typStr)
		}
	}
	return strings.Join(parts, ", ")
}

func formatExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return formatExpr(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + formatExpr(e.X)
	case *ast.ArrayType:
		return "[]" + formatExpr(e.Elt)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", formatExpr(e.Key), formatExpr(e.Value))
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func(...)"
	case *ast.Ellipsis:
		return "..." + formatExpr(e.Elt)
	default:
		return "?"
	}
}

func extractRecvTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return extractRecvTypeName(e.X)
	default:
		return ""
	}
}

func strPtrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
