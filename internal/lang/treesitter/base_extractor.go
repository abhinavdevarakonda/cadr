package treesitter

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/abhinavdevarakonda/maplet/internal/types"
)

type BaseExtractor struct {
	config LanguageConfig
}

// creates a new extractor with the given language config
func NewBaseExtractor(config LanguageConfig) *BaseExtractor {
	return &BaseExtractor{config: config}
}

func (e *BaseExtractor) ExtractSymbols(files []string) ([]types.Symbol, error) {
	var allSymbols []types.Symbol

	parser := sitter.NewParser()
	parser.SetLanguage(e.config.Grammar())

	queryStr := e.config.SymbolQuery()
	
	query, err := sitter.NewQuery([]byte(queryStr), e.config.Grammar())
	if err != nil {
		return nil, fmt.Errorf("failed to compile symbol query: %w", err)
	}
	defer query.Close()

	for _, path := range files {
		symbols, err := e.extractSymbolsFromFile(parser, query, path)
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

	parser := sitter.NewParser()
	parser.SetLanguage(e.config.Grammar())

	query, err := sitter.NewQuery([]byte(e.config.FactQuery()), e.config.Grammar())
	if err != nil {
		return nil, fmt.Errorf("failed to compile fact query: %w", err)
	}
	defer query.Close()

	for _, path := range files {
		facts, err := e.extractFactsFromFile(parser, query, path)
		if err != nil {
			// log error but continue processing other files
			fmt.Printf("Warning: failed to extract facts from %s: %v\n", path, err)
			continue
		}
		allFacts = append(allFacts, facts...)
	}

	return allFacts, nil
}

func (e *BaseExtractor) extractSymbolsFromFile(parser *sitter.Parser, query *sitter.Query, path string) ([]types.Symbol, error) {
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
	matchCount := 0
	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}
		matchCount++

		for _, capture := range match.Captures {
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

func (e *BaseExtractor) extractFactsFromFile(parser *sitter.Parser, query *sitter.Query, path string) ([]types.Fact, error) {
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
