package types

type SymbolKind string

const (
	FunctionSymbol SymbolKind = "function"
	StructSymbol   SymbolKind = "struct"
)

type Symbol struct {
	ID        string
	Name      string
	Kind      SymbolKind
	Path      string
	StartLine int
	EndLine   int
}

type Fact struct {
	Path            string
	Line            int
	StartLine		int
	EndLine			int
	CalleeName      string
	CalleeQualifier string
}

