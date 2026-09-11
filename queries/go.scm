;; Function declarations
(function_declaration
  name: (identifier) @func.name) @function

;; Method declarations
(method_declaration
  name: (field_identifier) @func.name) @function

;; Direct calls: foo()
(call_expression
  function: (identifier) @call.name) @call

;; Qualified calls: pkg.Foo() or obj.Method()
(call_expression
  function: (selector_expression
    (identifier) @call.qualifier
    (field_identifier) @call.name)) @call
