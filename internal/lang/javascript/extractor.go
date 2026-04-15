package javascript

import (
	"github.com/abhinavdevarakonda/maplet/internal/lang"
	"github.com/abhinavdevarakonda/maplet/internal/lang/treesitter"
	"github.com/abhinavdevarakonda/maplet/internal/types"
)

type JSLang struct{}

func init() {
	lang.Register(&JSLang{})
}

func (j *JSLang) Extensions() []string {
	return []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"}
}

func (j *JSLang) ExtractSymbols(files []string) ([]types.Symbol, error) {
	config := NewJSConfig()
	extractor := treesitter.NewBaseExtractor(config)
	return extractor.ExtractSymbols(files)
}

func (j *JSLang) ExtractFacts(files []string) ([]types.Fact, error) {
	config := NewJSConfig()
	extractor := treesitter.NewBaseExtractor(config)
	return extractor.ExtractFacts(files)
}
