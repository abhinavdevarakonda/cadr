package frameworks

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/abhinavdevarakonda/cadr/queries"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

var ginParamRegex = regexp.MustCompile(`[:*]([a-zA-Z_]\w*)`)

var ginSupportedMethods = map[string]string{
	"GET":     "GET",
	"POST":    "POST",
	"PUT":     "PUT",
	"DELETE":  "DELETE",
	"PATCH":   "PATCH",
	"OPTIONS": "OPTIONS",
	"HEAD":    "HEAD",
	"Any":     "ANY",
}

// DetectGinEndpoints scans the given Go files for Gin route registrations.
func DetectGinEndpoints(files []string) ([]Endpoint, error) {
	var endpoints []Endpoint

	queryBytes, err := os.ReadFile("queries/gin.scm")
	if err != nil {
		queryBytes, err = queries.Files.ReadFile("gin.scm")
		if err != nil {
			return nil, fmt.Errorf("failed to read gin query: %w", err)
		}
	}

	language := golang.GetLanguage()
	query, err := sitter.NewQuery(queryBytes, language)
	if err != nil {
		return nil, fmt.Errorf("failed to compile gin query: %w", err)
	}
	defer query.Close()

	for _, file := range files {
		if !strings.HasSuffix(file, ".go") {
			continue
		}
		fileEndpoints, err := detectFileGinEndpoints(query, file)
		if err != nil {
			continue
		}
		endpoints = append(endpoints, fileEndpoints...)
	}

	return endpoints, nil
}

func detectFileGinEndpoints(query *sitter.Query, path string) ([]Endpoint, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	sourceStr := string(source)
	if !strings.Contains(sourceStr, "gin") {
		return nil, nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(golang.GetLanguage())

	tree, err := parser.ParseCtx(context.Background(), nil, source)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(query, tree.RootNode())

	// 1. Collect file-level constants and variables (e.g. const ApiPrefix = "/api/v1")
	consts := make(map[string]string)
	extractStringConstants(tree.RootNode(), source, consts)

	// 2. groups maps router variable names to their cumulative route prefix (e.g. "v1" -> "/api/v1")
	groups := make(map[string]string)

	type rawRoute struct {
		routerVar string
		method    string
		argsNode  *sitter.Node
		callNode  *sitter.Node
	}
	var rawRoutes []rawRoute

	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}

		var routerVar, method, groupVar, parentRouter string
		var argsNode, callNode, groupArgsNode *sitter.Node

		for _, capture := range match.Captures {
			name := query.CaptureNameForId(capture.Index)
			switch name {
			case "router":
				routerVar = capture.Node.Content(source)
			case "method":
				method = capture.Node.Content(source)
			case "args":
				argsNode = capture.Node
			case "route_call":
				callNode = capture.Node
			case "group_var":
				groupVar = capture.Node.Content(source)
			case "parent_router":
				parentRouter = capture.Node.Content(source)
			case "group_args":
				groupArgsNode = capture.Node
			}
		}

		// Handle route group creation: v1 := router.Group("/api/v1") or router.Group(ApiPrefix)
		if groupVar != "" && groupArgsNode != nil {
			prefix := extractFirstStringArg(groupArgsNode, source, consts)
			if parentPrefix, exists := groups[parentRouter]; exists {
				prefix = joinPath(parentPrefix, prefix)
			}
			groups[groupVar] = prefix
		} else if routerVar != "" && method != "" && argsNode != nil && callNode != nil {
			rawRoutes = append(rawRoutes, rawRoute{
				routerVar: routerVar,
				method:    method,
				argsNode:  argsNode,
				callNode:  callNode,
			})
		}
	}

	var fileEndpoints []Endpoint
	for _, rr := range rawRoutes {
		normMethod, isSupported := ginSupportedMethods[rr.method]
		isStatic := (rr.method == "Static" || rr.method == "StaticFile")
		if !isSupported && !isStatic {
			continue
		}

		routePath := extractFirstStringArg(rr.argsNode, source, consts)
		if routePath == "" && !strings.Contains(rr.argsNode.Content(source), `""`) {
			// If not an explicit empty string, skip malformed call
			continue
		}

		// Apply group prefix if router variable was a registered group
		if prefix, exists := groups[rr.routerVar]; exists {
			routePath = joinPath(prefix, routePath)
		}
		if routePath == "" {
			routePath = "/"
		} else if !strings.HasPrefix(routePath, "/") {
			routePath = "/" + routePath
		}

		// Extract handler name (last argument in the argument list)
		handlerName := extractLastArgHandler(rr.argsNode, source)
		if isStatic {
			normMethod = "GET"
			if rr.method == "StaticFile" {
				handlerName = "gin.StaticFile(" + extractSecondStringArg(rr.argsNode, source) + ")"
			} else {
				handlerName = "gin.Static(" + extractSecondStringArg(rr.argsNode, source) + ")"
			}
		}

		params := extractGinPathParams(routePath)

		fileEndpoints = append(fileEndpoints, Endpoint{
			Method:      normMethod,
			Path:        routePath,
			HandlerFunc: handlerName,
			File:        path,
			Line:        int(rr.callNode.StartPoint().Row) + 1,
			Framework:   "gin",
			PathParams:  params,
		})
	}

	return fileEndpoints, nil
}

