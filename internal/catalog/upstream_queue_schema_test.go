package catalog_test

import (
	"strings"
	"testing"
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
