package main

import (
	"context"
	"fmt"
	"os"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

func main() {
	src, _ := os.ReadFile("internal/analyzer/analyzer.go")

	parser := sitter.NewParser()
	parser.SetLanguage(golang.GetLanguage())

	ctx := context.Background()
	tree, err := parser.ParseCtx(ctx, nil, src)
	if err != nil {
		panic(err)
	}

	root := tree.RootNode()

	query,  err := sitter.NewQuery([]byte(`
	(function_declaration
	name: (identifier) @name)
	`), golang.GetLanguage())
	if err != nil  {
		panic(err)
	}

	qc := sitter.NewQueryCursor()
	qc.Exec(query, root)

	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}
		for _, c := range match.Captures {
			node := c.Node
			name := src[node.StartByte():node.EndByte()]
			fmt.Println("Function:", string(name))
		}
	}

}
