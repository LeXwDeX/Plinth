package openaiv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The fake-only request conventions of POST /responses, on top of the Chat
// Completions ones (tool markers, script markers, the system-contains question
// and StreamErrorMarker), which a Responses request is read through
// (responsesRequest.chatView). The real model has none of them; they let a
// test or a fake stack ask for an answer a faithful fake never gives by
// itself. They are read in the last user message.
const (
	// ReasoningMarker adds a reasoning item (with encrypted_content when the
	// request includes reasoning.encrypted_content) ahead of the answer, and
	// reasoning and cached token counts to the usage. The real model returned
	// one at effort xhigh (sample reasoning-tool-stream, observed): the item
	// has content and summary as empty arrays, an id, encrypted_content and no
	// status; response.output_item.added already carries a shorter
	// encrypted_content than response.output_item.done, and the item of
	// response.completed a third value; there is no summary event when no
	// summary was requested. The fake gives those three values (the Fixed
	// constants below); the summary events and the usage counts are not from a
	// sample.
	ReasoningMarker = "[[fake:reasoning]]"
	// IncompleteMarker cuts the answer: the stream ends with
	// response.incomplete (reason max_output_tokens) before any item is done,
	// and a non-stream answer has status incomplete. Constructed.
	IncompleteMarker = "[[fake:incomplete]]"
	// FailedMarker ends the response with status failed: response.failed in a
	// stream, a 200 body with status failed otherwise. Constructed.
	FailedMarker = "[[fake:failed]]"
	// UnknownEventMarker puts an event of a type no client knows into the
	// stream, and an unknown member into the response object.
	UnknownEventMarker = "[[fake:unknown-event]]"
	// RefusalMarker answers with the model's refusal instead of the text: the
	// message item's content part is {type: refusal, refusal}, streamed as
	// response.refusal.delta events (content_index, delta, item_id,
	// output_index, sequence_number) and one response.refusal.done (the same
	// members with refusal instead of delta), between the content part's added
	// and done events. Constructed from the official streaming events (the
	// openai-python ResponseRefusalDeltaEvent and ResponseRefusalDoneEvent
	// types, checked 2026-11-25); no sample has a refusal, so the refusal
	// part's added event and its wording are the fake's. A response with
	// function calls has no message item and so no refusal.
	RefusalMarker = "[[fake:refusal]]"
)

// FixedRefusal is the text of the RefusalMarker answer.
const FixedRefusal = "I'm sorry, but I can't help with that."

// The fixed encrypted_content values of the fake's reasoning item, in the form
// the real ones take (a token that begins gAAAAAB). The real gateway sent three
// different values for one item: a shorter one with response.output_item.added,
// the whole one with response.output_item.done and another of the done one's
// length in the output of response.completed; a client that sends the item
// back must take the done one (the replay of it was accepted).
const (
	FixedEncryptedPartial = "gAAAAABrFAKE_partial_token"
	FixedEncryptedDone    = "gAAAAABrFAKE_done_token_complete"
	FixedEncryptedFinal   = "gAAAAABrFAKE_final_token_complete"
)

const incompleteReason = "max_output_tokens"

// fakeItem is one output item of a planned response.
type fakeItem struct {
	kind      string // reasoning, message or function_call
	index     int    // output_index
	id        string
	callID    string
	name      string
	arguments string
	text      string
	// encrypted: a reasoning item carries encrypted_content. summary: it
	// carries a summary part (the request asked for one).
	encrypted bool
	summary   bool
	// refusal: a message item whose content part is a refusal (RefusalMarker)
	// and whose text is the refusal text.
	refusal bool
	// finalPadding pads the encrypted_content of a reasoning item's final
	// object (the output of response.completed) to that many bytes
	// (EmptyCompletionMarker).
	finalPadding int
}

type itemStage int

const (
	stageAdded itemStage = iota
	// stageDone is the item of its own done event; stageFinal the same item in
	// the output of response.completed or of a non-stream answer.
	stageDone
	stageFinal
	stageCut
)

