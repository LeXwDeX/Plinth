package meshy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

func TestScalarWireValuesProjectBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		mapping map[string]json.RawMessage
		want    any
		fail    bool
	}{
		{"numeric-duration", `"2.5"`, map[string]json.RawMessage{"2.5": json.RawMessage(`2.5`)}, float64(2.5), false},
		{"string-mapping", `"highest"`, map[string]json.RawMessage{"highest": json.RawMessage(`"PRIME"`)}, "PRIME", false},
		{"unmapped-choice", `"missing"`, map[string]json.RawMessage{"highest": json.RawMessage(`"PRIME"`)}, nil, true},
		{"invalid-mapping", `"highest"`, map[string]json.RawMessage{"highest": json.RawMessage(`{`)}, nil, true},
		{"nonscalar-mapping", `"highest"`, map[string]json.RawMessage{"highest": json.RawMessage(`{"mode":"prime"}`)}, nil, true},
		{"numeric-unselected-key", `2.5`, map[string]json.RawMessage{"2.5": json.RawMessage(`2.5`)}, nil, true},
		{"plain-string", `"legacy"`, nil, "legacy", false},
		{"plain-number", `2.5`, nil, float64(2.5), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["wire_value"] != tc.want {
					t.Errorf("wire value=%#v; want %#v", body["wire_value"], tc.want)
				}
				if _, ok := body["choice"]; ok {
					t.Error("unprojected scalar leaked")
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"result": "remote"})
			}))
			defer server.Close()
			adapter, constructErr := meshy.New(meshy.Config{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()})
			if constructErr != nil {
				t.Fatal(constructErr)
			}
			o := meshyDispatchOffering()
			o.Parameters = []generation.Parameter{{Name: "choice", Type: "select", WireName: "wire_value", WireValues: tc.mapping}}
			cp := &meshyRecordingCheckpointer{}
			_, err := adapter.Start(generation.WithDispatchCheckpointer(context.Background(), cp), generation.StartRequest{Offering: o, AttemptID: "a", SubmissionKey: "s", Input: json.RawMessage(`{"choice":` + tc.input + `}`)})
			if tc.fail {
				if err == nil || calls.Load() != 0 || cp.count() != 0 {
					t.Fatalf("bad mapping dispatched: err=%v calls=%d checkpoints=%d", err, calls.Load(), cp.count())
				}
			} else if err != nil || calls.Load() != 1 || cp.count() != 1 {
				t.Fatalf("projection failed: %v calls=%d checkpoints=%d", err, calls.Load(), cp.count())
			}
		})
	}
}
