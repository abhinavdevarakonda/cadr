package analyzer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/lang"
	"github.com/abhinavdevarakonda/cadr/internal/types"
	ignore "github.com/sabhiram/go-gitignore"
)

var ErrScanLimitExceeded = errors.New("cadr: aborted scanning — scanned more than 50,000 source files. Please configure ignores in .cadr/ignore or .gitignore")

type Result struct {
	Root  string
	Graph *graph.Graph
}

type ScanResult struct {
	Root        string
	Files       []string
	Directories []string
}

func Analyze(root string) Result {
	scan, err := Scan(root)
	if err != nil {
		if errors.Is(err, ErrScanLimitExceeded) {
			fmt.Fprintln(os.Stderr, err.Error())
			return Result{Root: root, Graph: graph.New()}
		}
		panic(fmt.Sprintf("failed scan: %v", err))
	}

	langs := lang.All()

	var totalSrc int
	for _, l := range langs {
		totalSrc += len(filterByExtension(scan.Files, l.Extensions()))
	}
	if totalSrc > 50000 {
		fmt.Fprintf(os.Stderr, "cadr: skipping analysis — %d source files is too many (limit: 50000). Set up a .gitignore or use a project-specific directory.\n", totalSrc)
		return Result{Root: root, Graph: graph.New()}
	}
	if totalSrc > 5000 {
		fmt.Fprintf(os.Stderr, "cadr: warning — analyzing %d source files, this may use a lot of memory\n", totalSrc)
	}

	var symbols []types.Symbol
	var facts []types.Fact

	for _, l := range langs {
		files := filterByExtension(scan.Files, l.Extensions())

		extractedSymbols, _ := l.ExtractSymbols(files)
		extractedFacts, _ := l.ExtractFacts(files)

		symbols = append(symbols, extractedSymbols...)
		facts = append(facts, extractedFacts...)
	}

	// fmt.Println("total symbols:", len(symbols), "total facts:", len(facts))
	res := Build(scan, symbols, facts)
	res.Root = root
	return res
}

func Scan(root string) (*ScanResult, error) {
	res := &ScanResult{Root: root}

	var ign *ignore.GitIgnore
	ignPath := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(ignPath); err == nil {
		ign, _ = ignore.CompileIgnoreFile(ignPath)
	}

	var cadrIgn *ignore.GitIgnore
	cadrIgnPath := filepath.Join(root, ".cadr", "ignore")
	if _, err := os.Stat(cadrIgnPath); err == nil {
		cadrIgn, _ = ignore.CompileIgnoreFile(cadrIgnPath)
	}

	langs := lang.All()
	extsMap := make(map[string]bool)
	for _, l := range langs {
		for _, ext := range l.Extensions() {
			extsMap[ext] = true
		}
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		if (ign != nil && ign.MatchesPath(rel)) || (cadrIgn != nil && cadrIgn.MatchesPath(rel)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "bower_components" || name == "vendor" ||
				name == "venv" || name == ".venv" || name == "env" || name == ".env" || name == "virtualenv" ||
				name == "__pycache__" || name == ".tox" || name == ".pytest_cache" || name == ".mypy_cache" ||
				name == "dist" || name == "build" || name == "out" || name == "target" || name == "bin" || name == "obj" ||
				name == ".next" || name == ".nuxt" || name == ".svelte-kit" || name == ".docusaurus" || name == ".vuepress" ||
				name == ".cache" || name == "tmp" || name == "temp" || name == ".temp" ||
				name == "coverage" || name == ".nyc_output" ||
				name == ".idea" || name == ".vscode" || name == ".settings" || name == ".project" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if extsMap[ext] {
			res.Files = append(res.Files, path)
			if len(res.Files) > 50000 {
				return ErrScanLimitExceeded
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Populate res.Directories dynamically based on the parent paths of res.Files
	dirsMap := make(map[string]bool)
	for _, file := range res.Files {
		dir := filepath.Dir(file)
		for dir != root && dir != "." && dir != "/" && dir != "" {
			if dirsMap[dir] {
				break
			}
			dirsMap[dir] = true
			dir = filepath.Dir(dir)
		}
	}
	for dir := range dirsMap {
		res.Directories = append(res.Directories, dir)
	}

	return res, nil
}

func Build(scan *ScanResult, symbols []types.Symbol, facts []types.Fact) Result {
	g := graph.New()

	// all paths inside the graph must be relative to project root
	normalize := func(p string) string {
		rel, err := filepath.Rel(scan.Root, p)
		if err != nil {
			return p
		}
		if rel == "" {
			return "."
		}
		return rel
	}

	// directories
	for _, dir := range scan.Directories {
		d := normalize(dir)

		g.AddNode(&graph.Node{
			ID:   d,
			Type: graph.DirectoryNode,
			Name: filepath.Base(d),
			Path: d,
		})

		if d != "." {
			g.AddEdge(filepath.Dir(d), d, graph.ContainsEdge, 0)
		}
	}

	// files
	for _, file := range scan.Files {
		f := normalize(file)

		g.AddNode(&graph.Node{
			ID:   f,
			Type: graph.FileNode,
			Name: filepath.Base(f),
			Path: f,
		})

		g.AddEdge(filepath.Dir(f), f, graph.ContainsEdge, 0)
	}

	// functions (symbols)
	for _, sym := range symbols {
		p := normalize(sym.Path)

		g.AddNode(&graph.Node{
			ID:      sym.ID,
			Type:    graph.FunctionNode,
			Name:    sym.Name,
			Path:    p,
			Line:    sym.StartLine,
			EndLine: sym.EndLine,
		})

		g.AddEdge(p, sym.ID, graph.ContainsEdge, 0)
	}

	for _, fact := range facts {
		caller := findCaller(fact, symbols)
		callee := findCallee(fact, symbols)
		if caller != "" && callee != "" {
			g.AddEdge(caller, callee, graph.CallsEdge, fact.Line)
		}
	}

	g.BuildIndex()

	return Result{Graph: g}
}

func findCaller(f types.Fact, symbols []types.Symbol) string {
	for _, sym := range symbols {
		if sym.Path != f.Path {
			continue
		}

		if f.StartLine >= sym.StartLine && f.EndLine <= sym.EndLine {
			return sym.ID
		}
	}
	return ""
}

func findCallee(f types.Fact, symbols []types.Symbol) string {
	var candidates []types.Symbol

	// first pass, collect all matching names
	for _, sym := range symbols {
		if sym.Name != f.CalleeName {
			continue
		}

		if f.CalleeQualifier != "" && !strings.Contains(sym.ID, f.CalleeQualifier) {
			continue
		}

		candidates = append(candidates, sym)
	}

	if len(candidates) == 0 {
		return ""
	}

	// if only one candidate, return it
	if len(candidates) == 1 {
		return candidates[0].ID
	}

	// prefer symbols from same package/dir as caller
	// handles same package calls better
	callerDir := filepath.Dir(f.Path)
	for _, sym := range candidates {
		if filepath.Dir(sym.Path) == callerDir {
			return sym.ID
		}
	}

	// fallback, return first match
	return candidates[0].ID
}

func filterByExtension(files []string, exts []string) []string {
	var out []string
	for _, f := range files {
		for _, e := range exts {
			if strings.HasSuffix(f, e) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}
