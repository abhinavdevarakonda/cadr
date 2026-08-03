package frameworks

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/abhinavdevarakonda/cadr/queries"
	sitter "github.com/smacker/go-tree-sitter"
	tree_sitter_python "github.com/smacker/go-tree-sitter/python"
)

var fastapiParamRegex = regexp.MustCompile(`{([a-zA-Z_]\w*)(?::([a-zA-Z_]\w*))?}`)

func DetectFastAPIEndpoints(files []string) ([]Endpoint, error) {
	var endpoints []Endpoint

	var queryBytes []byte
	var err error
	queryBytes, err = os.ReadFile("queries/fastapi.scm")
	if err != nil {
		queryBytes, err = queries.Files.ReadFile("fastapi.scm")
		if err != nil {
			return nil, fmt.Errorf("failed to read fastapi query: %w", err)
		}
	}

	language := tree_sitter_python.GetLanguage()
	query, err := sitter.NewQuery(queryBytes, language)
	if err != nil {
		return nil, fmt.Errorf("failed to compile fastapi query: %w", err)
	}
	defer query.Close()

	prefixes := getFastAPIMountPrefixes(files)

	for _, file := range files {
		if !strings.HasSuffix(file, ".py") {
			continue
		}
		fileEndpoints, err := detectFileFastAPIEndpoints(query, file)
		if err != nil {
			continue
		}

		if prefix, exists := prefixes[file]; exists {
			for i := range fileEndpoints {
				fileEndpoints[i].Path = joinPath(prefix, fileEndpoints[i].Path)
			}
		}

		endpoints = append(endpoints, fileEndpoints...)
	}

	return endpoints, nil
}

func detectFileFastAPIEndpoints(query *sitter.Query, path string) ([]Endpoint, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	sourceStr := string(source)
	if !strings.Contains(sourceStr, "fastapi") && !strings.Contains(sourceStr, "FastAPI") {
		return nil, nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(tree_sitter_python.GetLanguage())

	tree, err := parser.ParseCtx(context.Background(), nil, source)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(query, tree.RootNode())

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
			prefix := getKeywordArgValue(argsNode, "prefix", source)
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

		routePath := getFirstPositionalArg(rd.argsNode, source)

		// Apply APIRouter prefix
		if prefix, exists := routers[rd.appVar]; exists {
			routePath = joinPath(prefix, routePath)
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

		params := extractFastAPIPathParams(routePath)
		normalizedPath := normalizeFastAPIPath(routePath)

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
