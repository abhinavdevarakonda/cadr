package main

import (
	"fmt"
	"github.com/abhinavdevarakonda/maplet/internal/tracer"
)

func alpha() {
	defer tracer.Hit()()
	fmt.Println("Inside alpha")
	beta()
}

func beta() {
	defer tracer.Hit()()
	fmt.Println("Inside beta")
}

func main() {
	defer tracer.Hit()()
	fmt.Println("Go app starting...")
	alpha()
	beta()
}
