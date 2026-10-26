package frameworks

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

const (
	nodeModuleDepthLimit  = 4
	nodeConstFixpointPass = 8
)

type nodeBinding struct {
	module string // resolved module file ("" when the specifier is not local)
	key    string // exported name; "" means the whole module (namespace/default import)
}

type nodeRawBinding struct {
	spec string
	key  string
}

type nodeReexport struct {
	module string
	key    string
}

type nodeModule struct {
	path   string
	source []byte
	root   *sitter.Node

	consts map[string]string
	binds  map[string]nodeBinding

	rawBinds map[string]nodeRawBinding

	exports       map[string]*sitter.Node
	reexports     map[string]nodeReexport
	defaultExport *sitter.Node
	aliasModule   string
	starExports   []string
}

// nodeIndex resolves JavaScript/TypeScript values across files: module-local
// constants first, then imported/required exports, then a project-wide unique
// value declaration.
type nodeIndex struct {
	files  map[string]*nodeModule
	byName map[string][]nodeDeclRef

	resolving map[string]bool
	building  bool
}

type nodeDeclRef struct {
	module *nodeModule
	node   *sitter.Node
}

func cleanNodePath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func newNodeIndex(files []string) *nodeIndex {
	idx := &nodeIndex{
		files:     make(map[string]*nodeModule),
		byName:    make(map[string][]nodeDeclRef),
		resolving: make(map[string]bool),
		building:  true,
	}

	for _, file := range files {
		language := nodeLanguageForFile(file)
		if language == nil {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		parser := sitter.NewParser()
		parser.SetLanguage(language)
		tree, err := parser.ParseCtx(context.Background(), nil, source)
		if err != nil || tree == nil {
			continue
		}
		root := tree.RootNode()
		if root == nil {
			continue
		}

		module := &nodeModule{
			path:      cleanNodePath(file),
			source:    source,
			root:      root,
			consts:    make(map[string]string),
			binds:     make(map[string]nodeBinding),
			rawBinds:  make(map[string]nodeRawBinding),
			exports:   make(map[string]*sitter.Node),
			reexports: make(map[string]nodeReexport),
		}
		module.collect(idx)
		idx.files[module.path] = module
	}

	// Module specifiers can only be resolved once every file is known.
	for _, module := range idx.files {
		for name, raw := range module.rawBinds {
			target, ok := idx.resolveModule(module.path, raw.spec)
			if !ok {
				target = ""
			}
			module.binds[name] = nodeBinding{module: target, key: raw.key}
		}
		for key, raw := range module.reexports {
			target, ok := idx.resolveModule(module.path, raw.module)
			if !ok {
				target = ""
			}
			module.reexports[key] = nodeReexport{module: target, key: raw.key}
		}
		if module.aliasModule != "" {
			if target, ok := idx.resolveModule(module.path, module.aliasModule); ok {
				module.aliasModule = target
			} else {
				module.aliasModule = ""
			}
		}
		var stars []string
		for _, spec := range module.starExports {
			if target, ok := idx.resolveModule(module.path, spec); ok {
				stars = append(stars, target)
			}
		}
		module.starExports = stars
	}

	idx.fixpointConsts()
	idx.building = false
	return idx
}

// fixpointConsts resolves `const A = './x'; const B = A + '/y'` regardless of
// declaration order, including constants whose values come from imports.
func (idx *nodeIndex) fixpointConsts() {
	for pass := 0; pass < nodeConstFixpointPass; pass++ {
		changed := false
		for _, module := range idx.files {
			evaluator := module.evaluator(idx)
			nodeWalk(module.root, func(node *sitter.Node) {
				if node.Type() != "variable_declarator" {
					return
				}
				name := node.ChildByFieldName("name")
				value := node.ChildByFieldName("value")
				if name == nil || value == nil || name.Type() != "identifier" {
					return
				}
				key := name.Content(module.source)
				resolved, ok := evaluator.value(value)
				if !ok || resolved == "" {
					return
				}
				if module.consts[key] != resolved {
					module.consts[key] = resolved
					changed = true
				}
			})
		}
		if !changed {
			return
		}
	}
}

func (m *nodeModule) evaluator(idx *nodeIndex) *nodeEval {
	return &nodeEval{module: m, source: m.source, index: idx}
}

// resolveModule maps a relative import/require specifier onto a scanned file.
func (idx *nodeIndex) resolveModule(fromFile, spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || !(strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")) {
		return "", false
	}

	base := cleanNodePath(filepath.Join(filepath.Dir(fromFile), spec))
	candidates := []string{base}
	if ext := filepath.Ext(base); ext == "" || !nodeIndexLoopExtensions()[strings.ToLower(ext)] {
		for _, ext := range []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"} {
			candidates = append(candidates, base+ext, base+"/index"+ext)
		}
	}
	for _, candidate := range candidates {
		if module, ok := idx.files[candidate]; ok {
			return module.path, true
		}
	}

	// Suffix fallback: the scanner may hand us absolute paths while relative
	// specifiers only know the project-relative layout.
	for _, candidate := range candidates {
		for path := range idx.files {
			if path == candidate || strings.HasSuffix(path, "/"+candidate) {
				return path, true
			}
		}
	}
	return "", false
}

