package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/maplet/internal/graph"
)

func Analyze(root string) Result {
	scan, err := Scan(root)
	if err != nil {
		panic(fmt.Sprintf("failed scan: %v", err))
	}

	goExt := &GoExtractor{}
	symbols, _ := goExt.ExtractSymbols(scan.Files)
	facts, _ := goExt.ExtractFacts(scan.Files)

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

func Build(scan *ScanResult, symbols []Symbol, facts []Fact) Result {
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

func findCaller(f Fact, symbols []Symbol) string {
	for _, sym := range symbols {
		if sym.Path == f.Path && f.Line >= sym.StartLine && f.Line <= sym.EndLine {
			return sym.ID
		}
	}
	return ""
}

func findCallee(f Fact, symbols []Symbol) string {
	for _, sym := range symbols {
		if sym.Name != f.CalleeName {
			continue
		}
		if filepath.Dir(sym.Path) == filepath.Dir(f.Path) {
			return sym.ID
		}
		if f.CalleeQualifier != "" && strings.Contains(sym.ID, f.CalleeQualifier) {
			return sym.ID
		}
	}
	return ""
}
