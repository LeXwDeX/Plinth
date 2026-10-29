package fakeupstream

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// Samples is the set of committed, sanitized real exchanges one Version
// derives its response structures from.
type Samples struct {
	Exchanges []wirefixture.Exchange
}

// LoadSamples reads wirefixture candidate files ("*.candidate.json") from
// fsys. Every exchange must be complete and captured from the official API
// (provenance "captured", or "derived" when only request media was replaced) or the load fails: a fake must not be derived from
// synthetic data.
func LoadSamples(fsys fs.FS, names ...string) (*Samples, error) {
	samples := &Samples{}
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("fakeupstream: sample %s: %w", name, err)
		}
		var exchanges []wirefixture.Exchange
		if err := json.Unmarshal(raw, &exchanges); err != nil {
			return nil, fmt.Errorf("fakeupstream: sample %s: %w", name, err)
		}
		for _, exchange := range exchanges {
			if !exchange.Complete || exchange.Invalid != "" {
				return nil, fmt.Errorf("fakeupstream: sample %s #%d is incomplete", name, exchange.Sequence)
			}
			// "derived" is a captured exchange whose request media (an upload) was
			// replaced by a fixed recipe; the response is still the official one.
			if p := exchange.Metadata.Provenance; p != wirefixture.ProvenanceCaptured && p != wirefixture.ProvenanceDerived {
				return nil, fmt.Errorf("fakeupstream: sample %s #%d is %q, not captured", name, exchange.Sequence, p)
			}
		}
		samples.Exchanges = append(samples.Exchanges, exchanges...)
	}
	return samples, nil
}

var (
	placeholderID = regexp.MustCompile(`^(id-\d+|[0-9]+|[0-9a-fA-F]{8}-[0-9a-fA-F-]{27}|[0-9a-f]{16,})$`)
)

// SampleKey normalizes a request into "METHOD /path/{id}" the way endpoint
// tables name samples: sanitized placeholder ids and UUIDs become {id}.
func SampleKey(method, path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part != "" && placeholderID.MatchString(part) {
			parts[i] = "{id}"
		}
	}
	return method + " " + strings.Join(parts, "/")
}

// Find returns the exchanges whose request matches key (see SampleKey) after
// stripping prefix from the recorded path.
func (s *Samples) Find(key, prefix string) []wirefixture.Exchange {
	var found []wirefixture.Exchange
	for _, exchange := range s.Exchanges {
		path := strings.TrimPrefix(exchange.Request.Path, prefix)
		if SampleKey(exchange.Request.Method, path) == key {
			found = append(found, exchange)
		}
	}
	return found
}

// JSONBody returns the JSON response body of an exchange.
func JSONBody(exchange wirefixture.Exchange) (map[string]any, error) {
	var body map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(exchange.Response.Body.JSON)))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	return body, nil
}

// Clone deep-copies a decoded JSON object (numbers stay json.Number).
func Clone(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var out map[string]any
	_ = decoder.Decode(&out)
	return out
}

// Dig follows object keys (string steps) and array indexes (int steps).
func Dig(value any, path ...any) (any, bool) {
	for _, step := range path {
		switch key := step.(type) {
		case string:
			object, ok := value.(map[string]any)
			if !ok {
				return nil, false
			}
			if value, ok = object[key]; !ok {
				return nil, false
			}
		case int:
			list, ok := value.([]any)
			if !ok || key >= len(list) {
				return nil, false
			}
			value = list[key]
		}
	}
	return value, true
}

// DigMap is Dig that must end in an object.
func DigMap(value any, path ...any) (map[string]any, bool) {
	found, ok := Dig(value, path...)
	object, isObject := found.(map[string]any)
	return object, ok && isObject
}
