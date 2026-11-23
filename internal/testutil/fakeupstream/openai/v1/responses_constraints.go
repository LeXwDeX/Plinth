package openaiv1

import (
	"net/http"
	"strings"
)

// Model constraints on /v1/responses (#401). The model's rules are those of
// constraints.go; the Responses wire answers them in its own words, which are
// all observed on 2026-11-20 (the sample's error exchanges, byte for byte, the
// gateway's requestid replaced):
//   - any temperature, 0 included, is refused with "Unsupported parameter:
//     'temperature' is not supported with this model." (the Chat Completions
//     answer only refuses a value other than the default 1);
//   - a reasoning.effort that is no level the gateway knows is refused as an
//     Invalid value, with the list of the levels it knows (none and minimal
//     among them), code invalid_value;
//   - none and minimal, which the gateway knows, are refused as Unsupported
//     values for the model, which the message names with its dated snapshot
//     (gpt-6.2-astra-2026-11-17) and lists the levels it supports (low, medium,
//     high, xhigh, max), code unsupported_value. The Chat Completions wording
//     of the none refusal (constraints.go) is still the constructed one.
//
// The function tools that Chat Completions refuses for these models are
// accepted here: this is the wire they are served on.

// observedEfforts are the levels the gateway's refusal of an unknown level
// lists as known.
var observedEfforts = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

// responsesModelRefusal is the refusal the request earns from the rules of its
// model, if any: the status and the body text.
func (h *handler) responsesModelRefusal(request responsesRequest) (int, string, bool) {
	rule, constrained := constrainedModels[strings.ToLower(strings.TrimSpace(request.Model))]
	if !constrained {
		return 0, "", false
	}
	effort := request.effort()
	switch {
	case effort != "" && !observedEfforts[effort]:
		return http.StatusBadRequest, refusalText(h.responses.effortRefusal, func(message string) string {
			return strings.Replace(message, "'bogus'", "'"+effort+"'", 1)
		}), true
	case effort == "none" && rule.noEffortNone:
		return http.StatusBadRequest, h.responses.noneRefusal, true
	case effort == "minimal" && rule.noEffortMinimal:
		return http.StatusBadRequest, h.responses.minimalRefusal, true
	// Only a model whose own rule is the default temperature refuses one here;
	// the others keep the temperature the request carries.
	case rule.defaultTemperatureOnly && presentJSON(request.Temperature):
		return http.StatusBadRequest, h.responses.temperatureRefusal, true
	}
	return 0, "", false
}
