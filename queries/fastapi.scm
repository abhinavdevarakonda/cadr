(decorator
  (call
    function: (attribute
      object: (identifier) @app
      attribute: (identifier) @method)
    arguments: (argument_list) @args)) @decorator

(assignment
  left: (identifier) @var
  right: (call
    function: (identifier) @func_name (#eq? @func_name "APIRouter")
    arguments: (argument_list) @args)) @blueprint
