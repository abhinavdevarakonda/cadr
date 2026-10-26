package frameworks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// nodeRouteMethods are the Express/Koa-router/Fastify helpers that register a
// single route. Fastify's object form (`fastify.route({...})`) is handled
// separately as nodeRouteMethod.
var nodeRouteMethods = map[string]bool{
	"get":     true,
	"post":    true,
	"put":     true,
	"patch":   true,
	"delete":  true,
	"options": true,
	"head":    true,
	"all":     true,
}

// nodeRouterFactories are the callees that produce an Express app, an
// express.Router(), a Koa router, or a Fastify instance.
var nodeRouterFactories = map[string]bool{
	"express":   true,
	"Router":    true,
	"koaRouter": true,
	"KoaRouter": true,
	"fastify":   true,
	"Fastify":   true,
	"koa":       true,
}

// nodeConventionalRouters are names that are routers/apps by convention in
// Express/Fastify/Koa codebases even when the factory call is not visible in
// the same file (e.g. imported instances).
var nodeConventionalRouters = map[string]bool{
	"app":     true,
	"router":  true,
	"server":  true,
	"fastify": true,
}

// DetectNodeEndpoints scans JavaScript/TypeScript files for Express, Fastify
// and Koa-router route registrations. NestJS (class + decorator shape) is not
// covered.
//
// Precision guards: the project must mention express/fastify/koa somewhere,
// the file must look like a route registration file, and the receiver must be
// a known/constructed router or a conventional name — so `map.get('/x', y)`
// does not become an endpoint.
func DetectNodeEndpoints(files []string) ([]Endpoint, error) {
	var candidates []string
	for _, file := range files {
		if nodeLanguageForFile(file) != nil {
			candidates = append(candidates, file)
		}
	}
	if len(candidates) == 0 || !projectMentionsNodeFramework(candidates) {
		return nil, nil
	}

	index := newNodeIndex(candidates)

	queries := make(map[string]*sitter.Query)
	defer func() {
		for _, q := range queries {
			q.Close()
		}
	}()

	queryName := "express.scm"
	var endpoints []Endpoint
	for _, file := range candidates {
		module := index.files[cleanNodePath(file)]
		if module == nil {
			continue
		}
		language := nodeLanguageForFile(file)
		key := nodeLanguageKey(file)
		query, ok := queries[key]
		if !ok {
			queryBytes, err := loadQuery(queryName)
			if err != nil {
				return nil, err
			}
			query, err = sitter.NewQuery(queryBytes, language)
			if err != nil {
				return nil, fmt.Errorf("failed to compile node query for %s: %w", key, err)
			}
			queries[key] = query
		}

		fileEndpoints, err := detectFileNodeEndpoints(query, file, module, index)
		if err != nil {
			continue
		}
		endpoints = append(endpoints, fileEndpoints...)
	}

	return endpoints, nil
}

func nodeLanguageKey(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "tsx"
	default:
		return "javascript"
	}
}

func nodeLanguageForFile(path string) *sitter.Language {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".mjs", ".cjs":
		return javascript.GetLanguage()
	case ".ts", ".mts", ".cts":
		return typescript.GetLanguage()
	case ".tsx":
		return tsx.GetLanguage()
	}
	return nil
}

func projectMentionsNodeFramework(files []string) bool {
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		src := string(data)
		if strings.Contains(src, "express") || strings.Contains(src, "fastify") || strings.Contains(src, "koa") {
			return true
		}
	}
	return false
}

func nodeFileLooksLikeRoutes(src string) bool {
	if strings.Contains(src, "express") || strings.Contains(src, "fastify") || strings.Contains(src, "koa") {
		return true
	}
	return strings.Contains(src, "app.") ||
		strings.Contains(src, "router.") ||
		strings.Contains(src, "fastify.") ||
		strings.Contains(src, "server.")
}

type nodeScopedPrefix struct {
	startByte uint32
	endByte   uint32
	prefix    string
}

type nodePrefixStatement struct {
	receiver  string
	prefix    string
	startByte uint32
}

