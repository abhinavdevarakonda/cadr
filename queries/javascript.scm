; Function declarations
(function_declaration
    name: (identifier) @func.name) @function

; Method definitions in classes
(method_definition
    name: (property_identifier) @func.name) @function

; Arrow functions assigned to variables
(variable_declarator
    name: (identifier) @func.name
    value: (arrow_function)) @function

; Function calls - simple identifier
(call_expression
    function: (identifier) @call.name) @call

; Function calls - member expression (obj.method)
(call_expression
    function: (member_expression
        object: (_) @call.qualifier
        property: (property_identifier) @call.name
    )
) @call