// responsesPlan is what one request is answered with.
type responsesPlan struct {
	items []fakeItem
	// status is completed, incomplete or failed.
	status       string
	reasoning    bool
	unknownEvent bool
	streamError  bool
	// selfImage: the answer is a picture the model drew itself
	// (SelfImageMarker); hold is the wait before its .completed event, cut
	// short when done closes (the client went away).
	selfImage bool
	hold      time.Duration
	done      <-chan struct{}
}

func (h *handler) createResponse(w http.ResponseWriter, r *http.Request) {
	if fakeupstream.BearerKey(r) == "" {
		unauthorized(w)
		return
	}
	scenario := fakeupstream.ScenarioOf(r)
	if h.documentedFault(w, scenario) {
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
		return
	}
	var request responsesRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, officialErrorBody("We could not parse the JSON body of your request. The body may be malformed or contain unsupported characters.", "invalid_request_error", nil, nil))
		return
	}
	if strings.TrimSpace(request.Model) == "" {
		writeJSON(w, http.StatusBadRequest, officialErrorBody("Missing required parameter: 'model'.", "invalid_request_error", "model", "missing_required_parameter"))
		return
	}
	items, ok := request.inputItems()
	if !ok {
		writeJSON(w, http.StatusBadRequest, officialErrorBody("Missing required parameter: 'input'.", "invalid_request_error", "input", "missing_required_parameter"))
		return
	}
	chat := request.chatView(items)
	h.recordChatRequest(chat)
	if scenario == fakeupstream.ScenarioModelNotFound {
		writeJSON(w, http.StatusNotFound, officialErrorBody(fmt.Sprintf("The model %q does not exist or you do not have access to it.", request.Model), "invalid_request_error", "model", "model_not_found"))
		return
	}
	// #640: the gateway answers a muted target itself, before any model.
	if chat.gatewayMute() {
		h.writeGatewayMute(w, r)
		return
	}
	// #401: a model with declared constraints refuses a request shape it does
	// not accept before any response runs, streamed or not.
	if status, body, refused := h.responsesModelRefusal(request); refused {
		writeRaw(w, status, body)
		return
	}
	if refusal, refused := h.responses.pairingRefusal(items); refused {
		writeRaw(w, http.StatusBadRequest, refusal)
		return
	}
	// #536: a quota marker and the pictures of the request, as on Chat
	// Completions, in the Responses envelope.
	if code, asked := chat.quotaFault(); asked {
		h.writeQuotaFault(w, code)
		return
	}
	if kind, message, refused := chat.imageRefusal(); refused {
		writeJSON(w, imageRefusalStatus, responsesImageRefusalBody(kind, message))
		return
	}
	if scenario == fakeupstream.ScenarioLostResponse {
		fakeupstream.AbortConnection(w) // the response ran; its answer is lost
		return
	}
	number := h.responses.nextResponseNumber()
	plan := h.planResponse(request, chat, items, number)
	if err := h.applyObserved(r, request, chat, &plan, number); err != nil {
		writeJSON(w, http.StatusInternalServerError, officialErrorBody(err.Error(), "server_error", nil, nil))
		return
	}
	if request.Stream {
		h.writeResponsesStream(w, request, plan, number)
		return
	}
	switch plan.status {
	case "failed":
		writeJSON(w, http.StatusOK, h.responseObject(request, plan, "failed", nil, number))
	default:
		writeJSON(w, http.StatusOK, h.responseObject(request, plan, plan.status, h.outputObjects(plan, stageFinal), number))
	}
}

