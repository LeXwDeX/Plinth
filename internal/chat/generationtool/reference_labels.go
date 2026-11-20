package generationtool

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// referenceImageLabels turns the tool's marks argument (#376), a map from an
// image handle to the side that picture shows, into the list the chosen
// Offering's reference-image-labels parameter holds: one mark per picture, in
// the order the pictures are really sent. It runs after the final reference
// order is fixed (anchors, three-view replacement, de-duplication and the
// model's limit applied), so each mark follows its picture whatever order the
// Agent wrote the handles in, the way referencePurposes numbers its items.
//
//   - Nothing is written when the argument is absent, null or empty, when no
//     picture gets a mark other than the unknown one, or when the Offering
//     declares no reference-image-labels parameter (the argument is then
//     ignored without a refusal, as a seed the Offering lacks is dropped): the
//     frozen input, and so its hash, stay what they were without the argument.
//   - Otherwise the list holds exactly one mark per final picture and a picture
//     the Agent did not mark gets the tool's unknown mark. A list the Offering
//     could not complete that way (it declares no unknown mark) is not written.
//   - A key naming a picture of this session that this call does not send (not
//     in the references, cut by the model's limit, replaced by a three-view
//     group's front view) is dropped: marks are hints and those cuts are the
//     server's, made after the Agent wrote them. Unlike a reference purpose,
//     which is an instruction the person expects carried out, a dropped mark
//     changes nothing that is sent.
//   - Refused before submission, with the Schema's wording: a key that is not
//     an image of this session, the whole-selection alias, a picture that
//     appears twice in the final list (its place is ambiguous), two keys that
//     name one sent picture with different marks (two with the same mark are
//     one mark; for a picture that is not sent both are dropped), and a mark
//     outside the Offering's declared options. An empty mark is no mark.
func (executor *Executor) referenceImageLabels(
	command chat.ToolCommand,
	arguments map[string]json.RawMessage,
	offering generation.Offering,
	referenceURLs []string,
) ([]string, string) {
	argument := command.Tool.ReferenceImageLabelsArgument
	raw, given := arguments[argument]
	if argument == "" || !given || string(raw) == "null" {
		return nil, ""
	}
	parameter, takesMarks := semanticParameter(offering, generation.SemanticReferenceImageLabels)
	if !takesMarks {
		return nil, ""
	}
	context := executor.chat.Context
	refusePicture := func(handle string) ([]string, string) {
		return nil, strings.ReplaceAll(context.ReferenceLabelPictureTemplate, "{handle}", handle)
	}
	refuseValue := func(handle string) ([]string, string) {
		return nil, strings.NewReplacer("{handle}", handle, "{marks}", strings.Join(optionValues(parameter), ", ")).
			Replace(context.ReferenceLabelValueTemplate)
	}
	var byHandle map[string]string
	if err := json.Unmarshal(raw, &byHandle); err != nil {
		return refuseValue(argument)
	}
	// Sorted keys make the refusal, when there are several faults, the same
	// whichever order the Agent wrote the object in.
	handles := make([]string, 0, len(byHandle))
	for handle := range byHandle {
		handles = append(handles, handle)
	}
	sort.Strings(handles)
	policy := executor.handlePolicy()
	placed := map[int]string{}
	for _, key := range handles {
		mark, handle := strings.TrimSpace(byHandle[key]), strings.TrimSpace(key)
		if mark == "" {
			continue
		}
		if !slices.Contains(optionValues(parameter), mark) {
			return refuseValue(key)
		}
		if handle == "" || policy.IsSelectionAlias(handle) {
			// The alias names several pictures; one mark cannot be placed.
			return refusePicture(key)
		}
		urls, err := executor.resolveImageHandles(command, []string{handle})
		if err != nil || len(urls) != 1 || urls[0] == "" {
			return refusePicture(key)
		}
		position := slices.Index(referenceURLs, urls[0])
		if position < 0 {
			// A picture of this session that is not sent (not a reference of the
			// call, cut by the model's limit, or replaced by its three-view
			// group's front view): its mark has nothing to describe. The server
			// decides those cuts after the Agent wrote the marks, so they are
			// dropped rather than refused, which would only cost a retry.
			continue
		}
		if slices.Contains(referenceURLs[position+1:], urls[0]) {
			return refusePicture(key)
		}
		if previous, marked := placed[position]; marked && previous != mark {
			return refusePicture(key)
		}
		placed[position] = mark
	}
	unknown := command.Tool.ReferenceImageLabelUnknown
	definite := false
	for _, mark := range placed {
		definite = definite || mark != unknown
	}
	if !definite {
		// Every picture is unmarked or unknown: the marks say nothing the request
		// without them does not, so none is sent.
		return nil, ""
	}
	if len(placed) < len(referenceURLs) && !slices.Contains(optionValues(parameter), unknown) {
		return nil, ""
	}
	labels := make([]string, len(referenceURLs))
	for position := range labels {
		if mark, marked := placed[position]; marked {
			labels[position] = mark
		} else {
			labels[position] = unknown
		}
	}
	return labels, ""
}

// optionValues are the values a parameter declares, in declaration order.
func optionValues(parameter generation.Parameter) []string {
	values := make([]string, 0, len(parameter.Options))
	for _, option := range parameter.Options {
		values = append(values, option.Value)
	}
	return values
}
