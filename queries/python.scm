; Function definitions
(function_definition
    name: (identifier) @func.name) @function

; Class definitions
(class_definition
    name: (identifier) @class.name) @class

; Function calls - simple identifier
(call
    function: (identifier) @call.name) @call

; Function calls - attribute (obj.method)
(call
    function: (attribute
        object: (_) @call.qualifier
        attribute: (identifier) @call.name
    )
) @call
