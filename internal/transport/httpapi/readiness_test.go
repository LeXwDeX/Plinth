package httpapi

import (
	"context"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadinessFailsClosedWhileLivenessRemainsOK(t *testing.T) {
	t.Parallel()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{application: c.Application()}
	for _, probe := range []func(context.Context) error{nil, func(context.Context) error { return errors.New("private database path") }, func(context.Context) error { return nil }} {
		s.readiness = probe
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		s.ready(w, r)
		expected := http.StatusServiceUnavailable
		if probe != nil && probe(r.Context()) == nil {
			expected = http.StatusOK
		}
		if w.Code != expected {
			t.Fatalf("ready=%d want=%d", w.Code, expected)
		}
		live := httptest.NewRecorder()
		s.health(live, r)
		if live.Code != http.StatusOK {
			t.Fatalf("liveness=%d", live.Code)
		}
	}
}