// planResponse decides the answer: the calls the Chat Completions conventions
// ask for, else the fixed text, and the fake-only conventions above.
func (h *handler) planResponse(request responsesRequest, chat chatRequest, items []inputItem, number int64) responsesPlan {
	calls := chat.calls()
	scriptedCalls, scripted := chat.scriptCalls()
	if scripted {
		calls = scriptedCalls
	}
	reply := FixedReply
	if !scripted {
		if needle, asked := chat.systemContainsAsk(); asked {
			reply = "absent: " + needle
			if chat.systemContains(needle) {
				reply = "present: " + needle
			}
		}
	}
	if echo, asked := chat.toolResultsEcho(); asked && len(calls) == 0 {
		reply = echo
	}
	last := chat.lastText()
	plan := responsesPlan{
		status:       "completed",
		reasoning:    strings.Contains(last, ReasoningMarker),
		unknownEvent: strings.Contains(last, UnknownEventMarker),
		streamError:  request.Stream && strings.Contains(last, StreamErrorMarker),
	}
	switch {
	case strings.Contains(last, FailedMarker):
		plan.status = "failed"
		return plan
	case strings.Contains(last, IncompleteMarker):
		plan.status = "incomplete"
	}
	existing := 0
	for _, item := range items {
		if item.kind() == "function_call" {
			existing++
		}
	}
	// The identifiers have the real shapes: a prefix and 50 characters.
	if plan.reasoning {
		plan.items = append(plan.items, fakeItem{kind: "reasoning", id: placeholderID("rs_", 50, number),
			encrypted: request.includes("reasoning.encrypted_content"), summary: request.summaryRequested()})
	}
	if len(calls) == 0 {
		message := fakeItem{kind: "message", id: placeholderID("msg_", 50, number), text: reply}
		if strings.Contains(last, RefusalMarker) {
			message.text, message.refusal = FixedRefusal, true
		}
		plan.items = append(plan.items, message)
	}
	for index, call := range calls {
		plan.items = append(plan.items, fakeItem{kind: "function_call", id: placeholderID("fc_", 50, number*100+int64(index)),
			callID: fmt.Sprintf("call_fake_%d", existing+index), name: call.name, arguments: call.arguments})
	}
	for index := range plan.items {
		plan.items[index].index = index
	}
	return plan
}

// itemObject is an item of response.output or of an output_item event.
func (h *handler) itemObject(item fakeItem, stage itemStage) map[string]any {
	switch item.kind {
	case "function_call":
		object := fakeupstream.Clone(h.responses.callEvent("response.output_item.done")["item"].(map[string]any))
		object["id"], object["call_id"], object["name"] = item.id, item.callID, item.name
		object["arguments"], object["status"] = item.arguments, "completed"
		switch stage {
		case stageAdded:
			object["arguments"], object["status"] = "", "in_progress"
		case stageCut:
			object["arguments"], object["status"] = firstHalf(item.arguments), "incomplete"
		}
		return object
	case "message":
		object := fakeupstream.Clone(h.responses.textEvent("response.output_item.done")["item"].(map[string]any))
		object["id"] = item.id
		object["status"] = "completed"
		text := item.text
		switch stage {
		case stageAdded:
			object["content"], object["status"] = []any{}, "in_progress"
			return object
		case stageCut:
			text, object["status"] = firstHalf(item.text), "incomplete"
		}
		if item.refusal {
			object["content"] = []any{refusalPart(text)}
			return object
		}
		if parts, _ := object["content"].([]any); len(parts) > 0 {
			if part, _ := parts[0].(map[string]any); part != nil {
				part["text"] = text
			}
		}
		return object
	case imageGenerationKind:
		return imageGenerationObject(item, stage)
	default:
		// The reasoning item of the sample (observed at effort xhigh): content
		// and summary as empty arrays, id, encrypted_content, type and no
		// status. With no include the token is left out (not observed). The
		// summary part of a request that asked for a summary is from the
		// documents.
		object := fakeupstream.Clone(h.responses.reasoningEvent("response.output_item.done")["item"].(map[string]any))
		object["id"] = item.id
		delete(object, "encrypted_content")
		if item.encrypted {
			switch stage {
			case stageAdded:
				object["encrypted_content"] = FixedEncryptedPartial
			case stageFinal:
				object["encrypted_content"] = FixedEncryptedFinal
				if item.finalPadding > 0 {
					object["encrypted_content"] = paddedEncrypted(item.finalPadding)
				}
			default:
				object["encrypted_content"] = FixedEncryptedDone
			}
		}
		if item.summary && stage != stageAdded {
			object["summary"] = []any{map[string]any{"type": "summary_text", "text": FixedReasoning}}
		}
		return object
	}
}

