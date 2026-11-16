// Package samebytes is the rule the ZIP downloads share for a file that repeats
// another file of the archive (#293, #300): a file whose record names the same
// bytes as an entry the archive already holds is left out, and the README says
// which entry holds them. The one-click model ZIP and the administration
// generation download both go through it, so a repeat is judged and worded alike
// in both. It knows nothing of formats, slots or providers.
package samebytes

import "strings"

// Record is what a saved file's record says about its bytes.
type Record struct {
	SHA256 string
	Size   int64
}

// Same reports whether two saved files hold the same bytes, by their records: both
// carry a SHA-256 (a historical record, or a legacy source file, may not) and agree
// on it and on the size. The store verifies the digest of every file it opens, so a
// record that opens has the bytes it names. A record without a digest is never the
// same as anything: a file is not left out on a guess.
func Same(left, right Record) bool {
	return left.SHA256 != "" && left.SHA256 == right.SHA256 && left.Size == right.Size
}

// Text is the wording the Schema gives the README note: the README file name, the
// heading of the section and the line of one repeat, in which {label} is the slot
// left out and {path} the entry that holds its bytes.
type Text struct {
	Filename string
	Heading  string
	Line     string
}

// Declared reports whether the Schema names the README section. Without both the
// heading and the line a repeated file is never left out silently: every one is
// packed.
func (t Text) Declared() bool {
	return strings.TrimSpace(t.Heading) != "" && strings.TrimSpace(t.Line) != ""
}

// Repeat is a file the archive leaves out and the entry that holds its bytes.
type Repeat struct {
	// Label names the slot left out; see Label.
	Label string
	// Path is the entry of the archive that holds the bytes.
	Path string
}

// Section is the README lines that name the repeats: the heading, then one line
// each. It is nil when nothing was left out.
func (t Text) Section(repeats []Repeat) []string {
	if len(repeats) == 0 {
		return nil
	}
	lines := make([]string, 0, len(repeats)+1)
	lines = append(lines, t.Heading)
	for _, repeat := range repeats {
		lines = append(lines, strings.NewReplacer("{label}", repeat.Label, "{path}", repeat.Path).Replace(t.Line))
	}
	return lines
}

// Label names a saved file's slot for the README: its variant, or its role when it
// has none.
func Label(role, variant string) string {
	if variant != "" {
		return Sanitize(variant)
	}
	return Sanitize(role)
}

// Sanitize keeps the characters an archive path segment or a README label may hold
// (letters, digits, '.', '-', '_'), replaces the rest and bounds the length. It
// never returns an empty name or one that starts with a dot.
func Sanitize(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	result := strings.TrimLeft(builder.String(), ".")
	if len(result) > 80 {
		result = result[:80]
	}
	if result == "" {
		return "asset"
	}
	return result
}
