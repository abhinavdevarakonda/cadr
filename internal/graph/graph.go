package graph

type NodeType string

const (
	DirectoryNode NodeType = "directory"
	FileNode      NodeType = "file"
	FunctionNode  NodeType = "function"
)

type Node struct {
	ID   string   `json:"id"`
	Type NodeType `json:"type"`
	Name string   `json:"name"`
	Path string   `json:"path"`
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
}

type Graph struct {
	Nodes map[string]*Node `json:"nodes"`
	Edges []*Edge          `json:"edges"`
}

func New() *Graph {
	return &Graph{
		Nodes: make(map[string]*Node),
		Edges: []*Edge{},
	}
}

func (g *Graph) AddNode(n *Node) {
	g.Nodes[n.ID] = n
}

func (g *Graph) AddEdge(from, to string, t EdgeType) {
	g.Edges = append(g.Edges, &Edge{
		From: from,
		To:   to,
		Type: t,
	})
}
