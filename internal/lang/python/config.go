package python

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/lang/universal"
	"github.com/abhinavdevarakonda/cadr/internal/types"
	sitter "github.com/smacker/go-tree-sitter"
	tree_sitter_python "github.com/smacker/go-tree-sitter/python"
)

type PythonConfig struct{}

func NewPythonConfig() *PythonConfig {
	return &PythonConfig{}
}

func (c *PythonConfig) Grammar() *sitter.Language {
	return tree_sitter_python.GetLanguage()
}

func (c *PythonConfig) SymbolQuery() string {
	query, err := universal.LoadSymbolQuery("python")
	if err != nil {
		return `
		(function_definition
			name: (identifier) @func.name
		) @function
		`
	}
	return query
}

func (c *PythonConfig) FactQuery() string {
	query, err := universal.LoadFactQuery("python")
	if err != nil {
		return `
		(call
			function: (identifier) @call.name
		) @call

		(call
			function: (attribute
				object: (_) @call.qualifier
				attribute: (identifier) @call.name
			)
		) @call
		`
	}
	return query
}

func (c *PythonConfig) NodeToSymbol(node *sitter.Node, source []byte, path string) (*types.Symbol, error) {
	if node.Type() != "function_definition" {
		return nil, nil
	}

	funcName := c.childContent(node, "identifier", source)
	if funcName == "" {
		return nil, fmt.Errorf("python: function name not found")
	}

	module := moduleFromPath(path)
	className := c.enclosingClassName(node, source)

	var id string
	if className != "" {
		id = fmt.Sprintf("%s.%s.%s", module, className, funcName)
	} else {
		id = fmt.Sprintf("%s.%s", module, funcName)
	}

	return &types.Symbol{
		ID:        id,
		Name:      funcName,
		Kind:      types.FunctionSymbol,
		Path:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}, nil
}

func (c *PythonConfig) NodeToFact(node *sitter.Node, source []byte, path string) (*types.Fact, error) {
	if node.Type() != "call" {
		return nil, nil
	}

	var calleeName string
	var calleeQualifier string

	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return nil, fmt.Errorf("python: call has no function child")
	}

	switch funcNode.Type() {
	case "identifier":
		calleeName = funcNode.Content(source)
	case "attribute":
		for i := 0; i < int(funcNode.NamedChildCount()); i++ {
			child := funcNode.NamedChild(i)
			switch child.Type() {
			case "identifier":
				if calleeQualifier == "" {
					calleeQualifier = child.Content(source)
				} else {
					calleeName = child.Content(source)
				}
			}
		}
		attrNode := funcNode.ChildByFieldName("attribute")
		if attrNode != nil {
			calleeName = attrNode.Content(source)
		}
	}

	if calleeName == "" {
		return nil, fmt.Errorf("python: callee name not found")
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

func (c *PythonConfig) enclosingClassName(node *sitter.Node, source []byte) string {
	parent := node.Parent()
	for parent != nil {
		if parent.Type() == "class_definition" {
			nameNode := parent.ChildByFieldName("name")
			if nameNode != nil {
				return nameNode.Content(source)
			}
		}
		parent = parent.Parent()
	}
	return ""
}

func (c *PythonConfig) childContent(node *sitter.Node, childType string, source []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == childType {
			return child.Content(source)
		}
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
