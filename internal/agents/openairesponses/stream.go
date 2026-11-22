package openairesponses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
)

// Stream sends the request as a stream and maps the events to the Port.
//
// Observed on 2026-11-20 (the probe's sample): every event is an SSE frame with
// an event: line equal to data.type and one data: line; the stream ends after
// response.completed, with no [DONE]. A tool call is response.output_item.added
// (the function_call item with its call_id and name), response.function_call_
// arguments.delta, response.function_call_arguments.done and
// response.output_item.done; a text answer is response.output_item.added (a
// message item), response.content_part.added, response.output_text.delta,
// response.output_text.done, response.content_part.done and
// response.output_item.done; response.created and response.in_progress open the
// stream and response.completed ends it, carrying the usage and the complete
// output. An event of a type the Adapter does not know is ignored, so are the
// members it does not read.
//
// Observed since: two parallel calls (each its own run of events, output_index 0
// and 1) and a reasoning item (response.output_item.added and
// response.output_item.done, nothing between them, at output_index 0, the
// message or the call after it).
//
// A function call's start is reported to the caller with one content-free
// ToolCalling delta per stream (#431), the same signal the Chat Completions
// Adapter sends on the first delta.tool_calls fragment (#375).
//
// Per the documents, no real sample yet: the reasoning summary events,
// response.refusal.delta, response.incomplete, response.failed and the error
// event (a flat object, or one with the error nested).
func (adapter *Adapter) Stream(
	ctx context.Context,
	request agents.Request,
	emit func(agents.Delta) error,
) (result agents.Completion, err error) {
	// One line for the call, whatever path it returns by: the deferred log is
	// the last thing that runs and the named error carries the outcome (#382).
	started := time.Now()
	var (
		first     time.Duration
		bodyBytes int
		response  *http.Response
		usage     *agents.Usage
	)
	defer func() {
		adapter.logCall(request, true, started, first, bodyBytes, usage, response, err)
	}()
	response, bodyBytes, err = adapter.send(ctx, request, true)
	if err != nil {
		return agents.Completion{}, err
	}
	defer response.Body.Close()
	if err = adapter.requireSuccess(response, request); err != nil {
		return agents.Completion{}, err
	}
	state := &streamState{adapter: adapter, emit: emit, outputs: newOutputs()}
	lines := upstreamhttp.NewLines(response.Body, adapter.scannerMaxBytes)
	event := "" // the event: line of the frame being read
read:
	for lines.Scan() {
		if first == 0 {
			// The first frame is what a person waits through: a slow model
			// shows up as its own number, apart from the whole call.
			first = time.Since(started)
		}
		if lines.Oversized() {
			state.skipOversized(event)
			event = ""
			if state.terminal {
				break
			}
			continue
		}
		line := strings.TrimSpace(lines.Text())
		switch {
		case line == "":
			event = ""
		case strings.HasPrefix(line, ":"):
			// A comment (keep-alive).
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			hint := event
			event = ""
			if payload == "[DONE]" {
				break read
			}
			if err := state.handle(hint, payload); err != nil {
				return agents.Completion{}, err
			}
			if state.terminal {
				break read
			}
		}
	}
	if err := lines.Err(); err != nil {
		return agents.Completion{}, fmt.Errorf("read agent stream: %w", err)
	}
	if !state.terminal {
		// A stream that stops before its last event is a cut one: the text or
		// the call arguments so far may be half of what the model wrote.
		return agents.Completion{}, errors.New("agent stream ended before the response completed")
	}
	if state.failure != nil {
		return agents.Completion{}, state.failure
	}
	result = state.outputs.completion(state.status == statusIncomplete)
	usage = state.usage
	result.Usage = usage
	// Text that was never streamed (an upstream that sends its message only in
	// the done item) is delivered once, so the caller still sees it arrive.
	if state.emit != nil && !state.emitted && result.Content != "" {
		if err := state.emit(agents.Delta{Content: result.Content}); err != nil {
			return agents.Completion{}, err
		}
	}
	return result, nil
}

// streamState is what a stream has told so far.
type streamState struct {
	adapter *Adapter
	emit    func(agents.Delta) error
	outputs *outputs
	// thinking is true from the first sign of reasoning until visible text
	// arrives, so each stretch of reasoning yields one content-free Thinking
	// delta (#285): the reasoning itself never leaves the Adapter.
	thinking bool
	// toolCalling is true from the first sign of a function call, so a round
	// reports a call once however many fragments or calls follow (#431).
	toolCalling bool
	// emitted: some text delta was passed on.
	emitted  bool
	terminal bool
	status   string
	usage    *agents.Usage
	failure  error
}

// streamEvent decodes the members of the events the Adapter reads; each event
// type reads the ones it has.
type streamEvent struct {
	Type        string          `json:"type"`
	OutputIndex int             `json:"output_index"`
	Item        json.RawMessage `json:"item"`
	Delta       string          `json:"delta"`
	Arguments   string          `json:"arguments"`
	Response    *responseBody   `json:"response"`
	// The error event: the members of the error object are flat on the event,
	// or nested under error.
	Code    any        `json:"code"`
	Message string     `json:"message"`
	Param   any        `json:"param"`
	Error   *wireError `json:"error"`
}

