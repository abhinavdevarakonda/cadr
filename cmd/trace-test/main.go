package main

import (
	"fmt"
	"os"
	"github.com/abhinavdevarakonda/maplet/internal/tracer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: trace-test <python_file>")
		return
	}

	fmt.Println("--- STARTING RECORDING ---")
	rec, err := tracer.RecordPython(os.Args[1], []string{})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("--- FINISHED RECORDING: %d events captured\n", len(rec.Events))
}
