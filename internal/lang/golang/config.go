package golang

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/abhinavdevarakonda/maplet/internal/types"
)

type GoConfig struct{}

func NewGoConfig() *GoConfig {
	return &GoConfig{}
}

func (c *GoConfig) Grammar() *sitter.Language {
	return golang.GetLanguage()
}

func (c *GoConfig) SymbolQuery() string {
	return `
	(
		function_declaration
			name: (identifier) @func.name
	) @function

	(
		method_declaration
		name: (field_identifier) @func.name
	) @function
	`
}

func (c *GoConfig) FactQuery() string {
	return `
		(call_expression
			function: (identifier) @call.name) @call

		(call_expression
			function: (selector_expression
				(identifier) @call.qualifier
				(field_identifier) @call.name)) @call
	`
}

func (c *GoConfig) NodeToSymbol(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	nodeType := node.Type()

	switch nodeType {
	case "function_declaration":
		return c.extractFunction(node, source, path)
	case "method_declaration":
		return c.extractMethod(node, source, path)
	default:
		// Skip other captures like identifier, type_identifier, etc.
		return nil, nil
	}
}

func (c *GoConfig) extractFunction(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	var funcName string

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "identifier" {
			funcName = child.Content(source)
			break
		}
	}

	if funcName == "" {
		return nil, fmt.Errorf("function name not found")
	}

	packageName := extractPackageName(path)
	id := fmt.Sprintf("%s.%s", packageName, funcName)

	return &types.Symbol{
		ID:        id,
		Name:      funcName,
		Kind:      types.FunctionSymbol,
		Path:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}, nil
}

func (c *GoConfig) extractMethod(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	var methodName string
	var receiverType string

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)

		if child.Type() == "field_identifier" {
			methodName = child.Content(source)
		}

		if child.Type() == "parameter_list" {
			receiverType = extractReceiverType(child, source)
		}
	}

	if methodName == "" || receiverType == "" {
		return nil, fmt.Errorf("method name or receiver not found")
	}

	packageName := extractPackageName(path)
	id := fmt.Sprintf("%s.%s.%s", packageName, receiverType, methodName)

	return &types.Symbol{
		ID:        id,
		Name:      methodName,
		Kind:      types.FunctionSymbol,
		Path:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}, nil
}

func (c *GoConfig) NodeToFact(node *sitter.Node, source []byte, path string) (*types.Fact, error) {
	if node.Type() != "call_expression" {
		// Skip other captures
		return nil, nil
	}

	var calleeName string
	var calleeQualifier string

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)

		switch child.Type() {
		case "identifier":
			calleeName = child.Content(source)
		case "selector_expression":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				grandchild := child.NamedChild(j)
				if grandchild.Type() == "identifier" {
					calleeQualifier = grandchild.Content(source)
				} else if grandchild.Type() == "field_identifier" {
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
		CalleeQualifier: calleeQualifier,
	}, nil
}

func extractPackageName(path string) string {
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" && !strings.HasSuffix(parts[i], ".go") {
			return parts[i]
		}
	}
	return "main"
}

func extractReceiverType(paramList *sitter.Node, source []byte) string {
	for i := 0; i < int(paramList.NamedChildCount()); i++ {
		param := paramList.NamedChild(i)
		if param.Type() == "parameter_declaration" {
			for j := 0; j < int(param.NamedChildCount()); j++ {
				typeNode := param.NamedChild(j)
				if typeNode.Type() == "type_identifier" {
					return typeNode.Content(source)
				} else if typeNode.Type() == "pointer_type" {
					for k := 0; k < int(typeNode.NamedChildCount()); k++ {
						innerType := typeNode.NamedChild(k)
						if innerType.Type() == "type_identifier" {
							return "*" + innerType.Content(source)
						}
					}
				}
			}
		}
	}
	return ""
}
