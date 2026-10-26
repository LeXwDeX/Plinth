package mock

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// The in-chat choice card (#321) as the mock Agent plays it: a tool that opens
// a card closes the turn with a one-line question; an answer given on the card
// carries the exact next call, which the mock repeats; a typed reply is mapped
// to the open card's option by name, position or "default". The words below
// are a test vocabulary like the rest of the routing table.

type choiceItem struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Arguments json.RawMessage `json:"arguments"`
}

type choiceContinuation struct {
	ChoiceID string       `json:"choice_id"`
	Tool     string       `json:"tool"`
	Chosen   *choiceItem  `json:"chosen"`
	Options  []choiceItem `json:"options"`
	Default  *choiceItem  `json:"default"`
}

var (
	defaultChoicePattern = regexp.MustCompile(`默认|随便|都行|都可以|(?i)\bdefault\b|\bany\b`)
	ordinalChoicePattern = regexp.MustCompile(`第\s*([一二三四五六七八1-8])\s*个`)
	choiceTitleSplit     = regexp.MustCompile(`[\s·（）()，,、]+`)
)

// lastContinuation finds the last card JSON in a text.
func lastContinuation(text string) (choiceContinuation, bool) {
	start := strings.LastIndex(text, `{"choice_id"`)
	if start < 0 {
		return choiceContinuation{}, false
	}
	var value choiceContinuation
	if err := json.NewDecoder(strings.NewReader(text[start:])).Decode(&value); err != nil || value.Tool == "" {
		return choiceContinuation{}, false
	}
	return value, true
}

func choiceCall(tool string, arguments json.RawMessage) ([]plannedCall, bool) {
	var values map[string]any
	if json.Unmarshal(arguments, &values) != nil || values == nil {
		return nil, false
	}
	return []plannedCall{{name: tool, arguments: values}}, true
}

// choiceCalls returns the call that continues a card, if this turn answers one.
func (adapter *Adapter) choiceCalls(messages []agents.Message) ([]plannedCall, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != adapter.userRole {
			continue
		}
		text := message.Content
		for _, part := range message.Parts {
			text += "\n" + part.Text
		}
		if answered, ok := lastContinuation(text); ok && answered.Chosen != nil {
			return choiceCall(answered.Tool, answered.Chosen.Arguments)
		}
		break
	}
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role == adapter.userRole || message.Role == adapter.toolRole {
			continue
		}
		pending, ok := lastContinuation(message.Content)
		if !ok || len(pending.Options) == 0 {
			continue
		}
		reply := strings.ToLower(adapter.latestUserText(messages))
		if pending.Default != nil && defaultChoicePattern.MatchString(reply) {
			return choiceCall(pending.Tool, pending.Default.Arguments)
		}
		if match := ordinalChoicePattern.FindStringSubmatch(reply); match != nil {
			position := strings.Index("一二三四五六七八", match[1]) / len("一")
			if digit := strings.Index("12345678", match[1]); digit >= 0 {
				position = digit
			}
			if position >= 0 && position < len(pending.Options) {
				return choiceCall(pending.Tool, pending.Options[position].Arguments)
			}
		}
		if option, ok := optionNamedIn(pending.Options, reply); ok {
			return choiceCall(pending.Tool, option.Arguments)
		}
		return nil, false
	}
	return nil, false
}

// optionNamedIn picks the option whose distinctive title words the reply
// uses: words that no other option's title has.
func optionNamedIn(options []choiceItem, reply string) (choiceItem, bool) {
	words := make([]map[string]bool, len(options))
	counts := map[string]int{}
	for index, option := range options {
		words[index] = map[string]bool{}
		for _, word := range choiceTitleSplit.Split(strings.ToLower(option.Title), -1) {
			if len([]rune(word)) >= 2 && !words[index][word] {
				words[index][word] = true
				counts[word]++
			}
		}
	}
	found, matches := choiceItem{}, 0
	for index, option := range options {
		for word := range words[index] {
			if counts[word] == 1 && strings.Contains(reply, word) {
				found = option
				matches++
				break
			}
		}
	}
	return found, matches == 1
}

// choiceOpened reports whether a tool result of this turn opened a card.
func choiceOpened(content string) bool {
	var result struct {
		ChoiceRequired bool `json:"choice_required"`
	}
	return json.Unmarshal([]byte(content), &result) == nil && result.ChoiceRequired
}
