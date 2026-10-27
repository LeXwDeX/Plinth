package openaicompatible

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
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
	// EffortField receives the request's thinking level when EffortLevels
	// offers it, else DefaultEffort (#322). Empty: no level is sent.
	EffortField   string
	EffortLevels  []string
	DefaultEffort string
	// StreamIncludeUsage asks a streamed request for its token usage with
	// stream_options.include_usage (#322).
	StreamIncludeUsage bool
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
	ErrorBodyMaxBytes int64
	Client            *http.Client
	// Thinking applies to every model; ThinkingFor, when set, wins and picks
	// the options of each request's model (nil: the model has no profile).
	Thinking    *ThinkingOptions
	ThinkingFor func(model string) *ThinkingOptions
	// StreamIncludeUsage asks streamed requests of models without thinking
	// options for their token usage (chat.agent.stream_include_usage, #322).
	StreamIncludeUsage bool
}

type Adapter struct {
	endpoint          string
	apiKey            string
	authorizationName string
	authorizationType string
	scannerMaxBytes   int
	errorBodyMaxBytes int64
	client            *http.Client
	thinking          *ThinkingOptions
	thinkingFor       func(model string) *ThinkingOptions
	includeUsage      bool
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
		client:            config.Client,
		thinking:          thinking,
		thinkingFor:       config.ThinkingFor,
		includeUsage:      config.StreamIncludeUsage,
	}, nil
}

// options are the vendor extensions of the request's model.
func (adapter *Adapter) options(model string) *ThinkingOptions {
	if adapter.thinkingFor != nil {
		return adapter.thinkingFor(model)
	}
	return adapter.thinking
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
) (agents.Completion, error) {
	response, err := adapter.send(ctx, request, true)
	if err != nil {
		return agents.Completion{}, err
	}
	defer response.Body.Close()
	if err := adapter.requireSuccess(response, request); err != nil {
		return agents.Completion{}, err
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), adapter.scannerMaxBytes)
	options := adapter.options(request.Model)
	var content, reasoning strings.Builder
	var usage *agents.Usage
	// thinking is true from the first reasoning fragment until visible
	// content arrives, so each stretch of reasoning yields one content-free
	// Thinking delta whatever the model's thinking profile (#285).
	thinking := false
	calls := map[int]*agents.ToolCall{}
	var callOrder []int
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
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
				call, found := calls[fragment.Index]
				if !found {
					call = &agents.ToolCall{}
					calls[fragment.Index] = call
					callOrder = append(callOrder, fragment.Index)
				}
				if fragment.ID != "" {
					call.ID += fragment.ID
				}
				call.Name += fragment.Function.Name
				call.Arguments = append(
					call.Arguments,
					fragment.Function.Arguments...,
				)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return agents.Completion{}, fmt.Errorf("read agent stream: %w", err)
	}
	result := agents.Completion{Content: content.String(), ReasoningContent: reasoning.String(), Usage: usage}
	for _, index := range callOrder {
		result.ToolCalls = append(result.ToolCalls, *calls[index])
	}
	return result, nil
}

func (adapter *Adapter) Complete(
	ctx context.Context,
	request agents.Request,
) (agents.Completion, error) {
	response, err := adapter.send(ctx, request, false)
	if err != nil {
		return agents.Completion{}, err
	}
	defer response.Body.Close()
	if err := adapter.requireSuccess(response, request); err != nil {
		return agents.Completion{}, err
	}
	data, err := io.ReadAll(
		io.LimitReader(response.Body, adapter.errorBodyMaxBytes+1),
	)
	if err != nil {
		return agents.Completion{}, fmt.Errorf(
			"read agent completion: %w",
			err,
		)
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
	result := agents.Completion{Content: message.Content}
	if body.Usage != nil {
		result.Usage = body.Usage.agentUsage()
	}
	if passback(adapter.options(request.Model)) {
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

func passback(options *ThinkingOptions) bool {
	return options != nil && options.ReasoningPassback
}

func (adapter *Adapter) send(
	ctx context.Context,
	request agents.Request,
	stream bool,
) (*http.Response, error) {
	options := adapter.options(request.Model)
	body, err := marshalRequest(request, stream, adapter.streamUsage(options), options)
	if err != nil {
		return nil, err
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		adapter.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("call agent: %w", err)
	}
	return response, nil
}

func (adapter *Adapter) requireSuccess(response *http.Response, request agents.Request) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(
		io.LimitReader(response.Body, adapter.errorBodyMaxBytes),
	)
	// The status travels as agents.StatusError (#322), so a caller can
	// classify the refusal without reading the body.
	err := fmt.Errorf(
		"%w: %s",
		&agents.StatusError{Status: response.StatusCode},
		strings.TrimSpace(string(body)),
	)
	// #253: a request with pictures refused as invalid input, whose error
	// names image/vision/multimodal content, is a model without image input;
	// the caller can say so instead of an unknown error. Any other refusal
	// (context too long, a bad parameter, an unknown model) stays generic.
	if imageRefusalStatus(response.StatusCode) && requestHasImages(request) && mentionsImageInput(body) {
		return errors.Join(agents.ErrImagesRejected, err)
	}
	return err
}

func imageRefusalStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

func mentionsImageInput(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, word := range []string{"image", "vision", "multimodal", "multi-modal"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func requestHasImages(request agents.Request) bool {
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.URL != "" {
				return true
			}
		}
	}
	return false
}

type wireRequest struct {
	Model       string        `json:"model,omitempty"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature"`
	// StreamOptions is sent only with stream=true: DeepSeek answers 400 to
	// stream_options on a non-streamed request.
	StreamOptions *wireStreamOptions `json:"stream_options,omitempty"`
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
	thinking *ThinkingOptions,
) ([]byte, error) {
	wire := wireRequest{
		Model:       request.Model,
		Stream:      stream,
		Temperature: request.Temperature,
	}
	if stream && includeUsage {
		wire.StreamOptions = &wireStreamOptions{IncludeUsage: true}
	}
	effort := thinking.effort(request.ReasoningEffort)
	vendorFields := thinking != nil && (len(thinking.RequestFields) > 0 || effort != "")
	for _, message := range request.Messages {
		item := wireMessage{
			Role:       message.Role,
			Content:    message.Content,
			ToolCallID: message.ToolCallID,
		}
		if thinking != nil && thinking.ReasoningPassback &&
			(message.ReasoningContent != "" || len(message.ToolCalls) > 0) {
			reasoning := message.ReasoningContent
			item.ReasoningContent = &reasoning
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
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
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
		Message struct {
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
