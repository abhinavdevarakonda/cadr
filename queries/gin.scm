;; Gin route registrations: router.GET("/path", handler)
(call_expression
  function: (selector_expression
    operand: (identifier) @router
    field: (field_identifier) @method)
  arguments: (argument_list) @args) @route_call

;; Gin route group assignment: v1 := router.Group("/prefix")
(short_var_declaration
  left: (expression_list
    (identifier) @group_var)
  right: (expression_list
    (call_expression
      function: (selector_expression
        operand: (identifier) @parent_router
        field: (field_identifier) @group_call (#eq? @group_call "Group"))
      arguments: (argument_list) @group_args))) @group_decl
