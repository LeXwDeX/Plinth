package openaicompatible

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/inlinepictures"
	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
)

// ThinkingOptions are the vendor request extensions of the configured model,
// taken from a Product Schema model profile (#241, #253). nil means the model
// matched no profile: only standard OpenAI chat-completions fields are sent.
type ThinkingOptions struct {
	// RequestFields are top-level body fields of the vendor's own contract
	// (its thinking switch), sent verbatim.
	RequestFields map[string]json.RawMessage
	// ReasoningPassback keeps reasoning_content from responses and sends it
	// back on assistant messages, as the vendor's tool-loop contract requires.
	ReasoningPassback bool
	// ReasoningKind is how this model carries its thinking (a profile's
	// thinking_kind, #377): only items of this kind are sent back, so a model
	// is never given another kind's reasoning.
	ReasoningKind string
	// EffortField receives the request's thinking level when EffortLevels
	// offers it, else DefaultEffort (#322). Empty: no level is sent.
	EffortField   string
	EffortLevels  []string
	DefaultEffort string
	// StreamIncludeUsage asks a streamed request for its token usage with
	// stream_options.include_usage (#322).
	StreamIncludeUsage bool
	// OmitTemperature leaves temperature out of every request to the model:
	// its upstream accepts only the default value and answers 400 to any other
	// (#401). Every request path goes through the adapter, so the Agent turn,
	// the title, the summary and the connection test all follow it.
	OmitTemperature bool
}

// reasoningKind is the kind of reasoning this Adapter can send back: the
// profile's thinking_kind, and plaintext when one declares none — reasoning_content
// on chat completions is the plaintext string, the only kind this wire carries
// (#377). Another kind reads nothing, so an encrypted item is never written
// here as if it were a plaintext one.
func (options *ThinkingOptions) reasoningKind() string {
	if options == nil || options.ReasoningKind == "" {
		return agents.ReasoningPlaintext
	}
	return options.ReasoningKind
}

// effort is the level a request sends for a chosen one.
func (options *ThinkingOptions) effort(chosen string) string {
	if options == nil || options.EffortField == "" || len(options.EffortLevels) == 0 {
		return ""
	}
	for _, level := range options.EffortLevels {
		if level == chosen {
			return level
		}
	}
	return options.DefaultEffort
}

type Config struct {
	BaseURL           string
	APIKey            string
	EndpointPath      string
	AuthorizationName string
	AuthorizationType string
	RequestTimeout    time.Duration
	ScannerMaxBytes   int
	// FirstByteTimeout bounds how long a streamed call waits for its first
	// frame, the response headers included (chat.agent.
	// first_byte_timeout_seconds, #509); a call that gets none in time fails
	// as transient. 0: only RequestTimeout bounds it.
	FirstByteTimeout  time.Duration
	ErrorBodyMaxBytes int64
	Client            *http.Client
	// Thinking applies to every model; ThinkingFor, when set, wins and picks
	// the options of each request's model (nil: the model has no profile).
	Thinking    *ThinkingOptions
	ThinkingFor func(model string) *ThinkingOptions
	// StreamIncludeUsage asks streamed requests of models without thinking
	// options for their token usage (chat.agent.stream_include_usage, #322).
	StreamIncludeUsage bool
	// ParallelToolCalls sends parallel_tool_calls=true on requests that carry
	// tools, so the model may emit independent calls in one reply
	// (chat.agent.parallel_tool_calls).
	ParallelToolCalls bool
	// ExhaustedQuota, when set, reports whether a refusal (HTTP status and
	// body) is a used-up account balance or key quota and names its failure
	// class. The application derives it from the chat connection's Schema
	// error_mapping (#120); nil: no refusal is read as one.
	ExhaustedQuota func(status int, body []byte) (class string, exhausted bool)
	// Logger takes the one line every model call leaves: model, thinking
	// level, duration, first byte, tokens and the upstream's request id
	// (#382). Nil writes nothing.
	Logger *slog.Logger
	// Pictures encodes every inline picture of a request into the format
	// chat.agent.request_pictures declares, just before the request is
	// encoded; nil sends the pictures as they came.
	Pictures *inlinepictures.Encoder
}

