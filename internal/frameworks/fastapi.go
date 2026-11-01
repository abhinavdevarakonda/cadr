package frameworks

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tree_sitter_python "github.com/smacker/go-tree-sitter/python"
)

var fastapiParamRegex = regexp.MustCompile(`{([a-zA-Z_]\w*)(?::([a-zA-Z_]\w*))?}`)

func DetectFastAPIEndpoints(files []string) ([]Endpoint, error) {
	// Gate before indexing: a project without FastAPI gets no extra parses.
	if !projectMentions(files, "fastapi", "FastAPI") {
		return nil, nil
	}

	var endpoints []Endpoint

	queryBytes, err := loadQuery("fastapi.scm")
	if err != nil {
		return nil, err
	}

	language := tree_sitter_python.GetLanguage()
	query, err := sitter.NewQuery(queryBytes, language)
	if err != nil {
		return nil, fmt.Errorf("failed to compile fastapi query: %w", err)
	}
	defer query.Close()

	prefixes := getFastAPIMountPrefixes(files)
	index := newPyIndex(files)
	includePrefixes := collectFastAPIIncludePrefixes(index)

	for _, file := range files {
		if !strings.HasSuffix(file, ".py") {
			continue
		}
		fileEndpoints, err := detectFileFastAPIEndpoints(query, file, index)
		if err != nil {
			continue
		}

		mountPrefix, hasMount := prefixes[file]
		include, hasInclude := includePrefixes[cleanNodePath(file)]
		for i := range fileEndpoints {
			ep := &fileEndpoints[i]
			if hasMount {
				ep.Path = joinPrefixPath(mountPrefix, ep.Path)
			}
			if hasInclude {
				includePrefix := include.prefix
				if include.ambiguous {
					// The same router is included under two different prefixes:
					// never guess, leave a fillable token.
					includePrefix = placeholderToken("include_prefix", 0)
				}
				ep.Path = joinPrefixPath(includePrefix, ep.Path)
			}
			if ep.Path == "" {
				ep.Path = "/"
			} else if !strings.HasPrefix(ep.Path, "/") {
				ep.Path = "/" + ep.Path
			}
			// A prefix that could not be resolved ([settings.api_base]) is
			// fillable, so it has to carry a path param after the joins.
			ep.PathParams = mergePathParams(ep.PathParams, unresolvedParams(ep.Path))
		}

		endpoints = append(endpoints, fileEndpoints...)
	}

	return endpoints, nil
}

// joinPrefixPath mirrors FastAPI: an empty route path under a prefix is the
// prefix itself, never prefix + "/". Nothing to join leaves the path empty; the
// caller turns a still-empty path into "/".
func joinPrefixPath(prefix, path string) string {
	if path == "" {
		return prefix
	}
	return joinPath(prefix, path)
}

// fastAPIIncludePrefix is the prefix one router module is included under. A
// module included twice under different prefixes is ambiguous: the prefix is
// not guessed, the caller emits a fillable token instead.
type fastAPIIncludePrefix struct {
	prefix    string
	ambiguous bool
}

// collectFastAPIIncludePrefixes maps a router module (file) to the prefix it is
// included under: `app.include_router(routes_books.router, prefix=...)` in one
// file applies to the decorators in routes_books.py. Only module-alias targets
// (`module.router`) are followed; same-file includes are handled per file.
func collectFastAPIIncludePrefixes(index *pyIndex) map[string]fastAPIIncludePrefix {
	prefixes := make(map[string]fastAPIIncludePrefix)

	for _, module := range index.files {
		evaluator := index.evaluator(module)
		nodeWalk(module.root, func(node *sitter.Node) {
			if node.Type() != "call" {
				return
			}
			function := node.ChildByFieldName("function")
			arguments := node.ChildByFieldName("arguments")
			if function == nil || arguments == nil || function.Type() != "attribute" {
				return
			}
			attribute := function.ChildByFieldName("attribute")
			if attribute == nil || attribute.Content(module.source) != "include_router" {
				return
			}
			first := arguments.NamedChild(0)
			if first == nil || first.Type() != "attribute" {
				return
			}
			object := first.ChildByFieldName("object")
			if object == nil || object.Type() != "identifier" {
				return
			}
			binding, ok := index.binds[module.path][object.Content(module.source)]
			if !ok || binding.module == "" || binding.key != "" {
				return
			}
			prefix, present := getKeywordArgValue(arguments, "prefix", evaluator)
			if !present || prefix == "" {
				return
			}
			if existing, seen := prefixes[binding.module]; seen {
				if existing.prefix != prefix {
					existing.ambiguous = true
					prefixes[binding.module] = existing
				}
				return
			}
			prefixes[binding.module] = fastAPIIncludePrefix{prefix: prefix}
		})
	}

	return prefixes
}

