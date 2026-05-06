package python

import (
	"github.com/abhinavdevarakonda/cadastre/internal/lang"
	"github.com/abhinavdevarakonda/cadastre/internal/lang/treesitter"
	"github.com/abhinavdevarakonda/cadastre/internal/types"
)

type PythonLang struct{}

func init() {
	lang.Register(&PythonLang{})
}

func (p *PythonLang) Extensions() []string {
	return []string{".py"}
}

func (p *PythonLang) ExtractSymbols(files []string) ([]types.Symbol, error) {
	config := NewPythonConfig()
	extractor := treesitter.NewBaseExtractor(config)
	return extractor.ExtractSymbols(files)
}

func (p *PythonLang) ExtractFacts(files []string) ([]types.Fact, error) {
	config := NewPythonConfig()
	extractor := treesitter.NewBaseExtractor(config)
	return extractor.ExtractFacts(files)
}
