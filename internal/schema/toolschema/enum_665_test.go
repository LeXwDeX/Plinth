package toolschema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const enumSample = `{
	"type": "object",
	"additionalProperties": false,
	"properties": {
		"purpose": {"type": "string", "enum": ["model_input", "design_sheet"]},
		"note": {"type": "string"},
		"tags": {"type": "array", "items": {"type": "string", "enum": ["a", "b"]}}
	}
}`

// #665: a string an enum constrains is trimmed of whitespace only; other
// strings keep their bytes.
func TestTrimEnumStringsTrimsOnlyEnumWhitespace(t *testing.T) {
	schema := parse(t, enumSample, Options{ClosedObjects: true, RootObject: true})
	got, changed := schema.TrimEnumStrings(json.RawMessage(`{"purpose":" design_sheet\n","note":"  keep  ","tags":[" a","b"]}`))
	if !changed {
		t.Fatal("nothing was trimmed")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["purpose"] != "design_sheet" || decoded["note"] != "  keep  " ||
		decoded["tags"].([]any)[0] != "a" {
		t.Fatalf("trimmed = %s", got)
	}
	if err := schema.Validate(got); err != nil {
		t.Fatalf("trimmed value is refused: %v", err)
	}
	same := json.RawMessage(`{"purpose":"model_input"}`)
	if out, changed := schema.TrimEnumStrings(same); changed || string(out) != string(same) {
		t.Fatalf("untouched document changed: %s", out)
	}
	if out, changed := schema.TrimEnumStrings(json.RawMessage(`{"purpose":"MODEL_INPUT"}`)); changed {
		t.Fatalf("case was changed: %s", out)
	}
}

// #665: an enum violation lists the allowed values.
func TestEnumViolationListsTheChoices(t *testing.T) {
	schema := parse(t, enumSample, Options{ClosedObjects: true, RootObject: true})
	err := schema.Validate(json.RawMessage(`{"purpose":"poster"}`))
	var invalid *ValidationError
	if !errors.As(err, &invalid) || invalid.Path != "$.purpose" || len(invalid.Allowed) != 2 ||
		!strings.Contains(invalid.Message, "model_input, design_sheet") {
		t.Fatalf("err = %#v", err)
	}
	if choices := EnumChoices([]any{"x", json.Number("2"), true}); choices != "x, 2, true" {
		t.Fatalf("choices = %q", choices)
	}
}

// #665: a keyword outside the subset is a typed error naming the parameter.
func TestUnsupportedKeywordNamesTheParameter(t *testing.T) {
	_, err := Parse(json.RawMessage(`{"type":"object","additionalProperties":false,
		"properties":{"purpose":{"type":"string","pattern":"^a"}}}`), Options{ClosedObjects: true, RootObject: true})
	var unsupported *UnsupportedKeywordError
	if !errors.As(err, &unsupported) || unsupported.Keyword != "pattern" || unsupported.Parameter() != "purpose" {
		t.Fatalf("err = %#v", err)
	}
	_, err = Parse(json.RawMessage(`{"type":"object","oneOf":[]}`), Options{RootObject: true})
	if !errors.As(err, &unsupported) || unsupported.Keyword != "oneOf" || unsupported.Parameter() != "" {
		t.Fatalf("root err = %#v", err)
	}
}