// refusalPart is the content part of a refused answer (the official
// ResponseOutputRefusal: type refusal and the refusal text).
func refusalPart(text string) map[string]any {
	return map[string]any{"type": "refusal", "refusal": text}
}

// firstHalf is the first half of text in runes: what a cut answer got to.
func firstHalf(text string) string {
	runes := []rune(text)
	return string(runes[:len(runes)/2])
}

func (h *handler) outputObjects(plan responsesPlan, stage itemStage) []any {
	if plan.status == "incomplete" {
		stage = stageCut
	}
	output := make([]any, 0, len(plan.items))
	for _, item := range plan.items {
		output = append(output, h.itemObject(item, stage))
	}
	return output
}

// responseUsage is the usage object of the sample, with the reasoning and
// cached counts a ReasoningMarker answer reports.
func (h *handler) responseUsage(plan responsesPlan) map[string]any {
	usage := fakeupstream.Clone(h.responses.envelope["usage"].(map[string]any))
	if plan.reasoning {
		usage["output_tokens"], usage["total_tokens"] = 14, 36
		if details, _ := usage["output_tokens_details"].(map[string]any); details != nil {
			details["reasoning_tokens"] = 5
		}
		if details, _ := usage["input_tokens_details"].(map[string]any); details != nil {
			details["cached_tokens"] = 10
		}
	}
	return usage
}

// responseObject is the response object of an event or of a non-stream answer:
// the sample's complete object with what the request and the plan decide
// replaced. A status of in_progress is the object of response.created.
func (h *handler) responseObject(request responsesRequest, plan responsesPlan, status string, output []any, number int64) map[string]any {
	body := fakeupstream.Clone(h.responses.envelope)
	body["id"] = placeholderID("resp_", 50, number)
	body["status"] = status
	body["model"] = request.Model
	if rule, constrained := constraintFor(request.Model); constrained {
		body["model"] = rule.resolvedModel(request.Model)
	}
	body["instructions"] = nil
	if instructions := request.instructionsText(); instructions != "" {
		body["instructions"] = instructions
	}
	body["max_output_tokens"] = nil
	if presentJSON(request.MaxOutputTokens) {
		body["max_output_tokens"] = json.RawMessage(request.MaxOutputTokens)
	}
	if presentJSON(request.ParallelToolCalls) {
		body["parallel_tool_calls"] = json.RawMessage(request.ParallelToolCalls)
	}
	tools := make([]any, 0, len(request.Tools))
	for _, tool := range request.Tools {
		echoed := fakeupstream.Clone(tool.Raw)
		echoed["output_schema"] = nil
		tools = append(tools, echoed)
	}
	body["tools"] = tools
	effort := request.effort()
	if effort == "" {
		effort = "medium"
	}
	summary := any(nil)
	if request.summaryRequested() {
		summary = "detailed"
	}
	body["reasoning"] = map[string]any{"context": "all_turns", "effort": effort, "mode": "standard", "summary": summary}
	body["output"] = output
	if output == nil {
		body["output"] = []any{}
	}
	switch status {
	case "in_progress":
		body["completed_at"], body["content_filters"], body["usage"] = nil, nil, nil
	case "failed":
		body["content_filters"], body["usage"] = nil, nil
		body["error"] = map[string]any{"code": "server_error", "message": "The server had an error while processing your request. Sorry about that!"}
	default:
		body["usage"] = h.responseUsage(plan)
		if status == "incomplete" {
			body["incomplete_details"] = map[string]any{"reason": incompleteReason}
		}
	}
	if plan.unknownEvent {
		body["fake_future_member"] = map[string]any{"extra": true}
	}
	if plan.selfImage {
		selfImageResponse(body)
	}
	return body
}