func nodeIndexLoopExtensions() map[string]bool {
	return map[string]bool{
		".js": true, ".jsx": true, ".ts": true, ".tsx": true,
		".mjs": true, ".cjs": true, ".mts": true, ".cts": true,
	}
}

// exportValue reads an exported value, following re-exports (depth-limited and
// cycle-guarded).
func (idx *nodeIndex) exportValue(file, key string, depth int) (string, bool) {
	if depth > nodeModuleDepthLimit || key == "" {
		return "", false
	}
	module := idx.files[cleanNodePath(file)]
	if module == nil {
		return "", false
	}

	guard := "export|" + module.path + "|" + key
	if idx.resolving[guard] {
		return "", false
	}
	idx.resolving[guard] = true
	defer delete(idx.resolving, guard)

	evaluator := module.evaluator(idx)

	if node, ok := module.exports[key]; ok {
		if value, ok := evaluator.value(node); ok {
			return value, true
		}
	}
	if reexport, ok := module.reexports[key]; ok && reexport.module != "" {
		if value, ok := idx.exportValue(reexport.module, reexport.key, depth+1); ok {
			return value, true
		}
	}
	if key == "default" && module.defaultExport != nil {
		if value, ok := evaluator.value(module.defaultExport); ok {
			return value, true
		}
	}
	if module.aliasModule != "" {
		if value, ok := idx.exportValue(module.aliasModule, key, depth+1); ok {
			return value, true
		}
	}
	for _, star := range module.starExports {
		if value, ok := idx.exportValue(star, key, depth+1); ok {
			return value, true
		}
	}
	return "", false
}

// uniqueConst is the project-wide fallback: a bare name resolves only when
// exactly one value declaration of it exists. Two differing declarations are
// an ambiguity, never a guess.
func (idx *nodeIndex) uniqueConst(name string) (string, bool) {
	decls := idx.byName[name]
	if len(decls) != 1 {
		return "", false
	}
	guard := "unique|" + name
	if idx.resolving[guard] {
		return "", false
	}
	idx.resolving[guard] = true
	defer delete(idx.resolving, guard)

	decl := decls[0]
	return decl.module.evaluator(idx).value(decl.node)
}

func (m *nodeModule) collect(idx *nodeIndex) {
	nodeWalk(m.root, func(node *sitter.Node) {
		switch node.Type() {
		case "variable_declarator":
			m.collectDeclarator(idx, node)
		case "import_statement":
			m.collectImport(node)
		case "assignment_expression":
			m.collectAssignment(node)
		case "export_statement":
			m.collectExport(node)
		}
	})
}

func (m *nodeModule) collectDeclarator(idx *nodeIndex, node *sitter.Node) {
	name := node.ChildByFieldName("name")
	value := node.ChildByFieldName("value")
	if name == nil || value == nil {
		return
	}

	if spec, ok := nodeRequireSpec(value, m.source); ok {
		switch name.Type() {
		case "identifier":
			m.rawBinds[name.Content(m.source)] = nodeRawBinding{spec: spec}
		case "object_pattern":
			m.collectDestructuredBindings(name, spec)
		}
		return
	}

	if name.Type() != "identifier" {
		return
	}
	localName := name.Content(m.source)
	idx.byName[localName] = append(idx.byName[localName], nodeDeclRef{module: m, node: value})
	if nodeIsExportedDeclarator(node) {
		m.exports[localName] = value
	}
}

func (m *nodeModule) collectDestructuredBindings(pattern *sitter.Node, spec string) {
	for i := 0; i < int(pattern.NamedChildCount()); i++ {
		child := pattern.NamedChild(i)
		switch child.Type() {
		case "shorthand_property_identifier_pattern":
			name := child.Content(m.source)
			m.rawBinds[name] = nodeRawBinding{spec: spec, key: name}
		case "pair_pattern":
			keyNode := child.ChildByFieldName("key")
			valueNode := child.ChildByFieldName("value")
			if keyNode == nil || valueNode == nil || valueNode.Type() != "identifier" {
				continue
			}
			key := strings.Trim(keyNode.Content(m.source), `"'`)
			m.rawBinds[valueNode.Content(m.source)] = nodeRawBinding{spec: spec, key: key}
		case "object_assignment_pattern":
			left := child.ChildByFieldName("left")
			if left == nil || left.Type() != "identifier" {
				continue
			}
			name := left.Content(m.source)
			m.rawBinds[name] = nodeRawBinding{spec: spec, key: name}
		}
	}
}

