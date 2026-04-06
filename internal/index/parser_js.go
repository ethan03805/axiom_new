package index

// javascriptParser reuses the TypeScript parser since the symbol extraction
// patterns are compatible for the declarations we care about.
type javascriptParser = typescriptParser

func init() {
	// Override the javascript entry with a JS-labeled variant
	parserRegistry["javascript"] = &jsParserWrapper{}
}

type jsParserWrapper struct {
	typescriptParser
}

func (p *jsParserWrapper) Language() string { return "javascript" }
