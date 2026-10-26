package httpapi

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestHistoricalAuditProjectionHidesProviderAddresses(t *testing.T) {
	t.Parallel()
	event := administration.AuditEvent{
		ResourceID: "https://assets.private.example/item?secret=1",
		UserAgent:  "https%253A%252F%252Fassets.private.example%252Fagent",
		Detail: map[string]any{
			"target_url": "https://assets.private.example/model.glb?secret=1",
			"nested":     map[string]any{"url": "https%3A%2F%2Fassets.private.example%2Fimage.png"},
		},
	}
	fields := catalog.AdministrationAuditLogFieldSchema{
		ResourceID: "resource_id", Detail: "detail", UserAgent: "user_agent",
	}
	response := administrationAuditLogWire(fields, event)
	for _, key := range []string{"resource_id", "user_agent", "detail"} {
		if strings.Contains(strings.ToLower(response[key].(string)), "private.example") {
			t.Fatalf("%s exposed historical provider address: %v", key, response[key])
		}
	}
}
