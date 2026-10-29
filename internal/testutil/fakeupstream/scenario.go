package fakeupstream

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Scenario is a fault the fake injects. It is chosen per request by the
// X-Fake-Scenario header or, for stacks that cannot add headers, by the test
// API key prefix "fake-<scenario>" (for example "fake-rate-limit-key1"). The
// header wins over the key.
type Scenario string

const (
	ScenarioNone Scenario = ""
	// ScenarioInsufficientBalance answers the provider's "not enough credits"
	// (HTTP 402-equivalent) error on task creation.
	ScenarioInsufficientBalance Scenario = "insufficient-balance"
	// ScenarioKeyQuota answers the provider's per-key credit limit error on
	// task creation.
	ScenarioKeyQuota Scenario = "key-quota"
	// ScenarioRateLimit answers HTTP 429 with Retry-After.
	ScenarioRateLimit Scenario = "rate-limit"
	// ScenarioServerError answers HTTP 500.
	ScenarioServerError Scenario = "server-error"
	// ScenarioTimeout holds the request open (Config.TimeoutHold) and then
	// drops the connection without a response.
	ScenarioTimeout Scenario = "timeout"
	// ScenarioLostResponse accepts the task creation (the work exists
	// upstream) and drops the connection before the response.
	ScenarioLostResponse Scenario = "lost-response"
	// ScenarioTaskFailed makes the created task end in the provider's failed
	// state.
	ScenarioTaskFailed Scenario = "task-failed"
	// ScenarioArtifactNotFound makes the finished task's artifact URLs
	// answer 404.
	ScenarioArtifactNotFound Scenario = "artifact-404"
	// ScenarioArtifactCorrupt makes the finished task's artifact URLs serve
	// bytes that no longer match the declared format.
	ScenarioArtifactCorrupt Scenario = "artifact-corrupt"
)

// Scenarios lists every injectable scenario.
var Scenarios = []Scenario{
	ScenarioInsufficientBalance, ScenarioKeyQuota, ScenarioRateLimit, ScenarioServerError,
	ScenarioTimeout, ScenarioLostResponse, ScenarioTaskFailed, ScenarioArtifactNotFound, ScenarioArtifactCorrupt,
}

const (
	// ScenarioHeader selects a Scenario for one request.
	ScenarioHeader = "X-Fake-Scenario"
	// FailTimesHeader overrides Config.FailTimes for one request.
	FailTimesHeader = "X-Fake-Fail-Times"
	// KeyPrefix starts a test API key that selects a scenario.
	KeyPrefix = "fake-"
)

// ScenarioOf resolves the scenario a request asks for.
func ScenarioOf(r *http.Request) Scenario {
	if value := strings.TrimSpace(r.Header.Get(ScenarioHeader)); value != "" {
		return parseScenario(value)
	}
	return ScenarioFromKey(BearerKey(r))
}

// ScenarioFromKey reads the scenario out of a "fake-<scenario>[-suffix]" key.
func ScenarioFromKey(key string) Scenario {
	rest, ok := strings.CutPrefix(key, KeyPrefix)
	if !ok {
		return ScenarioNone
	}
	best := ScenarioNone
	for _, scenario := range Scenarios {
		name := string(scenario)
		if rest == name || (strings.HasPrefix(rest, name) && (rest[len(name)] == '-' || rest[len(name)] == '_')) {
			if len(name) > len(best) {
				best = scenario
			}
		}
	}
	return best
}

func parseScenario(value string) Scenario {
	for _, scenario := range Scenarios {
		if string(scenario) == value {
			return scenario
		}
	}
	return ScenarioNone
}

// BearerKey returns the API key of an "Authorization: Bearer <key>" header, or
// "".
func BearerKey(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) > 7 && strings.EqualFold(value[:7], "bearer ") {
		return strings.TrimSpace(value[7:])
	}
	return ""
}

// Phase says what a request does, which decides which scenarios apply.
type Phase int

const (
	// PhaseCreate submits work (POST that creates a task or a completion).
	PhaseCreate Phase = iota
	// PhaseRead polls, lists or reads the balance.
	PhaseRead
)

// ErrorRenderer writes a provider-shaped error body for a scenario. The
// version handlers supply it; the status code and Retry-After header are
// part of what it writes.
type ErrorRenderer func(w http.ResponseWriter, r *http.Request, scenario Scenario)

// Env is the state shared by every version of one fake.
type Env struct {
	Config

	mu       sync.Mutex
	failures map[string]int
}

// NewEnv applies the defaults to a Config.
func NewEnv(config Config) *Env {
	return &Env{Config: config.withDefaults(), failures: make(map[string]int)}
}

// Intercept applies a request-phase scenario. It reports true when it already
// answered (or dropped) the request. ScenarioLostResponse is not handled here:
// the handler must create the task first and then call LoseResponse.
func (e *Env) Intercept(w http.ResponseWriter, r *http.Request, phase Phase, render ErrorRenderer) bool {
	scenario := ScenarioOf(r)
	switch scenario {
	case ScenarioInsufficientBalance, ScenarioKeyQuota:
		if phase != PhaseCreate {
			return false
		}
	case ScenarioRateLimit, ScenarioServerError, ScenarioTimeout:
	default:
		return false
	}
	if !e.consume(r, scenario) {
		return false
	}
	switch scenario {
	case ScenarioTimeout:
		e.Hold(r)
		AbortConnection(w)
	default:
		render(w, r, scenario)
	}
	return true
}

// consume reports whether this request still fails, counting per scenario and
// API key against the configured FailTimes.
func (e *Env) consume(r *http.Request, scenario Scenario) bool {
	limit := e.FailTimes
	if value := strings.TrimSpace(r.Header.Get(FailTimesHeader)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			limit = parsed
		}
	}
	if limit == 0 {
		return true
	}
	key := string(scenario) + "\x00" + BearerKey(r) + "\x00" + r.Header.Get(FailTimesHeader)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failures[key] >= limit {
		return false
	}
	e.failures[key]++
	return true
}

// Hold blocks until the client gives up or Config.TimeoutHold elapses.
func (e *Env) Hold(r *http.Request) {
	timer := time.NewTimer(e.TimeoutHold)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
	case <-timer.C:
	}
}

// AbortConnection drops the connection without writing a response.
func AbortConnection(w http.ResponseWriter) {
	if hijacker, ok := w.(http.Hijacker); ok {
		if connection, _, err := hijacker.Hijack(); err == nil {
			_ = connection.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}

// RetryAfterSeconds is the Retry-After header value of the rate-limit
// scenario.
func (e *Env) RetryAfterSeconds() string {
	seconds := int(e.RetryAfter / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

// SortedScenarioNames is used by help text and tests.
func SortedScenarioNames() []string {
	names := make([]string, 0, len(Scenarios))
	for _, scenario := range Scenarios {
		names = append(names, string(scenario))
	}
	sort.Strings(names)
	return names
}
