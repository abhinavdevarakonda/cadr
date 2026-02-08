package treesitter

import (
	"os"
)

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func GetNodeText(node interface{ Content(source []byte) string }, source []byte) string {
	return node.Content(source)
}
