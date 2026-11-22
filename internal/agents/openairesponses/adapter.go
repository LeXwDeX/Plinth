// Package openairesponses is the chat model Adapter of the OpenAI Responses
// API (POST /v1/responses, #401). It implements the same Port as
// openaicompatible, which speaks Chat Completions: a model whose profile
// declares wire_api responses is served here for every kind of request (Agent
// turn, title, compaction summary, prompt enhancement, connection test).
//
// What the real gateway was seen to do (two probes of 2026-11-20 through the
// dev-test stack, model gpt-astra; their sanitized excerpt is the fake's
// testdata/responses-2026-11-20.sample.json) and what is only from the OpenAI
// documents is kept apart in the comments below: "observed" marks what the
// probes showed, a comment that says "per the documents" marks behavior no real
// sample has shown yet (response.incomplete, response.failed, an error event in
// a stream, response.refusal.delta and the reasoning summary events, and the
// Adapter's own handling of a stream line over the bound).
//
// The Adapter is stateless on the wire: store is always false and nothing
// refers to an earlier response, so every request carries the whole
// conversation. The reasoning items a reply returns travel back through the
// Port's ReasoningContent (see reasoning.go).
package openairesponses

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

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
)

// Options are the request extensions of the configured model, taken from a
// Product Schema model profile that declares wire_api responses. nil means the
// model matched no profile: no reasoning member is sent and the temperature is
// sent as the request carries it.
type Options struct {
	// EffortLevels and DefaultEffort are the thinking levels the model offers
	// and the one sent when the request chose none, or one the model does not
	// offer (#322). The chosen level is written to reasoning.effort. Empty
	// levels: no reasoning member is sent. Observed for gpt-astra: low, medium,
	// high, xhigh and max are served; none and minimal are refused as
	// unsupported values, so a profile must not offer them.
	EffortLevels  []string
	DefaultEffort string
	// OmitTemperature leaves temperature out of every request to the model: its
	// upstream refuses the member, whatever its value (#401, observed on
	// 2026-11-20: "Unsupported parameter: 'temperature' is not supported with
	// this model."). Every request path goes through the Adapter, so the Agent
	// turn, the title, the summary and the connection test all follow it.
	OmitTemperature bool
}

// effort is the level a request sends for a chosen one.
func (options *Options) effort(chosen string) string {
	if options == nil || len(options.EffortLevels) == 0 {
		return ""
	}
	for _, level := range options.EffortLevels {
		if level == chosen {
			return level
		}
	}
	return options.DefaultEffort
}

func (options *Options) omitTemperature() bool { return options != nil && options.OmitTemperature }

type Config struct {
	BaseURL           string
	APIKey            string
	EndpointPath      string
	AuthorizationName string
	AuthorizationType string
	RequestTimeout    time.Duration
	// ScannerMaxBytes bounds one stream line and a non-stream response body. A
	// Responses event carries more than a Chat Completions chunk: the response
	// objects of response.created, response.in_progress and response.completed
	// echo the instructions and the tools, and an output item can hold an
	// encrypted reasoning item.
	ScannerMaxBytes int
	// ErrorBodyMaxBytes bounds the body of a refusal.
	ErrorBodyMaxBytes int64
	Client            *http.Client
	// OptionsFor picks the options of each request's model (nil: the model has
	// no profile).
	OptionsFor func(model string) *Options
	// ParallelToolCalls sends parallel_tool_calls=true on requests that carry
	// tools, so the model may emit independent calls in one reply
	// (chat.agent.parallel_tool_calls). Observed: a request that sent it true
	// was accepted and answered with two function calls, each its own run of
	// events (output_index 0 and 1).
	ParallelToolCalls bool
	// ExhaustedQuota, when set, reports whether a refusal (HTTP status and
	// body) is a used-up account balance or key quota and names its failure
	// class (the chat connection's Schema error_mapping, #120); nil: no
	// refusal is read as one.
	ExhaustedQuota func(status int, body []byte) (class string, exhausted bool)
	// Logger takes the one line every model call leaves: model, thinking
	// level, duration, first byte, tokens and the upstream's request id
	// (#382). Nil writes nothing.
	Logger *slog.Logger
}

type Adapter struct {
	endpoint          string
	apiKey            string
	authorizationName string
	authorizationType string
	scannerMaxBytes   int
	errorBodyMaxBytes int64
	client            *http.Client
	optionsFor        func(model string) *Options
	parallelToolCalls bool
	exhaustedQuota    func(status int, body []byte) (string, bool)
	logs              *upstreamhttp.CallLogger
}

