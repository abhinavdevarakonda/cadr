package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"

	"github.com/abhinavdevarakonda/cadastre/internal/graph"
)

//go:embed frontend/*
var frontendAssets embed.FS

type Server struct {
	graph *graph.Graph
}

func New(graph *graph.Graph) *Server {
	return &Server{graph: graph}
}

func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.graph)
	})

	// Serve embedded frontend assets
	stripped, err := fs.Sub(frontendAssets, "frontend")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(stripped)))

	log.Printf("Serving on http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}
