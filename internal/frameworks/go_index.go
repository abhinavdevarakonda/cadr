package frameworks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

const goConstFixpointPasses = 8

type goFile struct {
	path   string
	dir    string
	source []byte
	root   *sitter.Node
}

type goDeclRef struct {
	file *goFile
	name string
	node *sitter.Node
}

// goFieldRef is one struct-literal field value (`APIBase: DefaultAPIBase`) seen
// anywhere in the scanned project.
type goFieldRef struct {
	file *goFile
	node *sitter.Node
}

// goIndex resolves Go constants across a package (directory) and across
// packages through imports.
type goIndex struct {
	files      map[string]*goFile
	pkgConsts  map[string]map[string]string
	imports    map[string]map[string]string // file -> alias -> directory
	decls      map[string][]goDeclRef       // directory -> package-level const/var decls
	fieldDecls map[string][]goFieldRef      // struct field name -> literal values
	resolving  map[string]bool

	root       string
	modulePath string
}

func newGoIndex(files []string, root string) *goIndex {
	idx := &goIndex{
		files:      make(map[string]*goFile),
		pkgConsts:  make(map[string]map[string]string),
		imports:    make(map[string]map[string]string),
		decls:      make(map[string][]goDeclRef),
		fieldDecls: make(map[string][]goFieldRef),
		resolving:  make(map[string]bool),
	}
	if root == "" {
		root = findGoModuleRoot(files)
	}
	if root != "" {
		idx.root = cleanNodePath(root)
	}
	idx.modulePath = readGoModulePath(root)

	for _, file := range files {
		if !strings.HasSuffix(strings.ToLower(file), ".go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		parser := sitter.NewParser()
		parser.SetLanguage(golang.GetLanguage())
		tree, err := parser.ParseCtx(context.Background(), nil, source)
		if err != nil || tree == nil {
			continue
		}
		rootNode := tree.RootNode()
		if rootNode == nil {
			continue
		}

		module := &goFile{
			path:   cleanNodePath(file),
			dir:    cleanNodePath(filepath.Dir(file)),
			source: source,
			root:   rootNode,
		}
		idx.files[module.path] = module
		idx.collectImports(module)
		idx.collectPackageDecls(module)
		idx.collectFieldDecls(module)
	}

	idx.fixpoint()
	return idx
}

func (idx *goIndex) fixpoint() {
	for pass := 0; pass < goConstFixpointPasses; pass++ {
		changed := false
		for dir, decls := range idx.decls {
			consts := idx.pkgConstsFor(dir)
			for _, decl := range decls {
				evaluator := &goEval{idx: idx, file: decl.file, consts: consts}
				value, ok := evaluator.value(decl.node)
				if !ok || value == "" {
					continue
				}
				if consts[decl.name] != value {
					consts[decl.name] = value
					changed = true
				}
			}
		}
		if !changed {
			return
		}
	}
}

func (idx *goIndex) pkgConstsFor(dir string) map[string]string {
	if consts, ok := idx.pkgConsts[dir]; ok {
		return consts
	}
	consts := make(map[string]string)
	idx.pkgConsts[dir] = consts
	return consts
}

func (idx *goIndex) collectPackageDecls(module *goFile) {
	nodeWalk(module.root, func(node *sitter.Node) {
		if node.Type() != "const_spec" && node.Type() != "var_spec" {
			return
		}
		if !nodeIsTopLevelGoDecl(node) {
			return
		}
		name := node.ChildByFieldName("name")
		value := node.ChildByFieldName("value")
		if name == nil || value == nil || name.Type() != "identifier" {
			return
		}
		idx.pkgConstsFor(module.dir)
		idx.decls[module.dir] = append(idx.decls[module.dir], goDeclRef{
			file: module,
			name: name.Content(module.source),
			node: value,
		})
	})
}

// collectFieldDecls indexes struct-literal field values so a config field read
// (`cfg.APIBase`) can fall back to its statically-known default. Map literals
// also produce keyed_element nodes, but their keys are string literals, so
// requiring an identifier key keeps them out.
func (idx *goIndex) collectFieldDecls(module *goFile) {
	nodeWalk(module.root, func(node *sitter.Node) {
		if node.Type() != "keyed_element" || node.NamedChildCount() != 2 {
			return
		}
		key := node.NamedChild(0)
		if key.Type() != "literal_element" || key.NamedChildCount() != 1 {
			return
		}
		nameNode := key.NamedChild(0)
		if nameNode.Type() != "identifier" {
			return
		}
		value := node.NamedChild(1)
		// The value side is wrapped too: literal_element("DefaultAPIBase").
		for value.Type() == "literal_element" && value.NamedChildCount() == 1 {
			value = value.NamedChild(0)
		}
		idx.fieldDecls[nameNode.Content(module.source)] = append(idx.fieldDecls[nameNode.Content(module.source)], goFieldRef{
			file: module,
			node: value,
		})
	})
}

// fieldDefault returns the value of a struct field when every statically-known
// literal for that field agrees. Disagreement (or nothing known) keeps the
// caller's fillable placeholder.
func (idx *goIndex) fieldDefault(field string) (string, bool) {
	refs := idx.fieldDecls[field]
	if len(refs) == 0 {
		return "", false
	}
	guard := "gofield|" + field
	if idx.resolving[guard] {
		return "", false
	}
	idx.resolving[guard] = true
	defer delete(idx.resolving, guard)

	value := ""
	found := false
	for _, ref := range refs {
		evaluator := &goEval{idx: idx, file: ref.file, consts: idx.copyPackageConsts(ref.file.dir)}
		resolved, ok := evaluator.value(ref.node)
		if !ok || resolved == "" {
			continue
		}
		if found && resolved != value {
			return "", false
		}
		value = resolved
		found = true
	}
	if !found {
		return "", false
	}
	return value, true
}

func (idx *goIndex) collectImports(module *goFile) {
	aliases := make(map[string]string)
	nodeWalk(module.root, func(node *sitter.Node) {
		if node.Type() != "import_spec" {
			return
		}
		pathNode := node.ChildByFieldName("path")
		if pathNode == nil {
			return
		}
		importPath, ok := nodeUnquoteString(pathNode.Content(module.source))
		if !ok {
			return
		}
		dir := idx.importDir(importPath)
		if dir == "" {
			return
		}

		alias := ""
		if nameNode := node.ChildByFieldName("name"); nameNode != nil {
			alias = nameNode.Content(module.source)
		}
		if alias == "_" || alias == "." {
			return
		}
		if alias == "" {
			parts := strings.Split(strings.TrimSuffix(importPath, "/"), "/")
			alias = parts[len(parts)-1]
		}
		aliases[alias] = dir
	})
	if len(aliases) > 0 {
		idx.imports[module.path] = aliases
	}
}

// importDir maps an import path onto a directory in this project. Standard
// library and third-party imports resolve to "" (unresolved).
func (idx *goIndex) importDir(importPath string) string {
	importPath = strings.TrimSpace(importPath)
	if importPath == "" || idx.root == "" {
		return ""
	}
	if idx.modulePath != "" {
		if importPath == idx.modulePath {
			return idx.root
		}
		prefix := idx.modulePath + "/"
		if strings.HasPrefix(importPath, prefix) {
			return cleanNodePath(filepath.Join(idx.root, strings.TrimPrefix(importPath, prefix)))
		}
		return ""
	}
	// A project without go.mod can still use root-relative imports.
	first := strings.SplitN(importPath, "/", 2)[0]
	if strings.Contains(first, ".") {
		return ""
	}
	return cleanNodePath(filepath.Join(idx.root, importPath))
}

func nodeIsTopLevelGoDecl(node *sitter.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Type() {
		case "source_file":
			return true
		case "const_declaration", "var_declaration":
			continue
		default:
			return false
		}
	}
	return false
}