type Adapter struct {
	endpoint          string
	apiKey            string
	authorizationName string
	authorizationType string
	scannerMaxBytes   int
	errorBodyMaxBytes int64
	firstByteTimeout  time.Duration
	client            *http.Client
	thinking          *ThinkingOptions
	thinkingFor       func(model string) *ThinkingOptions
	includeUsage      bool
	parallelToolCalls bool
	exhaustedQuota    func(status int, body []byte) (string, bool)
	logs              *upstreamhttp.CallLogger
	pictures          *inlinepictures.Encoder
}

func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.BaseURL) == "" ||
		strings.TrimSpace(config.EndpointPath) == "" ||
		strings.TrimSpace(config.AuthorizationName) == "" ||
		strings.TrimSpace(config.AuthorizationType) == "" {
		return nil, errors.New("openai-compatible agent: incomplete config")
	}
	if config.RequestTimeout <= 0 ||
		config.ScannerMaxBytes <= 0 ||
		config.ErrorBodyMaxBytes <= 0 {
		return nil, errors.New(
			"openai-compatible agent: limits and timeout must be positive",
		)
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: config.RequestTimeout}
	}
	var thinking *ThinkingOptions
	if config.Thinking != nil {
		cloned := *config.Thinking
		thinking = &cloned
	}
	return &Adapter{
		endpoint: strings.TrimRight(config.BaseURL, "/") +
			"/" + strings.TrimLeft(config.EndpointPath, "/"),
		apiKey:            config.APIKey,
		authorizationName: config.AuthorizationName,
		authorizationType: config.AuthorizationType,
		scannerMaxBytes:   config.ScannerMaxBytes,
		errorBodyMaxBytes: config.ErrorBodyMaxBytes,
		firstByteTimeout:  config.FirstByteTimeout,
		client:            config.Client,
		thinking:          thinking,
		thinkingFor:       config.ThinkingFor,
		includeUsage:      config.StreamIncludeUsage,
		parallelToolCalls: config.ParallelToolCalls,
		exhaustedQuota:    config.ExhaustedQuota,
		logs:              upstreamhttp.NewCallLogger(config.Logger),
		pictures:          config.Pictures,
	}, nil
}

// options are the vendor extensions of the request's model.
func (adapter *Adapter) options(model string) *ThinkingOptions {
	if adapter.thinkingFor != nil {
		return adapter.thinkingFor(model)
	}
	return adapter.thinking
}

// optionsFor are the vendor extensions of a request's model: the Adapter's own
// profile, with the thinking contract the request froze when it is one (#377),
// so that a turn keeps the kind and the passback rule its model had when it
// started, also after a restart into a changed Catalog.
func (adapter *Adapter) optionsFor(request agents.Request) *ThinkingOptions {
	options := adapter.options(request.Model)
	thinking := request.Thinking
	if thinking == nil {
		return options
	}
	if options == nil {
		options = &ThinkingOptions{}
	} else {
		cloned := *options
		options = &cloned
	}
	options.ReasoningKind = thinking.Kind
	options.ReasoningPassback = thinking.Passback
	return options
}

// streamUsage is whether a streamed request of the model asks for usage.
func (adapter *Adapter) streamUsage(options *ThinkingOptions) bool {
	if options != nil {
		return options.StreamIncludeUsage
	}
	return adapter.includeUsage
}

