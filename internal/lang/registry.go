package lang

import (
	"github.com/abhinavdevarakonda/cadastre/internal/types"
)

type LanguageExtractor interface {
	Extensions() []string
	ExtractSymbols(files []string) ([]types.Symbol, error)
	ExtractFacts(files []string) ([]types.Fact, error)
}

var languages []LanguageExtractor

func Register(l LanguageExtractor) {
	languages = append(languages, l)
}

func All() []LanguageExtractor {
	return languages
}

