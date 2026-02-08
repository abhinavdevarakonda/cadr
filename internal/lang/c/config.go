package c

import (
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tree_sitter_c "github.com/smacker/go-tree-sitter/c"
	"github.com/abhinavdevarakonda/maplet/internal/types"
)

type CConfig struct{}

func NewCConfig() *CConfig {
	return &CConfig{}
}

func (c *CConfig) Grammar() *sitter.Language {
	return tree_sitter_c.GetLanguage()
}

func (c *CConfig) SymbolQuery() string {
	return `
		(function_definition
			declarator: (function_declarator
				declarator: (identifier) @func.name)) @function

		(function_definition
			declarator: (function_declarator
				declarator: (pointer_declarator
					declarator: (identifier) @func.name))) @function
	`
}

func (c *CConfig) FactQuery() string {
	return `
		(call_expression
			function: [
				(identifier) @call.name
				(field_expression
					argument: (_)
					field: (field_identifier) @call.name)
			]) @call
	`
}

func (c *CConfig) NodeToSymbol(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	if node.Type() != "function" {
		return nil, fmt.Errorf("expected function node, got %s", node.Type())
	}

	funcName := c.extractFunctionName(node, source)
	if funcName == "" {
		return nil, fmt.Errorf("function name not found")
	}

	fileName := c.extractFileName(path)
	id := fmt.Sprintf("%s::%s", fileName, funcName)

	return &types.Symbol{
		ID:        id,
		Name:      funcName,
		Kind:      types.FunctionSymbol,
		Path:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}, nil
}

func (c *CConfig) extractFunctionName(node *sitter.Node, source []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "function_declarator" {
			return c.findIdentifierInDeclarator(child, source)
		}
	}
	return ""
}

func (c *CConfig) findIdentifierInDeclarator(node *sitter.Node, source []byte) string {
	if node.Type() == "identifier" {
		return node.Content(source)
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "identifier" {
			return child.Content(source)
		}
		if child.Type() == "pointer_declarator" {
			result := c.findIdentifierInDeclarator(child, source)
			if result != "" {
				return result
			}
		}
	}
	return ""
}

func (c *CConfig) NodeToFact(node *sitter.Node, source []byte, path string) (*types.Fact, error) {
	if node.Type() != "call" {
		return nil, fmt.Errorf("expected call node, got %s", node.Type())
	}

	var calleeName string

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)

		switch child.Type() {
		case "identifier":
			calleeName = child.Content(source)
		case "field_expression":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				grandchild := child.NamedChild(j)
				if grandchild.Type() == "field_identifier" {
					calleeName = grandchild.Content(source)
				}
			}
		}
	}

	if calleeName == "" {
		return nil, fmt.Errorf("callee name not found")
	}

	return &types.Fact{
		Path:            path,
		Line:            int(node.StartPoint().Row) + 1,
		StartLine:       int(node.StartPoint().Row) + 1,
		EndLine:         int(node.EndPoint().Row) + 1,
		CalleeName:      calleeName,
		CalleeQualifier: "",
	}, nil
}

func (c *CConfig) extractFileName(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}