func (adapter *Adapter) Stream(
	ctx context.Context,
	request agents.Request,
	emit func(agents.Delta) error,
) (result agents.Completion, err error) {
	// One line for the call, whatever path it returns by: the deferred log is
	// the last thing that runs, and the named error carries the outcome (#382).
	started := time.Now()
	var (
		first     time.Duration
		usage     *agents.Usage
		bodyBytes int
		response  *http.Response
		pictures  inlinepictures.Stats
		refused   []byte
	)
	defer func() {
		adapter.logCall(request, true, started, first, bodyBytes, usage, response, pictures, refused, err)
	}()
	// An upstream that sends nothing ends at the first-byte timeout, not at the
	// request timeout (#509).
	guard := upstreamhttp.GuardFirstByte(ctx, adapter.firstByteTimeout)
	defer guard.Release()
	// Every inline picture goes out in the declared format.
	var sent agents.Request
	sent, pictures = adapter.pictures.Request(request)
	response, bodyBytes, err = adapter.send(guard.Context(), sent, true)
	if err != nil {
		return agents.Completion{}, guard.Err(err)
	}
	defer response.Body.Close()
	if refused, err = adapter.requireSuccess(response, request); err != nil {
		return agents.Completion{}, err
	}
	// first is when the upstream answered: the first stream frame is what a
	// person waits through, so a slow model shows up as its own number.
	lines := upstreamhttp.NewLines(response.Body, adapter.scannerMaxBytes)
	// The stream is whole only with its end (#509): [DONE], or a choice that
	// carries its finish_reason (the usage chunk and [DONE] may still be cut
	// after it, the reply is not). plain is the first line that is no SSE
	// frame, for a 200 answer that is not a stream at all.
	ended, framed := false, false
	plain := ""
	// finish is the last finish_reason the stream reported (#665).
	finish := ""
	options := adapter.optionsFor(request)
	var content, reasoning strings.Builder
	// thinking is true from the first reasoning fragment until visible
	// content arrives, so each stretch of reasoning yields one content-free
	// Thinking delta whatever the model's thinking profile (#285).
	thinking := false
	// toolCalling is set once the first tool call fragment has been reported:
	// a model writes call arguments for a long while before the call can run,
	// so the caller learns of the call at its first fragment (#375).
	toolCalling := false
	// calls are the assembled calls in stream order; byIndex is the call the
	// upstream index currently continues (#665).
	var calls []*agents.ToolCall
	byIndex := map[int]int{}
	declared := map[string]bool{}
	for _, tool := range request.Tools {
		declared[tool.Name] = true
	}
	// inlineImage is set when the model drew a picture itself (delta.images,
	// a multi-megabyte base64 line from models such as gpt-astra): the reply
	// has no place for it, so it is dropped, and a turn that said nothing
	// else ends with ErrInlineImageOutput instead of an unknown error.
	inlineImage := false
	for lines.Scan() {
		if first == 0 {
			first = time.Since(started)
			guard.Arrived()
		}
		if lines.Oversized() {
			framed = true
			if !oversizedImage(lines.Head()) {
				return agents.Completion{}, fmt.Errorf("read agent stream: line exceeds %d bytes", adapter.scannerMaxBytes)
			}
			inlineImage = true
			continue
		}
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			if plain == "" && !strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "id:") &&
				!strings.HasPrefix(line, "retry:") {
				plain = line
			}
			continue
		}
		framed = true
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			ended = true
			break
		}
		if upstreamhttp.HasError([]byte(payload)) {
			// An error frame ends the stream: the upstream broke off (#509).
			return agents.Completion{}, upstreamhttp.StreamError([]byte(payload), adapter.exhaustedQuota)
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return agents.Completion{}, fmt.Errorf(
				"decode agent stream chunk: %w",
				err,
			)
		}
		if chunk.Usage != nil {
			// The usage of the whole request rides on the last chunk (with or
			// without a choice); earlier chunks carry none or null.
			usage = chunk.Usage.agentUsage()
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				ended = true
				finish = *choice.FinishReason
			}
			if len(choice.Delta.Images) > 0 {
				inlineImage = true
			}
			if passback(options) {
				reasoning.WriteString(choice.Delta.ReasoningContent)
			}
			if choice.Delta.ReasoningContent != "" && !thinking {
				thinking = true
				if emit != nil {
					if err := emit(agents.Delta{Thinking: true}); err != nil {
						return agents.Completion{}, err
					}
				}
			}
			if choice.Delta.Content != "" {
				thinking = false
				content.WriteString(choice.Delta.Content)
				if emit != nil {
					if err := emit(agents.Delta{
						Content: choice.Delta.Content,
					}); err != nil {
						return agents.Completion{}, err
					}
				}
			}
			for _, fragment := range choice.Delta.ToolCalls {
				if !toolCalling {
					toolCalling = true
					if emit != nil {
						if err := emit(agents.Delta{ToolCalling: true}); err != nil {
							return agents.Completion{}, err
						}
					}
				}
				// #665: a fragment without an index continues the last call.
				// A different call ID at the call being continued starts a
				// new call only when the fragment also names a tool and the
				// call so far is whole, or both IDs look complete on their own
				// while the call so far already has a name or arguments (a
				// gateway that numbers parallel calls all 0, or leaves out the
				// index, after a call with an undeclared name or arguments
				// that are not JSON); otherwise it is the next piece of an ID
				// streamed in pieces and is appended. One that repeats the
				// whole ID or name in each fragment does not double them.
				position := len(calls) - 1
				if fragment.Index != nil {
					if known, found := byIndex[*fragment.Index]; found {
						position = known
					} else {
						position = -1
					}
				}
				if position >= 0 && fragment.ID != "" && calls[position].ID != "" && fragment.ID != calls[position].ID &&
					fragment.Function.Name != "" && startsNewToolCall(calls[position], fragment.ID, declared) {
					position = -1
				}
				if position < 0 {
					calls = append(calls, &agents.ToolCall{})
					position = len(calls) - 1
					if fragment.Index != nil {
						byIndex[*fragment.Index] = position
					}
				}
				call := calls[position]
				if call.ID == "" {
					call.ID = fragment.ID
				} else if fragment.ID != "" && fragment.ID != call.ID {
					call.ID += fragment.ID
				}
				call.Name = appendNameFragment(call.Name, fragment.Function.Name, declared)
				call.Arguments = append(
					call.Arguments,
					fragment.Function.Arguments...,
				)
			}
		}
	}
	if err := lines.Err(); err != nil {
		return agents.Completion{}, guard.Err(fmt.Errorf("read agent stream: %w", err))
	}
	if !ended {
		if !framed && plain != "" {
			return agents.Completion{}, upstreamhttp.NotAStream(response.Header.Get("Content-Type"), plain, adapter.exhaustedQuota)
		}
		// A stream that stops before its end is a cut one: the text or the
		// call arguments so far may be half of what the model wrote.
		return agents.Completion{}, agents.Transient(errors.New("agent stream ended before its last chunk"), 0)
	}
	if inlineImage && strings.TrimSpace(content.String()) == "" && len(calls) == 0 {
		return agents.Completion{}, agents.ErrInlineImageOutput
	}
	result = agents.Completion{Content: content.String(), ReasoningContent: reasoning.String(), Usage: usage,
		FinishReason: agents.FinishReasonOf(finish)}
	for _, call := range calls {
		result.ToolCalls = append(result.ToolCalls, *call)
	}
	return result, nil
}

