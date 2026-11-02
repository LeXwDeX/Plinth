package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TestThreeViewQueueLimitIsValidatedAndPublic covers #59: the page's queue
// limit for a view refused by admission is declared by the Schema, validated,
// and projected as chat.three_view_limits while chat.three_view stays private.
func TestThreeViewQueueLimitIsValidatedAndPublic(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	field := `"queue_limit_seconds": 1800,`
	for name, replacement := range map[string]string{
		"missing":      ``,
		"zero":         `"queue_limit_seconds": 0,`,
		"negative":     `"queue_limit_seconds": -1,`,
		"over maximum": `"queue_limit_seconds": 86401,`,
	} {
		changed := bytes.Replace(source, []byte(field), []byte(replacement), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, field)
		}
		_, err := catalog.Load(bytes.NewReader(changed))
		if err == nil || !strings.Contains(err.Error(), "three_view.queue_limit_seconds") {
			t.Errorf("%s: error = %v, want three_view.queue_limit_seconds", name, err)
		}
	}

	application, err := catalog.Load(bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(application.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		Chat map[string]json.RawMessage `json:"chat"`
	}
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	if _, leaked := public.Chat["three_view"]; leaked {
		t.Fatal("chat.three_view must stay server-only")
	}
	var limits struct {
		QueueLimitSeconds int `json:"queue_limit_seconds"`
	}
	if err := json.Unmarshal(public.Chat["three_view_limits"], &limits); err != nil || limits.QueueLimitSeconds != 1800 {
		t.Fatalf("chat.three_view_limits = %s (%v), want queue_limit_seconds 1800", public.Chat["three_view_limits"], err)
	}
}
