package graph

type NodeType string

const (
	DirectoryNode NodeType = "directory"
	FileNode      NodeType = "file"
	FunctionNode  NodeType = "function"
)

type Node struct {
	ID      string   `json:"id"`
	Type    NodeType `json:"type"`
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Line    int      `json:"line,omitempty"`
	EndLine int      `json:"end_line,omitempty"`
}

type EdgeType string

const (
	ContainsEdge EdgeType = "contains"
	CallsEdge    EdgeType = "calls"
)

type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Type EdgeType `json:"type"`
	Line int      `json:"line,omitempty"` // Line number where 'from' calls 'to'
}

type Graph struct {
	Nodes  map[string]*Node    `json:"nodes"`
	Edges  []*Edge             `json:"edges"`
	AdjOut map[string][]string `json:"-"` // AdjOut[from] = []to  (what does 'from' call?)
	AdjIn  map[string][]string `json:"-"` // AdjIn[to]   = []from (who calls 'to'?)
}

func New() *Graph {
	return &Graph{
		Nodes:  make(map[string]*Node),
		Edges:  []*Edge{},
		AdjOut: make(map[string][]string),
		AdjIn:  make(map[string][]string),
	}
}

// BuildIndex populates AdjOut and AdjIn from the Edges slice
// should only be called once after the graph is fully constructed
// only CallsEdges are indexed, ContainsEdges are skipped
func (g *Graph) BuildIndex() {
	g.AdjOut = make(map[string][]string)
	g.AdjIn = make(map[string][]string)
	for _, e := range g.Edges {
		if e.Type == CallsEdge {
			g.AdjOut[e.From] = append(g.AdjOut[e.From], e.To)
			g.AdjIn[e.To] = append(g.AdjIn[e.To], e.From)
		}
	}
}

func (g *Graph) AddNode(n *Node) {
	g.Nodes[n.ID] = n
}

func (g *Graph) AddEdge(from, to string, t EdgeType, line int) {
	g.Edges = append(g.Edges, &Edge{
		From: from,
		To:   to,
		Type: t,
		Line: line,
	})
}
