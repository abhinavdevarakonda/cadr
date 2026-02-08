package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/abhinavdevarakonda/maplet/internal/graph"
)

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

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	base := filepath.Dir(exe)
	frontendPath := filepath.Join(base, "frontend")

	mux.Handle("/", http.FileServer(http.Dir(frontendPath)))

	log.Printf("Serving on http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}

