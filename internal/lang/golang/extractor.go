package golang

import (
	"github.com/abhinavdevarakonda/cadastre/internal/lang"
	"github.com/abhinavdevarakonda/cadastre/internal/lang/treesitter"
	"github.com/abhinavdevarakonda/cadastre/internal/types"
)

type GoLang struct{}

func init() {
	lang.Register(&GoLang{})
}

func (g *GoLang) Extensions() []string {
	return []string{".go"}
}

func (g *GoLang) ExtractSymbols(files []string) ([]types.Symbol, error) {
	config := NewGoConfig()
	extractor := treesitter.NewBaseExtractor(config)
	return extractor.ExtractSymbols(files)
}

func (g *GoLang) ExtractFacts(files []string) ([]types.Fact, error) {
	config := NewGoConfig()
	extractor := treesitter.NewBaseExtractor(config)
	return extractor.ExtractFacts(files)
}
