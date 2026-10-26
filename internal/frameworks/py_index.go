package frameworks

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tree_sitter_python "github.com/smacker/go-tree-sitter/python"
)

const pyConstFixpointPasses = 8

type pyModule struct {
	path   string
	source []byte
	root   *sitter.Node
}

type pyBinding struct {
	module string // resolved module file
	key    string // name imported from that module; "" means the whole module
}

type pyDeclRef struct {
	module *pyModule
	node   *sitter.Node
}

// pyIndex resolves Python module-level constants through imports
// (`from .settings import API_PREFIX`, `import app.cfg as cfg`).
type pyIndex struct {
	files      map[string]*pyModule
	consts     map[string]map[string]string // file -> name -> value
	binds      map[string]map[string]pyBinding
	byName     map[string][]pyDeclRef
	fieldDecls map[string][]pyDeclRef // class/dataclass field name -> defaults

	resolving map[string]bool
	building  bool
}

func newPyIndex(files []string) *pyIndex {
	idx := &pyIndex{
		files:      make(map[string]*pyModule),
		consts:     make(map[string]map[string]string),
		binds:      make(map[string]map[string]pyBinding),
		byName:     make(map[string][]pyDeclRef),
		fieldDecls: make(map[string][]pyDeclRef),
		resolving:  make(map[string]bool),
		building:   true,
	}

	for _, file := range files {
		if !strings.HasSuffix(strings.ToLower(file), ".py") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		parser := sitter.NewParser()
		parser.SetLanguage(tree_sitter_python.GetLanguage())
		tree, err := parser.ParseCtx(context.Background(), nil, source)
		if err != nil || tree == nil {
			continue
		}
		root := tree.RootNode()
		if root == nil {
			continue
		}
		module := &pyModule{path: cleanNodePath(file), source: source, root: root}
		idx.files[module.path] = module
		idx.consts[module.path] = make(map[string]string)
	}

	for _, module := range idx.files {
		idx.collectModule(module)
	}
	idx.fixpoint()
	idx.building = false
	return idx
}

func (idx *pyIndex) fixpoint() {
	for pass := 0; pass < pyConstFixpointPasses; pass++ {
		changed := false
		for _, module := range idx.files {
			evaluator := idx.evaluator(module)
			nodeWalk(module.root, func(node *sitter.Node) {
				if node.Type() != "assignment" || !pyIsModuleLevel(node) {
					return
				}
				left := node.ChildByFieldName("left")
				right := node.ChildByFieldName("right")
				if left == nil || right == nil || left.Type() != "identifier" {
					return
				}
				name := left.Content(module.source)
				value, ok := evaluator.value(right)
				if !ok || value == "" {
					return
				}
				if idx.consts[module.path][name] != value {
					idx.consts[module.path][name] = value
					changed = true
				}
			})
		}
		if !changed {
			return
		}
	}
}

func (idx *pyIndex) collectModule(module *pyModule) {
	nodeWalk(module.root, func(node *sitter.Node) {
		switch node.Type() {
		case "assignment":
			left := node.ChildByFieldName("left")
			right := node.ChildByFieldName("right")
			if left == nil || right == nil || left.Type() != "identifier" {
				return
			}
			name := left.Content(module.source)
			if pyIsClassLevel(node) {
				// `api_base: str = DEFAULT_API_BASE` in a dataclass body: the
				// default of a config field.
				idx.fieldDecls[name] = append(idx.fieldDecls[name], pyDeclRef{module: module, node: right})
				return
			}
			if !pyIsModuleLevel(node) {
				return
			}
			idx.byName[name] = append(idx.byName[name], pyDeclRef{module: module, node: right})
		case "import_from_statement":
			idx.collectFromImport(module, node)
		case "import_statement":
			idx.collectImport(module, node)
		}
	})
}

func (idx *pyIndex) collectFromImport(module *pyModule, node *sitter.Node) {
	moduleNode := node.ChildByFieldName("module_name")
	moduleName := ""
	if moduleNode != nil {
		moduleName = moduleNode.Content(module.source)
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		var imported, alias string
		switch child.Type() {
		case "dotted_name":
			imported = child.Content(module.source)
		case "aliased_import":
			nameNode := child.ChildByFieldName("name")
			aliasNode := child.ChildByFieldName("alias")
			if nameNode == nil || aliasNode == nil {
				continue
			}
			imported = nameNode.Content(module.source)
			alias = aliasNode.Content(module.source)
		default:
			continue
		}

		local := imported
		if alias != "" {
			local = alias
		}
		if imported == "*" {
			continue
		}

		// `from app import settings` may mean the module app.settings or the
		// attribute settings of module app; prefer the module interpretation.
		if alias == "" && moduleName != "" {
			spec := strings.TrimSuffix(moduleName, ".") + "." + imported
			if target, ok := idx.resolveModule(module.path, spec); ok {
				idx.addBinding(module, local, pyBinding{module: target})
				continue
			}
		}

		if target, ok := idx.resolveModule(module.path, moduleName); ok {
			idx.addBinding(module, local, pyBinding{module: target, key: imported})
		}
	}
}