// writeResponsesStream streams the response as the gateway does: the same
// events in the same order as the sample, an event: line equal to data.type,
// no [DONE]. A function call, a text answer and a reasoning item each have
// their own run of events.
func (h *handler) writeResponsesStream(w http.ResponseWriter, request responsesRequest, plan responsesPlan, number int64) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	sequence := 0
	emit := func(data map[string]any) {
		data["sequence_number"] = sequence
		sequence++
		_, _ = w.Write(sseFrame(data))
		if flusher != nil {
			flusher.Flush()
		}
	}
	response := func(status string, output []any) map[string]any {
		return h.responseObject(request, plan, status, output, number)
	}
	emit(map[string]any{"type": "response.created", "response": response("in_progress", nil)})
	emit(map[string]any{"type": "response.in_progress", "response": response("in_progress", nil)})
	if plan.unknownEvent {
		emit(map[string]any{"type": "response.fake_future_event", "payload": map[string]any{"extra": true}})
	}
	if plan.streamError {
		emit(map[string]any{"type": "error", "code": "server_is_overloaded", "param": nil,
			"message": "The model is temporarily overloaded. Please retry your request after a brief delay."})
		return
	}
	if plan.status == "failed" {
		emit(map[string]any{"type": "response.failed", "response": response("failed", nil)})
		return
	}
	cut := plan.status == "incomplete"
	for _, item := range plan.items {
		for _, event := range h.itemEvents(request, item, cut) {
			if event["type"] == "response.image_generation_call.completed" && plan.hold > 0 {
				timer := time.NewTimer(plan.hold)
				select {
				case <-timer.C:
				case <-plan.done:
					timer.Stop()
					return
				}
			}
			emit(event)
		}
	}
	terminal := "response.completed"
	if cut {
		terminal = "response.incomplete"
	}
	emit(map[string]any{"type": terminal, "response": response(plan.status, h.outputObjects(plan, stageFinal))})
}

