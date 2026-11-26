// Package toolschema is the small JSON Schema subset the external MCP tools
// declare their arguments and results in (#332). It is deliberately not a full
// JSON Schema implementation: Parse refuses every keyword outside the subset,
// so a Product Schema can never declare a constraint that silently is not
// enforced, and Validate checks a value against a parsed schema.
//
// The subset: type (one name or a list, with "null"), enum, minimum, maximum,
// exclusiveMinimum, exclusiveMaximum (numbers), minLength, maxLength, items,
// minItems, maxItems, uniqueItems, properties, required and
// additionalProperties (a boolean, true only on a free-form map when
// Options.FreeFormObjects allows it; or the schema of every value of a map
// that declares no property of its own, also only on such a map). description, title, default
// and examples are annotations and are kept but never enforced. There are no
// references, no combinators and no patterns.
//
// This package knows nothing of the internal read-only MCP service
// (internal/mcp), whose own, narrower argument validation is unchanged.
package toolschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Schema is one parsed schema node.
type Schema struct {
	Types            []string
	Description      string
	Enum             []any
	Minimum, Maximum *float64
	// ExclusiveMinimum and ExclusiveMaximum are strict bounds (#665).
	ExclusiveMinimum, ExclusiveMaximum *float64
	MinLength, MaxLength               *int
	Items                              *Schema
	MinItems, MaxItems                 *int
	UniqueItems                        bool
	Properties                         map[string]*Schema
	Required                           []string
	// AdditionalProperties is nil when the schema does not say (an object then
	// accepts other properties, as in JSON Schema).
	AdditionalProperties *bool
	// AdditionalSchema is the schema every value of a map must match, when
	// additionalProperties is a schema rather than a boolean (#665).
	AdditionalSchema *Schema
}

// Options tune what Parse accepts.
type Options struct {
	// ClosedObjects requires every object schema to say
	// additionalProperties: false. Tool arguments use it, so an unknown
	// argument is refused instead of ignored.
	ClosedObjects bool
	// RootObject requires the root to be an object schema.
	RootObject bool
	// FreeFormObjects lets a tool argument be a deliberately open map: under
	// ClosedObjects, a nested object that says additionalProperties: true and
	// declares no property of its own is accepted. Its keys and values are not
	// checked here; the service that owns their meaning checks them (a
	// caller's service parameters, which the offering declares). The root is
	// never one, and an object that declares properties is closed.
	FreeFormObjects bool
}

var annotations = map[string]bool{"description": true, "title": true, "default": true, "examples": true}

var knownTypes = map[string]bool{
	"string": true, "integer": true, "number": true, "boolean": true,
	"array": true, "object": true, "null": true,
}

// Parse reads a schema document and refuses anything outside the subset.
func Parse(raw json.RawMessage, options Options) (*Schema, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("schema is empty")
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("schema is not JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("schema has trailing content")
	}
	root, err := parseNode(document, "$", options)
	if err != nil {
		return nil, err
	}
	if options.RootObject && !(len(root.Types) == 1 && root.Types[0] == "object") {
		return nil, errors.New("schema root must be an object schema")
	}
	return root, nil
}

