package frameworks

import (
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// nodeEval resolves the value of a JavaScript/TypeScript expression in one
// module's scope, following imports and concatenation.
type nodeEval struct {
	module *nodeModule
	source []byte
	index  *nodeIndex
}

// values expands a path argument into every concrete path it can produce:
// arrays expand to one path per element and anything unresolvable becomes a
// fillable `[NAME]` / `[exprL<line>]` token.
func (e *nodeEval) values(node *sitter.Node) []string {
	if node == nil {
		return nil
	}
	if node.Type() == "array" {
		var values []string
		for i := 0; i < int(node.NamedChildCount()); i++ {
			values = append(values, e.values(node.NamedChild(i))...)
		}
		return values
	}
	if value, ok := e.value(node); ok {
		return []string{value}
	}
	return []string{placeholderToken(node.Content(e.source), int(node.StartPoint().Row)+1)}
}

func (e *nodeEval) value(node *sitter.Node) (string, bool) {
	if node == nil {
		return "", false
	}

	switch node.Type() {
	case "string":
		return nodeUnquoteString(node.Content(e.source))

	case "template_string":
		return e.templateString(node)

	case "identifier", "shorthand_property_identifier", "shorthand_property_identifier_pattern":
		return e.identifier(node.Content(e.source))

	case "member_expression":
		return e.member(node)

	case "binary_expression":
		return e.binary(node)

	case "parenthesized_expression":
		return e.value(node.NamedChild(0))

	case "as_expression", "satisfies_expression", "non_null_expression", "type_assertion":
		if inner := node.ChildByFieldName("expression"); inner != nil {
			return e.value(inner)
		}
		if node.NamedChildCount() > 0 {
			return e.value(node.NamedChild(int(node.NamedChildCount()) - 1))
		}
	}
	return "", false
}

func (e *nodeEval) identifier(name string) (string, bool) {
	if e.module != nil {
		if value, ok := e.module.consts[name]; ok {
			return value, true
		}
		if binding, ok := e.module.binds[name]; ok && binding.module != "" && binding.key != "" {
			return e.index.exportValue(binding.module, binding.key, 1)
		}
	}
	if e.index != nil && !e.index.building {
		return e.index.uniqueConst(name)
	}
	return "", false
}

func (e *nodeEval) member(node *sitter.Node) (string, bool) {
	object := node.ChildByFieldName("object")
	property := node.ChildByFieldName("property")
	if object == nil || property == nil || object.Type() != "identifier" || e.module == nil {
		return "", false
	}
	binding, ok := e.module.binds[object.Content(e.source)]
	if !ok || binding.module == "" || binding.key != "" {
		return "", false
	}
	return e.index.exportValue(binding.module, strings.Trim(property.Content(e.source), `"'`), 1)
}

func (e *nodeEval) binary(node *sitter.Node) (string, bool) {
	operatorNode := node.ChildByFieldName("operator")
	operator := "+"
	if operatorNode != nil {
		operator = operatorNode.Content(e.source)
	}
	leftNode := node.ChildByFieldName("left")
	rightNode := node.ChildByFieldName("right")

	switch operator {
	case "+":
		left, lok := e.value(leftNode)
		if !lok {
			left = placeholderToken(nodeContent(leftNode, e.source), nodeLine(leftNode))
		}
		right, rok := e.value(rightNode)
		if !rok {
			right = placeholderToken(nodeContent(rightNode, e.source), nodeLine(rightNode))
		}
		if left == "" && right == "" {
			return "", false
		}
		return left + right, true
	case "||", "??":
		if left, ok := e.value(leftNode); ok && left != "" {
			return left, true
		}
		return e.value(rightNode)
	}
	return "", false
}

// templateString implements C2: `${API_PREFIX}/books` resolves when every
// substitution resolves.
func (e *nodeEval) templateString(node *sitter.Node) (string, bool) {
	var builder strings.Builder
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "string_fragment":
			builder.WriteString(child.Content(e.source))
		case "template_substitution":
			if child.NamedChildCount() == 0 {
				return "", false
			}
			value, ok := e.value(child.NamedChild(0))
			if !ok {
				return "", false
			}
			builder.WriteString(value)
		default:
			builder.WriteString(child.Content(e.source))
		}
	}
	return builder.String(), true
}

func nodeContent(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return node.Content(source)
}

func nodeLine(node *sitter.Node) int {
	if node == nil {
		return 0
	}
	return int(node.StartPoint().Row) + 1
}

func nodeUnquoteString(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 {
		return "", false
	}
	switch raw[0] {
	case '"':
		if unquoted, err := strconv.Unquote(raw); err == nil {
			return unquoted, true
		}
		return raw[1 : len(raw)-1], true
	case '\'', '`':
		return raw[1 : len(raw)-1], true
	}
	return "", false
}
