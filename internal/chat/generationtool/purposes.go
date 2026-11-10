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
// sends, after input ordering and the Offering's limit are applied, so the
// numbering in the prompt matches the order of the frozen
// references. Anything else is refused before submission with the Schema's
// wording. An absent or empty argument yields no text.
func (executor *Executor) referencePurposes(
	command chat.ToolCommand,
	arguments map[string]json.RawMessage,
	referenceURLs []string,
	serverPurposes map[string]string,
) (string, string) {
	argument := command.Tool.ReferencePurposesArgument
	raw, present := arguments[argument]
	if argument == "" {
		return "", ""
	}
	refuse := func(handle string) (string, string) {
		return "", strings.ReplaceAll(executor.chat.Context.ReferencePurposeInvalidTemplate, "{handle}", handle)
	}
	purposes := map[string]string{}
	for handle, purpose := range command.Options.AttachmentPurposes {
		text := executor.chat.AttachmentPurposes[purpose].AgentText
		if text == "" {
			return refuse(handle)
		}
		purposes[handle] = text
	}
	if present && string(raw) != "null" {
		var explicit map[string]string
		if err := json.Unmarshal(raw, &explicit); err != nil {
			return refuse(argument)
		}
		for handle, text := range explicit {
			purposes[handle] = text
		}
	}
	type item struct {
		position int
		handle   string
		purpose  string
	}
	// Server-generated purposes (#250) name an identity or style picture the
	// call did not select by handle. Their position is the final reference
	// order, so the numbering matches the frozen pictures. A purpose the Agent
	// states for the same picture replaces the server wording.
	byPosition := map[int]item{}
	for referenceURL, purpose := range serverPurposes {
		purpose = strings.TrimSpace(purpose)
		if purpose == "" {
			continue
		}
		position := slices.Index(referenceURLs, referenceURL)
		if position < 0 || slices.Contains(referenceURLs[position+1:], referenceURL) {
			continue
		}
		byPosition[position] = item{position: position + 1, purpose: purpose}
	}
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
		resolved, err := policy.ResolveHandles(command.ReferenceHistory(), command.Options.Selection, []string{handle}, chat.AttachmentKindImage)
		var unknown *chat.UnknownHandleError
		if err != nil && !errors.As(err, &unknown) {
			return "", err.Error()
		}
		if err != nil || len(resolved) != 1 {
			return refuse(handle)
		}
		position := slices.Index(referenceURLs, resolved[0].URL)
		if position < 0 || resolved[0].URL == "" || slices.Contains(referenceURLs[position+1:], resolved[0].URL) {
			return refuse(handle)
		}
		entry := item{position: position + 1, handle: resolved[0].Attachment.Handle, purpose: purpose}
		byPosition[position] = entry
	}
	items := make([]item, 0, len(byPosition))
	for _, entry := range byPosition {
		items = append(items, entry)
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
