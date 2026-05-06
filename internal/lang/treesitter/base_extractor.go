package treesitter

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/abhinavdevarakonda/cadastre/internal/types"
)

type BaseExtractor struct {
	config LanguageConfig
	language *sitter.Language
}

// creates a new extractor with the given language config
func NewBaseExtractor(config LanguageConfig) *BaseExtractor {
	return &BaseExtractor{
		config: config,
		language: config.Grammar(),
	}
}

func (e *BaseExtractor) ExtractSymbols(files []string) ([]types.Symbol, error) {
	var allSymbols []types.Symbol

	queryStr := e.config.SymbolQuery()
	
	query, err := sitter.NewQuery([]byte(queryStr), e.language)
	if err != nil {
		return nil, fmt.Errorf("failed to compile symbol query: %w", err)
	}
	defer query.Close()

	for _, path := range files {
		symbols, err := e.extractSymbolsFromFile(query, path)
		if err != nil {
			// log error but continue processing other files
			fmt.Printf("Warning: failed to extract symbols from %s: %v\n", path, err)
			continue
		}
		allSymbols = append(allSymbols, symbols...)
	}

	return allSymbols, nil
}

func (e *BaseExtractor) ExtractFacts(files []string) ([]types.Fact, error) {
	var allFacts []types.Fact

	query, err := sitter.NewQuery([]byte(e.config.FactQuery()), e.language)
	if err != nil {
		return nil, fmt.Errorf("failed to compile fact query: %w", err)
	}
	defer query.Close()

	for _, path := range files {
		facts, err := e.extractFactsFromFile(query, path)
		if err != nil {
			// log error but continue processing other files
			fmt.Printf("Warning: failed to extract facts from %s: %v\n", path, err)
			continue
		}
		allFacts = append(allFacts, facts...)
	}

	return allFacts, nil
}

func (e *BaseExtractor) extractSymbolsFromFile(query *sitter.Query, path string) ([]types.Symbol, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(e.language)

	source, err := readFile(path)
	if err != nil {
		return nil, err
	}

	tree, err := parser.ParseCtx(context.Background(), nil, source)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	defer tree.Close()
	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(query, tree.RootNode())
	var symbols []types.Symbol

	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}

		for _, capture := range match.Captures {
			name := query.CaptureNameForId(capture.Index)
			if name != "function" {
				continue
			}

			symbol, err := e.config.NodeToSymbol(capture.Node, source, path)
			if err != nil {
				continue
			}
			if symbol != nil {
				symbols = append(symbols, *symbol)
			}
		}
	}

	return symbols, nil
}


func (e *BaseExtractor) extractFactsFromFile(query *sitter.Query, path string) ([]types.Fact, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(e.language)

	source, err := readFile(path)
	if err != nil {
		return nil, err
	}

	tree, err := parser.ParseCtx(context.Background(), nil, source)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	defer tree.Close()
	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(query, tree.RootNode())
	var facts []types.Fact

	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}

		for _, capture := range match.Captures {
			name := query.CaptureNameForId(capture.Index)
			if name != "call" {
				continue
			}

			fact, err := e.config.NodeToFact(capture.Node, source, path)
			if err != nil {
				continue
			}
			if fact != nil {
				facts = append(facts, *fact)
			}
		}
	}

	return facts, nil
}

