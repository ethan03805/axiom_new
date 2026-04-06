package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestIndexRefreshAction(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexRefreshAction(application, buf)
	if err != nil {
		t.Fatalf("indexRefreshAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Index") || !strings.Contains(output, "refreshed") {
		t.Errorf("expected output to contain index refresh confirmation, got: %s", output)
	}
}

func TestIndexQueryAction_LookupSymbol(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "lookup_symbol", "main", "", buf)
	if err != nil {
		t.Fatalf("indexQueryAction lookup_symbol: %v", err)
	}

	// May return empty results on a temp dir with no code, but should not error
	_ = buf.String()
}

func TestIndexQueryAction_ReverseDependencies(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "reverse_dependencies", "SomeSymbol", "", buf)
	if err != nil {
		t.Fatalf("indexQueryAction reverse_dependencies: %v", err)
	}
}

func TestIndexQueryAction_ListExports(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "list_exports", "", "somepkg", buf)
	if err != nil {
		t.Fatalf("indexQueryAction list_exports: %v", err)
	}
}

func TestIndexQueryAction_FindImplementations(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "find_implementations", "SomeInterface", "", buf)
	if err != nil {
		t.Fatalf("indexQueryAction find_implementations: %v", err)
	}
}

func TestIndexQueryAction_ModuleGraph(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "module_graph", "", "", buf)
	if err != nil {
		t.Fatalf("indexQueryAction module_graph: %v", err)
	}
}

func TestIndexQueryAction_InvalidType(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "invalid_query_type", "", "", buf)
	if err == nil {
		t.Fatal("expected error for invalid query type")
	}
}

func TestIndexQueryAction_LookupSymbolRequiresName(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "lookup_symbol", "", "", buf)
	if err == nil {
		t.Fatal("expected error when name is empty for lookup_symbol")
	}
}

func TestIndexQueryAction_ReverseDependenciesRequiresName(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "reverse_dependencies", "", "", buf)
	if err == nil {
		t.Fatal("expected error when name is empty for reverse_dependencies")
	}
}

func TestIndexQueryAction_ListExportsRequiresPackage(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "list_exports", "", "", buf)
	if err == nil {
		t.Fatal("expected error when package is empty for list_exports")
	}
}

func TestIndexQueryAction_FindImplementationsRequiresName(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := indexQueryAction(application, "find_implementations", "", "", buf)
	if err == nil {
		t.Fatal("expected error when name is empty for find_implementations")
	}
}
