package openaiv1

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Model constraints on /v1/responses (#401). The model's rules are those of
// constraints.go; the Responses wire answers them in its own words.
//
// Evidence, kept apart as there:
//   - Observed on 2026-11-20 (the sample's error-temperature and
//     error-effort exchanges, byte for byte): any temperature, 0 included, is
//     refused with "Unsupported parameter: 'temperature' is not supported with
//     this model." (the Chat Completions answer only refuses a value other
//     than the default 1); a reasoning.effort that is no level the gateway
//     knows is refused with the list of the levels it supports.
//   - Constructed from the OpenAI documents (the latest-model guide read the
//     same day): reasoning.effort none is refused for these models, in the
//     wording of the Chat Completions answer with param reasoning.effort.
//
// The function tools that Chat Completions refuses for these models are
// accepted here: this is the wire they are served on.

// observedEfforts are the levels the gateway's refusal of an unknown level
// lists as supported.
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
	if effort != "" && !observedEfforts[effort] {
		return http.StatusBadRequest, refusalText(h.responses.effortRefusal, func(message string) string {
			return strings.Replace(message, "'bogus'", "'"+effort+"'", 1)
		}), true
	}
	if rule.noEffortNone && effort == "none" {
		raw, _ := json.Marshal(newConstraintRefusal(
			"Unsupported value: 'reasoning.effort' does not support 'none' with this model.",
			"reasoning.effort", "unsupported_value"))
		return http.StatusBadRequest, string(raw), true
	}
	if presentJSON(request.Temperature) {
		return http.StatusBadRequest, h.responses.temperatureRefusal, true
	}
	return 0, "", false
}
