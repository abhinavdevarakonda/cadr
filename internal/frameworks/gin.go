package frameworks

import (
	"fmt"
	"regexp"
	"strings"

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
}

// DetectGinEndpoints scans the given Go files for Gin route registrations.
func DetectGinEndpoints(files []string) ([]Endpoint, error) {
	// Gate before indexing: a project without Gin gets no extra Go parses. The
	// gate is loose (any "gin" substring) on purpose: files that declare the
	// constants (`consts.go`) do not import Gin themselves.
	if !projectMentions(files, "gin") {
		return nil, nil
	}

	var endpoints []Endpoint

	queryBytes, err := loadQuery("gin.scm")
	if err != nil {
		return nil, err
	}

	language := golang.GetLanguage()
	query, err := sitter.NewQuery(queryBytes, language)
	if err != nil {
		return nil, fmt.Errorf("failed to compile gin query: %w", err)
	}
	defer query.Close()

	index := newGoIndex(files, "")
	for _, file := range files {
		if !strings.HasSuffix(file, ".go") {
			continue
		}
		module := index.files[cleanNodePath(file)]
		if module == nil {
			continue
		}
		fileEndpoints, err := detectFileGinEndpoints(query, module, index)
		if err != nil {
			continue
		}
		endpoints = append(endpoints, fileEndpoints...)
	}

	return endpoints, nil
}

func detectFileGinEndpoints(query *sitter.Query, module *goFile, index *goIndex) ([]Endpoint, error) {
	source := module.source
	path := module.path

	if !strings.Contains(string(source), "gin") {
		return nil, nil
	}

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(query, module.root)

	// Package-level constants come from the project index (any file in the
	// package); locals are resolved on top of them.
	evaluator := index.evaluator(module)
	evaluator.collectLocalConsts()

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

		var routerVar, method, groupVar, groupCall, parentRouter string
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
			case "group_call":
				groupCall = capture.Node.Content(source)
			}
		}

		// Handle route group creation: v1 := router.Group("/api/v1") or router.Group(ApiPrefix)
		// go-tree-sitter does not evaluate #eq? predicates, so the call name is
		// checked here; without it `r := gin.Default()` would look like a group.
		if groupVar != "" && groupArgsNode != nil && groupCall == "Group" {
			prefix, ok := evaluator.value(nodeArg(groupArgsNode, 0))
			if !ok {
				prefix = placeholderToken(nodeContent(nodeArg(groupArgsNode, 0), source), nodeLine(nodeArg(groupArgsNode, 0)))
			}
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
		methods := expandRouteMethods(rr.method)
		isStatic := (rr.method == "Static" || rr.method == "StaticFile")
		if len(methods) == 0 && !isStatic {
			continue
		}

		routePath, resolved := evaluator.value(nodeArg(rr.argsNode, 0))
		if !resolved {
			routePath = placeholderToken(nodeContent(nodeArg(rr.argsNode, 0), source), nodeLine(nodeArg(rr.argsNode, 0)))
		}
		if routePath == "" && !strings.Contains(rr.argsNode.Content(source), `""`) {
			// If not an explicit empty string, skip malformed call
			continue
		}

		// Apply group prefix if router variable was a registered group. Gin's
		// joinPaths keeps an empty relative path at the group base, so
		// books.GET("") is /api/v1/books and not /api/v1/books/.
		if prefix, exists := groups[rr.routerVar]; exists {
			if routePath == "" {
				routePath = prefix
			} else {
				routePath = joinPath(prefix, routePath)
			}
		}
		if routePath == "" {
			routePath = "/"
		} else if !strings.HasPrefix(routePath, "/") {
			routePath = "/" + routePath
		}

		// Extract handler name (last argument in the argument list)
		handlerName := extractLastArgHandler(rr.argsNode, source)
		if isStatic {
			methods = []string{"GET"}
			if rr.method == "StaticFile" {
				handlerName = "gin.StaticFile(" + extractSecondStringArg(rr.argsNode, source) + ")"
			} else {
				handlerName = "gin.Static(" + extractSecondStringArg(rr.argsNode, source) + ")"
			}
		}

		params := mergePathParams(extractGinPathParams(routePath), unresolvedParams(routePath))

		for _, normMethod := range methods {
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
	}

	return fileEndpoints, nil
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