func (idx *pyIndex) collectImport(module *pyModule, node *sitter.Node) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		var spec, alias string
		switch child.Type() {
		case "dotted_name":
			spec = child.Content(module.source)
		case "aliased_import":
			nameNode := child.ChildByFieldName("name")
			aliasNode := child.ChildByFieldName("alias")
			if nameNode == nil || aliasNode == nil {
				continue
			}
			spec = nameNode.Content(module.source)
			alias = aliasNode.Content(module.source)
		default:
			continue
		}
		if !strings.Contains(spec, ".") {
			if alias == "" {
				alias = spec
			}
		} else if alias == "" {
			// `import app.settings` binds `app`; nested attributes are out of scope.
			continue
		}
		target, ok := idx.resolveModule(module.path, spec)
		if !ok {
			continue
		}
		idx.addBinding(module, alias, pyBinding{module: target})
	}
}

func (idx *pyIndex) addBinding(module *pyModule, name string, binding pyBinding) {
	if name == "" || binding.module == "" {
		return
	}
	if idx.binds[module.path] == nil {
		idx.binds[module.path] = make(map[string]pyBinding)
	}
	idx.binds[module.path][name] = binding
}

func pyIsModuleLevel(node *sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}
	if parent.Type() == "module" {
		return true
	}
	return parent.Type() == "expression_statement" && parent.Parent() != nil && parent.Parent().Type() == "module"
}

// pyIsClassLevel reports whether an assignment sits directly in a class body
// (a dataclass field default) rather than at module or function level.
func pyIsClassLevel(node *sitter.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Type() {
		case "class_definition":
			return true
		case "module", "function_definition", "lambda":
			return false
		}
	}
	return false
}

