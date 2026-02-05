package golang

import (
	"go/ast"
	"go/parser"
	"go/token"

	"github.com/abhinavdevarakonda/maplet/internal/types"
)

type GoExtractor struct{}

func (e *GoExtractor) ExtractSymbols(files []string) ([]types.Symbol, error) {
	fset := token.NewFileSet()
	var allSymbols []types.Symbol

	for _, path := range files {
		// only process .go files (now)
		if filepathExt(path) != ".go" {
			continue
		}

		node, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			// skip files with errors
			continue
		}

		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			receiver := ""
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				switch t := fn.Recv.List[0].Type.(type) {
				case *ast.Ident:
					receiver = t.Name
				case *ast.StarExpr:
					if ident, ok := t.X.(*ast.Ident); ok {
						receiver = "*" + ident.Name
					}
				}
			}

			start := fset.Position(fn.Pos()).Line
			end := fset.Position(fn.End()).Line

			// ID format: package.Receiver.Name
			id := node.Name.Name
			if receiver != "" {
				id += "." + receiver
			}
			id += "." + fn.Name.Name

			allSymbols = append(allSymbols, types.Symbol{
				ID:        id,
				Name:      fn.Name.Name,
				Kind:      types.FunctionSymbol,
				Path:      path,
				StartLine: start,
				EndLine:   end,
			})
		}
	}

	return allSymbols, nil
}

func (e *GoExtractor) ExtractFacts(files []string) ([]types.Fact, error) {
	fset := token.NewFileSet()
	var allFacts []types.Fact

	for _, path := range files {
		// only process .go files (now)
		if filepathExt(path) != ".go" {
			continue
		}

		node, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}

		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			pos := fset.Position(call.Pos())
			end := fset.Position(call.End())

			fact := types.Fact{
				Path: 		path,
				Line: 		fset.Position(call.Pos()).Line,
				StartLine:	pos.Line,
				EndLine:	end.Line,
			}

			switch fun := call.Fun.(type) {
			case *ast.Ident:
				fact.CalleeName = fun.Name
			case *ast.SelectorExpr:
				fact.CalleeName = fun.Sel.Name
				if x, ok := fun.X.(*ast.Ident); ok {
					fact.CalleeQualifier = x.Name
				}
			}

			if fact.CalleeName != "" {
				allFacts = append(allFacts, fact)
			}

			return true
		})
	}

	return allFacts, nil
}

// file path helper 
func filepathExt(path string) string {
	for i := len(path) - 1; i >= 0 && path[i] != '/'; i-- {
		if path[i] == '.' {
			return path[i:]
		}
	}
	return ""
}