// itemEvents are the events of one output item, without sequence numbers. A
// cut item gets its start and its deltas and none of the done events.
func (h *handler) itemEvents(request responsesRequest, item fakeItem, cut bool) []map[string]any {
	t := h.responses
	var events []map[string]any
	switch item.kind {
	case "function_call":
		added := t.callEvent("response.output_item.added")
		added["item"], added["output_index"] = h.itemObject(item, stageAdded), item.index
		events = append(events, added)
		arguments := item.arguments
		if cut {
			arguments = firstHalf(arguments)
		}
		for _, piece := range chunks(arguments, 5) {
			delta := t.callEvent("response.function_call_arguments.delta")
			delta["delta"], delta["item_id"], delta["output_index"] = piece, item.id, item.index
			events = append(events, delta)
		}
		if cut {
			return events
		}
		done := t.callEvent("response.function_call_arguments.done")
		done["arguments"], done["item_id"], done["output_index"] = item.arguments, item.id, item.index
		itemDone := t.callEvent("response.output_item.done")
		itemDone["item"], itemDone["output_index"] = h.itemObject(item, stageDone), item.index
		return append(events, done, itemDone)
	case "message":
		added := t.textEvent("response.output_item.added")
		added["item"], added["output_index"] = h.itemObject(item, stageAdded), item.index
		partAdded := t.textEvent("response.content_part.added")
		partAdded["item_id"], partAdded["output_index"] = item.id, item.index
		if item.refusal {
			partAdded["part"] = refusalPart("")
		}
		events = append(events, added, partAdded)
		text := item.text
		if cut {
			text = firstHalf(text)
		}
		if item.refusal {
			return append(events, h.refusalEvents(item, text, cut)...)
		}
		for _, piece := range chunks(text, 6) {
			delta := t.textEvent("response.output_text.delta")
			delta["delta"], delta["item_id"], delta["output_index"] = piece, item.id, item.index
			events = append(events, delta)
		}
		if cut {
			return events
		}
		done := t.textEvent("response.output_text.done")
		done["text"], done["item_id"], done["output_index"] = item.text, item.id, item.index
		partDone := t.textEvent("response.content_part.done")
		partDone["item_id"], partDone["output_index"] = item.id, item.index
		if part, _ := partDone["part"].(map[string]any); part != nil {
			part["text"] = item.text
		}
		itemDone := t.textEvent("response.output_item.done")
		itemDone["item"], itemDone["output_index"] = h.itemObject(item, stageDone), item.index
		return append(events, done, partDone, itemDone)
	case imageGenerationKind:
		return h.imageGenerationEvents(item)
	default:
		// The reasoning item's events of the sample (observed): its
		// output_item.added, then its output_item.done, and nothing between
		// them when no summary was asked for. The summary events of a request
		// that asked for one are from the documents, not from a sample.
		added := t.reasoningEvent("response.output_item.added")
		added["item"], added["output_index"] = h.itemObject(item, stageAdded), item.index
		events = append(events, added)
		if request.summaryRequested() && !cut {
			events = append(events,
				map[string]any{"type": "response.reasoning_summary_part.added", "item_id": item.id, "output_index": item.index,
					"summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}},
				map[string]any{"type": "response.reasoning_summary_text.delta", "item_id": item.id, "output_index": item.index,
					"summary_index": 0, "delta": FixedReasoning},
				map[string]any{"type": "response.reasoning_summary_text.done", "item_id": item.id, "output_index": item.index,
					"summary_index": 0, "text": FixedReasoning},
				map[string]any{"type": "response.reasoning_summary_part.done", "item_id": item.id, "output_index": item.index,
					"summary_index": 0, "part": map[string]any{"type": "summary_text", "text": FixedReasoning}})
		}
		if cut {
			return events
		}
		done := t.reasoningEvent("response.output_item.done")
		done["item"], done["output_index"] = h.itemObject(item, stageDone), item.index
		return append(events, done)
	}
}

// refusalEvents are the events of a refused message after its content part's
// added event: response.refusal.delta pieces and, unless the answer is cut,
// response.refusal.done, the content part's done and the item's done. The
// members of the two refusal events are the official ones; no sample has them.
func (h *handler) refusalEvents(item fakeItem, text string, cut bool) []map[string]any {
	t := h.responses
	var events []map[string]any
	for _, piece := range chunks(text, 6) {
		events = append(events, map[string]any{"type": "response.refusal.delta", "content_index": 0,
			"delta": piece, "item_id": item.id, "output_index": item.index})
	}
	if cut {
		return events
	}
	done := map[string]any{"type": "response.refusal.done", "content_index": 0,
		"refusal": item.text, "item_id": item.id, "output_index": item.index}
	partDone := t.textEvent("response.content_part.done")
	partDone["item_id"], partDone["output_index"], partDone["part"] = item.id, item.index, refusalPart(item.text)
	itemDone := t.textEvent("response.output_item.done")
	itemDone["item"], itemDone["output_index"] = h.itemObject(item, stageDone), item.index
	return append(events, done, partDone, itemDone)
}

// writeRaw answers a JSON body kept as text, byte for byte plus the newline the
// fake adds to every body.
func writeRaw(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body+"\n")
}

// observedRefusal is the gateway's own error envelope of /v1/responses, in its
// key order: message, type, param, requestid, then code when there is one.
type observedRefusal struct {
	Error struct {
		Message   string  `json:"message"`
		Type      string  `json:"type"`
		Param     string  `json:"param"`
		RequestID string  `json:"requestid"`
		Code      *string `json:"code,omitempty"`
	} `json:"error"`
}

// refusalText re-encodes an observed body with its message rewritten; the
// untouched body comes back byte for byte.
func refusalText(observed string, rewrite func(string) string) string {
	var refusal observedRefusal
	if json.Unmarshal([]byte(observed), &refusal) != nil {
		return observed
	}
	refusal.Error.Message = rewrite(refusal.Error.Message)
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(refusal)
	return strings.TrimRight(out.String(), "\n")
}