func (adapter *Adapter) Complete(
	ctx context.Context,
	request agents.Request,
) (result agents.Completion, err error) {
	// One line for the call, as in Stream.
	started := time.Now()
	var (
		first     time.Duration
		bodyBytes int
		response  *http.Response
		pictures  inlinepictures.Stats
		refused   []byte
		usage     *agents.Usage
	)
	defer func() {
		// A call that failed before the response has no first byte to report.
		adapter.logCall(request, false, started, first, bodyBytes, usage, response, pictures, refused, err)
	}()
	// A non-streamed answer arrives whole, so only the request timeout bounds
	// it; the guard only classifies a broken connection as transient (#509).
	guard := upstreamhttp.GuardFirstByte(ctx, 0)
	// Every inline picture goes out in the declared format.
	var sent agents.Request
	sent, pictures = adapter.pictures.Request(request)
	response, bodyBytes, err = adapter.send(ctx, sent, false)
	if err != nil {
		return agents.Completion{}, guard.Err(err)
	}
	defer response.Body.Close()
	if refused, err = adapter.requireSuccess(response, request); err != nil {
		return agents.Completion{}, err
	}
	first = time.Since(started)
	data, err := io.ReadAll(
		io.LimitReader(response.Body, adapter.errorBodyMaxBytes+1),
	)
	if err != nil {
		return agents.Completion{}, guard.Err(fmt.Errorf(
			"read agent completion: %w",
			err,
		))
	}
	if int64(len(data)) > adapter.errorBodyMaxBytes {
		return agents.Completion{}, fmt.Errorf(
			"agent completion exceeds %d bytes",
			adapter.errorBodyMaxBytes,
		)
	}
	var body completionBody
	if err := json.Unmarshal(data, &body); err != nil {
		return agents.Completion{}, fmt.Errorf(
			"decode agent completion: %w",
			err,
		)
	}
	if len(body.Choices) == 0 {
		return agents.Completion{}, errors.New(
			"agent completion has no choices",
		)
	}
	message := body.Choices[0].Message
	result = agents.Completion{Content: message.Content, FinishReason: agents.FinishReasonOf(body.Choices[0].FinishReason)}
	if body.Usage != nil {
		usage = body.Usage.agentUsage()
		result.Usage = usage
	}
	if passback(adapter.optionsFor(request)) {
		result.ReasoningContent = message.ReasoningContent
	}
	for _, call := range message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, agents.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	return result, nil
}

