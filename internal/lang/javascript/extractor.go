package javascript

import (
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/lang"
	"github.com/abhinavdevarakonda/cadr/internal/lang/treesitter"
	"github.com/abhinavdevarakonda/cadr/internal/types"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

type JSLang struct{}

func init() {
	lang.Register(&JSLang{})
}

func (j *JSLang) Extensions() []string {
	return []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"}
}

func (j *JSLang) ExtractSymbols(files []string) ([]types.Symbol, error) {
	var symbols []types.Symbol
	for _, group := range groupFilesByGrammar(files) {
		extractor := treesitter.NewBaseExtractor(NewJSConfigWithGrammar(group.grammar))
		extracted, err := extractor.ExtractSymbols(group.files)
		if err != nil {
			return symbols, err
		}
		symbols = append(symbols, extracted...)
	}
	return symbols, nil
}

func (j *JSLang) ExtractFacts(files []string) ([]types.Fact, error) {
	var facts []types.Fact
	for _, group := range groupFilesByGrammar(files) {
		extractor := treesitter.NewBaseExtractor(NewJSConfigWithGrammar(group.grammar))
		extracted, err := extractor.ExtractFacts(group.files)
		if err != nil {
			return facts, err
		}
		facts = append(facts, extracted...)
	}
	return facts, nil
}

type grammarGroup struct {
	grammar *sitter.Language
	files   []string
}

// groupFilesByGrammar splits the extension list so `.ts`/`.tsx` are parsed with
// the TypeScript grammars instead of the plain JavaScript one.
func groupFilesByGrammar(files []string) []grammarGroup {
	groups := []grammarGroup{
		{grammar: javascript.GetLanguage()},
		{grammar: typescript.GetLanguage()},
		{grammar: tsx.GetLanguage()},
	}

	for _, file := range files {
		switch strings.ToLower(filepath.Ext(file)) {
		case ".ts", ".mts", ".cts":
			groups[1].files = append(groups[1].files, file)
		case ".tsx":
			groups[2].files = append(groups[2].files, file)
		default:
			groups[0].files = append(groups[0].files, file)
		}
	}

	nonEmpty := groups[:0]
	for _, group := range groups {
		if len(group.files) > 0 {
			nonEmpty = append(nonEmpty, group)
		}
	}
	return nonEmpty
}