func findGoModuleRoot(files []string) string {
	for _, file := range files {
		if !strings.HasSuffix(strings.ToLower(file), ".go") {
			continue
		}
		dir := filepath.Dir(file)
		for depth := 0; depth < 12; depth++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

func readGoModulePath(root string) string {
	if root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// goEval resolves a Go expression in one file's scope (package consts seeded,
// imports available for cross-package selectors).
type goEval struct {
	idx    *goIndex
	file   *goFile
	consts map[string]string
}

func (idx *goIndex) evaluator(module *goFile) *goEval {
	return &goEval{idx: idx, file: module, consts: idx.copyPackageConsts(module.dir)}
}

func (idx *goIndex) copyPackageConsts(dir string) map[string]string {
	consts := make(map[string]string)
	for name, value := range idx.pkgConstsFor(dir) {
		consts[name] = value
	}
	return consts
}

// collectLocalConsts resolves function-local `:=` declarations (and re-reads
// package-level ones) with a fixpoint so declaration order does not matter.
func (e *goEval) collectLocalConsts() {
	for pass := 0; pass < 4; pass++ {
		changed := false
		nodeWalk(e.file.root, func(node *sitter.Node) {
			switch node.Type() {
			case "short_var_declaration":
				left := node.ChildByFieldName("left")
				right := node.ChildByFieldName("right")
				if left == nil || right == nil || left.NamedChildCount() != 1 || right.NamedChildCount() != 1 {
					return
				}
				nameNode := left.NamedChild(0)
				if nameNode.Type() != "identifier" {
					return
				}
				name := nameNode.Content(e.file.source)
				value, ok := e.value(right.NamedChild(0))
				if !ok || value == "" || e.consts[name] == value {
					return
				}
				e.consts[name] = value
				changed = true
			case "const_spec", "var_spec":
				name := node.ChildByFieldName("name")
				value := node.ChildByFieldName("value")
				if name == nil || value == nil || name.Type() != "identifier" {
					return
				}
				constName := name.Content(e.file.source)
				resolved, ok := e.value(value)
				if !ok || resolved == "" || e.consts[constName] == resolved {
					return
				}
				e.consts[constName] = resolved
				changed = true
			}
		})
		if !changed {
			return
		}
	}
}

func (e *goEval) value(node *sitter.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Type() {
	case "interpreted_string_literal", "raw_string_literal":
		return nodeUnquoteString(node.Content(e.file.source))
	case "identifier":
		value, ok := e.consts[node.Content(e.file.source)]
		return value, ok
	case "selector_expression":
		operand := node.ChildByFieldName("operand")
		field := node.ChildByFieldName("field")
		if operand == nil || field == nil || operand.Type() != "identifier" {
			return "", false
		}
		name := field.Content(e.file.source)
		if r, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(r) {
			// `pkg.ExportedConst` through a resolved import alias.
			if dir := e.idx.imports[e.file.path][operand.Content(e.file.source)]; dir != "" {
				if value, ok := e.idx.pkgConstsFor(dir)[name]; ok {
					return value, true
				}
			}
		}
		// `cfg.APIBase` where the value is a config parameter: fall back to the
		// struct field's statically-known default.
		return e.idx.fieldDefault(name)
	case "binary_expression":
		if op := node.ChildByFieldName("operator"); op != nil && op.Content(e.file.source) != "+" {
			return "", false
		}
		leftNode := node.ChildByFieldName("left")
		rightNode := node.ChildByFieldName("right")
		left, lok := e.value(leftNode)
		if !lok {
			left = placeholderToken(nodeContent(leftNode, e.file.source), nodeLine(leftNode))
		}
		right, rok := e.value(rightNode)
		if !rok {
			right = placeholderToken(nodeContent(rightNode, e.file.source), nodeLine(rightNode))
		}
		if left == "" && right == "" {
			return "", false
		}
		return left + right, true
	case "expression_list":
		if node.NamedChildCount() > 0 {
			return e.value(node.NamedChild(0))
		}
	case "parenthesized_expression":
		if node.NamedChildCount() > 0 {
			return e.value(node.NamedChild(0))
		}
	}
	return "", false
}