// wholeToolCall reports whether a call assembled so far could end here: it
// names a tool (a declared one, when the request declares tools) and its
// arguments are empty or a complete JSON value.
func wholeToolCall(call *agents.ToolCall, declared map[string]bool) bool {
	if call.Name == "" || (len(declared) > 0 && !declared[call.Name]) {
		return false
	}
	return len(call.Arguments) == 0 || json.Valid(call.Arguments)
}

// startsNewToolCall reports whether a named fragment with a call ID other than
// the current call's starts the next call (#665). A whole call ends there. A
// call that is not whole (an undeclared or prefixed name, arguments that are
// not JSON) also ends when it already has a name or arguments and both IDs
// look complete: two complete IDs are two calls, while a piece of an ID ("a"
// after "call-") continues the current one.
func startsNewToolCall(call *agents.ToolCall, nextID string, declared map[string]bool) bool {
	if wholeToolCall(call, declared) {
		return true
	}
	return (call.Name != "" || len(call.Arguments) > 0) && completeToolCallID(call.ID) && completeToolCallID(nextID)
}

// completeToolCallID reports whether an ID has the shape of a whole call ID:
// a letter prefix, a separator and a non-empty rest ("call_abc", "call-a",
// "toolu_01", "chatcmpl-tool-9", "functions.name:0"), or a UUID. A piece of an
// ID ("call-", "abc") does not.
func completeToolCallID(id string) bool {
	if len(id) == 36 && strings.Count(id, "-") == 4 {
		return true
	}
	prefix := 0
	for prefix < len(id) && (id[prefix] >= 'a' && id[prefix] <= 'z' || id[prefix] >= 'A' && id[prefix] <= 'Z') {
		prefix++
	}
	if prefix == 0 || prefix+1 >= len(id) {
		return false
	}
	return strings.ContainsRune("-_.:", rune(id[prefix]))
}

// appendNameFragment adds a fragment of a call's name. A fragment that
// repeats the whole name so far is a gateway resending it (#665) and is
// dropped, unless it is a single character (a name streamed character by
// character, as "l", "l") or the doubled name is a tool the request declares.
func appendNameFragment(value, fragment string, declared map[string]bool) string {
	if fragment == "" {
		return value
	}
	if fragment == value && utf8.RuneCountInString(fragment) > 1 && !declared[value+fragment] {
		return value
	}
	return value + fragment
}

func passback(options *ThinkingOptions) bool {
	return options != nil && options.ReasoningPassback
}

