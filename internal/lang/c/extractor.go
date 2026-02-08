package c

import (
	"github.com/abhinavdevarakonda/maplet/internal/lang"
	"github.com/abhinavdevarakonda/maplet/internal/lang/treesitter"
	"github.com/abhinavdevarakonda/maplet/internal/types"
)

type CLang struct{}

func init() {
	lang.Register(&CLang{})
}

func (c *CLang) Extensions() []string {
	return []string{".c", ".h"}
}

func (c *CLang) ExtractSymbols(files []string) ([]types.Symbol, error) {
	config := NewCConfig()
	extractor := treesitter.NewBaseExtractor(config)
	return extractor.ExtractSymbols(files)
}

func (c *CLang) ExtractFacts(files []string) ([]types.Fact, error) {
	config := NewCConfig()
	extractor := treesitter.NewBaseExtractor(config)
	return extractor.ExtractFacts(files)
}
