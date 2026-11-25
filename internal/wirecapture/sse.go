package wirecapture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// SSE fixtures are normalized, not byte-for-byte copies. OpenAI-style data
// chunks are aggregated by choice and tool-call index; split fields are joined,
// validated and sanitized as one value, then written once in the last original
// fragment. Other chunk fields and the chunk order remain unchanged.
//
// A stream whose blocks carry an event: line (the OpenAI Responses wire, #536)
// is a named stream. It is written as "event: NAME\ndata: JSON\n\n" per block,
// in the original order, and gets no [DONE] unless the original had one. It is
// complete when its last block is a terminal event (sseTerminalEvents) or a
// [DONE] closes it. Its "*.delta" fragments are joined per event name, item and
// content index the way a Chat choice's content is, so a split text or a split
// function-call argument is approved as one value. Any other SSE field (id:,
// retry:, comments), an event: line without data, and a stream that mixes named
// and bare blocks stay unsupported.

// SSEFramed reports whether raw reached a complete SSE boundary.
func SSEFramed(raw []byte) bool {
	_, err := parseSSE(raw)
	if err == nil {
		return true
	}
	// Content errors are reported by sanitizeSSE; framing only decides whether
	// the captured bytes reached a complete SSE boundary.
	return errors.Is(err, ErrUnsupportedBody) || errors.Is(err, ErrUnsafePublicValue)
}

func sanitizeSSE(raw []byte, policy Policy) (string, error) {
	stream, err := parseSSEStream(raw)
	if err != nil {
		return "", err
	}
	if stream.named {
		return sanitizeNamedSSE(stream, policy)
	}
	chunks := stream.chunks()
	if err := normalizeSSE(chunks, policy); err != nil {
		return "", err
	}
	var out bytes.Buffer
	for _, chunk := range chunks {
		clean, err := sanitizeValue(chunk, policy, 0)
		if err != nil {
			return "", err
		}
		data, err := marshalJSON(clean)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "data: %s\n\n", data)
	}
	out.WriteString("data: [DONE]\n\n")
	return out.String(), nil
}

// sseTerminalEvents are the events that end a named stream: the Responses
// stream's three final response states and its in-stream error.
var sseTerminalEvents = map[string]bool{
	"response.completed": true, "response.incomplete": true, "response.failed": true, "error": true,
}

// sseTaskFinalStates are the final task states of a task stream whose
// "message" events each carry the whole task object (Meshy's GET
// /<kind>/{id}/stream, #608): the message that reports one of them is the
// stream's last block, and the server then closes the stream.
var sseTaskFinalStates = map[string]bool{"SUCCEEDED": true, "FAILED": true, "CANCELED": true, "EXPIRED": true}

// sseTerminal reports whether a named block ends its stream.
func sseTerminal(name string, data map[string]any) bool {
	if sseTerminalEvents[name] {
		return true
	}
	status, _ := data["status"].(string)
	return name == "message" && sseTaskFinalStates[status]
}

