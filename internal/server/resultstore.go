package server

import (
	"sync"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
)

// ResultStore guards the shared analyzer result. `cadr mcp` answers requests
// before analysis finishes: a background goroutine fills the result while tools
// read it, and set_project_root swaps it. Every access goes through this store so
// those reads and writes cannot race.
type ResultStore struct {
	mu sync.RWMutex
	r  *analyzer.Result
}

// NewResultStore wraps r.
func NewResultStore(r *analyzer.Result) *ResultStore {
	return &ResultStore{r: r}
}

// Get returns the current result. The graph inside it is immutable once analysis
// completes, so callers may hold the pointer after the lock is released.
func (s *ResultStore) Get() *analyzer.Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.r
}

// Set replaces the current result.
func (s *ResultStore) Set(r *analyzer.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.r = r
}
