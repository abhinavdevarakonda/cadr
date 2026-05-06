package treesitter

import (
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/abhinavdevarakonda/cadr/internal/types"
)

// LanguageConfig defines the interface that each language must implement
// to provide Tree-sitter-based extraction capabilities.
type LanguageConfig interface {
	// Grammar returns the Tree-sitter language grammar for parsing
	Grammar() *sitter.Language

	// SymbolQuery returns the Tree-sitter query string to find function/method definitions
	// Example: "(function_declaration name: (identifier) @name) @function"
	SymbolQuery() string

	// FactQuery returns the Tree-sitter query string to find function calls
	// Example: "(call_expression function: (identifier) @name) @call"
	FactQuery() string

	// NodeToSymbol converts a Tree-sitter node (from SymbolQuery) into a Symbol
	// The node parameter is the captured node, source is the file content, path is the file path
	NodeToSymbol(node *sitter.Node, source []byte, path string) (*types.Symbol, error)

	// NodeToFact converts a Tree-sitter node (from FactQuery) into a Fact
	// The node parameter is the captured node, source is the file content, path is the file path
	NodeToFact(node *sitter.Node, source []byte, path string) (*types.Fact, error)
}
