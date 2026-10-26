;; Express / Koa-router / Fastify route registrations and mounts.
;;
;; app.get('/path', handler)         router.post('/path', mw, handler)
;; fastify.get('/path', handler)
(call_expression
  function: (member_expression
    object: (identifier) @router
    property: (property_identifier) @method)
  arguments: (arguments) @args) @route_call

;; app = express() / router = express.Router()
(variable_declarator
  name: (identifier) @router_var
  value: (call_expression
    function: (_) @factory
    arguments: (arguments) @factory_args)) @router_decl

;; const router = new Router() / new KoaRouter()
(variable_declarator
  name: (identifier) @router_var
  value: (new_expression
    constructor: (identifier) @factory
    arguments: (arguments) @factory_args)) @router_decl
