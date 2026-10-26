package generationtool

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// referencePurposes renders what each reference picture is for (#279), from
// the tool's purposes argument, as the {purposes} text of the Schema template.
// Each key is a handle; it must name one of the pictures the call really
// sends, after the canvas snapshot is put first and the Offering's limit is
// applied, so the numbering in the prompt matches the order of the frozen
// references. Anything else is refused before submission with the Schema's
// wording. An absent or empty argument yields no text.
func (executor *Executor) referencePurposes(
	command chat.ToolCommand,
	arguments map[string]json.RawMessage,
	referenceURLs []string,
) (string, string) {
	argument := command.Tool.ReferencePurposesArgument
	raw, present := arguments[argument]
	if argument == "" || !present || string(raw) == "null" {
		return "", ""
	}
	refuse := func(handle string) (string, string) {
		return "", strings.ReplaceAll(executor.chat.Context.ReferencePurposeInvalidTemplate, "{handle}", handle)
	}
	var purposes map[string]string
	if err := json.Unmarshal(raw, &purposes); err != nil {
		return refuse(argument)
	}
	type item struct {
		position int
		handle   string
		purpose  string
	}
	var items []item
	policy := executor.handlePolicy()
	for handle, purpose := range purposes {
		purpose = strings.TrimSpace(purpose)
		if purpose == "" {
			continue
		}
		if policy.IsSelectionAlias(handle) {
			// The alias names several pictures; one purpose cannot be placed.
			return refuse(handle)
		}
		resolved, err := policy.ResolveHandles(command.History, command.Options.Selection, []string{handle}, chat.AttachmentKindImage)
		var unknown *chat.UnknownHandleError
		if err != nil && !errors.As(err, &unknown) {
			return "", err.Error()
		}
		if err != nil || len(resolved) != 1 {
			return refuse(handle)
		}
		position := slices.Index(referenceURLs, resolved[0].URL)
		if position < 0 || resolved[0].URL == "" {
			return refuse(handle)
		}
		items = append(items, item{position: position + 1, handle: resolved[0].Attachment.Handle, purpose: purpose})
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].position != items[right].position {
			return items[left].position < items[right].position
		}
		return items[left].handle < items[right].handle
	})
	var text strings.Builder
	for _, entry := range items {
		text.WriteString(strings.NewReplacer(
			"{position}", strconv.Itoa(entry.position),
			"{purpose}", entry.purpose,
		).Replace(command.Tool.ReferencePurposeItemTemplate))
	}
	return text.String(), ""
}

// promptWithPurposes puts the reference purposes in front of the Agent's
// prompt with the tool's template. It replaces the reference and previous
// templates: once each picture has a stated role, "edit the reference image"
// would tell the model to keep the first picture whole.
func promptWithPurposes(command chat.ToolCommand, purposes, prompt string) string {
	return strings.NewReplacer("{purposes}", purposes, "{prompt}", prompt).
		Replace(command.Tool.PromptWithReferencePurposesTemplate)
}
