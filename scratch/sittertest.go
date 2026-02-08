package main

import (
	"context"
	"fmt"
	"os"

	sitter "github.com/smacker/go-tree-sitter"
	tree_sitter_c "github.com/smacker/go-tree-sitter/c"
)

func main() {
	parser := sitter.NewParser()
	parser.SetLanguage(tree_sitter_c.GetLanguage())

	src, _ := os.ReadFile("test.c") // any C file
	tree, _ := parser.ParseCtx(context.Background(), nil, src)

	fmt.Println(tree.RootNode().String())
}

