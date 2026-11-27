package toolschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// UnsupportedKeywordError is Parse's refusal of a keyword outside the subset
// (#665). Path is the schema node that declares it ("$" for the root,
// "$.properties.purpose" for a parameter), so a caller can name the tool, the
// parameter and the keyword.
type UnsupportedKeywordError struct {
	Path    string
	Keyword string
}

func (e *UnsupportedKeywordError) Error() string {
	return fmt.Sprintf("%s: keyword %q is not supported", e.Path, e.Keyword)
}

// Parameter is the top-level parameter the node belongs to, or "" for the
// root itself.
func (e *UnsupportedKeywordError) Parameter() string {
	rest, found := strings.CutPrefix(e.Path, "$.properties.")
	if !found {
		return ""
	}
	name, _, _ := strings.Cut(rest, ".")
	return name
}

// EnumChoices lists an enum's values for a reader: strings as they are,
// other values as JSON, separated by ", ".
func EnumChoices(values []any) string {
	choices := make([]string, 0, len(values))
	for _, value := range values {
		if text, isString := value.(string); isString {
			choices = append(choices, text)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		choices = append(choices, string(encoded))
	}
	return strings.Join(choices, ", ")
}

// TrimEnumStrings returns the document with the surrounding whitespace of
// every string a string enum constrains removed, and whether anything
// changed (#665). A model often writes " humanoid" for humanoid; the value it
// meant is still the declared one. Only whitespace is trimmed, never case or
// other characters, and strings no enum constrains keep their bytes. A
// document that is not valid JSON is returned unchanged.
func (s *Schema) TrimEnumStrings(raw json.RawMessage) (json.RawMessage, bool) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return raw, false
	}
	trimmed, changed := s.trimEnums(value)
	if !changed {
		return raw, false
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(trimmed); err != nil {
		return raw, false
	}
	return json.RawMessage(bytes.TrimSpace(buffer.Bytes())), true
}

func (s *Schema) trimEnums(value any) (any, bool) {
	if s == nil {
		return value, false
	}
	switch typed := value.(type) {
	case string:
		if !s.hasStringEnum() {
			return value, false
		}
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != typed
	case []any:
		changed := false
		for index, item := range typed {
			next, itemChanged := s.Items.trimEnums(item)
			if itemChanged {
				typed[index], changed = next, true
			}
		}
		return typed, changed
	case map[string]any:
		changed := false
		for name, item := range typed {
			child, declared := s.Properties[name]
			if !declared {
				child = s.AdditionalSchema
			}
			next, itemChanged := child.trimEnums(item)
			if itemChanged {
				typed[name], changed = next, true
			}
		}
		return typed, changed
	}
	return value, false
}

func (s *Schema) hasStringEnum() bool {
	for _, candidate := range s.Enum {
		if _, isString := candidate.(string); isString {
			return true
		}
	}
	return false
}
