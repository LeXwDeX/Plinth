package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The webhook sender of the Meshy fake, from the Webhooks page
// (https://docs.meshy.ai/en/api/webhooks, read 2026-11-24). Nothing here has
// been observed from the real service: a real delivery needs a public https
// payload URL and a webhook configured in the Developer Platform, which also
// reaches every other application of the account, so the page is the only
// evidence (constructed). What the page states, and the fake reproduces:
//
//   - Every active webhook of the account receives the status updates of all
//     the account's API tasks. A webhook is registered on the Store
//     (Store.RegisterWebhook), the account-level object of the real service.
//   - A delivery is a POST of the task object the task's GET endpoint returns,
//     with progress 50 for an IN_PROGRESS update, 100 for a task that succeeded
//     and 0 for one that failed or was canceled. One is sent when the task
//     reaches IN_PROGRESS and one when it reaches a final status; the page says
//     a task can reach a final status without an IN_PROGRESS delivery.
//   - Headers: X-MESHY-API-WEBHOOK-SECRET-KEY (the webhook's secret),
//     X-MESHY-API-WEBHOOK-USER-ID, Meshy-Webhook-Version v1, Content-Type
//     application/json and User-Agent MeshyAPIWebhook/1.0.0.
//   - Any status below 400 is a success; 400 and above, or no answer within
//     about 25 seconds, is a failed delivery. A failed update is retried up to
//     4 times for IN_PROGRESS and 9 times for a final status; the page does not
//     give the waits, so Webhook.RetryDelay (zero: none) stands for them.
//   - 20 updates in a row that fail every attempt may deactivate the webhook;
//     the fake does, and Reactivate undoes it.
//   - Deliveries may arrive out of order (an IN_PROGRESS update after the final
//     one) and the same update may arrive twice; the receiver must process each
//     id and status pair once and never let IN_PROGRESS overwrite a final status.
//     The task-level selector WebhookModeHeader asks the fake for either.
//
// Deviations: the real service requires an https payload URL, while the fake
// delivers to any http or https URL so a local receiver can be tested; the
// fake's task state advances on polls, so a delivery is emitted by the poll that
// first sees each state, synchronously before the poll answers.

// Webhook headers and values.
const (
	WebhookSecretHeader  = "X-MESHY-API-WEBHOOK-SECRET-KEY"
	WebhookUserHeader    = "X-MESHY-API-WEBHOOK-USER-ID"
	WebhookVersionHeader = "Meshy-Webhook-Version"
	WebhookVersion       = "v1"
	WebhookUserAgent     = "MeshyAPIWebhook/1.0.0"

	// WebhookModeHeader selects, on the create request of a task, how its
	// updates are delivered: WebhookDuplicate sends each one twice,
	// WebhookReorder sends the final update before IN_PROGRESS, WebhookFinalOnly
	// leaves IN_PROGRESS out. Any other value is the in-order, once-each default.
	WebhookModeHeader = "X-Fake-Webhook-Mode"
	WebhookDuplicate  = "duplicate"
	WebhookReorder    = "reorder"
	WebhookFinalOnly  = "final-only"
)

// Retry counts after the first attempt, from the page.
const (
	webhookInProgressRetries = 4
	webhookFinalRetries      = 9
	// webhookDeactivateAfter is the run of fully failed updates after which the
	// webhook is deactivated.
	webhookDeactivateAfter = 20
	webhookTimeout         = 25 * time.Second
)

// Webhook is one webhook of the account.
type Webhook struct {
	// URL is the payload URL.
	URL string
	// Secret is sent in WebhookSecretHeader with every delivery.
	Secret string
	// UserID is the Meshy user that owns the webhook (WebhookUserHeader).
	UserID string
	// Client sends the deliveries; nil uses a client with the 25 second wait.
	Client *http.Client
	// RetryDelay is the wait before each retry (zero: none).
	RetryDelay time.Duration
}

// WebhookDelivery is the record of one update sent to a webhook.
type WebhookDelivery struct {
	TaskID string
	// Status is the status the body carried and Progress its progress field.
	Status   string
	Progress int
	// Attempts is how many POSTs were made, the retries included.
	Attempts int
	// Delivered is whether an attempt was answered with a status below 400.
	Delivered bool
	// Duplicate marks the second send of an update in WebhookDuplicate mode.
	Duplicate bool
}

// WebhookHandle is the registered webhook.
type WebhookHandle struct {
	webhook Webhook
	mu      sync.Mutex
	// failedRun counts the updates in a row that failed every attempt.
	failedRun  int
	active     bool
	deliveries []WebhookDelivery
}

// Deliveries returns the updates sent so far, in sending order.
func (h *WebhookHandle) Deliveries() []WebhookDelivery {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]WebhookDelivery(nil), h.deliveries...)
}

