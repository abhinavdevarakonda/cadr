package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadastre/internal/graph"
	"github.com/abhinavdevarakonda/cadastre/internal/lang"
	"github.com/abhinavdevarakonda/cadastre/internal/types"
)

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
		panic(fmt.Sprintf("failed scan: %v", err))
	}

	langs := lang.All()
	// fmt.Println("registered languages:", len(langs))

	var symbols []types.Symbol
	var facts []types.Fact

	for _, l := range langs {
		files := filterByExtension(scan.Files, l.Extensions())
		// fmt.Println("language extensions:", l.Extensions(), "files:", len(files))

		extractedSymbols, _ := l.ExtractSymbols(files)
		extractedFacts, _ := l.ExtractFacts(files)

		// fmt.Println("symbols:", len(extractedSymbols), "facts:", len(extractedFacts))

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

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" ||
				name == "venv" || name == ".venv" ||
				name == "env" || name == "__pycache__" ||
				name == ".tox" {
				return filepath.SkipDir
			}
			res.Directories = append(res.Directories, path)
			return nil
		}
		res.Files = append(res.Files, path)
		return nil
	})

	return res, err
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
