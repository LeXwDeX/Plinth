package hyper3d_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
)

// #131: Rodin sub-jobs that are all Waiting hold the task in the upstream
// queue; the default Schema declares waiting as queued and queue_length as
// the count ahead. The status body shapes follow the captured bang status
// sample, where a Waiting job carried queue_length 0.
func TestRodinWaitingJobsReportTheUpstreamQueue(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var mapping *generation.StatusMapping
	for _, connection := range taskCatalog.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == "hyper3d.rodin.v1" {
			mapping = connection.StatusMapping
		}
	}
	if mapping == nil {
		t.Fatal("默认 Catalog 缺少 hyper3d.rodin.v1 连接声明")
	}
	var body atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, body.Load().(string))
	}))
	t.Cleanup(server.Close)
	port, err := hyper3d.New(hyper3d.Config{
		BaseURL: server.URL + "/api/v2", APIKey: "test-key", StatusMapping: mapping,
		HTTPClient: server.Client(), AllowHTTPAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: offering(generation.TextTo3D),
		Job:    generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "task-uuid"},
		Opaque: json.RawMessage(`{"task_uuid":"task-uuid","subscription_key":"status-secret"}`),
	}
	ahead := func(value int) *int { return &value }
	for _, test := range []struct {
		name  string
		body  string
		phase generation.UpstreamPhase
		queue *generation.UpstreamQueue
	}{
		{"all waiting with a count", `{"jobs":[{"uuid":"a","status":"Waiting","queue_length":3},{"uuid":"b","status":"Waiting"}]}`,
			generation.UpstreamAccepted, &generation.UpstreamQueue{Ahead: ahead(3)}},
		{"waiting without a count", `{"jobs":[{"uuid":"a","status":"Waiting"}]}`,
			generation.UpstreamAccepted, &generation.UpstreamQueue{}},
		{"next job waiting after one is done", `{"jobs":[{"status":"Done","uuid":"a"},{"queue_length":0,"status":"Waiting","uuid":"b"},{"status":"Waiting","uuid":"c"}]}`,
			generation.UpstreamAccepted, &generation.UpstreamQueue{Ahead: ahead(0)}},
		{"a job generating", `{"jobs":[{"status":"Generating","uuid":"a"},{"queue_length":2,"status":"Waiting","uuid":"b"}]}`,
			generation.UpstreamRunning, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			body.Store(test.body)
			observed, err := port.Observe(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if observed.Phase != test.phase {
				t.Fatalf("phase = %s, want %s", observed.Phase, test.phase)
			}
			got, _ := json.Marshal(observed.Queue)
			want, _ := json.Marshal(test.queue)
			if string(got) != string(want) {
				t.Fatalf("queue = %s, want %s", got, want)
			}
		})
	}
}
