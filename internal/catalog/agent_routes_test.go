package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// #511: PUT /api/v1/agent/bind wrote a task id per session that nothing read; the route is gone from the Schema.
func TestRuntimeRoutesDeclareNoAgentBind(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := json.Marshal(product.Application().Runtime.HTTP.Routes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(routes), "agent_bind") || strings.Contains(string(routes), "/api/v1/agent/bind") {
		t.Fatalf("routes still declare the agent bind route: %s", routes)
	}
}