func parseNode(value any, path string, options Options) (*Schema, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: a schema must be an object", path)
	}
	node := &Schema{}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := object[key]
		switch key {
		case "type":
			switch typed := item.(type) {
			case string:
				node.Types = []string{typed}
			case []any:
				for _, entry := range typed {
					name, isString := entry.(string)
					if !isString {
						return nil, fmt.Errorf("%s.type: names must be strings", path)
					}
					node.Types = append(node.Types, name)
				}
			default:
				return nil, fmt.Errorf("%s.type: must be a name or a list of names", path)
			}
			if len(node.Types) == 0 {
				return nil, fmt.Errorf("%s.type: is empty", path)
			}
			seen := map[string]bool{}
			for _, name := range node.Types {
				if !knownTypes[name] || seen[name] {
					return nil, fmt.Errorf("%s.type: %q is unknown or repeated", path, name)
				}
				seen[name] = true
			}
		case "description":
			text, isString := item.(string)
			if !isString {
				return nil, fmt.Errorf("%s.description: must be a string", path)
			}
			node.Description = text
		case "enum":
			values, isList := item.([]any)
			if !isList || len(values) == 0 {
				return nil, fmt.Errorf("%s.enum: must be a non-empty list", path)
			}
			node.Enum = values
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum":
			number, isNumber := item.(json.Number)
			if !isNumber {
				return nil, fmt.Errorf("%s.%s: must be a number", path, key)
			}
			parsed, err := number.Float64()
			if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
				return nil, fmt.Errorf("%s.%s: is not a finite number", path, key)
			}
			switch key {
			case "minimum":
				node.Minimum = &parsed
			case "maximum":
				node.Maximum = &parsed
			case "exclusiveMinimum":
				node.ExclusiveMinimum = &parsed
			default:
				node.ExclusiveMaximum = &parsed
			}
		case "minLength", "maxLength", "minItems", "maxItems":
			count, err := nonNegativeInt(item)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", path, key, err)
			}
			switch key {
			case "minLength":
				node.MinLength = &count
			case "maxLength":
				node.MaxLength = &count
			case "minItems":
				node.MinItems = &count
			default:
				node.MaxItems = &count
			}
		case "uniqueItems":
			flag, isBool := item.(bool)
			if !isBool {
				return nil, fmt.Errorf("%s.uniqueItems: must be a boolean", path)
			}
			node.UniqueItems = flag
		case "items":
			child, err := parseNode(item, path+".items", options)
			if err != nil {
				return nil, err
			}
			node.Items = child
		case "properties":
			properties, isObject := item.(map[string]any)
			if !isObject {
				return nil, fmt.Errorf("%s.properties: must be an object", path)
			}
			node.Properties = make(map[string]*Schema, len(properties))
			for name, child := range properties {
				parsed, err := parseNode(child, path+".properties."+name, options)
				if err != nil {
					return nil, err
				}
				node.Properties[name] = parsed
			}
		case "required":
			names, isList := item.([]any)
			if !isList {
				return nil, fmt.Errorf("%s.required: must be a list of names", path)
			}
			for _, entry := range names {
				name, isString := entry.(string)
				if !isString || name == "" {
					return nil, fmt.Errorf("%s.required: names must be non-empty strings", path)
				}
				node.Required = append(node.Required, name)
			}
		case "additionalProperties":
			if values, isSchema := item.(map[string]any); isSchema {
				child, err := parseNode(values, path+".additionalProperties", options)
				if err != nil {
					return nil, err
				}
				node.AdditionalSchema = child
				open := true
				node.AdditionalProperties = &open
				continue
			}
			flag, isBool := item.(bool)
			if !isBool {
				return nil, fmt.Errorf("%s.additionalProperties: must be a boolean or a schema", path)
			}
			node.AdditionalProperties = &flag
		default:
			if !annotations[key] {
				return nil, fmt.Errorf("%s: keyword %q is not supported", path, key)
			}
		}
	}
	return node, node.check(path, options)
}

func nonNegativeInt(value any) (int, error) {
	number, isNumber := value.(json.Number)
	if !isNumber {
		return 0, errors.New("must be a non-negative integer")
	}
	parsed, err := number.Int64()
	if err != nil || parsed < 0 || parsed > math.MaxInt32 {
		return 0, errors.New("must be a non-negative integer")
	}
	return int(parsed), nil
}

// check is the cross-keyword consistency of one node.
func (s *Schema) check(path string, options Options) error {
	if len(s.Types) == 0 && len(s.Enum) == 0 {
		return fmt.Errorf("%s: a schema needs a type", path)
	}
	has := func(name string) bool {
		for _, candidate := range s.Types {
			if candidate == name {
				return true
			}
		}
		return false
	}
	numeric := has("integer") || has("number")
	if (s.Minimum != nil || s.Maximum != nil || s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil) && !numeric {
		return fmt.Errorf("%s: minimum and maximum need a numeric type", path)
	}
	lower, upper := s.Minimum, s.Maximum
	if s.ExclusiveMinimum != nil && (lower == nil || *s.ExclusiveMinimum >= *lower) {
		lower = s.ExclusiveMinimum
	}
	if s.ExclusiveMaximum != nil && (upper == nil || *s.ExclusiveMaximum <= *upper) {
		upper = s.ExclusiveMaximum
	}
	if lower != nil && upper != nil && (*lower > *upper ||
		(*lower == *upper && (s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil))) {
		return fmt.Errorf("%s: minimum is above maximum", path)
	}
	if s.AdditionalSchema != nil && len(s.Properties) > 0 {
		return fmt.Errorf("%s: a map with a value schema declares no properties", path)
	}
	if (s.MinLength != nil || s.MaxLength != nil) && !has("string") {
		return fmt.Errorf("%s: minLength and maxLength need the string type", path)
	}
	if s.MinLength != nil && s.MaxLength != nil && *s.MinLength > *s.MaxLength {
		return fmt.Errorf("%s: minLength is above maxLength", path)
	}
	if (s.Items != nil || s.MinItems != nil || s.MaxItems != nil || s.UniqueItems) && !has("array") {
		return fmt.Errorf("%s: array keywords need the array type", path)
	}
	if s.MinItems != nil && s.MaxItems != nil && *s.MinItems > *s.MaxItems {
		return fmt.Errorf("%s: minItems is above maxItems", path)
	}
	if (s.Properties != nil || len(s.Required) > 0 || s.AdditionalProperties != nil) && !has("object") {
		return fmt.Errorf("%s: object keywords need the object type", path)
	}
	if has("object") && options.ClosedObjects {
		freeForm := options.FreeFormObjects && path != "$" && s.AdditionalProperties != nil && *s.AdditionalProperties &&
			len(s.Properties) == 0 && len(s.Required) == 0
		if (s.AdditionalProperties == nil || *s.AdditionalProperties) && !freeForm {
			return fmt.Errorf("%s: an object schema must say additionalProperties: false", path)
		}
	}
	seen := map[string]bool{}
	for _, name := range s.Required {
		if seen[name] {
			return fmt.Errorf("%s.required: %q is repeated", path, name)
		}
		seen[name] = true
		if _, declared := s.Properties[name]; !declared {
			return fmt.Errorf("%s.required: %q is not a declared property", path, name)
		}
	}
	return nil
}

