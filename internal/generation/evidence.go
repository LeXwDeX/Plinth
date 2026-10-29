package generation

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/logging"
)

// UpstreamExcerptRunes bounds Failure.UpstreamExcerpt.
const UpstreamExcerptRunes = 400

// echoMinimumRunes is the shortest input value treated as echoable. Shorter
// values ("glb", "quad") are indistinguishable from the upstream's own
// vocabulary and carry no user content worth stripping.
const echoMinimumRunes = 5

// echoPrefixRunes is how much of a long input must match before a truncated
// echo ("...a tiny dragon with") is recognised and stripped to its end.
const echoPrefixRunes = 24

var (
	excerptURL   = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
	excerptToken = regexp.MustCompile(`(?i)\b(?:bearer\s+\S+|msy[-_][A-Za-z0-9_-]{8,}|(?:token|key|signature|secret)=\S+)`)
	excerptEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
)

// UpstreamExcerpt is the only sanitizer of upstream-controlled text that is
// persisted or logged as Failure.UpstreamExcerpt (A4, #121). Upstream error
// bodies and task errors are not under Plinth's control: they can echo the
// submitted prompt, carry signed asset URLs, tokens, private identifiers or
// an account e-mail. It strips, in order:
//
//   - echoes: exact occurrences of the request's own text inputs (and a
//     truncated echo of a long one), so the prompt never reaches a log line or
//     the database;
//   - every URL (signed query, private path, task identifier), replaced by
//     [url];
//   - bearer/key/token assignments and provider key shapes (logging.Redact
//     plus the Meshy msy- shape);
//   - e-mail addresses;
//
// then drops unprintable characters, collapses whitespace and bounds the
// result by runes (never splitting a multi-byte character). maxRunes <= 0 uses
// UpstreamExcerptRunes. Sanitizing is idempotent, so a Provider may apply it
// early and the kernel again before persisting.
func UpstreamExcerpt(text string, maxRunes int, echoes ...string) string {
	if maxRunes <= 0 {
		maxRunes = UpstreamExcerptRunes
	}
	text = strings.ToValidUTF8(text, " ")
	text = stripEchoes(text, echoes)
	text = excerptURL.ReplaceAllString(text, "[url]")
	text = excerptToken.ReplaceAllString(text, "[redacted]")
	text = logging.Redact(text)
	text = excerptEmail.ReplaceAllString(text, "[email]")
	text = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > maxRunes {
		text = string(runes[:maxRunes])
	}
	return strings.TrimSpace(text)
}

// stripEchoes replaces each echo by [input]. A long echo that the upstream cut
// short is recognised by its leading echoPrefixRunes runes and removed as far
// as it keeps matching.
func stripEchoes(text string, echoes []string) string {
	for _, echo := range echoes {
		echo = strings.TrimSpace(echo)
		runes := []rune(echo)
		if len(runes) < echoMinimumRunes {
			continue
		}
		text = strings.ReplaceAll(text, echo, "[input]")
		if len(runes) <= echoPrefixRunes {
			continue
		}
		prefix := string(runes[:echoPrefixRunes])
		for {
			index := strings.Index(text, prefix)
			if index < 0 {
				break
			}
			matched := commonPrefixRunes(text[index:], runes)
			text = text[:index] + "[input]" + text[index+len(string(runes[:matched])):]
		}
	}
	return text
}

func commonPrefixRunes(text string, echo []rune) int {
	count := 0
	for _, r := range text {
		if count >= len(echo) || r != echo[count] {
			break
		}
		count++
	}
	return count
}

// InputEchoes lists the free-text values of a frozen Task input: the values an
// upstream may echo back in an error and that must not be persisted or logged
// from there. The parameters come from the Offering's own declaration (type
// text, or a prompt semantic), not from a hard-coded name list.
func InputEchoes(offering Offering, input json.RawMessage) []string {
	if len(input) == 0 {
		return nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return nil
	}
	var echoes []string
	for _, parameter := range offering.Parameters {
		if parameter.Type != "text" && parameter.Semantic != SemanticPrompt && parameter.Semantic != SemanticNegativePrompt {
			continue
		}
		raw, present := values[parameter.Name]
		if !present {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) == nil && text != "" {
			echoes = append(echoes, text)
		}
	}
	return echoes
}

// sanitizeFailureEvidence returns failure with its upstream excerpt passed
// through UpstreamExcerpt for the Task being run. The input failure is not
// modified; a failure without an excerpt is returned as is.
func sanitizeFailureEvidence(task Task, failure *Failure) *Failure {
	if failure == nil || failure.UpstreamExcerpt == "" {
		return failure
	}
	cleaned := *failure
	cleaned.UpstreamExcerpt = UpstreamExcerpt(failure.UpstreamExcerpt, UpstreamExcerptRunes, InputEchoes(task.Offering, task.Input)...)
	return &cleaned
}
