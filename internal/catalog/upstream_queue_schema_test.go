package catalog_test

import (
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #131: the default Schema declares which upstream status words wait in the
// Provider's own queue and where Meshy and Hyper3D report the count ahead.
func TestDefaultCatalogDeclaresUpstreamQueues(t *testing.T) {
	document := defaultCatalogDocument(t)
	for _, test := range []struct {
		connection, status, ahead string
	}{
		{"meshy", "pending", "preceding_tasks"},
		{"tripo", "queued", ""},
		{"hyper3d", "waiting", "queue_length"},
	} {
		mapping := connectionMap(t, document, test.connection)["status_mapping"].(map[string]any)
		rule := mapping["upstream_status"].(map[string]any)[test.status].(map[string]any)
		if rule["phase"] != "accepted" || rule["queued"] != true {
			t.Fatalf("%s %s = %v", test.connection, test.status, rule)
		}
		if field, _ := mapping["queue_ahead_field"].(string); field != test.ahead {
			t.Fatalf("%s queue_ahead_field = %q, want %q", test.connection, field, test.ahead)
		}
	}
}

func TestProviderConnectionsRejectInvalidUpstreamQueue(t *testing.T) {
	for name, test := range map[string]struct {
		mapping map[string]any
		want    string
	}{
		"queued running phase": {map[string]any{"upstream_status": map[string]any{
			"running": map[string]any{"phase": "running", "queued": true}}}, "queued"},
		"queued with a class": {map[string]any{"upstream_status": map[string]any{
			"queued": map[string]any{"phase": "accepted", "queued": true, "class": "rejected"}}}, "queued"},
		"ahead field without a queued word": {map[string]any{"queue_ahead_field": "position", "upstream_status": map[string]any{
			"queued": map[string]any{"phase": "accepted"}}}, "queue_ahead_field"},
		"ahead field not a path": {map[string]any{"queue_ahead_field": "queue[0]", "upstream_status": map[string]any{
			"queued": map[string]any{"phase": "accepted", "queued": true}}}, "queue_ahead_field"},
	} {
		t.Run(name, func(t *testing.T) {
			document := defaultCatalogDocument(t)
			connection := cloneValue(t, connectionMap(t, document, "tripo"))
			connection["status_mapping"] = test.mapping
			replaceConnection(t, document, connection)
			if _, err := loadDocument(t, document); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// #136: the default Schema bounds the Meshy, Tripo and Hyper3D queues at two
// hours, and each Offering of those connections freezes that limit; Providers
// without an upstream queue declare none.
func TestDefaultOfferingsFreezeTheProviderQueueLimit(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []schema.OfferingID{
		"meshy/meshy-7.1/text-to-3d", "tripo/tripo-v3.1-full/text-to-3d", "hyper3d/rodin-gen-2.5/text-to-3d",
	} {
		offering, ok := product.Get(id)
		if !ok || offering.UpstreamQueueTimeout != 2*time.Hour {
			t.Fatalf("%s queue limit = %v (found %v), want 2h", id, offering.UpstreamQueueTimeout, ok)
		}
	}
	for _, id := range []schema.OfferingID{"openai/gpt-image-2/text-to-image", "matting/default/background-removal"} {
		if offering, ok := product.Get(id); !ok || offering.UpstreamQueueTimeout != 0 {
			t.Fatalf("%s declares no upstream queue, limit = %v", id, offering.UpstreamQueueTimeout)
		}
	}
}

// An Offering may declare its own limit, only for a connection with queued
// status words; neither limit may be negative.
func TestUpstreamQueueTimeoutDeclarations(t *testing.T) {
	offeringNamed := func(document map[string]any, id string) map[string]any {
		for _, raw := range document["offerings"].([]any) {
			if offering := raw.(map[string]any); offering["id"] == id {
				return offering
			}
		}
		t.Fatalf("missing offering %s", id)
		return nil
	}
	document := defaultCatalogDocument(t)
	offeringNamed(document, "meshy/meshy-7.1/text-to-3d")["upstream_queue_timeout_seconds"] = 3600
	product, err := loadDocument(t, document)
	if err != nil {
		t.Fatal(err)
	}
	if offering, _ := product.Get("meshy/meshy-7.1/text-to-3d"); offering.UpstreamQueueTimeout != time.Hour {
		t.Fatalf("Offering override = %v, want 1h", offering.UpstreamQueueTimeout)
	}
	for name, edit := range map[string]func(map[string]any){
		"negative offering limit": func(document map[string]any) {
			offeringNamed(document, "meshy/meshy-7.1/text-to-3d")["upstream_queue_timeout_seconds"] = -1
		},
		"offering limit without a queue": func(document map[string]any) {
			offeringNamed(document, "openai/gpt-image-2/text-to-image")["upstream_queue_timeout_seconds"] = 60
		},
		"negative connection limit": func(document map[string]any) {
			connectionMap(t, document, "meshy")["status_mapping"].(map[string]any)["queue_timeout_seconds"] = -1
		},
		"connection limit without a queue": func(document map[string]any) {
			connection := cloneValue(t, connectionMap(t, document, "tripo"))
			connection["status_mapping"] = map[string]any{"queue_timeout_seconds": 60, "upstream_status": map[string]any{
				"queued": map[string]any{"phase": "accepted"}}}
			replaceConnection(t, document, connection)
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := defaultCatalogDocument(t)
			edit(document)
			if _, err := loadDocument(t, document); err == nil || !strings.Contains(err.Error(), "queue_timeout_seconds") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// The server fills {ahead}; a copy without it, or with another placeholder,
// is refused.
func TestUpstreamQueueNoticeKeepsItsPlaceholder(t *testing.T) {
	for name, text := range map[string]string{
		"missing":   "上游排队中",
		"different": "上游排队中（前方约 {count} 个任务）",
	} {
		t.Run(name, func(t *testing.T) {
			document := defaultCatalogDocument(t)
			content := applicationOf(t, document)["content"].(map[string]any)
			content["messages"].(map[string]any)["task.queue_reason.upstream_queued_ahead"] = text
			if _, err := loadDocument(t, document); err == nil || !strings.Contains(err.Error(), "upstream_queued_ahead") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
