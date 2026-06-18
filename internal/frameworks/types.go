package frameworks

type PathParam struct {
	Name string // e.g. user_id
	Type string // e.g. int, string
}

type Endpoint struct {
	Method      string      // e.g. GET, POST
	Path        string      // e.g. /api/v1/users/<int:user_id>
	HandlerFunc string      // e.g. get_user
	File        string      // e.g. routes.py
	Line        int         // start line of decorator
	Framework   string      // e.g. flask
	PathParams  []PathParam // list of extracted parameters
}
