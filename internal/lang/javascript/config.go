package javascript

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/lang/universal"
	"github.com/abhinavdevarakonda/cadr/internal/types"
	sitter "github.com/smacker/go-tree-sitter"
	tree_sitter_javascript "github.com/smacker/go-tree-sitter/javascript"
)

type JSConfig struct{}

func NewJSConfig() *JSConfig {
	return &JSConfig{}
}

func (c *JSConfig) Grammar() *sitter.Language {
	return tree_sitter_javascript.GetLanguage()
}

func (c *JSConfig) SymbolQuery() string {
	query, err := universal.LoadSymbolQuery("javascript")
	if err != nil {
		return `
		(function_declaration
			name: (identifier) @func.name
		) @function
		`
	}
	return query
}

func (c *JSConfig) FactQuery() string {
	query, err := universal.LoadFactQuery("javascript")
	if err != nil {
		return `
		(call_expression
			function: (identifier) @call.name
		) @call
		`
	}
	return query
}

func (c *JSConfig) NodeToSymbol(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	nodeType := node.Type()

	switch nodeType {
	case "function_declaration":
		return c.extractFunction(node, source, path)
	case "method_definition":
		return c.extractMethod(node, source, path)
	case "variable_declarator":
		return c.extractArrowFunction(node, source, path)
	default:
		return nil, nil
	}
}

func (c *JSConfig) extractFunction(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil, fmt.Errorf("javascript: function name not found")
	}

	funcName := nameNode.Content(source)
	module := moduleFromPath(path)
	id := fmt.Sprintf("%s.%s", module, funcName)

	return &types.Symbol{
		ID:        id,
		Name:      funcName,
		Kind:      types.FunctionSymbol,
		Path:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}, nil
}

func (c *JSConfig) extractMethod(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil, fmt.Errorf("javascript: method name not found")
	}

	methodName := nameNode.Content(source)
	module := moduleFromPath(path)
	className := c.enclosingClassName(node, source)

	var id string
	if className != "" {
		id = fmt.Sprintf("%s.%s.%s", module, className, methodName)
	} else {
		id = fmt.Sprintf("%s.%s", module, methodName)
	}

	return &types.Symbol{
		ID:        id,
		Name:      methodName,
		Kind:      types.FunctionSymbol,
		Path:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}, nil
}

func (c *JSConfig) extractArrowFunction(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil, fmt.Errorf("javascript: arrow function name not found")
	}

	funcName := nameNode.Content(source)
	module := moduleFromPath(path)
	id := fmt.Sprintf("%s.%s", module, funcName)

	return &types.Symbol{
		ID:        id,
		Name:      funcName,
		Kind:      types.FunctionSymbol,
		Path:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}, nil
}

func (c *JSConfig) NodeToFact(node *sitter.Node, source []byte, path string) (*types.Fact, error) {
	if node.Type() != "call_expression" {
		return nil, nil
	}

	var calleeName string
	var calleeQualifier string

	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return nil, fmt.Errorf("javascript: call has no function child")
	}

	switch funcNode.Type() {
	case "identifier":
		calleeName = funcNode.Content(source)
	case "member_expression":
		objNode := funcNode.ChildByFieldName("object")
		propNode := funcNode.ChildByFieldName("property")
		if objNode != nil {
			calleeQualifier = objNode.Content(source)
		}
		if propNode != nil {
			calleeName = propNode.Content(source)
		}
	}

	if calleeName == "" {
		return nil, fmt.Errorf("javascript: callee name not found")
	}

	return &types.Fact{
		Path:            path,
		Line:            int(node.StartPoint().Row) + 1,
		StartLine:       int(node.StartPoint().Row) + 1,
		EndLine:         int(node.EndPoint().Row) + 1,
		CalleeName:      calleeName,
		CalleeQualifier: calleeQualifier,
	}, nil
}

func (c *JSConfig) enclosingClassName(node *sitter.Node, source []byte) string {
	parent := node.Parent()
	for parent != nil {
		if parent.Type() == "class_body" {
			classNode := parent.Parent()
			if classNode != nil && classNode.Type() == "class_declaration" {
				nameNode := classNode.ChildByFieldName("name")
				if nameNode != nil {
					return nameNode.Content(source)
				}
			}
		}
		parent = parent.Parent()
	}
	return ""
}

func moduleFromPath(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" {
		return "module"
	}
	return name
}
