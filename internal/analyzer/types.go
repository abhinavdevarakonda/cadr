package analyzer

import (
	"github.com/abhinavdevarakonda/maplet/internal/graph"
)

type Result struct {
	Graph *graph.Graph
}

type ScanResult struct {
	Root        string
	Files       []string
	Directories []string
}

type SymbolKind string

const (
	FunctionSymbol SymbolKind = "function"
	StructSymbol   SymbolKind = "struct"
)

type Symbol struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      SymbolKind `json:"kind"`
	Path      string     `json:"path"`
	StartLine int        `json:"startLine"`
	EndLine   int        `json:"endLine"`
}

type SymbolTable map[string]Symbol

type Fact struct {
	Path            string `json:"path"`
	Line            int    `json:"line"`
	CalleeName      string `json:"calleeName"`
	CalleeQualifier string `json:"calleeQualifier,omitempty"`
}
