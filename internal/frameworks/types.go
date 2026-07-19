package frameworks

type PathParam struct {
	Name string `json:"name"` // e.g. user_id
	Type string `json:"type"` // e.g. int, string
}

type Endpoint struct {
	Method      string      `json:"method"`       // e.g. GET, POST
	Path        string      `json:"path"`         // e.g. /api/v1/users/<int:user_id>
	HandlerFunc string      `json:"handler_func"` // e.g. get_user
	File        string      `json:"file"`         // e.g. routes.py
	Line        int         `json:"line"`         // start line of decorator
	Framework   string      `json:"framework"`    // e.g. flask
	PathParams  []PathParam `json:"path_params"`  // list of extracted parameters
}
