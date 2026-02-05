package golang

import (
	"github.com/abhinavdevarakonda/maplet/internal/lang"
	"github.com/abhinavdevarakonda/maplet/internal/types"
)

type GoLang struct{}

func init() {
	lang.Register(&GoLang{})
}

func (g *GoLang) Extensions() []string {
	return []string{".go"}
}

func (g *GoLang) ExtractSymbols(files []string) ([]types.Symbol, error) {
	goExt := &GoExtractor{}
	return goExt.ExtractSymbols(files)
}

func (g *GoLang) ExtractFacts(files []string) ([]types.Fact, error) {
	goExt := &GoExtractor{}
	return goExt.ExtractFacts(files)
}
