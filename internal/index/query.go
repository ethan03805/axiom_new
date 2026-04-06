package index

import (
	"context"
	"fmt"

	"github.com/openaxiom/axiom/internal/state"
)

// LookupSymbol finds symbols by name, optionally filtered by kind.
// Per Architecture Section 17.5.
func (idx *Indexer) LookupSymbol(ctx context.Context, name, kind string) ([]SymbolResult, error) {
	return idx.db.LookupSymbol(name, kind)
}

// ReverseDependencies returns all files/symbols that reference the given symbol.
// Per Architecture Section 17.5.
func (idx *Indexer) ReverseDependencies(ctx context.Context, symbolName string) ([]ReferenceResult, error) {
	return idx.db.ListReferencesBySymbol(symbolName)
}

// ListExports returns all exported symbols for a package identified by directory path.
// Per Architecture Section 17.5.
func (idx *Indexer) ListExports(ctx context.Context, packagePath string) ([]SymbolResult, error) {
	return idx.db.ListExportedSymbolsByPackageDir(packagePath)
}

// FindImplementations returns types that implement the given interface.
// Per Architecture Section 17.5.
func (idx *Indexer) FindImplementations(ctx context.Context, interfaceName string) ([]state.IndexReference, error) {
	return idx.db.FindImplementations(interfaceName)
}

// ModuleGraph returns the package dependency graph, optionally rooted at a specific package.
// Per Architecture Section 17.5.
func (idx *Indexer) ModuleGraph(ctx context.Context, rootPackage string) (*ModuleGraphResult, error) {
	result := &ModuleGraphResult{}

	if rootPackage != "" {
		return idx.moduleGraphFrom(rootPackage)
	}

	// Full graph — get all packages and their edges
	// Query all packages
	rows, err := idx.db.Query(`SELECT id, path, dir FROM index_packages ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("listing packages: %w", err)
	}
	defer rows.Close()

	var pkgs []state.IndexPackage
	for rows.Next() {
		var pkg state.IndexPackage
		if err := rows.Scan(&pkg.ID, &pkg.Path, &pkg.Dir); err != nil {
			return nil, fmt.Errorf("scanning package: %w", err)
		}
		pkgs = append(pkgs, pkg)
		result.Packages = append(result.Packages, PackageNode{Path: pkg.Path, Dir: pkg.Dir})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Get all edges
	for _, pkg := range pkgs {
		deps, err := idx.db.ListPackageDeps(pkg.ID)
		if err != nil {
			continue
		}
		for _, dep := range deps {
			result.Edges = append(result.Edges, PackageEdge{From: pkg.Path, To: dep.Path})
		}
	}

	return result, nil
}

// moduleGraphFrom returns the subgraph reachable from a specific package.
func (idx *Indexer) moduleGraphFrom(rootPath string) (*ModuleGraphResult, error) {
	result := &ModuleGraphResult{}

	root, err := idx.db.GetIndexPackage(rootPath)
	if err != nil {
		// If exact match not found, try as a directory prefix
		// (user might specify "cmd/server" instead of full module path)
		rows, err2 := idx.db.Query(`SELECT id, path, dir FROM index_packages WHERE dir = ? OR path = ?`, rootPath, rootPath)
		if err2 != nil {
			return result, nil // empty graph, no error
		}
		defer rows.Close()
		var found bool
		for rows.Next() {
			root = &state.IndexPackage{}
			if err := rows.Scan(&root.ID, &root.Path, &root.Dir); err != nil {
				continue
			}
			found = true
			break
		}
		if !found {
			return result, nil
		}
		_ = err // original error, suppressed since we found via fallback
	}

	// BFS from root
	visited := make(map[int64]bool)
	queue := []state.IndexPackage{*root}
	visited[root.ID] = true

	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]

		result.Packages = append(result.Packages, PackageNode{Path: pkg.Path, Dir: pkg.Dir})

		deps, err := idx.db.ListPackageDeps(pkg.ID)
		if err != nil {
			continue
		}
		for _, dep := range deps {
			result.Edges = append(result.Edges, PackageEdge{From: pkg.Path, To: dep.Path})
			if !visited[dep.ID] {
				visited[dep.ID] = true
				queue = append(queue, dep)
			}
		}
	}

	return result, nil
}