func detectFileNodeEndpoints(query *sitter.Query, path string, module *nodeModule, index *nodeIndex) ([]Endpoint, error) {
	source := module.source
	root := module.root

	if !nodeFileLooksLikeRoutes(string(source)) {
		return nil, nil
	}

	// Values come from the project index: file-local constants first, then
	// imported modules, then a project-wide unique name.
	evaluator := module.evaluator(index)
	functions := make(map[string]*sitter.Node)
	nodeCollectFunctions(root, source, functions)

	routerVars := make(map[string]bool)
	for name := range nodeConventionalRouters {
		routerVars[name] = true
	}

	routes := make([]nodeRoute, 0)
	routePrefixes := make(map[string]string)
	var scopedPrefixes []nodeScopedPrefix
	var prefixStatements []nodePrefixStatement
	type nodeMountCall struct {
		method   string
		receiver string
		argsNode *sitter.Node
		callNode *sitter.Node
	}
	var mountCalls []nodeMountCall

	qc := sitter.NewQueryCursor()
	defer qc.Close()

	// Pass 1: router construction, `use`/`register` mounts and `prefix()` calls.
	qc.Exec(query, root)
	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}

		var routerVar, receiver, method, factory string
		var argsNode, callNode *sitter.Node
		for _, capture := range match.Captures {
			name := query.CaptureNameForId(capture.Index)
			switch name {
			case "router_var":
				routerVar = capture.Node.Content(source)
			case "factory":
				factory = capture.Node.Content(source)
			case "router":
				receiver = capture.Node.Content(source)
			case "method":
				method = capture.Node.Content(source)
			case "args":
				argsNode = capture.Node
			case "route_call":
				callNode = capture.Node
			}
		}

		if routerVar != "" {
			if nodeIsRouterFactory(factory) {
				routerVars[routerVar] = true
			}
			continue
		}

		if receiver == "" || argsNode == nil {
			continue
		}

		switch method {
		case "use", "register", "prefix":
			mountCalls = append(mountCalls, nodeMountCall{
				method:   method,
				receiver: receiver,
				argsNode: argsNode,
				callNode: callNode,
			})
		}
	}

	// Mounts run after every router construction is known, so
	// `app.use(API_PREFIX, r)` also resolves when `r` is declared later.
	for _, mount := range mountCalls {
		switch mount.method {
		case "use":
			nodeCollectUseMount(mount.argsNode, evaluator, routerVars, routePrefixes)
		case "register":
			nodeCollectRegisterMount(mount.argsNode, evaluator, routerVars, functions, routePrefixes, &scopedPrefixes)
		case "prefix":
			if p, ok := evaluator.value(nodeArg(mount.argsNode, 0)); ok && p != "" {
				prefixStatements = append(prefixStatements, nodePrefixStatement{
					receiver:  mount.receiver,
					prefix:    p,
					startByte: nodeStartByte(mount.callNode),
				})
			}
		}
	}

	// Pass 2: route registrations.
	qc.Exec(query, root)
	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}

		var receiver, method string
		var argsNode, callNode *sitter.Node
		for _, capture := range match.Captures {
			name := query.CaptureNameForId(capture.Index)
			switch name {
			case "router":
				receiver = capture.Node.Content(source)
			case "method":
				method = capture.Node.Content(source)
			case "args":
				argsNode = capture.Node
			case "route_call":
				callNode = capture.Node
			}
		}

		if receiver == "" || argsNode == nil || callNode == nil {
			continue
		}
		if !routerVars[receiver] {
			continue
		}
		routes = append(routes, nodeRoute{
			receiver: receiver,
			method:   method,
			argsNode: argsNode,
			callNode: callNode,
		})
	}

	var fileEndpoints []Endpoint
	for _, route := range routes {
		methods := expandNodeMethods(route.method, route.argsNode, evaluator)
		if len(methods) == 0 {
			continue
		}

		paths := nodeRoutePaths(route, evaluator)
		if len(paths) == 0 {
			continue
		}

		handlerName := nodeRouteHandler(route, source, functions)

		prefix := routePrefixes[route.receiver]
		for _, stmt := range prefixStatements {
			if stmt.receiver == route.receiver && stmt.startByte < route.callNode.StartByte() {
				prefix = joinPath(prefix, stmt.prefix)
			}
		}
		for _, scoped := range scopedPrefixes {
			if route.callNode.StartByte() >= scoped.startByte && route.callNode.EndByte() <= scoped.endByte {
				prefix = joinPath(prefix, scoped.prefix)
			}
		}

		for _, p := range paths {
			routePath := normalizeNodePath(joinPath(prefix, p))
			params := mergePathParams(extractGinPathParams(routePath), unresolvedParams(routePath))
			for _, method := range methods {
				fileEndpoints = append(fileEndpoints, Endpoint{
					Method:      method,
					Path:        routePath,
					HandlerFunc: handlerName,
					File:        path,
					Line:        int(route.callNode.StartPoint().Row) + 1,
					Framework:   "node",
					PathParams:  params,
				})
			}
		}
	}

	return fileEndpoints, nil
}

type nodeRoute struct {
	receiver string
	method   string
	argsNode *sitter.Node
	callNode *sitter.Node
}

func nodeStartByte(node *sitter.Node) uint32 {
	if node == nil {
		return 0
	}
	return node.StartByte()
}

