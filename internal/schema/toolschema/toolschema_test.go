package toolschema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const sample = `{
  "type": "object",
  "properties": {
    "task_id": {"type": "string", "minLength": 1, "maxLength": 8, "description": "id"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100},
    "ratio": {"type": "number", "minimum": 0, "maximum": 1},
    "flags": {"type": "array", "items": {"type": "string", "enum": ["a", "b"]}, "maxItems": 2, "uniqueItems": true},
    "options": {"type": "object", "properties": {"deep": {"type": "boolean"}}, "required": ["deep"], "additionalProperties": false},
    "maybe": {"type": ["integer", "null"]}
  },
  "required": ["task_id"],
  "additionalProperties": false
}`

func parse(t *testing.T, document string, options Options) *Schema {
	t.Helper()
	schema, err := Parse(json.RawMessage(document), options)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return schema
}

func TestValidateAcceptsArraysObjectsAndNumbers(t *testing.T) {
	schema := parse(t, sample, Options{ClosedObjects: true, RootObject: true})
	for name, value := range map[string]string{
		"minimal":         `{"task_id":"t1"}`,
		"full":            `{"task_id":"t1","limit":5,"ratio":0.25,"flags":["a","b"],"options":{"deep":true},"maybe":null}`,
		"integral number": `{"task_id":"t1","limit":5.0}`,
		"maybe integer":   `{"task_id":"t1","maybe":3}`,
	} {
		if err := schema.Validate(json.RawMessage(value)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidateRefusesWithThePath(t *testing.T) {
	schema := parse(t, sample, Options{ClosedObjects: true, RootObject: true})
	for name, test := range map[string]struct{ value, path, text string }{
		"missing required": {`{}`, "$.task_id", "required"},
		"unknown property": {`{"task_id":"t","owner_id":"x"}`, "$.owner_id", "not an allowed"},
		"wrong type":       {`{"task_id":5}`, "$.task_id", "type string"},
		"too long":         {`{"task_id":"123456789"}`, "$.task_id", "at most 8"},
		"empty":            {`{"task_id":""}`, "$.task_id", "at least 1"},
		"below minimum":    {`{"task_id":"t","limit":0}`, "$.limit", "at least 1"},
		"above maximum":    {`{"task_id":"t","limit":101}`, "$.limit", "at most 100"},
		"fractional int":   {`{"task_id":"t","limit":1.5}`, "$.limit", "type integer"},
		"number as string": {`{"task_id":"t","ratio":"0.5"}`, "$.ratio", "type number"},
		"enum":             {`{"task_id":"t","flags":["c"]}`, "$.flags[0]", "allowed values"},
		"too many items":   {`{"task_id":"t","flags":["a","b","a"]}`, "$.flags", "at most 2"},
		"repeated item":    {`{"task_id":"t","flags":["a","a"]}`, "$.flags", "repeat"},
		"nested required":  {`{"task_id":"t","options":{}}`, "$.options.deep", "required"},
		"nested unknown":   {`{"task_id":"t","options":{"deep":true,"x":1}}`, "$.options.x", "not an allowed"},
		"null not allowed": {`{"task_id":null}`, "$.task_id", "type string"},
		"not an object":    {`[]`, "$", "type object"},
		"trailing content": {`{"task_id":"t"} {}`, "$", "trailing"},
		"not json":         {`nope`, "$", "not valid JSON"},
		"maybe wrong type": {`{"task_id":"t","maybe":"x"}`, "$.maybe", "integer or null"},
	} {
		err := schema.Validate(json.RawMessage(test.value))
		var invalid *ValidationError
		if !errors.As(err, &invalid) || invalid.Path != test.path || !strings.Contains(invalid.Message, test.text) {
			t.Errorf("%s: err = %v, want %s containing %q", name, err, test.path, test.text)
		}
	}
}

func TestParseRefusesWhatIsOutsideTheSubset(t *testing.T) {
	for name, document := range map[string]string{
		"reference":        `{"type":"object","$ref":"#/x","additionalProperties":false}`,
		"combinator":       `{"type":"object","oneOf":[],"additionalProperties":false}`,
		"pattern":          `{"type":"string","pattern":"^a"}`,
		"unknown type":     `{"type":"date"}`,
		"repeated type":    `{"type":["string","string"]}`,
		"no type":          `{"description":"x"}`,
		"bad bound":        `{"type":"integer","minimum":5,"maximum":1}`,
		"bound on string":  `{"type":"string","minimum":1}`,
		"length on number": `{"type":"number","maxLength":3}`,
		"items on object":  `{"type":"object","items":{"type":"string"},"additionalProperties":false}`,
		"open object":      `{"type":"object","properties":{},"additionalProperties":true}`,
		"object no flag":   `{"type":"object","properties":{}}`,
		"required unknown": `{"type":"object","required":["x"],"additionalProperties":false}`,
		"required repeat":  `{"type":"object","properties":{"x":{"type":"string"}},"required":["x","x"],"additionalProperties":false}`,
		"negative length":  `{"type":"string","minLength":-1}`,
		"not an object":    `[]`,
		"trailing":         `{"type":"string"} {}`,
		"empty":            ``,
		"nested open":      `{"type":"object","properties":{"o":{"type":"object","properties":{}}},"additionalProperties":false}`,
	} {
		if _, err := Parse(json.RawMessage(document), Options{ClosedObjects: true}); err == nil {
			t.Errorf("%s: Parse accepted %s", name, document)
		}
	}
	if _, err := Parse(json.RawMessage(`{"type":"string"}`), Options{RootObject: true}); err == nil {
		t.Error("a string root passed RootObject")
	}
}

func TestOpenObjectsAreAllowedWhenNotClosed(t *testing.T) {
	schema := parse(t, `{"type":"object","properties":{"a":{"type":"string"}}}`, Options{RootObject: true})
	if err := schema.Validate(json.RawMessage(`{"a":"x","b":1}`)); err != nil {
		t.Fatalf("an open object refused an extra property: %v", err)
	}
	if got := strings.Join(schema.PropertyNames(), ","); got != "a" {
		t.Fatalf("PropertyNames = %q", got)
	}
}

// A caller's service parameters are a map the service that owns their meaning
// checks, so a tool may declare one deliberately free-form object, and only
// that: never the root, never one that also declares properties, never one that
// leaves additionalProperties unsaid.
func TestFreeFormObjectsAreDeliberateAndNarrow(t *testing.T) {
	options := Options{ClosedObjects: true, RootObject: true, FreeFormObjects: true}
	schema := parse(t, `{"type":"object","properties":{"name":{"type":"string"},"parameters":{"type":"object","additionalProperties":true}},"required":["name"],"additionalProperties":false}`, options)
	if err := schema.Validate(json.RawMessage(`{"name":"x","parameters":{"any":[1,{"deep":true}],"other":"y"}}`)); err != nil {
		t.Fatalf("a free-form map was refused: %v", err)
	}
	if err := schema.Validate(json.RawMessage(`{"name":"x","parameters":"not a map"}`)); err == nil {
		t.Fatal("a string passed for an object")
	}
	if err := schema.Validate(json.RawMessage(`{"name":"x","stray":1}`)); err == nil {
		t.Fatal("the closed root accepted a stray property")
	}
	for name, document := range map[string]string{
		"open root":            `{"type":"object","properties":{},"additionalProperties":true}`,
		"open with properties": `{"type":"object","properties":{"o":{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":true}},"additionalProperties":false}`,
		"open with required":   `{"type":"object","properties":{"o":{"type":"object","required":["a"],"additionalProperties":true}},"additionalProperties":false}`,
		"nested unsaid":        `{"type":"object","properties":{"o":{"type":"object"}},"additionalProperties":false}`,
	} {
		if _, err := Parse(json.RawMessage(document), options); err == nil {
			t.Errorf("%s: Parse accepted %s", name, document)
		}
	}
	// Without the option the same free-form map is still refused.
	if _, err := Parse(json.RawMessage(`{"type":"object","properties":{"o":{"type":"object","additionalProperties":true}},"additionalProperties":false}`),
		Options{ClosedObjects: true, RootObject: true}); err == nil {
		t.Fatal("a free-form object was accepted without FreeFormObjects")
	}
}