func (m *nodeModule) collectImport(node *sitter.Node) {
	sourceNode := node.ChildByFieldName("source")
	if sourceNode == nil {
		return
	}
	spec, ok := nodeUnquoteString(sourceNode.Content(m.source))
	if !ok {
		return
	}

	// The grammar does not give import_clause a field name.
	var clause *sitter.Node
	for i := 0; i < int(node.NamedChildCount()); i++ {
		if child := node.NamedChild(i); child.Type() == "import_clause" {
			clause = child
			break
		}
	}
	if clause == nil {
		return
	}
	for i := 0; i < int(clause.NamedChildCount()); i++ {
		child := clause.NamedChild(i)
		switch child.Type() {
		case "identifier": // default import
			m.rawBinds[child.Content(m.source)] = nodeRawBinding{spec: spec, key: "default"}
		case "namespace_import":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				alias := child.NamedChild(j)
				if alias.Type() == "identifier" {
					m.rawBinds[alias.Content(m.source)] = nodeRawBinding{spec: spec}
				}
			}
		case "named_imports":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				specifier := child.NamedChild(j)
				if specifier.Type() != "import_specifier" {
					continue
				}
				imported := specifier.ChildByFieldName("name")
				local := specifier.ChildByFieldName("alias")
				if imported == nil {
					continue
				}
				key := imported.Content(m.source)
				localName := key
				if local != nil {
					localName = local.Content(m.source)
				}
				m.rawBinds[localName] = nodeRawBinding{spec: spec, key: key}
			}
		}
	}
}

func (m *nodeModule) collectAssignment(node *sitter.Node) {
	left := node.ChildByFieldName("left")
	right := node.ChildByFieldName("right")
	if left == nil || right == nil {
		return
	}

	leftText := strings.TrimSpace(left.Content(m.source))
	switch {
	case leftText == "module.exports":
		if right.Type() == "object" {
			m.collectExportsObject(right)
			return
		}
		if spec, ok := nodeRequireSpec(right, m.source); ok {
			m.aliasModule = spec
			return
		}
		m.defaultExport = right
	case leftText == "exports" || strings.HasPrefix(leftText, "module.exports.") || strings.HasPrefix(leftText, "exports."):
		key := ""
		if left.Type() == "member_expression" {
			if property := left.ChildByFieldName("property"); property != nil {
				key = strings.Trim(property.Content(m.source), `"'`)
			}
		}
		if key != "" {
			m.exports[key] = right
		}
	}
}

func (m *nodeModule) collectExportsObject(obj *sitter.Node) {
	for i := 0; i < int(obj.NamedChildCount()); i++ {
		child := obj.NamedChild(i)
		switch child.Type() {
		case "pair":
			keyNode := child.ChildByFieldName("key")
			valueNode := child.ChildByFieldName("value")
			if keyNode == nil || valueNode == nil {
				continue
			}
			m.exports[strings.Trim(keyNode.Content(m.source), `"'`)] = valueNode
		case "shorthand_property_identifier", "shorthand_property_identifier_pattern":
			name := child.Content(m.source)
			m.exports[name] = child
		}
	}
}

func (m *nodeModule) collectExport(node *sitter.Node) {
	sourceNode := node.ChildByFieldName("source")
	spec := ""
	if sourceNode != nil {
		spec, _ = nodeUnquoteString(sourceNode.Content(m.source))
	}

	if value := node.ChildByFieldName("value"); value != nil {
		m.defaultExport = value
	}

	// export { A, B as C } [from './m'] / export * from './m'
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "export_clause":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				specifier := child.NamedChild(j)
				if specifier.Type() != "export_specifier" {
					continue
				}
				imported := specifier.ChildByFieldName("name")
				local := specifier.ChildByFieldName("alias")
				if imported == nil {
					continue
				}
				exported := imported.Content(m.source)
				if local != nil {
					exported = local.Content(m.source)
				}
				if spec != "" {
					m.reexports[exported] = nodeReexport{module: spec, key: imported.Content(m.source)}
				} else {
					m.exports[exported] = imported
				}
			}
		case "namespace_export":
			if spec != "" {
				m.starExports = append(m.starExports, spec)
			}
		}
	}
}

// nodeRequireSpec reports the module specifier of a `require('./m')` call.
func nodeRequireSpec(node *sitter.Node, source []byte) (string, bool) {
	if node == nil || node.Type() != "call_expression" {
		return "", false
	}
	function := node.ChildByFieldName("function")
	if function == nil || function.Type() != "identifier" || function.Content(source) != "require" {
		return "", false
	}
	arguments := node.ChildByFieldName("arguments")
	if arguments == nil || arguments.NamedChildCount() == 0 {
		return "", false
	}
	return nodeUnquoteString(arguments.NamedChild(0).Content(source))
}

func nodeIsExportedDeclarator(node *sitter.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Type() {
		case "export_statement":
			return true
		case "program", "statement_block":
			return false
		}
	}
	return false
}

func nodeWalk(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := 0; i < int(node.NamedChildCount()); i++ {
		nodeWalk(node.NamedChild(i), fn)
	}
}
