package fakeupstream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Shape is the structure of a JSON document: every path that exists, with the
// set of JSON types seen there. Values are ignored. Arrays contribute their
// element structure under "[]" (the union over all elements).
type Shape map[string]string

// ShapeOf extracts the Shape of a JSON document.
func ShapeOf(raw []byte) (Shape, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("fakeupstream: shape: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("fakeupstream: shape: trailing data")
	}
	types := map[string]map[string]bool{}
	walkShape("$", value, types)
	shape := make(Shape, len(types))
	for path, set := range types {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		shape[path] = strings.Join(names, "|")
	}
	return shape, nil
}

func walkShape(path string, value any, types map[string]map[string]bool) {
	add := func(name string) {
		if types[path] == nil {
			types[path] = map[string]bool{}
		}
		types[path][name] = true
	}
	switch typed := value.(type) {
	case nil:
		add("null")
	case bool:
		add("boolean")
	case json.Number, float64:
		add("number")
	case string:
		add("string")
	case []any:
		add("array")
		for _, child := range typed {
			walkShape(path+"[]", child, types)
		}
	case map[string]any:
		add("object")
		for key, child := range typed {
			walkShape(path+"."+key, child, types)
		}
	}
}

// ShapeDiff is the difference between an expected and an observed Shape.
type ShapeDiff struct {
	// Missing are paths the expected shape has and the observed one lacks.
	Missing []string
	// Extra are paths only the observed shape has.
	Extra []string
	// TypeMismatch are paths in both whose type sets differ.
	TypeMismatch []string
}

// Empty reports whether the two shapes are identical.
func (d ShapeDiff) Empty() bool {
	return len(d.Missing) == 0 && len(d.Extra) == 0 && len(d.TypeMismatch) == 0
}

func (d ShapeDiff) String() string {
	var parts []string
	for _, path := range d.Missing {
		parts = append(parts, "missing "+path)
	}
	for _, path := range d.Extra {
		parts = append(parts, "extra "+path)
	}
	for _, path := range d.TypeMismatch {
		parts = append(parts, "type "+path)
	}
	return strings.Join(parts, "; ")
}

// DiffShape compares want (the real structure) with got (the fake's).
func DiffShape(want, got Shape) ShapeDiff {
	var diff ShapeDiff
	for path, wantTypes := range want {
		gotTypes, ok := got[path]
		switch {
		case !ok:
			diff.Missing = append(diff.Missing, path)
		case gotTypes != wantTypes:
			diff.TypeMismatch = append(diff.TypeMismatch, fmt.Sprintf("%s: real %s, fake %s", path, wantTypes, gotTypes))
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			diff.Extra = append(diff.Extra, path)
		}
	}
	sort.Strings(diff.Missing)
	sort.Strings(diff.Extra)
	sort.Strings(diff.TypeMismatch)
	return diff
}