func (adapter *Adapter) send(
	ctx context.Context,
	request agents.Request,
	stream bool,
) (*http.Response, int, error) {
	options := adapter.optionsFor(request)
	body, err := marshalRequest(request, stream, adapter.streamUsage(options), adapter.parallelToolCalls, options)
	if err != nil {
		return nil, 0, err
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		adapter.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, 0, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if adapter.apiKey != "" {
		httpRequest.Header.Set(
			adapter.authorizationName,
			adapter.authorizationType+" "+adapter.apiKey,
		)
	}
	response, err := adapter.client.Do(httpRequest)
	if err != nil {
		return nil, len(body), fmt.Errorf("call agent: %w", err)
	}
	return response, len(body), nil
}

// logCall writes the one line of a finished model call (#382).
func (adapter *Adapter) logCall(
	request agents.Request, stream bool, started time.Time, first time.Duration,
	bodyBytes int, usage *agents.Usage, response *http.Response,
	pictures inlinepictures.Stats, refused []byte, err error,
) {
	options := adapter.optionsFor(request)
	requestID, status := "", 0
	if response != nil {
		requestID = upstreamhttp.RequestID(response.Header)
		status = response.StatusCode
	}
	var echoes []string
	if len(refused) > 0 {
		echoes = upstreamhttp.RequestTexts(request)
	}
	adapter.logs.Log(upstreamhttp.Call{
		Model:        request.Model,
		Connection:   request.Connection,
		Effort:       options.effort(request.ReasoningEffort),
		Purpose:      request.Purpose,
		Stream:       stream,
		Elapsed:      time.Since(started),
		FirstByte:    first,
		RequestBytes: bodyBytes,
		Usage:        usage,
		RequestID:    requestID,
		Images:       pictures.Images,
		// The bytes the pictures had before any encoding, and on the wire.
		OriginalImageBytes: pictures.OriginalBytes,
		ImageBytes:         pictures.Bytes,
		Status:             status,
		ResponseBody:       refused,
		Echoes:             echoes,
		Err:                err,
	})
}

func (adapter *Adapter) requireSuccess(response *http.Response, request agents.Request) ([]byte, error) {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil, nil
	}
	body, _ := io.ReadAll(
		io.LimitReader(response.Body, adapter.errorBodyMaxBytes),
	)
	// The refusal's class (content moderation, used-up quota, no image input,
	// a refused request field) is read by the helper every chat model Adapter
	// shares; the status always travels as agents.StatusError (#322).
	return body, upstreamhttp.Refusal{
		Status:         response.StatusCode,
		Body:           body,
		HasImages:      upstreamhttp.RequestHasImages(request),
		ExhaustedQuota: adapter.exhaustedQuota,
		RetryAfter:     upstreamhttp.RetryAfter(response.Header, time.Now()),
		Echoes:         upstreamhttp.RequestTexts(request),
	}.Err()
}

// oversizedImage reports whether the head of an oversized stream line is a
// chunk that carries a picture the model drew itself.
func oversizedImage(head []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(head), []byte("data:")) && bytes.Contains(head, []byte(`"images"`))
}