// sseEventNameRE bounds an event name to a protocol token; it is public text,
// never a value of the person's.
var sseEventNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,63}$`)

type sseEvent struct {
	// name is the event: line; "" for a block that carries only data.
	name string
	data map[string]any
}

type sseStream struct {
	events []sseEvent
	// named is whether the blocks carry event: lines; done whether [DONE] closed
	// the stream.
	named, done bool
}

// chunks are the data objects of the stream's events, in order.
func (s sseStream) chunks() []map[string]any {
	chunks := make([]map[string]any, 0, len(s.events))
	for _, event := range s.events {
		chunks = append(chunks, event.data)
	}
	return chunks
}

func parseSSE(raw []byte) ([]map[string]any, error) {
	stream, err := parseSSEStream(raw)
	if err != nil {
		return nil, err
	}
	return stream.chunks(), nil
}

func parseSSEStream(raw []byte) (sseStream, error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 4096), len(raw)+1)
	var stream sseStream
	var data []string
	var name string
	var named, bare bool
	terminal := false
	dispatch := func() error {
		if len(data) == 0 {
			if name != "" {
				return fmt.Errorf("%w: SSE event without data", ErrUnsupportedBody)
			}
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		eventName := name
		name = ""
		if payload == "[DONE]" {
			if eventName != "" {
				return fmt.Errorf("%w: SSE event with DONE", ErrUnsupportedBody)
			}
			if stream.done {
				return fmt.Errorf("%w: duplicate DONE", ErrInvalidExchange)
			}
			stream.done = true
			return nil
		}
		if stream.done || terminal {
			return ErrPartialSSE
		}
		chunk, err := decodeSSEChunk(payload)
		if err != nil {
			return err
		}
		if eventName == "" {
			bare = true
		} else {
			named = true
			terminal = sseTerminal(eventName, chunk)
		}
		if named && bare {
			return fmt.Errorf("%w: SSE named and bare events", ErrUnsupportedBody)
		}
		stream.events = append(stream.events, sseEvent{name: eventName, data: chunk})
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return sseStream{}, err
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			value = strings.TrimPrefix(value, " ")
			if name != "" || len(data) > 0 {
				return sseStream{}, fmt.Errorf("%w: SSE event field", ErrUnsupportedBody)
			}
			if !sseEventNameRE.MatchString(value) || checkText(value, false) != nil {
				return sseStream{}, fmt.Errorf("%w: SSE event name", ErrUnsafePublicValue)
			}
			name = value
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			return sseStream{}, fmt.Errorf("%w: SSE field", ErrUnsupportedBody)
		}
		data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
	}
	if err := scanner.Err(); err != nil {
		return sseStream{}, fmt.Errorf("%w: SSE read", ErrUnsafePublicValue)
	}
	if len(data) > 0 || name != "" {
		if stream.done || name != "" || !strings.HasSuffix(text, "\n") || len(data) != 1 || data[0] != "[DONE]" {
			return sseStream{}, ErrPartialSSE
		}
		if err := dispatch(); err != nil {
			return sseStream{}, err
		}
	}
	stream.named = named
	if len(stream.events) == 0 || !(stream.done || terminal) {
		return sseStream{}, ErrPartialSSE
	}
	return stream, nil
}

// sanitizeNamedSSE writes a named stream in its canonical form.
func sanitizeNamedSSE(stream sseStream, policy Policy) (string, error) {
	if err := normalizeNamedSSE(stream.events, policy); err != nil {
		return "", err
	}
	var out bytes.Buffer
	for _, event := range stream.events {
		clean, err := sanitizeValue(event.data, policy, 0)
		if err != nil {
			return "", err
		}
		data, err := marshalJSON(clean)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", event.name, data)
	}
	if stream.done {
		out.WriteString("data: [DONE]\n\n")
	}
	return out.String(), nil
}

// sseDeltaKey groups the fragments that belong to one value of a named stream:
// the event name, the item and the output, content and summary positions.
func sseDeltaKey(event sseEvent) string {
	var key strings.Builder
	key.WriteString(event.name)
	for _, field := range []string{"item_id", "output_index", "content_index", "summary_index"} {
		fmt.Fprintf(&key, "|%v", event.data[field])
	}
	return key.String()
}

// normalizeNamedSSE joins the "*.delta" fragments of each value, sanitizes the
// joined text (or, for function-call arguments, JSON) once and writes it in the
// last fragment, like normalizeSSE does for a Chat choice.
func normalizeNamedSSE(events []sseEvent, policy Policy) error {
	type group struct {
		field     sseField
		arguments bool
	}
	groups := map[string]*group{}
	var order []string
	for _, event := range events {
		if !strings.HasSuffix(event.name, ".delta") {
			continue
		}
		raw, found := event.data["delta"]
		if !found {
			continue
		}
		text, ok := sseString(raw)
		if !ok {
			return fmt.Errorf("%w: SSE delta", ErrUnsafePublicValue)
		}
		key := sseDeltaKey(event)
		state := groups[key]
		if state == nil {
			state = &group{arguments: strings.HasSuffix(event.name, "arguments.delta")}
			groups[key] = state
			order = append(order, key)
		}
		state.field.add(event.data, "delta", text)
	}
	for _, key := range order {
		state := groups[key]
		if err := state.field.normalize(func(value string) (string, error) {
			if state.arguments {
				return sanitizeSSEArguments(value, policy)
			}
			return sanitizeSSEText(value, policy)
		}); err != nil {
			return err
		}
	}
	return nil
}

func decodeSSEChunk(payload string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: SSE JSON", ErrUnsafePublicValue)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: SSE trailing JSON", ErrUnsafePublicValue)
	}
	chunk, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: SSE chunk", ErrUnsafePublicValue)
	}
	return chunk, nil
}

type sseOccurrence struct {
	holder map[string]any
	key    string
}

type sseField struct {
	value       strings.Builder
	last        string
	seen        bool
	occurrences []sseOccurrence
}

func (field *sseField) add(holder map[string]any, key, value string) {
	field.occurrences = append(field.occurrences, sseOccurrence{holder: holder, key: key})
	if value != "" {
		field.seen = true
		field.last = value
		field.value.WriteString(value)
	}
}

func (field *sseField) normalize(clean func(string) (string, error)) error {
	if !field.seen {
		return nil
	}
	value, err := clean(field.value.String())
	if err != nil {
		return err
	}
	for i, occurrence := range field.occurrences {
		if i == len(field.occurrences)-1 {
			occurrence.holder[occurrence.key] = value
		} else {
			occurrence.holder[occurrence.key] = ""
		}
	}
	return nil
}

type sseToolState struct {
	id        sseField
	name      sseField
	arguments sseField
}

var sseDeltaFields = map[string]bool{
	"role": true, "content": true, "reasoning_content": true, "tool_calls": true,
}

var sseChunkFields = map[string]bool{
	"id": true, "object": true, "created": true, "model": true, "choices": true,
	"usage": true, "system_fingerprint": true, "service_tier": true,
}

var sseChoiceFields = map[string]bool{
	"index": true, "delta": true, "finish_reason": true, "logprobs": true, "refusal": true,
}

type sseChoiceState struct {
	content   sseField
	reasoning sseField
	tools     map[int]*sseToolState
	toolOrder []int
}

func checkSSEChunk(chunk map[string]any) error {
	for field := range chunk {
		if !sseChunkFields[field] {
			return fmt.Errorf("%w: SSE chunk field", ErrUnsafePublicValue)
		}
	}
	for _, field := range []string{"id", "object", "model", "system_fingerprint", "service_tier"} {
		if raw, found := chunk[field]; found && raw != nil {
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("%w: SSE chunk field", ErrUnsafePublicValue)
			}
		}
	}
	if raw, found := chunk["created"]; found && raw != nil {
		if _, ok := sseIndex(raw); !ok {
			return fmt.Errorf("%w: SSE created", ErrUnsafePublicValue)
		}
	}
	if raw, found := chunk["usage"]; found && raw != nil {
		if _, ok := raw.(map[string]any); !ok {
			return fmt.Errorf("%w: SSE usage", ErrUnsafePublicValue)
		}
	}
	return nil
}

func normalizeSSE(chunks []map[string]any, policy Policy) error {
	choices := map[int]*sseChoiceState{}
	for _, chunk := range chunks {
		if err := checkSSEChunk(chunk); err != nil {
			return err
		}
		rawChoices, found := chunk["choices"]
		if !found {
			continue
		}
		list, ok := rawChoices.([]any)
		if !ok {
			return fmt.Errorf("%w: SSE choices", ErrUnsafePublicValue)
		}
		seen := map[int]bool{}
		for _, rawChoice := range list {
			choice, ok := rawChoice.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: SSE choice", ErrUnsafePublicValue)
			}
			for field := range choice {
				if !sseChoiceFields[field] {
					return fmt.Errorf("%w: SSE choice field %q", ErrUnsafePublicValue, field)
				}
			}
			if rawFinish, found := choice["finish_reason"]; found && rawFinish != nil {
				if _, ok := rawFinish.(string); !ok {
					return fmt.Errorf("%w: SSE finish reason", ErrUnsafePublicValue)
				}
			}
			index := 0
			if rawIndex, found := choice["index"]; found {
				var valid bool
				index, valid = sseIndex(rawIndex)
				if !valid {
					return fmt.Errorf("%w: SSE choice index", ErrUnsafePublicValue)
				}
			}
			if seen[index] {
				return fmt.Errorf("%w: duplicate SSE choice index", ErrUnsafePublicValue)
			}
			seen[index] = true
			state := choices[index]
			if state == nil {
				state = &sseChoiceState{tools: map[int]*sseToolState{}}
				choices[index] = state
			}
			rawDelta, found := choice["delta"]
			if !found || rawDelta == nil {
				continue
			}
			delta, ok := rawDelta.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: SSE delta", ErrUnsafePublicValue)
			}
			for field := range delta {
				if !sseDeltaFields[field] {
					return fmt.Errorf("%w: SSE delta field", ErrUnsafePublicValue)
				}
			}
			if rawContent, found := delta["content"]; found {
				content, ok := sseString(rawContent)
				if !ok {
					return fmt.Errorf("%w: SSE content", ErrUnsafePublicValue)
				}
				state.content.add(delta, "content", content)
			}
			if rawReasoning, found := delta["reasoning_content"]; found {
				reasoning, ok := sseString(rawReasoning)
				if !ok {
					return fmt.Errorf("%w: SSE reasoning", ErrUnsafePublicValue)
				}
				state.reasoning.add(delta, "reasoning_content", reasoning)
			}
			rawTools, found := delta["tool_calls"]
			if !found || rawTools == nil {
				continue
			}
			tools, ok := rawTools.([]any)
			if !ok {
				return fmt.Errorf("%w: SSE tool calls", ErrUnsafePublicValue)
			}
			seenTools := map[int]bool{}
			for _, rawTool := range tools {
				tool, ok := rawTool.(map[string]any)
				if !ok {
					return fmt.Errorf("%w: SSE tool call", ErrUnsafePublicValue)
				}
				rawIndex, found := tool["index"]
				if !found {
					return fmt.Errorf("%w: SSE tool index", ErrUnsafePublicValue)
				}
				toolIndex, ok := sseIndex(rawIndex)
				if !ok {
					return fmt.Errorf("%w: SSE tool index", ErrUnsafePublicValue)
				}
				if seenTools[toolIndex] {
					return fmt.Errorf("%w: duplicate SSE tool index", ErrUnsafePublicValue)
				}
				seenTools[toolIndex] = true
				stateTool := state.tools[toolIndex]
				if stateTool == nil {
					stateTool = &sseToolState{}
					state.tools[toolIndex] = stateTool
					state.toolOrder = append(state.toolOrder, toolIndex)
				}
				if rawID, found := tool["id"]; found {
					id, ok := sseString(rawID)
					if !ok {
						return fmt.Errorf("%w: SSE tool id", ErrUnsafePublicValue)
					}
					stateTool.id.add(tool, "id", id)
				}
				rawFunction, found := tool["function"]
				if !found || rawFunction == nil {
					continue
				}
				function, ok := rawFunction.(map[string]any)
				if !ok {
					return fmt.Errorf("%w: SSE tool function", ErrUnsafePublicValue)
				}
				if rawName, found := function["name"]; found {
					name, ok := sseString(rawName)
					if !ok {
						return fmt.Errorf("%w: SSE tool name", ErrUnsafePublicValue)
					}
					stateTool.name.add(function, "name", name)
				}
				if rawArguments, found := function["arguments"]; found {
					arguments, ok := sseString(rawArguments)
					if !ok {
						return fmt.Errorf("%w: SSE tool arguments", ErrUnsafePublicValue)
					}
					stateTool.arguments.add(function, "arguments", arguments)
				}
			}
		}
	}

	for _, state := range choices {
		if err := state.content.normalize(func(value string) (string, error) {
			return sanitizeSSEText(value, policy)
		}); err != nil {
			return err
		}
		if err := state.reasoning.normalize(func(value string) (string, error) {
			return sanitizeSSEText(value, policy)
		}); err != nil {
			return err
		}
		for _, toolIndex := range state.toolOrder {
			tool := state.tools[toolIndex]
			if tool.id.seen {
				if err := tool.id.normalize(func(value string) (string, error) {
					id, err := sanitizeSSEText(value, policy)
					if err != nil {
						return "", err
					}
					if !identifierRE.MatchString(id) {
						return "", fmt.Errorf("%w: SSE tool id", ErrUnsafePublicValue)
					}
					return id, nil
				}); err != nil {
					return err
				}
			}
			if err := tool.name.normalize(func(value string) (string, error) {
				return sanitizeSSEText(value, policy)
			}); err != nil {
				return err
			}
			if err := tool.arguments.normalize(func(value string) (string, error) {
				if !json.Valid([]byte(value)) && json.Valid([]byte(tool.arguments.last)) {
					// Compatible providers resend the cumulative value per fragment.
					value = tool.arguments.last
				}
				return sanitizeSSEArguments(value, policy)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func sseString(value any) (string, bool) {
	if value == nil {
		return "", true
	}
	text, ok := value.(string)
	return text, ok
}

func sseIndex(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	index, err := strconv.Atoi(number.String())
	return index, err == nil && index >= 0
}

func sanitizeSSEText(value string, policy Policy) (string, error) {
	if value == "" {
		return "", nil
	}
	if _, aliased := policy.Aliases[value]; aliased {
		return policy.resolve(value, 0)
	}
	if nested, ok, err := nestedString(value, policy, 0); err != nil {
		if policy.projector != nil {
			policy.Allowed["ok"] = true
			return "ok", nil
		}
		return "", err
	} else if ok {
		return nested, nil
	}
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 || (trimmed[0] != '{' && trimmed[0] != '[') {
		if mapped, err := policy.resolve(value, 0); err == nil {
			return mapped, nil
		}
	}
	if err := checkText(value, true); err != nil {
		return "", err
	}
	if mapped, err := policy.resolve(value, 0); err == nil {
		return mapped, nil
	}
	if policy.projector != nil {
		if identifierRE.MatchString(value) {
			return policy.projector.id(value, policy)
		}
		policy.Allowed["ok"] = true
		return "ok", nil
	}
	return policy.resolve(value, 0)
}

func sanitizeSSEArguments(value string, policy Policy) (string, error) {
	if value == "" {
		return "", nil
	}
	if !json.Valid([]byte(value)) {
		return "", fmt.Errorf("%w: SSE tool arguments are not JSON", ErrUnsafePublicValue)
	}
	clean, err := sanitizeJSON([]byte(value), policy)
	if err != nil {
		if policy.projector != nil {
			policy.Allowed["ok"] = true
			return `"ok"`, nil
		}
		return "", err
	}
	return string(clean), nil
}
