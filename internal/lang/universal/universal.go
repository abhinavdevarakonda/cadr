package universal

import (
	"os"
	"path/filepath"
)

var queriesDir = "queries"

func SetQueriesDir(dir string) {
	queriesDir = dir
}

func LoadSymbolQuery(lang string) (string, error) {
	return loadQuery(lang, "symbol")
}

func LoadFactQuery(lang string) (string, error) {
	return loadQuery(lang, "fact")
}

func loadQuery(lang, queryType string) (string, error) {
	path := filepath.Join(queriesDir, lang+".scm")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