func extractStringConstants(node *sitter.Node, source []byte, consts map[string]string) {
	if node == nil {
		return
	}
	switch node.Type() {
	case "const_spec", "var_spec":
		nameNode := node.ChildByFieldName("name")
		valNode := node.ChildByFieldName("value")
		if nameNode != nil && valNode != nil {
			val := extractNodeStringValue(valNode, source, consts)
			if val != "" {
				consts[nameNode.Content(source)] = val
			}
		}
	case "short_var_declaration":
		leftNode := node.ChildByFieldName("left")
		rightNode := node.ChildByFieldName("right")
		if leftNode != nil && rightNode != nil {
			varNames := leftNode.NamedChildCount()
			varValues := rightNode.NamedChildCount()
			if varNames == 1 && varValues == 1 {
				val := extractNodeStringValue(rightNode.NamedChild(0), source, consts)
				if val != "" && !strings.HasPrefix(val, "[") {
					consts[leftNode.NamedChild(0).Content(source)] = val
				}
			}
		}
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		extractStringConstants(node.NamedChild(i), source, consts)
	}
}

func extractNodeStringValue(node *sitter.Node, source []byte, consts map[string]string) string {
	if node == nil {
		return ""
	}
	switch node.Type() {
	case "interpreted_string_literal", "raw_string_literal":
		raw := node.Content(source)
		if (strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"")) ||
			(strings.HasPrefix(raw, "`") && strings.HasSuffix(raw, "`")) {
			if len(raw) >= 2 {
				return raw[1 : len(raw)-1]
			}
		}
		return raw
	case "identifier":
		name := node.Content(source)
		if v, ok := consts[name]; ok {
			return v
		}
		return "[" + name + "]"
	case "binary_expression":
		left := extractNodeStringValue(node.ChildByFieldName("left"), source, consts)
		right := extractNodeStringValue(node.ChildByFieldName("right"), source, consts)
		return left + right
	case "expression_list":
		if node.NamedChildCount() > 0 {
			return extractNodeStringValue(node.NamedChild(0), source, consts)
		}
	}
	return ""
}

func extractFirstStringArg(argsNode *sitter.Node, source []byte, consts map[string]string) string {
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		val := extractNodeStringValue(child, source, consts)
		if val != "" {
			return val
		}
	}
	return ""
}

func extractSecondStringArg(argsNode *sitter.Node, source []byte) string {
	count := 0
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		if child.Type() == "interpreted_string_literal" || child.Type() == "raw_string_literal" {
			count++
			if count == 2 {
				raw := child.Content(source)
				if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") && len(raw) >= 2 {
					return raw[1 : len(raw)-1]
				}
				if strings.HasPrefix(raw, "`") && strings.HasSuffix(raw, "`") && len(raw) >= 2 {
					return raw[1 : len(raw)-1]
				}
				return raw
			}
		}
	}
	return ""
}

func extractLastArgHandler(argsNode *sitter.Node, source []byte) string {
	n := int(argsNode.NamedChildCount())
	if n <= 1 {
		return "handler"
	}
	lastChild := argsNode.NamedChild(n - 1)
	switch lastChild.Type() {
	case "selector_expression", "identifier":
		return lastChild.Content(source)
	case "func_literal":
		return fmt.Sprintf("func(c *gin.Context):%d", lastChild.StartPoint().Row+1)
	default:
		content := lastChild.Content(source)
		if len(content) > 30 {
			content = content[:30] + "..."
		}
		return content
	}
}

func extractGinPathParams(path string) []PathParam {
	matches := ginParamRegex.FindAllStringSubmatch(path, -1)
	var params []PathParam
	for _, m := range matches {
		pType := "string"
		if strings.HasPrefix(m[0], "*") {
			pType = "wildcard"
		}
		params = append(params, PathParam{
			Name: m[1],
			Type: pType,
		})
	}
	return params
}
