// Package dsml reads DeepSeek-style tool calls that a model writes into its
// reply text. The accepted spellings come from Product Schema chat.dsml: a
// list of markers, a list of names for the outer element and whether
// whitespace may separate a marker from the element name (#275). Parse and
// the streaming Filter compile the same grammar, so a tag the parser reads is
// exactly a tag the filter holds back from the person.
package dsml

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type Result struct {
	Content   string
	ToolCalls []agents.ToolCall
}

func Parse(schema catalog.ChatDSMLSchema, content string) Result {
	if !schema.Enabled {
		return Result{Content: content}
	}
	syntax := compile(schema)
	start, length := syntax.find(content, false)
	if start < 0 {
		return Result{Content: content}
	}
	result := Result{Content: strings.TrimSpace(content[:start])}
	block := content[start+length:]
	if end, _ := syntax.find(block, true); end >= 0 {
		block = block[:end]
	}
	for index, invocation := range syntax.invoke.FindAllStringSubmatch(
		block,
		-1,
	) {
		arguments := map[string]any{}
		for _, parameter := range syntax.parameter.FindAllStringSubmatch(
			invocation[2],
			-1,
		) {
			switch parameter[2] {
			case schema.TrueValue:
				arguments[parameter[1]] = parameter[3]
			case schema.FalseValue:
				var value any
				if err := json.Unmarshal(
					[]byte(parameter[3]),
					&value,
				); err != nil {
					value = parameter[3]
				}
				arguments[parameter[1]] = value
			}
		}
		encoded, err := json.Marshal(arguments)
		if err != nil {
			continue
		}
		result.ToolCalls = append(result.ToolCalls, agents.ToolCall{
			ID: strings.ReplaceAll(
				schema.CallIDTemplate,
				"{index}",
				fmt.Sprint(index),
			),
			Name:      invocation[1],
			Arguments: encoded,
		})
	}
	return result
}

// Filter streams reply text to the person and stops at the first opening tag
// of a tool-call block. Text that could still turn into such a tag is held
// back until the next chunk decides it; any other text passes straight on.
type Filter struct {
	enabled    bool
	syntax     grammar
	buffer     string
	suppressed bool
}

func NewFilter(schema catalog.ChatDSMLSchema) *Filter {
	filter := &Filter{enabled: schema.Enabled}
	if schema.Enabled {
		filter.syntax = compile(schema)
	}
	return filter
}

func (filter *Filter) Push(
	chunk string,
	emit func(string) error,
) error {
	if chunk == "" {
		return nil
	}
	if !filter.enabled {
		return emit(chunk)
	}
	if filter.suppressed {
		return nil
	}
	filter.buffer += chunk
	held := len(filter.buffer)
	for offset := 0; offset < len(filter.buffer); {
		next := strings.IndexByte(filter.buffer[offset:], '<')
		if next < 0 {
			break
		}
		position := offset + next
		_, state := filter.syntax.match(filter.buffer[position:], false)
		if state == matchFull {
			if position > 0 {
				if err := emit(filter.buffer[:position]); err != nil {
					return err
				}
			}
			filter.buffer = ""
			filter.suppressed = true
			return nil
		}
		if state == matchPartial {
			held = position
			break
		}
		offset = position + 1
	}
	if held > 0 {
		if err := emit(filter.buffer[:held]); err != nil {
			return err
		}
		filter.buffer = filter.buffer[held:]
	}
	return nil
}

func (filter *Filter) Flush(emit func(string) error) error {
	if !filter.suppressed && filter.buffer != "" {
		if err := emit(filter.buffer); err != nil {
			return err
		}
	}
	filter.buffer = ""
	return nil
}

type matchState int

const (
	matchNone matchState = iota
	matchPartial
	matchFull
)

type grammar struct {
	markers    []string
	elements   []string
	whitespace bool
	invoke     *regexp.Regexp
	parameter  *regexp.Regexp
}

func compile(schema catalog.ChatDSMLSchema) grammar {
	quoted := make([]string, 0, len(schema.Markers))
	for _, marker := range schema.Markers {
		quoted = append(quoted, regexp.QuoteMeta(marker))
	}
	marker := `(?:` + strings.Join(quoted, "|") + `)`
	if schema.MarkerWhitespace {
		marker += `\s*`
	}
	invokeTag := marker + regexp.QuoteMeta(schema.InvokeElement)
	parameterTag := marker + regexp.QuoteMeta(schema.ParameterElement)
	name := regexp.QuoteMeta(schema.NameAttribute)
	stringAttribute := regexp.QuoteMeta(schema.StringAttribute)
	return grammar{
		markers:    schema.Markers,
		elements:   schema.ToolCallsElements,
		whitespace: schema.MarkerWhitespace,
		invoke: regexp.MustCompile(
			`(?s)<` + invokeTag + `\s+` + name +
				`="([^"]+)"\s*>(.*?)</` + invokeTag + `\s*>`,
		),
		parameter: regexp.MustCompile(
			`(?s)<` + parameterTag + `\s+` + name +
				`="([^"]+)"\s+` + stringAttribute +
				`="([^"]+)"\s*>(.*?)</` + parameterTag + `\s*>`,
		),
	}
}

// find returns the position and length of the first complete opening (or
// closing) tool-call tag in value, or -1.
func (syntax grammar) find(value string, closing bool) (int, int) {
	for offset := 0; offset < len(value); {
		next := strings.IndexByte(value[offset:], '<')
		if next < 0 {
			return -1, 0
		}
		position := offset + next
		if length, state := syntax.match(value[position:], closing); state == matchFull {
			return position, length
		}
		offset = position + 1
	}
	return -1, 0
}

// match reports whether value starts with a tool-call tag: matchFull with the
// tag length, matchPartial when value ends before the tag could be decided,
// or matchNone.
func (syntax grammar) match(value string, closing bool) (int, matchState) {
	opening := "<"
	if closing {
		opening = "</"
	}
	if !strings.HasPrefix(value, opening) {
		if strings.HasPrefix(opening, value) {
			return 0, matchPartial
		}
		return 0, matchNone
	}
	best := matchNone
	for _, marker := range syntax.markers {
		rest := value[len(opening):]
		if !strings.HasPrefix(rest, marker) {
			if strings.HasPrefix(marker, rest) {
				best = matchPartial
			}
			continue
		}
		at := len(opening) + len(marker)
		if syntax.whitespace {
			at = skipSpace(value, at)
		}
		for _, element := range syntax.elements {
			rest := value[at:]
			if !strings.HasPrefix(rest, element) {
				if strings.HasPrefix(element, rest) {
					best = matchPartial
				}
				continue
			}
			end := skipSpace(value, at+len(element))
			if end == len(value) {
				best = matchPartial
				continue
			}
			if value[end] == '>' {
				return end + 1, matchFull
			}
		}
	}
	return 0, best
}

// skipSpace skips the whitespace that the regular expression class \s
// accepts, so the hand matcher and the invoke/parameter patterns agree.
func skipSpace(value string, at int) int {
	for at < len(value) && strings.IndexByte("\t\n\f\r ", value[at]) >= 0 {
		at++
	}
	return at
}
