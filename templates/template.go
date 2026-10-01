package templates

import (
	"bytes"
	"fmt"
	"reflect"
	"text/template"
)

// RenderLazy dynamically renders the specified field of taskDef using reflection but lazily.
func RenderLazy(tmpl *template.Template, vars map[string]interface{}, funcs template.FuncMap, taskDef *YamlTaskDefinition, fieldName string) (func() (string, error), error) {
	// Ensure the taskDef is not nil and is a valid struct
	if taskDef == nil {
		return nil, fmt.Errorf("task definition is nil")
	}

	// Use reflection to get the value of the struct
	val := reflect.ValueOf(taskDef).Elem()

	// Ensure the struct is addressable
	if !val.IsValid() || !val.CanAddr() {
		return nil, fmt.Errorf("task definition is not valid or addressable")
	}

	// Get the specified field by name using reflection
	field := val.FieldByName(fieldName)

	// Check if the field exists and is a string
	if !field.IsValid() || field.Kind() != reflect.String {
		return nil, fmt.Errorf("field '%s' not found or not of type string", fieldName)
	}

	// Get the string value of the field to be used as the template content
	templateContent := field.String()
	if templateContent == "" {
		return nil, fmt.Errorf("field '%s' is empty; no content to render", fieldName)
	}

	// Merge args from taskDef into vars
	for k, v := range taskDef.Args {
		vars[k] = v
	}

	// Return a closure (function) that will render the template lazily when called
	return func() (string, error) {
		// Execute the template with the provided variables.
		var queryBuffer bytes.Buffer

		// Create a new template with the defined YAML format.
		parsedTemplate, err := tmpl.Funcs(funcs).Parse(templateContent)
		if err != nil {
			return "", fmt.Errorf("error parsing template: %v", err)
		}

		// Execute the parsed template
		if err := parsedTemplate.Execute(&queryBuffer, vars); err != nil {
			return "", fmt.Errorf("error executing template: %v", err)
		}

		// Return the rendered result
		return queryBuffer.String(), nil
	}, nil
}