type wireRequest struct {
	Model    string        `json:"model,omitempty"`
	Messages []wireMessage `json:"messages"`
	Tools    []wireTool    `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
	// Temperature is a pointer so that 0 is still sent; nil (a model that
	// accepts only its default) leaves it out.
	Temperature *float64 `json:"temperature,omitempty"`
	// StreamOptions is sent only with stream=true: DeepSeek answers 400 to
	// stream_options on a non-streamed request.
	StreamOptions *wireStreamOptions `json:"stream_options,omitempty"`
	// ParallelToolCalls is sent only when the request carries tools.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (usage *wireUsage) agentUsage() *agents.Usage {
	return &agents.Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens}
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	// ReasoningContent is a pointer so that an empty reasoning string can
	// still be sent: DeepSeek thinking mode rejects a follow-up request with
	// 400 when any assistant tool-call message lacks the field, and a round
	// may come back with no reasoning at all (#283).
	ReasoningContent *string `json:"reasoning_content,omitempty"`
}

type wireContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type wireTool struct {
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

func marshalRequest(
	request agents.Request,
	stream bool,
	includeUsage bool,
	parallelToolCalls bool,
	thinking *ThinkingOptions,
) ([]byte, error) {
	wire := wireRequest{
		Model:  request.Model,
		Stream: stream,
	}
	if thinking == nil || !thinking.OmitTemperature {
		temperature := request.Temperature
		wire.Temperature = &temperature
	}
	if stream && includeUsage {
		wire.StreamOptions = &wireStreamOptions{IncludeUsage: true}
	}
	effort := thinking.effort(request.ReasoningEffort)
	vendorFields := thinking != nil && (len(thinking.RequestFields) > 0 || effort != "")
	// The source a reasoning item must name to be sent back (#377): this
	// request's own connection and model, this Adapter's Chat Completions wire
	// (a Responses request is routed to the Responses Adapter, #401), and the
	// thinking kind the request runs under.
	source := agents.Source{
		Connection: request.Connection,
		Model:      request.Model,
		Wire:       "chat_completions",
		Kind:       thinking.reasoningKind(),
	}
	for _, message := range request.Messages {
		item := wireMessage{
			Role:       message.Role,
			Content:    message.Content,
			ToolCallID: message.ToolCallID,
		}
		if thinking != nil && thinking.ReasoningPassback &&
			(len(message.Reasoning) > 0 || len(message.ToolCalls) > 0) {
			// Only the reasoning of the request's own source goes back (#377):
			// the same connection, model, wire and kind. The field itself is sent
			// even when nothing is left, because the vendor requires it on an
			// assistant message that calls tools.
			var reasoning strings.Builder
			for _, entry := range message.Reasoning {
				if entry.Matches(source) {
					reasoning.WriteString(entry.Content)
				}
			}
			text := reasoning.String()
			item.ReasoningContent = &text
		}
		if len(message.Parts) > 0 {
			parts := make([]wireContentPart, 0, len(message.Parts))
			for _, part := range message.Parts {
				wirePart := wireContentPart{
					Type: part.Kind,
					Text: part.Text,
				}
				if part.URL != "" {
					wirePart.ImageURL = &wireImageURL{
						URL:    part.URL,
						Detail: part.Detail,
					}
				}
				parts = append(parts, wirePart)
			}
			item.Content = parts
		}
		for _, call := range message.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, wireToolCall{
				ID:   call.ID,
				Type: "function",
				Function: wireToolFunction{
					Name:      call.Name,
					Arguments: string(call.Arguments),
				},
			})
		}
		wire.Messages = append(wire.Messages, item)
	}
	for _, tool := range request.Tools {
		wire.Tools = append(wire.Tools, wireTool{
			Type: "function",
			Function: wireToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	if parallelToolCalls && len(wire.Tools) > 0 {
		enabled := true
		wire.ParallelToolCalls = &enabled
	}
	if !vendorFields {
		return json.Marshal(wire)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		return nil, err
	}
	for field, value := range thinking.RequestFields {
		if _, owned := body[field]; owned {
			return nil, fmt.Errorf("thinking request field %q conflicts with the request", field)
		}
		body[field] = value
	}
	if effort != "" {
		if _, owned := body[thinking.EffortField]; owned {
			return nil, fmt.Errorf("thinking effort field %q conflicts with the request", thinking.EffortField)
		}
		encoded, err := json.Marshal(effort)
		if err != nil {
			return nil, err
		}
		body[thinking.EffortField] = encoded
	}
	return json.Marshal(body)
}

type streamChunk struct {
	Usage   *wireUsage `json:"usage"`
	Choices []struct {
		// FinishReason is set on the last chunk of a choice (#509).
		FinishReason *string `json:"finish_reason"`
		Delta        struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			// Images are pictures the model drew itself (OpenRouter-style
			// delta.images); they are noticed, never kept.
			Images    []json.RawMessage `json:"images"`
			ToolCalls []struct {
				// Index is nil when a gateway leaves it out (#665).
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

type completionBody struct {
	Usage   *wireUsage `json:"usage"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}