// PropertyNames lists the declared property names, sorted.
func (s *Schema) PropertyNames() []string {
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidationError names the first place a value breaks its schema.
type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string { return e.Path + ": " + e.Message }

// Validate checks a JSON document against the schema.
func (s *Schema) Validate(raw json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return &ValidationError{Path: "$", Message: "not valid JSON"}
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return &ValidationError{Path: "$", Message: "trailing content after the JSON value"}
	}
	return s.validate(value, "$")
}

func fail(path, format string, args ...any) error {
	return &ValidationError{Path: path, Message: fmt.Sprintf(format, args...)}
}

func (s *Schema) validate(value any, path string) error {
	if len(s.Types) > 0 {
		matched := false
		for _, name := range s.Types {
			if typeMatches(name, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fail(path, "must be of type %s", strings.Join(s.Types, " or "))
		}
	}
	if len(s.Enum) > 0 {
		found := false
		for _, candidate := range s.Enum {
			if equalJSON(candidate, value) {
				found = true
				break
			}
		}
		if !found {
			return fail(path, "is not one of the allowed values")
		}
	}
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return fail(path, "is not a finite number")
		}
		if s.Minimum != nil && number < *s.Minimum {
			return fail(path, "must be at least %s", trimFloat(*s.Minimum))
		}
		if s.Maximum != nil && number > *s.Maximum {
			return fail(path, "must be at most %s", trimFloat(*s.Maximum))
		}
		if s.ExclusiveMinimum != nil && number <= *s.ExclusiveMinimum {
			return fail(path, "must be above %s", trimFloat(*s.ExclusiveMinimum))
		}
		if s.ExclusiveMaximum != nil && number >= *s.ExclusiveMaximum {
			return fail(path, "must be below %s", trimFloat(*s.ExclusiveMaximum))
		}
	case string:
		length := utf8.RuneCountInString(typed)
		if s.MinLength != nil && length < *s.MinLength {
			return fail(path, "must have at least %d characters", *s.MinLength)
		}
		if s.MaxLength != nil && length > *s.MaxLength {
			return fail(path, "must have at most %d characters", *s.MaxLength)
		}
	case []any:
		if s.MinItems != nil && len(typed) < *s.MinItems {
			return fail(path, "must have at least %d items", *s.MinItems)
		}
		if s.MaxItems != nil && len(typed) > *s.MaxItems {
			return fail(path, "must have at most %d items", *s.MaxItems)
		}
		if s.UniqueItems {
			for i := range typed {
				for j := i + 1; j < len(typed); j++ {
					if equalJSON(typed[i], typed[j]) {
						return fail(path, "must not repeat an item")
					}
				}
			}
		}
		if s.Items != nil {
			for index, item := range typed {
				if err := s.Items.validate(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		for _, name := range s.Required {
			if _, present := typed[name]; !present {
				return fail(path+"."+name, "is required")
			}
		}
		names := make([]string, 0, len(typed))
		for name := range typed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, declared := s.Properties[name]
			if !declared {
				if s.AdditionalProperties != nil && !*s.AdditionalProperties {
					return fail(path+"."+name, "is not an allowed property")
				}
				if s.AdditionalSchema != nil {
					if err := s.AdditionalSchema.validate(typed[name], path+"."+name); err != nil {
						return err
					}
				}
				continue
			}
			if err := child.validate(typed[name], path+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

func typeMatches(name string, value any) bool {
	switch name {
	case "null":
		return value == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		parsed, err := number.Float64()
		return err == nil && !math.IsInf(parsed, 0)
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		if _, err := number.Int64(); err == nil {
			return true
		}
		parsed, err := number.Float64()
		return err == nil && !math.IsInf(parsed, 0) && parsed == math.Trunc(parsed)
	}
	return false
}

func equalJSON(a, b any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	if errLeft != nil || errRight != nil {
		return false
	}
	var x, y any
	if json.Unmarshal(left, &x) != nil || json.Unmarshal(right, &y) != nil {
		return false
	}
	left, _ = json.Marshal(x)
	right, _ = json.Marshal(y)
	return bytes.Equal(left, right)
}

func trimFloat(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", value), "0"), ".")
}
