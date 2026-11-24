package meshyopenapiv1

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
)

// #542 (I13): the webhook sender of the Meshy fake. The page
// (https://docs.meshy.ai/en/api/webhooks, read 2026-11-24) is the only
// evidence: no real delivery was ever received, so every behaviour here is
// constructed from it.

type receipt struct {
	header http.Header
	body   map[string]any
}

type receiver struct {
	*httptest.Server
	mu      sync.Mutex
	got     []receipt
	answers []int // statuses to answer in order; the last repeats
	calls   int
}

func newReceiver(t *testing.T, answers ...int) *receiver {
	t.Helper()
	r := &receiver{answers: answers}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		r.mu.Lock()
		r.got = append(r.got, receipt{header: req.Header.Clone(), body: body})
		status := http.StatusOK
		if len(r.answers) > 0 {
			status = r.answers[min(r.calls, len(r.answers)-1)]
		}
		r.calls++
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *receiver) receipts() []receipt {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]receipt(nil), r.got...)
}

func statusesOf(receipts []receipt) string {
	var out []string
	for _, r := range receipts {
		out = append(out, r.body["status"].(string)+":"+string(json.Number(jsonNumber(r.body["progress"]))))
	}
	return strings.Join(out, ",")
}

func jsonNumber(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func register(t *testing.T, f *fixture, r *receiver) *core.WebhookHandle {
	t.Helper()
	handle, err := core.SharedStore(f.Fake.Env()).RegisterWebhook(core.Webhook{URL: r.URL, Secret: "whsec", UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func TestWebhookDeliversTheWholeTaskObjectWithTheOfficialHeaders(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	r := newReceiver(t)
	handle := register(t, f, r)
	id := f.create("/remesh", map[string]any{"input_task_id": f.source()})
	_, done := f.finish("/remesh", id, key)

	got := r.receipts()
	// The source task of f.source() is polled too, so keep this task's updates.
	var mine []receipt
	for _, receipt := range got {
		if receipt.body["id"] == id {
			mine = append(mine, receipt)
		}
	}
	if statusesOf(mine) != "IN_PROGRESS:50,SUCCEEDED:100" {
		t.Fatalf("updates = %s", statusesOf(mine))
	}
	for _, receipt := range got {
		for header, want := range map[string]string{
			"X-Meshy-Api-Webhook-Secret-Key": "whsec", "X-Meshy-Api-Webhook-User-Id": "user-1",
			"Meshy-Webhook-Version": "v1", "Content-Type": "application/json", "User-Agent": "MeshyAPIWebhook/1.0.0",
		} {
			if receipt.header.Get(header) != want {
				t.Errorf("%s = %q, want %q", header, receipt.header.Get(header), want)
			}
		}
	}
	// The body is what the task's GET answers, progress included: the final
	// update carries every key of the polled success object.
	final := mine[len(mine)-1].body
	for key := range done {
		if _, has := final[key]; !has {
			t.Errorf("the final update lacks %s of the task object", key)
		}
	}
	if final["type"] != "remesh" || final["model_urls"] == nil {
		t.Errorf("final update = %v", final)
	}
	if n := len(handle.Deliveries()); n < 2 {
		t.Errorf("deliveries = %d", n)
	}
}

// The progress field is 50 for IN_PROGRESS, 100 for a task that succeeded and 0
// for one that failed or was canceled (the page's table).
func TestWebhookProgressOfFailedAndCanceledTasksIsZero(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	r := newReceiver(t)
	register(t, f, r)
	source := f.source()
	r.mu.Lock()
	r.got = nil
	r.mu.Unlock()
	failed := f.create("/remesh", map[string]any{"input_task_id": source}, core.TaskErrorHeader, "timeout")
	f.finish("/remesh", failed, key)
	canceled := f.create("/remesh", map[string]any{"input_task_id": source}, "X-Fake-Scenario", "task-canceled")
	f.finish("/remesh", canceled, key)
	byTask := map[string][]receipt{}
	for _, receipt := range r.receipts() {
		id := receipt.body["id"].(string)
		byTask[id] = append(byTask[id], receipt)
	}
	if got := statusesOf(byTask[failed]); got != "IN_PROGRESS:50,FAILED:0" {
		t.Errorf("failed task updates = %s", got)
	}
	if got := statusesOf(byTask[canceled]); got != "IN_PROGRESS:50,CANCELED:0" {
		t.Errorf("canceled task updates = %s", got)
	}
	if errObject, _ := byTask[failed][1].body["task_error"].(map[string]any); errObject["type"] != "timeout" {
		t.Errorf("a failed update carries the task_error: %v", byTask[failed][1].body)
	}
}

func TestWebhookDeliveryModes(t *testing.T) {
	for name, tc := range map[string]struct {
		mode string
		want string
	}{
		"in order, once each":                        {"", "IN_PROGRESS:50,SUCCEEDED:100"},
		"the same update twice":                      {core.WebhookDuplicate, "IN_PROGRESS:50,IN_PROGRESS:50,SUCCEEDED:100,SUCCEEDED:100"},
		"IN_PROGRESS arrives after the final update": {core.WebhookReorder, "SUCCEEDED:100,IN_PROGRESS:50"},
		"a final status with no IN_PROGRESS":         {core.WebhookFinalOnly, "SUCCEEDED:100"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			source := f.source()
			r := newReceiver(t)
			register(t, f, r)
			headers := []string{}
			if tc.mode != "" {
				headers = []string{core.WebhookModeHeader, tc.mode}
			}
			id := f.create("/remesh", map[string]any{"input_task_id": source}, headers...)
			f.finish("/remesh", id, key)
			// A state is announced once however many polls see it.
			f.Do("GET", f.v1("/remesh/"+id), key, nil)
			var mine []receipt
			for _, receipt := range r.receipts() {
				if receipt.body["id"] == id {
					mine = append(mine, receipt)
				}
			}
			if got := statusesOf(mine); got != tc.want {
				t.Fatalf("updates = %s, want %s", got, tc.want)
			}
		})
	}
}

// A failed delivery is retried: up to 4 times for IN_PROGRESS and 9 for a final
// status; a status below 400 is a success.
func TestWebhookRetriesFailedDeliveries(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	r := newReceiver(t, 500, 502, 202) // the first update fails twice, then 202 counts
	handle := register(t, f, r)
	id := f.create("/remesh", map[string]any{"input_task_id": source})
	f.finish("/remesh", id, key)
	deliveries := handle.Deliveries()
	var mine []core.WebhookDelivery
	for _, delivery := range deliveries {
		if delivery.TaskID == id {
			mine = append(mine, delivery)
		}
	}
	if len(mine) != 2 || mine[0].Attempts != 3 || !mine[0].Delivered || mine[1].Attempts != 1 {
		t.Fatalf("deliveries = %+v", mine)
	}

	// An unreachable receiver exhausts the retries: 5 attempts for IN_PROGRESS,
	// 10 for the final update.
	down := newReceiver(t, 503)
	f2 := newFixture(t, fakeupstream.Config{})
	source2 := f2.source()
	handle2 := register(t, f2, down)
	id2 := f2.create("/remesh", map[string]any{"input_task_id": source2})
	f2.finish("/remesh", id2, key)
	var failed []core.WebhookDelivery
	for _, delivery := range handle2.Deliveries() {
		if delivery.TaskID == id2 {
			failed = append(failed, delivery)
		}
	}
	if len(failed) != 2 || failed[0].Attempts != 5 || failed[1].Attempts != 10 || failed[0].Delivered || failed[1].Delivered {
		t.Fatalf("failed deliveries = %+v", failed)
	}
}

// 20 updates in a row that fail every attempt deactivate the webhook; editing
// it in the Developer Platform reactivates it.
func TestWebhookIsDeactivatedAfterTwentyFailedUpdates(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	down := newReceiver(t, 500)
	handle := register(t, f, down)
	source := f.source()
	for i := 0; i < 12 && handle.Active(); i++ {
		id := f.create("/remesh", map[string]any{"input_task_id": source})
		f.finish("/remesh", id, key)
	}
	if handle.Active() {
		t.Fatal("the webhook stayed active")
	}
	calls := down.calls
	id := f.create("/remesh", map[string]any{"input_task_id": source})
	f.finish("/remesh", id, key)
	if down.calls != calls {
		t.Fatal("a deactivated webhook still receives updates")
	}
	handle.Reactivate()
	if !handle.Active() {
		t.Fatal("Reactivate did not turn the webhook on")
	}
}

func TestWebhookRegistrationNeedsAnHTTPURL(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, bad := range []string{"", "ftp://example.test/hook", "not a url", "https://"} {
		if _, err := core.SharedStore(f.Fake.Env()).RegisterWebhook(core.Webhook{URL: bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// The secret header the sender writes is one the platform's declared Meshy
// webhook contract reads: the Catalog lists x-meshy-api-webhook-secret-key among
// its key headers (#515), and the payload's job id field is "id".
func TestWebhookHeadersMatchTheCatalogDeclaration(t *testing.T) {
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Application struct {
			ProviderWebhooks []struct {
				ProviderID         string   `json:"provider_id"`
				KeyHeaders         []string `json:"key_headers"`
				PayloadJobIDFields []string `json:"payload_job_id_fields"`
			} `json:"provider_webhooks"`
		} `json:"application"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, webhook := range file.Application.ProviderWebhooks {
		if webhook.ProviderID != "meshy" {
			continue
		}
		readsHeader := false
		for _, header := range webhook.KeyHeaders {
			readsHeader = readsHeader || strings.EqualFold(header, core.WebhookSecretHeader)
		}
		if !readsHeader {
			t.Fatalf("the Catalog reads %v, not %s", webhook.KeyHeaders, core.WebhookSecretHeader)
		}
		for _, field := range webhook.PayloadJobIDFields {
			if field == "id" {
				return
			}
		}
		t.Fatalf("payload id fields = %v", webhook.PayloadJobIDFields)
	}
	t.Fatal("the Catalog declares no Meshy webhook")
}
