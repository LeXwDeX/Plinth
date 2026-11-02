package mock

import (
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// harnessContract is the part of a selected operation's tool contract the mock
// reads: when the person saved an operation in the Features settings, the
// server replaces the tool's parameters with a fixed offering and a closed
// parameters object (internal/harness/resolve toolParameters).
type harnessContract struct {
	Properties struct {
		OfferingID struct {
			Const string `json:"const"`
		} `json:"offering_id"`
		Parameters *struct {
			Properties map[string]struct {
				Type     string `json:"type"`
				Pattern  string `json:"pattern"`
				MaxItems int    `json:"maxItems"`
				Items    *struct {
					Pattern string `json:"pattern"`
				} `json:"items"`
			} `json:"properties"`
		} `json:"parameters"`
	} `json:"properties"`
}

// harnessCall turns a routed free-mode call into the selected operation's
// contract, like a real Agent reading the tool it was given (#187 D): the
// fixed offering, the prompt when the operation lets the Agent fill it, and
// the picture named in or uploaded with the message for each input that takes
// an image handle (the system context's example handles are not read; a list
// takes every picture named or uploaded, up to its limit). A turn
// without a picture leaves that input out, so the server names what is
// missing. Free-mode arguments never reach a Harness tool.
func (adapter *Adapter) harnessCall(request agents.Request, call plannedCall, text string) plannedCall {
	var tool *agents.Tool
	for index := range request.Tools {
		if request.Tools[index].Name == call.name {
			tool = &request.Tools[index]
		}
	}
	if tool == nil || len(tool.Parameters) == 0 {
		return call
	}
	var contract harnessContract
	if json.Unmarshal(tool.Parameters, &contract) != nil || contract.Properties.Parameters == nil || contract.Properties.OfferingID.Const == "" {
		return call
	}
	pictures := imageHandlePattern.FindAllString(text, -1)
	if uploads := adapter.uploadedHandles(request.Messages); len(pictures) == 0 && len(uploads) > 0 {
		pictures = uploads
	}
	picture := ""
	if len(pictures) > 0 {
		picture = pictures[0]
		if len(pictures) > 1 && imageHandlePattern.FindString(text) == "" {
			picture = pictures[len(pictures)-1]
		}
	}
	parameters := map[string]any{}
	for name, field := range contract.Properties.Parameters.Properties {
		handle := strings.HasPrefix(field.Pattern, "^@") || (field.Items != nil && strings.HasPrefix(field.Items.Pattern, "^@"))
		switch {
		case handle && picture != "" && field.Type == "array":
			list := []string{picture}
			if field.MaxItems > 1 {
				list = pictures
				if len(list) > field.MaxItems {
					list = list[len(list)-field.MaxItems:]
				}
			}
			parameters[name] = list
		case handle && picture != "":
			parameters[name] = picture
		case name == "prompt" && field.Type == "string":
			parameters[name] = text
		}
	}
	return plannedCall{name: call.name, arguments: map[string]any{"offering_id": contract.Properties.OfferingID.Const, "parameters": parameters}}
}