// Active reports whether the webhook still receives updates.
func (h *WebhookHandle) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active
}

// Reactivate turns a deactivated webhook back on, as editing it in the
// Developer Platform does.
func (h *WebhookHandle) Reactivate() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active, h.failedRun = true, 0
}

// RegisterWebhook adds a webhook to the account of this fake. It receives the
// updates of every task polled from then on, as the page says an active
// webhook does.
func (s *Store) RegisterWebhook(webhook Webhook) (*WebhookHandle, error) {
	parsed, err := url.Parse(webhook.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("meshy webhook: %q is not an http or https payload URL", webhook.URL)
	}
	handle := &WebhookHandle{webhook: webhook, active: true}
	s.hookMu.Lock()
	s.webhooks = append(s.webhooks, handle)
	s.hookMu.Unlock()
	return handle, nil
}

// webhookProgress is the progress field of a delivery.
func webhookProgress(status string) json.Number {
	switch status {
	case StatusInProgress:
		return json.Number("50")
	case StatusSucceeded:
		return json.Number("100")
	}
	return json.Number("0")
}

// announce sends the webhook update a poll that has just seen status makes due.
// A state is sent once per task, whatever the number of polls.
func (e *engine) announce(r *http.Request, t *task, status string) {
	if status != StatusInProgress && status != StatusSucceeded && status != StatusFailed && status != StatusCanceled {
		return
	}
	store := e.cfg.Store
	store.hookMu.Lock()
	handles := append([]*WebhookHandle(nil), store.webhooks...)
	store.hookMu.Unlock()
	if len(handles) == 0 {
		return
	}
	store.mu.Lock()
	if t.announced == nil {
		t.announced = map[string]bool{}
	}
	already := t.announced[status]
	t.announced[status] = true
	mode := t.webhookMode
	store.mu.Unlock()
	if already {
		return
	}
	var updates []string
	final := status != StatusInProgress
	switch {
	case status == StatusInProgress && mode == WebhookFinalOnly:
	case status == StatusInProgress && mode == WebhookReorder:
		// Held back: the final update is delivered first.
	case final && mode == WebhookReorder:
		updates = []string{status, StatusInProgress}
	default:
		updates = []string{status}
	}
	for _, update := range updates {
		body := e.pollBody(r, t, update)
		body["progress"] = webhookProgress(update)
		raw, err := json.Marshal(body)
		if err != nil {
			continue
		}
		for _, handle := range handles {
			handle.send(t.id, update, raw, mode == WebhookDuplicate)
		}
	}
}

// send delivers one update to a webhook, retrying a failure.
func (h *WebhookHandle) send(taskID, status string, raw []byte, twice bool) {
	for round := 0; round < 1+b2i(twice); round++ {
		h.mu.Lock()
		if !h.active {
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		retries := webhookFinalRetries
		if status == StatusInProgress {
			retries = webhookInProgressRetries
		}
		record := WebhookDelivery{TaskID: taskID, Status: status, Progress: progressOf(status), Duplicate: round > 0}
		for attempt := 0; attempt <= retries; attempt++ {
			if attempt > 0 && h.webhook.RetryDelay > 0 {
				time.Sleep(h.webhook.RetryDelay)
			}
			record.Attempts++
			if h.post(raw) {
				record.Delivered = true
				break
			}
		}
		h.mu.Lock()
		h.deliveries = append(h.deliveries, record)
		if record.Delivered {
			h.failedRun = 0
		} else if h.failedRun++; h.failedRun >= webhookDeactivateAfter {
			h.active = false
		}
		h.mu.Unlock()
	}
}

func progressOf(status string) int {
	number, _ := webhookProgress(status).Int64()
	return int(number)
}

func b2i(value bool) int {
	if value {
		return 1
	}
	return 0
}

// post makes one delivery attempt and reports whether it succeeded.
func (h *WebhookHandle) post(raw []byte) bool {
	request, err := http.NewRequest(http.MethodPost, h.webhook.URL, bytes.NewReader(raw))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", WebhookUserAgent)
	request.Header.Set(WebhookVersionHeader, WebhookVersion)
	request.Header.Set(WebhookSecretHeader, h.webhook.Secret)
	request.Header.Set(WebhookUserHeader, h.webhook.UserID)
	client := h.webhook.Client
	if client == nil {
		client = &http.Client{Timeout: webhookTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode < http.StatusBadRequest
}

// webhookModeOf reads the delivery mode a create request selects.
func webhookModeOf(r *http.Request) string {
	mode := strings.TrimSpace(r.Header.Get(WebhookModeHeader))
	switch mode {
	case WebhookDuplicate, WebhookReorder, WebhookFinalOnly:
		return mode
	}
	return ""
}
