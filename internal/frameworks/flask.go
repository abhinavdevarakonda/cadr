package frameworks

import (
	"fmt"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tree_sitter_python "github.com/smacker/go-tree-sitter/python"
)

var flaskParamRegex = regexp.MustCompile(`<(?:([a-zA-Z_]\w*):)?([a-zA-Z_]\w*)>`)

func DetectFlaskEndpoints(files []string) ([]Endpoint, error) {
	// Gate before indexing: a project without Flask gets no extra parses.
	if !projectMentions(files, "flask", "Flask") {
		return nil, nil
	}

	var endpoints []Endpoint

	queryBytes, err := loadQuery("flask.scm")
	if err != nil {
		return nil, err
	}

	language := tree_sitter_python.GetLanguage()
	query, err := sitter.NewQuery(queryBytes, language)
	if err != nil {
		return nil, fmt.Errorf("failed to compile flask query: %w", err)
	}
	defer query.Close()

	index := newPyIndex(files)
	for _, file := range files {
		if !strings.HasSuffix(file, ".py") {
			continue
		}
		fileEndpoints, err := detectFileEndpoints(query, file, index)
		if err != nil {
			continue
		}
		endpoints = append(endpoints, fileEndpoints...)
	}

	return endpoints, nil
}

func detectFileEndpoints(query *sitter.Query, path string, index *pyIndex) ([]Endpoint, error) {
	module := index.files[cleanNodePath(path)]
	if module == nil {
		return nil, nil
	}
	source := module.source

	if !strings.Contains(string(source), "flask") && !strings.Contains(string(source), "Flask") {
		return nil, nil
	}

	evaluator := index.evaluator(module)

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(query, module.root)

	blueprints := make(map[string]string) // e.g. bp -> "/api/v1"
	type rawDecorator struct {
		appVar     string
		methodAttr string
		argsNode   *sitter.Node
		decNode    *sitter.Node
	}
	var rawDecorators []rawDecorator

	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}

		var appVar, methodAttr, varName, funcName string
		var argsNode, decNode *sitter.Node

		for _, capture := range match.Captures {
			name := query.CaptureNameForId(capture.Index)
			switch name {
			case "app":
				appVar = capture.Node.Content(source)
			case "method":
				methodAttr = capture.Node.Content(source)
			case "args":
				argsNode = capture.Node
			case "decorator":
				decNode = capture.Node
			case "var":
				varName = capture.Node.Content(source)
			case "func_name":
				funcName = capture.Node.Content(source)
			}
		}

		if varName != "" && funcName == "Blueprint" && argsNode != nil {
			prefix, _ := getKeywordArgValue(argsNode, "url_prefix", evaluator)
			blueprints[varName] = prefix
		} else if decNode != nil && argsNode != nil {
			rawDecorators = append(rawDecorators, rawDecorator{
				appVar:     appVar,
				methodAttr: methodAttr,
				argsNode:   argsNode,
				decNode:    decNode,
			})
		}
	}

	var fileEndpoints []Endpoint
	for _, rd := range rawDecorators {
		funcNode := getDecoratorFunction(rd.decNode)
		if funcNode == nil {
			continue
		}

		funcNameNode := funcNode.ChildByFieldName("name")
		if funcNameNode == nil {
			continue
		}
		handlerName := funcNameNode.Content(source)

		routePath, ok := getFirstPositionalArg(rd.argsNode, evaluator)
		if !ok {
			continue
		}

		// Apply blueprint prefix
		if prefix, exists := blueprints[rd.appVar]; exists {
			routePath = joinPath(prefix, routePath)
		}

		var methods []string
		mAttr := strings.ToLower(rd.methodAttr)
		if mAttr == "route" {
			methods = getMethodsArg(rd.argsNode, source)
			if len(methods) == 0 {
				methods = []string{"GET"}
			}
		} else if mAttr == "get" || mAttr == "post" || mAttr == "put" || mAttr == "delete" || mAttr == "patch" {
			methods = []string{strings.ToUpper(rd.methodAttr)}
		} else {
			continue // Skip non-routing decorators (e.g. errorhandler)
		}

		params := mergePathParams(extractPathParams(routePath), unresolvedParams(routePath))

		for _, method := range methods {
			fileEndpoints = append(fileEndpoints, Endpoint{
				Method:      method,
				Path:        routePath,
				HandlerFunc: handlerName,
				File:        path,
				Line:        int(rd.decNode.StartPoint().Row) + 1,
				Framework:   "flask",
				PathParams:  params,
			})
		}
	}

	return fileEndpoints, nil
}

// getKeywordArgValue resolves a keyword argument through the module index.
// present is false when the keyword is absent; when it is present but cannot be
// resolved the value is a fillable placeholder.
func getKeywordArgValue(node *sitter.Node, name string, evaluator *pyEval) (string, bool) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() != "keyword_argument" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil || nameNode.Content(evaluator.source) != name {
			continue
		}
		valNode := child.ChildByFieldName("value")
		if valNode == nil {
			return "", false
		}
		if value, ok := evaluator.value(valNode); ok {
			return value, true
		}
		return placeholderToken(valNode.Content(evaluator.source), int(valNode.StartPoint().Row)+1), true
	}
	return "", false
}

// getFirstPositionalArg resolves the first positional argument of a decorator.
// Unresolvable arguments become fillable placeholders.
func getFirstPositionalArg(node *sitter.Node, evaluator *pyEval) (string, bool) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "keyword_argument" {
			continue
		}
		if value, ok := evaluator.value(child); ok {
			return value, true
		}
		return placeholderToken(child.Content(evaluator.source), int(child.StartPoint().Row)+1), true
	}
	return "", false
}

func getMethodsArg(node *sitter.Node, source []byte) []string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "keyword_argument" {
			nameNode := child.ChildByFieldName("name")
			if nameNode != nil && nameNode.Content(source) == "methods" {
				valNode := child.ChildByFieldName("value")
				if valNode == nil {
					return nil
				}
				if valNode.Type() == "list" || valNode.Type() == "tuple" || valNode.Type() == "set" {
					var methods []string
					for j := 0; j < int(valNode.NamedChildCount()); j++ {
						item := valNode.NamedChild(j)
						if item.Type() == "string" {
							methods = append(methods, strings.ToUpper(cleanStringLiteral(item.Content(source))))
						}
					}
					return methods
				}
				if valNode.Type() == "string" {
					return []string{strings.ToUpper(cleanStringLiteral(valNode.Content(source)))}
				}
			}
		}
	}
	return nil
}

func getDecoratorFunction(node *sitter.Node) *sitter.Node {
	parent := node.Parent()
	if parent == nil || parent.Type() != "decorated_definition" {
		return nil
	}
	for i := 0; i < int(parent.NamedChildCount()); i++ {
		child := parent.NamedChild(i)
		if child.Type() == "function_definition" {
			return child
		}
	}
	return nil
}

func extractPathParams(path string) []PathParam {
	matches := flaskParamRegex.FindAllStringSubmatch(path, -1)
	var params []PathParam
	for _, m := range matches {
		t := "string"
		if m[1] != "" {
			t = m[1]
		}
		params = append(params, PathParam{
			Name: m[2],
			Type: t,
		})
	}
	return params
}

func cleanStringLiteral(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func joinPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	p := prefix + "/" + path
	p = regexp.MustCompile(`//+`).ReplaceAllString(p, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