// fieldDefault returns a class field's default when every statically-known
// declaration agrees; disagreement (or nothing known) leaves the caller's
// fillable placeholder in place.
func (idx *pyIndex) fieldDefault(field string) (string, bool) {
	decls := idx.fieldDecls[field]
	if len(decls) == 0 {
		return "", false
	}
	guard := "pyfield|" + field
	if idx.resolving[guard] {
		return "", false
	}
	idx.resolving[guard] = true
	defer delete(idx.resolving, guard)

	value := ""
	found := false
	for _, decl := range decls {
		resolved, ok := idx.evaluator(decl.module).value(decl.node)
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

// resolveModule maps a Python module reference (absolute or relative dots) onto
// a scanned file.
func (idx *pyIndex) resolveModule(fromFile, spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", false
	}

	level := 0
	for level < len(spec) && spec[level] == '.' {
		level++
	}
	rest := spec[level:]
	var parts []string
	if rest != "" {
		parts = strings.Split(rest, ".")
	}

	if level > 0 {
		base := filepath.Dir(fromFile)
		for i := 1; i < level; i++ {
			parent := filepath.Dir(base)
			if parent == base {
				break
			}
			base = parent
		}
		target := cleanNodePath(filepath.Join(append([]string{base}, parts...)...))
		for _, candidate := range []string{target + ".py", target + "/__init__.py"} {
			if module, ok := idx.files[candidate]; ok {
				return module.path, true
			}
			// Suffix fallback: the scanner may hand us absolute paths while a
			// project-relative spec only knows the relative layout.
			for path := range idx.files {
				if strings.HasSuffix(path, "/"+candidate) {
					return path, true
				}
			}
		}
	}

	rel := strings.Join(parts, "/")
	if rel == "" {
		return "", false
	}
	for path := range idx.files {
		if path == rel+".py" || strings.HasSuffix(path, "/"+rel+".py") ||
			path == rel+"/__init__.py" || strings.HasSuffix(path, "/"+rel+"/__init__.py") {
			return path, true
		}
	}
	return "", false
}

func (idx *pyIndex) evaluator(module *pyModule) *pyEval {
	return &pyEval{module: module, source: module.source, index: idx}
}

// bindingValue reads a module-level name from another module.
func (idx *pyIndex) bindingValue(file, name string, depth int) (string, bool) {
	if depth > nodeModuleDepthLimit {
		return "", false
	}
	module := idx.files[cleanNodePath(file)]
	if module == nil || name == "" {
		return "", false
	}
	guard := "py-export|" + module.path + "|" + name
	if idx.resolving[guard] {
		return "", false
	}
	idx.resolving[guard] = true
	defer delete(idx.resolving, guard)

	if value, ok := idx.consts[module.path][name]; ok {
		return value, true
	}
	evaluator := idx.evaluator(module)
	for _, decl := range idx.byName[name] {
		if decl.module == module {
			return evaluator.value(decl.node)
		}
	}
	return "", false
}

// uniquePyConst is the project-wide fallback: exactly one module-level
// declaration of the name, otherwise ambiguous.
func (idx *pyIndex) uniquePyConst(name string) (string, bool) {
	decls := idx.byName[name]
	if len(decls) != 1 {
		return "", false
	}
	guard := "py-unique|" + name
	if idx.resolving[guard] {
		return "", false
	}
	idx.resolving[guard] = true
	defer delete(idx.resolving, guard)

	return idx.evaluator(decls[0].module).value(decls[0].node)
}

// pyEval resolves a Python expression in one module's scope.
type pyEval struct {
	module *pyModule
	source []byte
	index  *pyIndex
}

func (e *pyEval) value(node *sitter.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Type() {
	case "string":
		return e.stringValue(node)
	case "concatenated_string":
		var builder strings.Builder
		for i := 0; i < int(node.NamedChildCount()); i++ {
			value, ok := e.stringValue(node.NamedChild(i))
			if !ok {
				return "", false
			}
			builder.WriteString(value)
		}
		return builder.String(), true
	case "identifier":
		return e.identifier(node.Content(e.source))
	case "attribute":
		return e.attribute(node)
	case "binary_operator":
		return e.binary(node)
	case "call":
		return e.call(node)
	case "parenthesized_expression":
		if node.NamedChildCount() > 0 {
			return e.value(node.NamedChild(0))
		}
	}
	return "", false
}

func (e *pyEval) identifier(name string) (string, bool) {
	if e.module != nil {
		if value, ok := e.index.consts[e.module.path][name]; ok {
			return value, true
		}
		if binding, ok := e.index.binds[e.module.path][name]; ok && binding.module != "" {
			if binding.key == "" {
				return "", false
			}
			return e.index.bindingValue(binding.module, binding.key, 1)
		}
	}
	if e.index != nil && !e.index.building {
		return e.index.uniquePyConst(name)
	}
	return "", false
}

func (e *pyEval) attribute(node *sitter.Node) (string, bool) {
	object := node.ChildByFieldName("object")
	attribute := node.ChildByFieldName("attribute")
	if object == nil || attribute == nil || e.module == nil {
		return "", false
	}
	name := attribute.Content(e.source)
	if object.Type() == "identifier" {
		// A module alias (`cfg.API_PREFIX`): read the name out of that module.
		if binding, ok := e.index.binds[e.module.path][object.Content(e.source)]; ok && binding.module != "" && binding.key == "" {
			if value, ok := e.index.bindingValue(binding.module, name, 1); ok {
				return value, true
			}
		}
	}
	// Anything else (`settings.api_base` where settings is a parameter): fall
	// back to the class field's statically-known default.
	return e.index.fieldDefault(name)
}

func (e *pyEval) binary(node *sitter.Node) (string, bool) {
	operator := "+"
	if op := node.ChildByFieldName("operator"); op != nil {
		operator = op.Content(e.source)
	}
	if operator != "+" {
		return "", false
	}
	leftNode := node.ChildByFieldName("left")
	rightNode := node.ChildByFieldName("right")
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
}

// call handles the C analogue for Python: environment variables with a
// default: os.getenv("X", "/default") / os.environ.get("X", "/default").
func (e *pyEval) call(node *sitter.Node) (string, bool) {
	function := node.ChildByFieldName("function")
	arguments := node.ChildByFieldName("arguments")
	if function == nil || arguments == nil || arguments.NamedChildCount() < 2 {
		return "", false
	}
	if !pyLooksLikeEnvLookup(function.Content(e.source)) {
		return "", false
	}
	first := arguments.NamedChild(0)
	if first.Type() != "string" {
		return "", false
	}
	return e.value(arguments.NamedChild(1))
}

func pyLooksLikeEnvLookup(function string) bool {
	switch function {
	case "os.getenv", "os.environ.get", "getenv":
		return true
	}
	return false
}

// stringValue handles plain strings and f-strings (C2). Escape sequences are
// kept verbatim; anything interpolated must resolve.
func (e *pyEval) stringValue(node *sitter.Node) (string, bool) {
	if node.Type() != "string" {
		return "", false
	}
	var builder strings.Builder
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "string_content":
			builder.WriteString(child.Content(e.source))
		case "interpolation":
			expression := child.ChildByFieldName("expression")
			if expression == nil {
				return "", false
			}
			value, ok := e.value(expression)
			if !ok {
				return "", false
			}
			builder.WriteString(value)
		case "escape_sequence":
			builder.WriteString(child.Content(e.source))
		}
	}
	return builder.String(), true
}
