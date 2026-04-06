package index

import (
	"context"

	"github.com/openaxiom/axiom/internal/engine"
)

// Compile-time interface assertion.
var _ engine.IndexService = (*IndexerAdapter)(nil)

// IndexerAdapter adapts the Indexer to the engine.IndexService interface.
type IndexerAdapter struct {
	idx *Indexer
}

// NewIndexerAdapter creates an adapter that satisfies engine.IndexService.
func NewIndexerAdapter(idx *Indexer) *IndexerAdapter {
	return &IndexerAdapter{idx: idx}
}

func (a *IndexerAdapter) Index(ctx context.Context, dir string) error {
	return a.idx.Index(ctx, dir)
}

func (a *IndexerAdapter) IndexFiles(ctx context.Context, dir string, paths []string) error {
	return a.idx.IndexFiles(ctx, dir, paths)
}

func (a *IndexerAdapter) LookupSymbol(ctx context.Context, name, kind string) ([]engine.SymbolResult, error) {
	results, err := a.idx.LookupSymbol(ctx, name, kind)
	if err != nil {
		return nil, err
	}
	return toEngineSymbols(results), nil
}

func (a *IndexerAdapter) ReverseDependencies(ctx context.Context, symbolName string) ([]engine.ReferenceResult, error) {
	results, err := a.idx.ReverseDependencies(ctx, symbolName)
	if err != nil {
		return nil, err
	}
	return toEngineRefs(results), nil
}

func (a *IndexerAdapter) ListExports(ctx context.Context, packagePath string) ([]engine.SymbolResult, error) {
	results, err := a.idx.ListExports(ctx, packagePath)
	if err != nil {
		return nil, err
	}
	return toEngineSymbols(results), nil
}

func (a *IndexerAdapter) FindImplementations(ctx context.Context, interfaceName string) ([]engine.ReferenceResult, error) {
	results, err := a.idx.FindImplementations(ctx, interfaceName)
	if err != nil {
		return nil, err
	}
	return toEngineRefs(results), nil
}

func (a *IndexerAdapter) ModuleGraph(ctx context.Context, rootPackage string) (*engine.ModuleGraphResult, error) {
	result, err := a.idx.ModuleGraph(ctx, rootPackage)
	if err != nil {
		return nil, err
	}
	return toEngineGraph(result), nil
}

func toEngineSymbols(syms []SymbolResult) []engine.SymbolResult {
	result := make([]engine.SymbolResult, len(syms))
	for i, s := range syms {
		result[i] = engine.SymbolResult{
			Name:     s.Name,
			Kind:     string(s.Kind),
			FilePath: s.FilePath,
			Line:     s.Line,
			Exported: s.Exported,
		}
		if s.Signature != nil {
			result[i].Signature = *s.Signature
		}
	}
	return result
}

func toEngineRefs(refs []ReferenceResult) []engine.ReferenceResult {
	result := make([]engine.ReferenceResult, len(refs))
	for i, r := range refs {
		result[i] = engine.ReferenceResult{
			FilePath:   r.FilePath,
			Line:       r.Line,
			SymbolName: r.SymbolName,
			UsageType:  string(r.UsageType),
		}
	}
	return result
}

func toEngineGraph(g *ModuleGraphResult) *engine.ModuleGraphResult {
	if g == nil {
		return nil
	}
	result := &engine.ModuleGraphResult{}
	for _, p := range g.Packages {
		result.Packages = append(result.Packages, engine.PackageNode{Path: p.Path, Dir: p.Dir})
	}
	for _, e := range g.Edges {
		result.Edges = append(result.Edges, engine.PackageEdge{From: e.From, To: e.To})
	}
	return result
}