func detectFileFastAPIEndpoints(query *sitter.Query, path string, index *pyIndex) ([]Endpoint, error) {
	module := index.files[cleanNodePath(path)]
	if module == nil {
		return nil, nil
	}
	source := module.source

	if !strings.Contains(string(source), "fastapi") && !strings.Contains(string(source), "FastAPI") {
		return nil, nil
	}

	evaluator := index.evaluator(module)

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(query, module.root)

	routers := make(map[string]string) // e.g. router -> "/users"
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

		if varName != "" && funcName == "APIRouter" && argsNode != nil {
			prefix, _ := getKeywordArgValue(argsNode, "prefix", evaluator)
			routers[varName] = prefix
		} else if decNode != nil && argsNode != nil {
			rawDecorators = append(rawDecorators, rawDecorator{
				appVar:     appVar,
				methodAttr: methodAttr,
				argsNode:   argsNode,
				decNode:    decNode,
			})
		}
	}

	// app.include_router(router, prefix=...) adds its prefix to routes that were
	// registered on a router in this same file. Cross-file includes are handled
	// by collectFastAPIIncludePrefixes.
	nodeWalk(module.root, func(node *sitter.Node) {
		if node.Type() != "call" {
			return
		}
		function := node.ChildByFieldName("function")
		arguments := node.ChildByFieldName("arguments")
		if function == nil || arguments == nil || function.Type() != "attribute" {
			return
		}
		attribute := function.ChildByFieldName("attribute")
		if attribute == nil || attribute.Content(source) != "include_router" {
			return
		}
		first := arguments.NamedChild(0)
		if first == nil || first.Type() != "identifier" {
			return
		}
		name := first.Content(source)
		prefix, present := getKeywordArgValue(arguments, "prefix", evaluator)
		if !present {
			return
		}
		routers[name] = joinPrefixPath(prefix, routers[name])
	})

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

		// Apply APIRouter prefix (an empty decorator path is the prefix itself).
		if prefix, exists := routers[rd.appVar]; exists {
			routePath = joinPrefixPath(prefix, routePath)
		}

		var methods []string
		mAttr := strings.ToLower(rd.methodAttr)
		if mAttr == "api_route" {
			methods = getMethodsArg(rd.argsNode, source)
			if len(methods) == 0 {
				methods = []string{"GET"}
			}
		} else if mAttr == "get" || mAttr == "post" || mAttr == "put" || mAttr == "delete" || mAttr == "patch" || mAttr == "options" || mAttr == "head" || mAttr == "trace" {
			methods = []string{strings.ToUpper(rd.methodAttr)}
		} else {
			continue
		}

		normalizedPath := normalizeFastAPIPath(routePath)
		params := mergePathParams(extractFastAPIPathParams(routePath), unresolvedParams(normalizedPath))

		for _, method := range methods {
			fileEndpoints = append(fileEndpoints, Endpoint{
				Method:      method,
				Path:        normalizedPath,
				HandlerFunc: handlerName,
				File:        path,
				Line:        int(rd.decNode.StartPoint().Row) + 1,
				Framework:   "fastapi",
				PathParams:  params,
			})
		}
	}

	return fileEndpoints, nil
}

func extractFastAPIPathParams(path string) []PathParam {
	matches := fastapiParamRegex.FindAllStringSubmatch(path, -1)
	var params []PathParam
	for _, m := range matches {
		t := "string"
		if m[2] != "" {
			t = m[2]
		}
		params = append(params, PathParam{
			Name: m[1],
			Type: t,
		})
	}
	return params
}

func normalizeFastAPIPath(path string) string {
	return fastapiParamRegex.ReplaceAllStringFunc(path, func(m string) string {
		sub := fastapiParamRegex.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		name := sub[1]
		typ := sub[2]
		if typ != "" {
			return fmt.Sprintf("<%s:%s>", typ, name)
		}
		return fmt.Sprintf("<%s>", name)
	})
}

func getFastAPIMountPrefixes(files []string) map[string]string {
	prefixes := make(map[string]string)
	mountRegex := regexp.MustCompile(`\.mount\(\s*['"]([^'"]+)['"]\s*,\s*([a-zA-Z0-9_\.]+)\s*\)`)

	for _, file := range files {
		if !strings.HasSuffix(file, ".py") {
			continue
		}
		contentBytes, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		content := string(contentBytes)
		if !strings.Contains(content, "mount(") {
			continue
		}

		matches := mountRegex.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			prefix := m[1]
			varName := m[2]
			if strings.Contains(varName, ".") {
				varName = strings.Split(varName, ".")[0]
			}

			// Find import for this variable name in the same file
			// Match "from module import varName [as ...]" or "import varName [as ...]"
			importPattern := `(?:from\s+(\S+)\s+import\s+|import\s+)(?:[a-zA-Z0-9_]+\s+as\s+)?` + regexp.QuoteMeta(varName) + `\b`
			importRegex, err := regexp.Compile(importPattern)
			if err != nil {
				continue
			}
			impMatch := importRegex.FindStringSubmatch(content)

			var modName string
			if len(impMatch) >= 2 {
				if impMatch[1] != "" {
					modParts := strings.Split(impMatch[1], ".")
					modName = modParts[len(modParts)-1]
				} else {
					modName = varName
				}
			} else {
				modName = varName
			}

			// Find matching file in the workspace
			for _, f := range files {
				if strings.HasSuffix(f, "/"+modName+".py") || strings.HasSuffix(f, "\\"+modName+".py") {
					prefixes[f] = prefix
					break
				}
			}
		}
	}
	return prefixes
}
