package chat

import "strings"

// The system prompt is assembled from named sections in a fixed order (#248).
// The order is code, not map iteration, so two turns with the same facts get
// the same prompt byte for byte; a section that renders empty is left out. The
// ledger and anchor sections are computed from stored facts rather than from
// the message window, so compaction cannot move or lose them.
type promptSection struct {
	name string
	text string
}

type promptSectionList struct {
	sections []promptSection
}

func (list *promptSectionList) add(name, text string) {
	if text == "" {
		return
	}
	list.sections = append(list.sections, promptSection{name: name, text: text})
}

func (list *promptSectionList) render() string {
	var builder strings.Builder
	for _, section := range list.sections {
		builder.WriteString(section.text)
	}
	return builder.String()
}

func (list *promptSectionList) names() []string {
	names := make([]string, 0, len(list.sections))
	for _, section := range list.sections {
		names = append(names, section.name)
	}
	return names
}