func nodeIsRouterFactory(factory string) bool {
	if factory == "" {
		return false
	}
	parts := strings.Split(factory, ".")
	return nodeRouterFactories[factory] || nodeRouterFactories[parts[len(parts)-1]] || nodeRouterFactories[parts[0]]
}

// nodeCollectUseMount records Express/Koa mounts: app.use('/api', usersRouter).
// The prefix is keyed by the mounted identifier, so it only affects routes
// registered in this same file (cross-file mounting is not resolved).
func nodeCollectUseMount(argsNode *sitter.Node, evaluator *nodeEval, routerVars map[string]bool, prefixes map[string]string) {
	pathArg := nodeArg(argsNode, 0)
	if pathArg == nil {
		return
	}
	prefix, ok := evaluator.value(pathArg)
	if !ok {
		// `app.use(mw1, mw2)` starts with middleware, not a mount prefix. Only
		// keep an unresolved first argument when something mounted later is a
		// known router (`app.use(API_PREFIX, booksRouter)`).
		if !nodeHasRouterArg(argsNode, evaluator, routerVars) {
			return
		}
		prefix = placeholderToken(pathArg.Content(evaluator.source), int(pathArg.StartPoint().Row)+1)
	}
	if prefix == "" {
		return
	}

	for i := 1; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		if child.Type() != "identifier" {
			continue
		}
		name := child.Content(evaluator.source)
		prefixes[name] = joinPath(prefixes[name], prefix)
	}
}

// nodeCollectRegisterMount records Fastify mounts:
//
//	fastify.register(usersRouter, { prefix: '/v1' })
//	fastify.register(routesPlugin, { prefix: '/v1' })   // same file only
func nodeHasRouterArg(argsNode *sitter.Node, evaluator *nodeEval, routerVars map[string]bool) bool {
	for i := 1; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		if child.Type() == "identifier" && routerVars[child.Content(evaluator.source)] {
			return true
		}
	}
	return false
}

func nodeCollectRegisterMount(argsNode *sitter.Node, evaluator *nodeEval, routerVars map[string]bool, functions map[string]*sitter.Node, prefixes map[string]string, scoped *[]nodeScopedPrefix) {
	source := evaluator.source
	pluginArg := nodeArg(argsNode, 0)
	optsArg := nodeArg(argsNode, 1)
	if pluginArg == nil || optsArg == nil {
		return
	}
	prefixNode := nodeObjectPair(optsArg, "prefix", source)
	if prefixNode == nil {
		return
	}
	prefix, ok := evaluator.value(prefixNode)
	if !ok {
		prefix = placeholderToken(prefixNode.Content(source), int(prefixNode.StartPoint().Row)+1)
	}
	if prefix == "" {
		return
	}

	if pluginArg.Type() != "identifier" {
		return
	}
	name := pluginArg.Content(source)

	if routerVars[name] {
		prefixes[name] = joinPath(prefixes[name], prefix)
		return
	}

	fn := functions[name]
	if fn == nil {
		return
	}

	// Route calls inside the plugin function use the function's first
	// parameter as the receiver, so scope the prefix to the function body.
	if param := nodeFirstParamName(fn, source); param != "" {
		*scoped = append(*scoped, nodeScopedPrefix{
			startByte: fn.StartByte(),
			endByte:   fn.EndByte(),
			prefix:    prefix,
		})
		routerVars[param] = true
	}
}

func expandNodeMethods(method string, argsNode *sitter.Node, evaluator *nodeEval) []string {
	if method == "route" {
		obj := nodeArg(argsNode, 0)
		if obj == nil || obj.Type() != "object" {
			return nil
		}
		methodNode := nodeObjectPair(obj, "method", evaluator.source)
		if methodNode == nil {
			return nil
		}
		var methods []string
		for _, m := range evaluator.values(methodNode) {
			m = strings.ToUpper(strings.TrimSpace(m))
			// A placeholder method cannot be sent; skip the route rather than
			// emitting an endpoint with an invalid verb.
			if m == "" || strings.ContainsAny(m, "[]") {
				continue
			}
			methods = append(methods, m)
		}
		return methods
	}

	if !nodeRouteMethods[method] {
		return nil
	}
	if method == "all" {
		return expandRouteMethods("all")
	}
	return []string{strings.ToUpper(method)}
}

func nodeRoutePaths(route nodeRoute, evaluator *nodeEval) []string {
	source := evaluator.source
	if route.method == "route" {
		obj := nodeArg(route.argsNode, 0)
		if obj == nil || obj.Type() != "object" {
			return nil
		}
		urlNode := nodeObjectPair(obj, "url", source)
		if urlNode == nil {
			urlNode = nodeObjectPair(obj, "path", source)
		}
		if urlNode == nil {
			return nil
		}
		return evaluator.values(urlNode)
	}

	pathArg := nodeArg(route.argsNode, 0)
	if pathArg == nil {
		return nil
	}
	return evaluator.values(pathArg)
}

