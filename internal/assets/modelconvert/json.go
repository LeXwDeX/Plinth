package modelconvert

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
)

// Nodes retain source spans. Re-marshalling a decoded map would round numbers or
// normalize their spelling and could hide duplicate members before validation.
type node struct {
	start, end int
	kind       byte
	text       string
	members    []member
	items      []*node
}
type member struct {
	key                 string
	keyStart, separator int
	value               *node
}

func (n *node) field(key string) *node {
	for _, m := range n.members {
		if m.key == key {
			return m.value
		}
	}
	return nil
}

type parser struct {
	op          *operation
	raw         []byte
	pos         int
	elements    int64
	maxElements int64
}

func (o *operation) parse(raw []byte, containerURI bool) (*node, error) {
	// Scan/decode string contents and the structural scan are bounded wire passes.
	if err := o.work(3 * int64(len(raw))); err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) {
		return nil, fail(modelinput.Invalid, "json_utf8")
	}
	maxElements := o.policy.Limits.MaxElements
	// The glTF container adds one URI key and its value. The reconstructed
	// model is still checked against the unmodified profile element ceiling.
	if containerURI {
		maxElements += 2
	}
	p := parser{op: o, raw: raw, maxElements: maxElements}
	n, err := p.value(1)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(raw) {
		return nil, fail(modelinput.Invalid, "json_trailing_data")
	}
	return n, nil
}
func (p *parser) space() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}
func (p *parser) element() error {
	p.elements++
	if p.elements > p.maxElements {
		return fail(modelinput.Limit, "json_elements")
	}
	return p.op.work(1)
}
func (p *parser) value(depth int64) (*node, error) {
	if err := p.element(); err != nil {
		return nil, err
	}
	if depth > p.op.policy.Limits.MaxDepth {
		return nil, fail(modelinput.Limit, "json_depth")
	}
	p.space()
	if p.pos >= len(p.raw) {
		return nil, fail(modelinput.Invalid, "json_value")
	}
	n := &node{start: p.pos, kind: p.raw[p.pos]}
	switch n.kind {
	case '{':
		p.pos++
		p.space()
		seen := make(map[string]bool)
		separator := -1
		if p.pos < len(p.raw) && p.raw[p.pos] == '}' {
			p.pos++
			break
		}
		for {
			p.space()
			keyStart := p.pos
			key, err := p.string()
			if err != nil {
				return nil, err
			}
			if err = p.element(); err != nil {
				return nil, err
			}
			if seen[key] {
				return nil, fail(modelinput.Invalid, "duplicate_json_key")
			}
			seen[key] = true
			p.space()
			if p.pos >= len(p.raw) || p.raw[p.pos] != ':' {
				return nil, fail(modelinput.Invalid, "json_member")
			}
			p.pos++
			child, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.members = append(n.members, member{key: key, keyStart: keyStart, separator: separator, value: child})
			p.space()
			if p.pos >= len(p.raw) {
				return nil, fail(modelinput.Invalid, "json_object")
			}
			if p.raw[p.pos] == '}' {
				p.pos++
				break
			}
			if p.raw[p.pos] != ',' {
				return nil, fail(modelinput.Invalid, "json_object")
			}
			separator = p.pos
			p.pos++
		}
	case '[':
		p.pos++
		p.space()
		if p.pos < len(p.raw) && p.raw[p.pos] == ']' {
			p.pos++
			break
		}
		for {
			child, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.items = append(n.items, child)
			p.space()
			if p.pos >= len(p.raw) {
				return nil, fail(modelinput.Invalid, "json_array")
			}
			if p.raw[p.pos] == ']' {
				p.pos++
				break
			}
			if p.raw[p.pos] != ',' {
				return nil, fail(modelinput.Invalid, "json_array")
			}
			p.pos++
		}
	case '"':
		s, err := p.string()
		if err != nil {
			return nil, err
		}
		n.text = s
	default:
		for p.pos < len(p.raw) {
			c := p.raw[p.pos]
			if c == ',' || c == ']' || c == '}' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				break
			}
			p.pos++
		}
		literal := p.raw[n.start:p.pos]
		if len(literal) == 0 || len(literal) > 128 || !json.Valid(literal) {
			return nil, fail(modelinput.Invalid, "json_literal")
		}
		if n.kind != 't' && n.kind != 'f' && n.kind != 'n' && n.kind != '-' && (n.kind < '0' || n.kind > '9') {
			return nil, fail(modelinput.Invalid, "json_literal")
		}
	}
	n.end = p.pos
	return n, nil
}
func (p *parser) string() (string, error) {
	if p.pos >= len(p.raw) || p.raw[p.pos] != '"' {
		return "", fail(modelinput.Invalid, "json_string")
	}
	start := p.pos
	p.pos++
	for p.pos < len(p.raw) {
		c := p.raw[p.pos]
		p.pos++
		if c == '\\' {
			if p.pos >= len(p.raw) {
				break
			}
			p.pos++
			continue
		}
		if c == '"' {
			var s string
			if json.Unmarshal(p.raw[start:p.pos], &s) != nil {
				return "", fail(modelinput.Invalid, "json_string")
			}
			return s, nil
		}
	}
	return "", fail(modelinput.Invalid, "json_string")
}

func bufferNode(doc *node) (*node, error) {
	if doc.kind != '{' {
		return nil, fail(modelinput.Invalid, "document")
	}
	buffers := doc.field("buffers")
	if buffers == nil || buffers.kind != '[' || len(buffers.items) != 1 {
		return nil, fail(modelinput.Unsupported, "buffer_count")
	}
	buffer := buffers.items[0]
	if buffer.kind != '{' {
		return nil, fail(modelinput.Invalid, "buffer")
	}
	return buffer, nil
}