func (state *streamState) handle(hint, payload string) error {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(payload), &header); err != nil {
		return fmt.Errorf("decode agent stream event: %w", err)
	}
	eventType := header.Type
	if eventType == "" {
		eventType = hint
	}
	switch eventType {
	case "response.output_item.added", "response.output_item.done",
		"response.function_call_arguments.delta", "response.function_call_arguments.done",
		"response.output_text.delta", "response.refusal.delta",
		"response.reasoning_summary_part.added", "response.reasoning_summary_text.delta", "response.reasoning_text.delta",
		"response.completed", "response.incomplete", "response.failed", "error":
	default:
		return nil // an event the Adapter does not know
	}
	var event streamEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return fmt.Errorf("decode agent stream %s: %w", eventType, err)
	}
	switch eventType {
	case "response.output_item.added":
		if err := state.outputs.start(event.OutputIndex, event.Item); err != nil {
			return err
		}
		switch state.outputs.slot(event.OutputIndex).kind {
		case "reasoning":
			return state.think()
		case "function_call":
			return state.call()
		}
	case "response.output_item.done":
		return state.outputs.settle(event.OutputIndex, event.Item)
	case "response.function_call_arguments.delta":
		state.outputs.slot(event.OutputIndex).streamed.WriteString(event.Delta)
		return state.call()
	case "response.function_call_arguments.done":
		// The done item carries the final arguments too, and wins over this.
		item := state.outputs.slot(event.OutputIndex)
		if item.kind == "" {
			item.kind = "function_call"
		}
		if !item.settled {
			arguments := event.Arguments
			item.arguments = &arguments
		}
	case "response.output_text.delta", "response.refusal.delta":
		item := state.outputs.slot(event.OutputIndex)
		if item.kind == "" {
			item.kind = "message"
		}
		item.streamed.WriteString(event.Delta)
		return state.text(event.Delta)
	case "response.reasoning_summary_part.added", "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return state.think()
	case "response.completed", "response.incomplete", "response.failed":
		return state.finish(eventType, event)
	case "error":
		wire := event.Error
		if wire == nil {
			wire = &wireError{Code: event.Code, Message: event.Message, Param: event.Param}
		}
		state.terminal = true
		state.failure = state.adapter.failure(200, wire)
	}
	return nil
}

// think reports the start of a stretch of reasoning.
func (state *streamState) think() error {
	if state.thinking {
		return nil
	}
	state.thinking = true
	if state.emit == nil {
		return nil
	}
	return state.emit(agents.Delta{Thinking: true})
}

// call reports the start of a function call: one content-free ToolCalling delta
// per round, whether the model writes one call or several (#431). The first
// sign is the function_call item being added; the first arguments fragment
// counts too, in case that item was never seen. A call that only appears in the
// terminal event never streamed, so it reports nothing.
func (state *streamState) call() error {
	if state.toolCalling {
		return nil
	}
	state.toolCalling = true
	if state.emit == nil {
		return nil
	}
	return state.emit(agents.Delta{ToolCalling: true})
}

// text passes a piece of the visible answer on.
func (state *streamState) text(delta string) error {
	if delta == "" {
		return nil
	}
	state.thinking = false
	state.emitted = true
	if state.emit == nil {
		return nil
	}
	return state.emit(agents.Delta{Content: delta})
}

// finish handles the event that ends the response. The response object carries
// the usage and the complete output, which settles every item the stream did
// not finish. An item the stream did close keeps its own done item: the
// reasoning item of the output of response.completed was observed to carry an
// encrypted_content of its own (the done event's was the one replayed and
// accepted), so the output only fills the gaps.
func (state *streamState) finish(eventType string, event streamEvent) error {
	state.terminal = true
	body := event.Response
	if body == nil {
		body = &responseBody{}
	}
	switch eventType {
	case "response.completed":
		state.status = statusCompleted
	case "response.incomplete":
		state.status = statusIncomplete
		body.Status = statusIncomplete
	case "response.failed":
		state.status = statusFailed
		body.Status = statusFailed
	}
	for index, raw := range body.Output {
		if state.outputs.isSettled(index) {
			continue
		}
		if err := state.outputs.settle(index, raw); err != nil {
			return err
		}
	}
	state.usage = body.Usage.agentUsage()
	state.failure = state.adapter.failure(200, body.failure())
	return nil
}

// skipOversized handles a line over the stream line bound. The events that
// matter for what the Adapter keeps are small; the large ones are the response
// objects (they echo the instructions and the tools) and a done item holding a
// big encrypted reasoning item. Losing the first costs nothing, losing the
// second only the reasoning it carried. A terminal event that is too large is
// told by its event: line: the response completed without a usage; one that
// says the response ended otherwise ends it as a failure, whose details are
// lost with the line.
func (state *streamState) skipOversized(event string) {
	switch event {
	case "response.completed":
		state.terminal, state.status = true, statusCompleted
	case "response.incomplete":
		state.terminal, state.status = true, statusIncomplete
	case "response.failed", "error":
		state.terminal, state.status = true, statusFailed
		state.failure = errors.New("agent response failed: the failure event is longer than the stream line bound")
	}
}