// nodeRouteHandler picks the last function-looking argument, skipping trailing
// error middleware (`(err, req, res, next)`) declared in the same file so
// `router.get(path, controller.list, appError)` reports the controller.
func nodeRouteHandler(route nodeRoute, source []byte, functions map[string]*sitter.Node) string {
	if route.method == "route" {
		obj := nodeArg(route.argsNode, 0)
		if obj != nil && obj.Type() == "object" {
			if handlerNode := nodeObjectPair(obj, "handler", source); handlerNode != nil {
				return nodeHandlerName(handlerNode, source)
			}
		}
		return "handler"
	}

	count := int(route.argsNode.NamedChildCount())
	chosen := nodeArg(route.argsNode, count-1)
	for i := count - 1; i >= 1; i-- {
		arg := nodeArg(route.argsNode, i)
		if !nodeLooksLikeHandler(arg) {
			continue
		}
		if nodeIsErrorMiddleware(arg, functions, source) {
			continue
		}
		chosen = arg
		break
	}
	if chosen == nil {
		return "handler"
	}
	return nodeHandlerName(chosen, source)
}

func nodeLooksLikeHandler(node *sitter.Node) bool {
	if node == nil {
		return false
	}
	switch node.Type() {
	case "identifier", "member_expression", "arrow_function", "function_expression",
		"function", "generator_function", "call_expression":
		return true
	}
	return false
}

func nodeIsErrorMiddleware(node *sitter.Node, functions map[string]*sitter.Node, source []byte) bool {
	if node == nil || node.Type() != "identifier" || functions == nil {
		return false
	}
	fn := functions[node.Content(source)]
	if fn == nil {
		return false
	}
	params := fn.ChildByFieldName("parameters")
	if params == nil {
		return false
	}
	return int(params.NamedChildCount()) == 4
}

func nodeHandlerName(node *sitter.Node, source []byte) string {
	switch node.Type() {
	case "identifier", "member_expression":
		return node.Content(source)
	case "arrow_function", "function_expression", "function", "generator_function", "generator_function_declaration":
		return fmt.Sprintf("fn@%d", node.StartPoint().Row+1)
	default:
		content := node.Content(source)
		if len(content) > 30 {
			content = content[:30] + "..."
		}
		return content
	}
}

func normalizeNodePath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func nodeArg(argsNode *sitter.Node, i int) *sitter.Node {
	if argsNode == nil || i < 0 || i >= int(argsNode.NamedChildCount()) {
		return nil
	}
	return argsNode.NamedChild(i)
}

func nodeObjectPair(obj *sitter.Node, key string, source []byte) *sitter.Node {
	if obj == nil {
		return nil
	}
	for i := 0; i < int(obj.NamedChildCount()); i++ {
		pair := obj.NamedChild(i)
		if pair.Type() != "pair" {
			continue
		}
		k := pair.ChildByFieldName("key")
		if k == nil {
			continue
		}
		if strings.Trim(k.Content(source), `"'`) == key {
			return pair.ChildByFieldName("value")
		}
	}
	return nil
}

func nodeFirstParamName(fn *sitter.Node, source []byte) string {
	params := fn.ChildByFieldName("parameters")
	if params == nil {
		return ""
	}
	first := params.NamedChild(0)
	if first == nil {
		return ""
	}
	switch first.Type() {
	case "identifier":
		return first.Content(source)
	case "required_parameter", "optional_parameter":
		pattern := first.ChildByFieldName("pattern")
		if pattern != nil && pattern.Type() == "identifier" {
			return pattern.Content(source)
		}
	case "assignment_pattern", "object_pattern", "array_pattern":
		// destructured/default params are not receivers
		return ""
	}
	return ""
}

// nodeCollectFunctions indexes function declarations and arrow functions
// assigned to consts, so `fastify.register(pluginFn, {prefix})` can be scoped
// to the plugin body.
func nodeCollectFunctions(node *sitter.Node, source []byte, functions map[string]*sitter.Node) {
	if node == nil {
		return
	}
	switch node.Type() {
	case "function_declaration", "generator_function_declaration":
		if nameNode := node.ChildByFieldName("name"); nameNode != nil {
			functions[nameNode.Content(source)] = node
		}
	case "variable_declarator":
		nameNode := node.ChildByFieldName("name")
		valueNode := node.ChildByFieldName("value")
		if nameNode != nil && nameNode.Type() == "identifier" && valueNode != nil {
			switch valueNode.Type() {
			case "arrow_function", "function_expression":
				functions[nameNode.Content(source)] = valueNode
			}
		}
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		nodeCollectFunctions(node.NamedChild(i), source, functions)
	}
}
