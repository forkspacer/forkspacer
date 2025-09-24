package resources

import (
	"bytes"
	"fmt"
	"text/template"

	"go.yaml.in/yaml/v3"
)

func ReadResource[T any](data []byte) (*T, error) {
	var resource T
	if err := yaml.Unmarshal(data, &resource); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML data into resource of type %T: %w", resource, err)
	}

	return &resource, nil
}

func renderSpecWithTypePreservation[T any](spec T, data any) (T, error) {
	var zero T

	// Marshal to interface{} to work with raw YAML structure
	specBytes, err := yaml.Marshal(spec)
	if err != nil {
		return zero, err
	}

	var specInterface any
	if err = yaml.Unmarshal(specBytes, &specInterface); err != nil {
		return zero, err
	}

	// Recursively process the interface structure
	renderedInterface, err := renderInterface(specInterface, data)
	if err != nil {
		return zero, err
	}

	// Marshal back to bytes and unmarshal to struct
	renderedBytes, err := yaml.Marshal(renderedInterface)
	if err != nil {
		return zero, err
	}

	var renderedSpec T
	if err = yaml.Unmarshal(renderedBytes, &renderedSpec); err != nil {
		return zero, err
	}

	return renderedSpec, nil
}

func renderInterface(v any, data any) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any)
		for k, v := range val {
			rendered, err := renderInterface(v, data)
			if err != nil {
				return nil, err
			}
			result[k] = rendered
		}
		return result, nil
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			rendered, err := renderInterface(v, data)
			if err != nil {
				return nil, err
			}
			result[i] = rendered
		}
		return result, nil
	case string:
		// Only process strings that contain template syntax
		if bytes.Contains([]byte(val), []byte("{{")) && bytes.Contains([]byte(val), []byte("}}")) {
			tmpl, err := template.New("").Parse(val)
			if err != nil {
				return nil, err
			}

			var buf bytes.Buffer
			if err = tmpl.Execute(&buf, data); err != nil {
				return nil, err
			}

			rendered := buf.String()

			// Try to parse as YAML to preserve types (numbers, booleans, etc.)
			var parsedValue any
			if err := yaml.Unmarshal([]byte(rendered), &parsedValue); err == nil {
				return parsedValue, nil
			}

			return rendered, nil
		}
		return val, nil
	default:
		return val, nil
	}
}
