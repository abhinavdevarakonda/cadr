package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/maplet/internal/graph"
	"github.com/abhinavdevarakonda/maplet/internal/lang"
	"github.com/abhinavdevarakonda/maplet/internal/types"
)

type Result struct {
	Graph *graph.Graph
}

type ScanResult struct {
	Root		string
	Files		[]string
	Directories	[]string
}

func Analyze(root string) Result {
	scan, err := Scan(root)
	if err != nil {
		panic(fmt.Sprintf("failed scan: %v", err))
	}

	langs := lang.All()
	fmt.Println("registered languages:", len(langs))

	var symbols []types.Symbol
	var facts []types.Fact

	for _, l := range langs {
		files := filterByExtension(scan.Files, l.Extensions())
		fmt.Println("language extensions:", l.Extensions(), "files:", len(files))

		extractedSymbols, _ := l.ExtractSymbols(files)
		extractedFacts, _ := l.ExtractFacts(files) 

		fmt.Println("symbols:", len(extractedSymbols), "facts:", len(extractedFacts))

		symbols = append(symbols, extractedSymbols...)
		facts = append(facts, extractedFacts...)
	}

	fmt.Println("total symbols:", len(symbols), "total facts:", len(facts))
	return Build(scan, symbols, facts)
}

func Scan(root string) (*ScanResult, error) {
	res := &ScanResult{Root: root}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
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

	for _, dir := range scan.Directories {
		g.AddNode(&graph.Node{ID: dir, Type: graph.DirectoryNode, Name: filepath.Base(dir), Path: dir})
		if dir != scan.Root {
			g.AddEdge(filepath.Dir(dir), dir, graph.ContainsEdge)
		}
	}

	for _, file := range scan.Files {
		g.AddNode(&graph.Node{ID: file, Type: graph.FileNode, Name: filepath.Base(file), Path: file})
		g.AddEdge(filepath.Dir(file), file, graph.ContainsEdge)
	}

	for _, sym := range symbols {
		g.AddNode(&graph.Node{ID: sym.ID, Type: graph.FunctionNode, Name: sym.Name, Path: sym.Path})
		g.AddEdge(sym.Path, sym.ID, graph.ContainsEdge)
	}

	for _, fact := range facts {
		caller := findCaller(fact, symbols)
		callee := findCallee(fact, symbols)
		if caller != "" && callee != "" {
			g.AddEdge(caller, callee, graph.CallsEdge)
		}
	}

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
	for _, sym := range symbols {
		if sym.Name != f.CalleeName {
			continue
		}

		if f.CalleeQualifier != "" && !strings.Contains(sym.ID, f.CalleeQualifier) {
			continue
		}

		return sym.ID
	}
	return ""
}


func filterByExtension(files []string, exts []string) []string {
	var out []string
	for _, f :=range files {
		for _, e := range exts {
			if strings.HasSuffix(f, e) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}
