package generation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

// DependencyBindingResolver projects only a successful source's durable local
// evidence. It must not perform I/O, mutate evidence or choose a new result.
type DependencyBindingResolver interface {
	ResolveDependencyBinding(DependencyBindingRequest) (json.RawMessage, error)
}

type DependencyBindingRequest struct {
	Offering  Offering
	TaskID    TaskID
	AttemptID AttemptID
	Job       UpstreamJob
	Claims    []ArtifactClaim
	Assets    []Asset
}

// CanonicalDependencyBinding bounds private Provider evidence before it enters
// PreparedInput/hash. Only objects, arrays, strings, booleans and int64 values
// are accepted. Duplicate keys, nulls, floats and multiple roots are rejected.
func CanonicalDependencyBinding(raw json.RawMessage) (json.RawMessage, error) {
	invalid := errors.New("invalid private dependency binding")
	if len(raw) == 0 || len(raw) > 16384 || !utf8.Valid(raw) {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		tokens++
		if tokens > 512 || depth > 8 {
			return nil, invalid
		}
		t, err := d.Token()
		if err != nil {
			return nil, invalid
		}
		switch v := t.(type) {
		case json.Delim:
			if v == '{' {
				m := map[string]any{}
				for d.More() {
					k, e := d.Token()
					key, ok := k.(string)
					if e != nil || !ok || key == "" || len(key) > 64 || len(m) >= 32 {
						return nil, invalid
					}
					if _, exists := m[key]; exists {
						return nil, invalid
					}
					value, e := read(depth + 1)
					if e != nil {
						return nil, e
					}
					m[key] = value
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return nil, invalid
				}
				return m, nil
			}
			if v == '[' {
				a := []any{}
				for d.More() {
					if len(a) >= 32 {
						return nil, invalid
					}
					value, e := read(depth + 1)
					if e != nil {
						return nil, e
					}
					a = append(a, value)
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return nil, invalid
				}
				return a, nil
			}
		case string:
			if len(v) <= 4096 {
				return v, nil
			}
		case bool:
			return v, nil
		case json.Number:
			if _, e := strconv.ParseInt(string(v), 10, 64); e == nil {
				return v, nil
			}
		}
		return nil, invalid
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if root, ok := value.(map[string]any); !ok || len(root) == 0 {
		return nil, invalid
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, invalid
	}
	result, err := json.Marshal(value)
	if err != nil || len(result) > 16384 {
		return nil, invalid
	}
	return result, nil
}