func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.BaseURL) == "" ||
		strings.TrimSpace(config.EndpointPath) == "" ||
		strings.TrimSpace(config.AuthorizationName) == "" ||
		strings.TrimSpace(config.AuthorizationType) == "" {
		return nil, errors.New("openai-responses agent: incomplete config")
	}
	if config.RequestTimeout <= 0 || config.ScannerMaxBytes <= 0 || config.ErrorBodyMaxBytes <= 0 {
		return nil, errors.New("openai-responses agent: limits and timeout must be positive")
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: config.RequestTimeout}
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
		optionsFor:        config.OptionsFor,
		parallelToolCalls: config.ParallelToolCalls,
		exhaustedQuota:    config.ExhaustedQuota,
		logs:              upstreamhttp.NewCallLogger(config.Logger),
	}, nil
}

func (adapter *Adapter) options(model string) *Options {
	if adapter.optionsFor == nil {
		return nil
	}
	return adapter.optionsFor(model)
}

// Complete sends the request without streaming and reads the response object.
func (adapter *Adapter) Complete(
	ctx context.Context,
	request agents.Request,
) (result agents.Completion, err error) {
	// One line for the call, whatever path it returns by (#382).
	started := time.Now()
	var (
		first     time.Duration
		bodyBytes int
		response  *http.Response
		usage     *agents.Usage
	)
	defer func() {
		if first == 0 {
			first = time.Since(started)
		}
		adapter.logCall(request, false, started, first, bodyBytes, usage, response, err)
	}()
	response, bodyBytes, err = adapter.send(ctx, request, false)
	if err != nil {
		return agents.Completion{}, err
	}
	defer response.Body.Close()
	if err = adapter.requireSuccess(response, request); err != nil {
		return agents.Completion{}, err
	}
	first = time.Since(started)
	// A response object echoes the instructions and the tools and may hold an
	// encrypted reasoning item, so its bound is the stream line's, not a
	// refusal's.
	bound := int64(adapter.scannerMaxBytes)
	data, err := io.ReadAll(io.LimitReader(response.Body, bound+1))
	if err != nil {
		return agents.Completion{}, fmt.Errorf("read agent completion: %w", err)
	}
	if int64(len(data)) > bound {
		return agents.Completion{}, fmt.Errorf("agent completion exceeds %d bytes", bound)
	}
	var body responseBody
	if err := json.Unmarshal(data, &body); err != nil {
		return agents.Completion{}, fmt.Errorf("decode agent completion: %w", err)
	}
	if body.Status == "" && body.Output == nil && body.Error == nil {
		return agents.Completion{}, errors.New("agent completion has no output")
	}
	if failure := adapter.failure(response.StatusCode, body.failure()); failure != nil {
		return agents.Completion{}, failure
	}
	outputs := newOutputs()
	for index, raw := range body.Output {
		if err := outputs.settle(index, raw); err != nil {
			return agents.Completion{}, err
		}
	}
	result = outputs.completion(body.Status == statusIncomplete)
	usage = body.Usage.agentUsage()
	result.Usage = usage
	return result, nil
}

func (adapter *Adapter) send(ctx context.Context, request agents.Request, stream bool) (*http.Response, int, error) {
	body, err := marshalRequest(request, stream, adapter.parallelToolCalls, adapter.options(request.Model))
	if err != nil {
		return nil, 0, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, adapter.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if adapter.apiKey != "" {
		httpRequest.Header.Set(adapter.authorizationName, adapter.authorizationType+" "+adapter.apiKey)
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
	bodyBytes int, usage *agents.Usage, response *http.Response, err error,
) {
	requestID := ""
	if response != nil {
		requestID = upstreamhttp.RequestID(response.Header)
	}
	adapter.logs.Log(upstreamhttp.Call{
		Model:        request.Model,
		Connection:   request.Connection,
		Effort:       adapter.options(request.Model).effort(request.ReasoningEffort),
		Purpose:      request.Purpose,
		Stream:       stream,
		Elapsed:      time.Since(started),
		FirstByte:    first,
		RequestBytes: bodyBytes,
		Usage:        usage,
		RequestID:    requestID,
		Err:          err,
	})
}

// requireSuccess turns a refusal into the Port's errors: the status always
// travels as agents.StatusError (#322), and content moderation, a used-up
// quota, a model without image input and a refused request field (#401, for
// example a temperature or a reasoning.effort the model refuses) are told
// apart by the helper every chat model Adapter shares. The refusal bodies the
// probe of 2026-11-20 saw, for temperature and for an unknown reasoning.effort,
// name the field in param (observed): both read as agents.RequestRejection.
func (adapter *Adapter) requireSuccess(response *http.Response, request agents.Request) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, adapter.errorBodyMaxBytes))
	return upstreamhttp.Refusal{
		Status:         response.StatusCode,
		Body:           body,
		HasImages:      upstreamhttp.RequestHasImages(request),
		ExhaustedQuota: adapter.exhaustedQuota,
	}.Err()
}
