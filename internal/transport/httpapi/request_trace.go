package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/transport/httplocale"
)

// requestIDHeader names the error ID users can quote; it is on every response.
const requestIDHeader = "X-Request-ID"

// requestTrace is what one request leaves for the access log (#233): who
// asked and why it failed. It never reaches the client except for its ID.
type requestTrace struct {
	id     string
	mu     sync.Mutex
	owner  string
	code   string
	detail string
	causes []string
	// route and sessionID come from the ServeMux match. The mux sets them on
	// the request it serves, which is a copy whenever a middleware called
	// WithContext, so routeTraceMiddleware copies them here.
	route     string
	sessionID string
	// locale and texts localize Schema copy written into this response
	// (problem details, authentication errors, pending session titles).
	locale string
	texts  *catalog.TextLocalizer
}

// localeText returns the request locale's copy of a default-locale Schema
// text; other text, and requests without a trace, keep it unchanged.
func (trace *requestTrace) localeText(text string) string {
	if trace == nil || trace.texts == nil {
		return text
	}
	return trace.texts.Text(trace.locale, text)
}

// localeMessage returns the request locale's message for a key.
func (trace *requestTrace) localeMessage(key, fallback string) string {
	if trace == nil || trace.texts == nil {
		return fallback
	}
	return trace.texts.Message(trace.locale, key)
}

type requestTraceKey struct{}

func newRequestTrace() *requestTrace {
	var random [8]byte
	_, _ = rand.Read(random[:])
	return &requestTrace{id: "req_" + hex.EncodeToString(random[:])}
}

// localeTraceMiddleware records the request's UI locale on its trace.
func localeTraceMiddleware(application catalog.ApplicationSchema, texts *catalog.TextLocalizer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if trace := requestTraceFrom(request.Context()); trace != nil {
			trace.locale = httplocale.FromRequest(application, request)
			trace.texts = texts
			request = request.WithContext(catalog.WithLocale(request.Context(), trace.locale))
		}
		next.ServeHTTP(writer, request)
	})
}

func requestTraceFrom(ctx context.Context) *requestTrace {
	trace, _ := ctx.Value(requestTraceKey{}).(*requestTrace)
	return trace
}

func (trace *requestTrace) noteOwner(owner string) {
	trace.mu.Lock()
	trace.owner = owner
	trace.mu.Unlock()
}

func (trace *requestTrace) noteProblem(code, detail string, causes []error) {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.code, trace.detail = code, detail
	for _, cause := range causes {
		if cause != nil {
			trace.causes = append(trace.causes, logging.ErrChain(cause))
		}
	}
}

// routeTraceMiddleware sits directly around the ServeMux and records the
// matched pattern and session path value into the trace once the mux has
// served the request (also when the handler panics).
func routeTraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			trace := requestTraceFrom(request.Context())
			if trace == nil {
				return
			}
			trace.mu.Lock()
			trace.route, trace.sessionID = request.Pattern, request.PathValue("sessionID")
			trace.mu.Unlock()
		}()
		next.ServeHTTP(writer, request)
	})
}

// traceFromWriter finds the access log's recorder under any wrapping writers.
func traceFromWriter(writer http.ResponseWriter) *requestTrace {
	for writer != nil {
		if recorder, ok := writer.(*statusRecorder); ok {
			return recorder.trace
		}
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		writer = unwrapper.Unwrap()
	}
	return nil
}

// tracingIdentity records the authenticated owner for the access log.
type tracingIdentity struct{ IdentityResolver }

func (identity tracingIdentity) Identify(request *http.Request) (access.Principal, error) {
	principal, err := identity.IdentityResolver.Identify(request)
	if err == nil {
		if trace := requestTraceFrom(request.Context()); trace != nil {
			trace.noteOwner(principal.ID())
		}
	}
	return principal, err
}

// noteCause attaches an internal error to the request's access log entry
// without changing the response. Domain error writers call it first.
func noteCause(writer http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if trace := traceFromWriter(writer); trace != nil {
		trace.mu.Lock()
		trace.causes = append(trace.causes, logging.ErrChain(err))
		trace.mu.Unlock()
	}
}
