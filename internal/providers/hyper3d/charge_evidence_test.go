package hyper3d_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
)

// Official contracts checked 2026-10-21:
//   - https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5
//   - https://docs.hyper3d.ai/en/api-specification/bang
//   - https://docs.hyper3d.ai/en/api-specification/generate-texture
//     Each submission response documents an optional number `consumed`:
//     "Credits consumed by this request".
//   - https://docs.hyper3d.ai/en/api-specification/check-balance
//     Usage records carry `amount` and `task_uuid` (the top-level uuid).
//
// Project constraint (not an upstream requirement): the per-request charge
// reported at submission is kept verbatim in the private Attempt checkpoint as
// billing evidence, next to the top-level uuid that usage rows reference. It
// never replaces the frozen quote, is never projected publicly, and a missing
// or non-numeric value is left absent rather than recorded as zero.
func TestSubmissionConsumedIsKeptAsPrivateChargeEvidence(t *testing.T) {
	for _, kind := range []string{"rodin-text", "bang-native", "bang-upload", "texture"} {
		for name, tc := range map[string]struct {
			consumed string
			want     string
		}{
			"fractional": {consumed: `,"consumed":0.5`, want: `0.5`},
			"integer":    {consumed: `,"consumed":2`, want: `2`},
			"absent":     {},
			"string":     {consumed: `,"consumed":"2"`},
			"null":       {consumed: `,"consumed":null`},
			"object":     {consumed: `,"consumed":{"amount":2}`},
		} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				uuid := "123e4567-e89b-42d3-a456-426614174000"
				var submits, statuses atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					switch r.URL.Path {
					case "/api/v2/rodin", "/api/v2/bang", "/api/v2/rodin_texture_only":
						submits.Add(1)
						_, _ = io.WriteString(w, `{"message":"Submitted.","uuid":"`+uuid+`","jobs":{"uuids":["223e4567-e89b-42d3-a456-426614174000"],"subscription_key":"synthetic-subscription"}`+tc.consumed+`}`)
					case "/api/v2/status":
						statuses.Add(1)
						_, _ = io.WriteString(w, `{"jobs":[{"uuid":"223e4567-e89b-42d3-a456-426614174000","status":"Generating"}]}`)
					default:
						t.Errorf("unexpected route %s", r.URL.Path)
					}
				}))
				defer server.Close()
				port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
				req := simpleHyperRequest(kind, "a chair", uuid)
				accepted, err := port.Start(context.Background(), req)
				if err != nil || accepted.Job == nil || accepted.Job.RemoteID != uuid || submits.Load() != 1 {
					t.Fatalf("accepted submission rejected by evidence parsing: %+v %v", accepted, err)
				}
				assertConsumedEvidence(t, accepted.Opaque, tc.want)
				running, err := port.Observe(context.Background(), generation.ObserveRequest{
					TaskID: req.TaskID, AttemptID: req.AttemptID, Offering: req.Offering, Input: req.Input,
					Job: *accepted.Job, Opaque: accepted.Opaque,
				})
				if err != nil || running.Phase != generation.UpstreamRunning || statuses.Load() != 1 {
					t.Fatalf("observe: %+v %v", running, err)
				}
				assertConsumedEvidence(t, running.Opaque, tc.want)
			})
		}
	}
}

func assertConsumedEvidence(t *testing.T, opaque json.RawMessage, want string) {
	t.Helper()
	var state map[string]json.RawMessage
	if err := json.Unmarshal(opaque, &state); err != nil {
		t.Fatalf("checkpoint is not an object: %v", err)
	}
	got, ok := state["consumed_credit"]
	if want == "" {
		if ok {
			t.Fatalf("consumed_credit = %s; a missing or non-numeric receipt must stay absent", got)
		}
		return
	}
	if !ok || string(got) != want {
		t.Fatalf("consumed_credit = %s (present=%v); want verbatim %s", got, ok, want)
	}
}

// Real-observation fixture (2026-10-21, dev-test Hyper3D account, read-only
// probe with a random identifier, balance unchanged): both POST /status with an
// unknown subscription_key and POST /download with an unknown task_uuid return
// HTTP 201 and the body {"error":"NO_SUCH_TASK"}. The official status and
// download pages list NO_SUCH_TASK as their body error. This is backfill
// coverage of existing behavior, not test-first: after an accepted (charged)
// submission the Attempt fails as a non-retryable, non-ambiguous rejection
// through the Schema error mapping, and a status error never reaches download.
func TestObserveNoSuchTaskBodyErrorFailsWithoutResubmission(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var errorMapping *generation.ErrorMapping
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID == "hyper3d" {
			errorMapping = connection.ErrorMapping
		}
	}
	if errorMapping == nil {
		t.Fatal("hyper3d connection has no error mapping")
	}
	for _, stage := range []string{"status", "download"} {
		t.Run(stage, func(t *testing.T) {
			uuid := "123e4567-e89b-42d3-a456-426614174000"
			var submits, statuses, downloads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusCreated)
				switch r.URL.Path {
				case "/api/v2/rodin":
					submits.Add(1)
					_, _ = io.WriteString(w, `{"uuid":"`+uuid+`","jobs":{"subscription_key":"synthetic-subscription"},"consumed":3}`)
				case "/api/v2/status":
					statuses.Add(1)
					if stage == "status" {
						_, _ = io.WriteString(w, `{"error":"NO_SUCH_TASK"}`)
						return
					}
					_, _ = io.WriteString(w, `{"jobs":[{"uuid":"223e4567-e89b-42d3-a456-426614174000","status":"Done"}]}`)
				case "/api/v2/download":
					downloads.Add(1)
					_, _ = io.WriteString(w, `{"error":"NO_SUCH_TASK"}`)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
			}))
			defer server.Close()
			port, err := hyper3d.New(hyper3d.Config{
				BaseURL: server.URL + "/api/v2", APIKey: "test-key", StatusMapping: mapping(),
				ErrorMapping: errorMapping, HTTPClient: server.Client(), AllowHTTPAssets: true, InputAssets: imageInputs{},
			})
			if err != nil {
				t.Fatal(err)
			}
			req := simpleHyperRequest("rodin-text", "a chair", uuid)
			accepted, err := port.Start(context.Background(), req)
			if err != nil || accepted.Job == nil {
				t.Fatalf("start: %+v %v", accepted, err)
			}
			_, err = port.Observe(context.Background(), generation.ObserveRequest{
				TaskID: req.TaskID, AttemptID: req.AttemptID, Offering: req.Offering, Input: req.Input,
				Job: *accepted.Job, Opaque: accepted.Opaque,
			})
			var providerErr *generation.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Failure == nil {
				t.Fatalf("NO_SUCH_TASK did not fail the observation: %v", err)
			}
			failure := providerErr.Failure
			if failure.Class != generation.FailureRejected || failure.Retryable || failure.Ambiguous || failure.UpstreamCode != "NO_SUCH_TASK" {
				t.Fatalf("failure = %+v", failure)
			}
			if strings.Contains(failure.SafeDetail, uuid) || strings.Contains(failure.SafeDetail, "synthetic-subscription") {
				t.Fatal("private upstream identifier leaked into the safe detail")
			}
			wantDownloads := int32(0)
			if stage == "download" {
				wantDownloads = 1
			}
			if submits.Load() != 1 || statuses.Load() != 1 || downloads.Load() != wantDownloads {
				t.Fatalf("calls submit=%d status=%d download=%d", submits.Load(), statuses.Load(), downloads.Load())
			}
		})
	}
}
