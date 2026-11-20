package openaiv1

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The sanitized excerpt of the 2026-11-20 probe of /v1/responses (#401). It is
// not a wirefixture candidate: the wire capture pipeline refuses SSE with
// event: lines and without [DONE], so a Responses stream cannot be recorded
// by it. responses_test.go keeps the fake's output identical to it, event by
// event; the file's own "provenance" lists what is verbatim from the probe
// and what was completed from the non-stream response.
//
//go:embed testdata/responses-2026-11-20.sample.json
var responsesSampleFS embed.FS

const responsesSampleFile = "testdata/responses-2026-11-20.sample.json"

type responsesSample struct {
	CapturedAt string              `json:"captured_at"`
	Source     string              `json:"source"`
	Exchanges  []responsesExchange `json:"exchanges"`
}

type responsesExchange struct {
	Name    string `json:"name"`
	Request struct {
		Body map[string]any `json:"body"`
	} `json:"request"`
	Response struct {
		Status      int            `json:"status"`
		ContentType string         `json:"content_type"`
		Events      []sampleEvent  `json:"events"`
		Body        map[string]any `json:"body"`
		// BodyText is a body kept as text, in the gateway's own key order.
		BodyText string `json:"body_text"`
	} `json:"response"`
}

type sampleEvent struct {
	Event string         `json:"event"`
	Data  map[string]any `json:"data"`
}

func loadResponsesSample() (*responsesSample, error) {
	raw, err := responsesSampleFS.ReadFile(responsesSampleFile)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var sample responsesSample
	if err := decoder.Decode(&sample); err != nil {
		return nil, fmt.Errorf("responses sample: %w", err)
	}
	return &sample, nil
}

func (s *responsesSample) exchange(name string) (responsesExchange, bool) {
	for _, exchange := range s.Exchanges {
		if exchange.Name == name {
			return exchange, true
		}
	}
	return responsesExchange{}, false
}

// responsesTemplates are the structures the fake clones its /v1/responses
// answers from, all taken from the sample.
type responsesTemplates struct {
	sample *responsesSample
	// envelope is the complete response object of the non-stream exchange.
	envelope map[string]any
	// callEvents and textEvents are the first event of each type of the
	// streamed tool-call and text exchanges.
	callEvents map[string]map[string]any
	textEvents map[string]map[string]any
	// temperatureRefusal and effortRefusal are the observed 400 bodies as text.
	temperatureRefusal string
	effortRefusal      string
	responseCounter    atomic.Int64
}

func newResponsesTemplates() (*responsesTemplates, error) {
	sample, err := loadResponsesSample()
	if err != nil {
		return nil, err
	}
	templates := &responsesTemplates{sample: sample}
	need := func(name string) (responsesExchange, error) {
		exchange, found := sample.exchange(name)
		if !found {
			return exchange, fmt.Errorf("responses sample lacks the %s exchange", name)
		}
		return exchange, nil
	}
	text, err := need("json-text")
	if err != nil {
		return nil, err
	}
	if templates.envelope = text.Response.Body; templates.envelope == nil {
		return nil, errors.New("responses sample: json-text has no body")
	}
	for name, target := range map[string]*map[string]map[string]any{
		"tool-call-stream":   &templates.callEvents,
		"tool-output-stream": &templates.textEvents,
	} {
		exchange, err := need(name)
		if err != nil {
			return nil, err
		}
		*target = map[string]map[string]any{}
		for _, event := range exchange.Response.Events {
			if _, seen := (*target)[event.Event]; !seen {
				(*target)[event.Event] = event.Data
			}
		}
	}
	for name, target := range map[string]*string{
		"error-temperature": &templates.temperatureRefusal,
		"error-effort":      &templates.effortRefusal,
	} {
		exchange, err := need(name)
		if err != nil {
			return nil, err
		}
		if *target = exchange.Response.BodyText; *target == "" {
			return nil, fmt.Errorf("responses sample: %s has no body text", name)
		}
	}
	return templates, nil
}

// event is a copy of the sample's first event of a type in the stream, so the
// fake never edits the sample.
func (t *responsesTemplates) event(stream map[string]map[string]any, eventType string) map[string]any {
	template, found := stream[eventType]
	if !found {
		// A type the sample lacks is a fake defect, never a request fault.
		panic("openai/v1 fake: the responses sample has no " + eventType + " event")
	}
	return fakeupstream.Clone(template)
}

func (t *responsesTemplates) callEvent(eventType string) map[string]any {
	return t.event(t.callEvents, eventType)
}

func (t *responsesTemplates) textEvent(eventType string) map[string]any {
	return t.event(t.textEvents, eventType)
}

// nextResponseNumber is the number of the next response, from 1.
func (t *responsesTemplates) nextResponseNumber() int64 { return t.responseCounter.Add(1) }

// placeholderID is an identifier shaped like the real ones (a prefix and a
// fixed width of digits) and as obviously fake.
func placeholderID(prefix string, width int, number int64) string {
	return prefix + fmt.Sprintf("%0*d", width, number)
}

// chunks cuts text into pieces of at most size runes (never inside a rune), so
// a streamed answer or argument arrives in several deltas.
func chunks(text string, size int) []string {
	var pieces []string
	runes := []rune(text)
	for start := 0; start < len(runes); start += size {
		pieces = append(pieces, string(runes[start:min(start+size, len(runes))]))
	}
	if len(pieces) == 0 {
		pieces = []string{""}
	}
	return pieces
}

// sseFrame is one server-sent event as the gateway sends it: an event: line
// equal to data.type, then the data: line.
func sseFrame(data map[string]any) []byte {
	raw, _ := json.Marshal(data)
	eventType, _ := data["type"].(string)
	return []byte("event: " + eventType + "\ndata: " + strings.TrimSpace(string(raw)) + "\n\n")
}
