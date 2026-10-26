package generationtool_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestFreeProcessingKeepsRequestedTargetFormat(t *testing.T) {
	for _, target := range []string{"FBX", "STL", "gltf"} {
		t.Run(target, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", meshyCatalogTasks)
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			legacy, ok := product.Get(catalogtest.TripoFormatConversionID)
			if !ok || legacy.Enabled {
				t.Fatal("default legacy converter must remain disabled")
			}
			h.gateway.product, h.gateway.normalize = product, true
			h.gateway.grantGateway.offering = product.Get
			source := h.gateway.tasks["tripo-1"]
			source.Assets[0].MediaType = "model/gltf-binary"
			h.gateway.tasks[source.ID] = source
			result, _ := h.run("convert_model_format", fmt.Sprintf(`{"source_task_id":"tripo-1","format":%q}`, target))
			if target == "gltf" {
				if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.submitCount != 1 || h.gateway.frozen[0].OfferingID != "local/static-glb/to-gltf" || h.frozenInput(0)["format"] != target {
					t.Fatalf("declared local target rejected or changed: %+v frozen=%+v", result, h.gateway.frozen)
				}
				return
			}
			h.expectRejected(result, "target format")
			if _, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-1"); !errors.Is(err, generationtool.ErrOperationNotFound) {
				t.Fatalf("incompatible target created an operation: %v", err)
			}
			databaseURL := url.URL{Scheme: "file", Path: h.databasePath, RawQuery: "mode=ro"}
			db, err := sql.Open("sqlite", databaseURL.String())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, table := range []string{"generation_prepared_inputs", "agent_generation_operations", "execution_grants", "generation_tasks"} {
				var count int
				if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("target rejection wrote %s: %d", table, count)
				}
			}
		})
	}
}

// validatingGateway adds the kernel's read-only input check to the harness
// gateway; refused Offerings fail it as an untrusted or oversized source would.
type validatingGateway struct {
	*processingGateway
	refuse    map[generation.OfferingID]bool
	validated []generation.OfferingID
}

func (g *validatingGateway) ValidateOfferingInput(_ context.Context, _ generation.OwnerID, offering generation.OfferingID, _ string, input json.RawMessage) error {
	g.validated = append(g.validated, offering)
	var values map[string]any
	if err := json.Unmarshal(input, &values); err != nil || values["source_asset"] == nil {
		return &generation.Error{Code: generation.ErrInvalidInput, Detail: "source missing"}
	}
	if g.refuse[offering] {
		return &generation.Error{Code: generation.ErrUnsupportedModelInput, Detail: "refused"}
	}
	return nil
}

// #264: chat picks the format coverage's Schema-ordered glTF route (the
// generated-model package first) after the kernel's own input check, and
// falls back to the ordinary Offering order when that check refuses it.
func TestConvertModelFormatFollowsTheCoverageRouteOrder(t *testing.T) {
	for _, test := range []struct {
		name   string
		refuse []generation.OfferingID
		want   generation.OfferingID
		checks []generation.OfferingID
	}{
		{"trusted generated model", nil, "local/generated-glb/to-gltf", []generation.OfferingID{"local/generated-glb/to-gltf"}},
		{"refused by the generated route", []generation.OfferingID{"local/generated-glb/to-gltf"}, "local/static-glb/to-gltf",
			[]generation.OfferingID{"local/generated-glb/to-gltf", "local/static-glb/to-gltf"}},
		{"refused by every route", []generation.OfferingID{"local/generated-glb/to-gltf", "local/static-glb/to-gltf"}, "local/static-glb/to-gltf",
			[]generation.OfferingID{"local/generated-glb/to-gltf", "local/static-glb/to-gltf"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", meshyCatalogTasks)
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			h.gateway.product, h.gateway.normalize = product, true
			h.gateway.grantGateway.offering = product.Get
			source := h.gateway.tasks["model-1"]
			source.Assets[0].MediaType = "model/gltf-binary"
			h.gateway.tasks[source.ID] = source
			gateway := &validatingGateway{processingGateway: h.gateway, refuse: map[generation.OfferingID]bool{}}
			for _, offering := range test.refuse {
				gateway.refuse[offering] = true
			}
			config := h.config
			config.Gateway = gateway
			if h.executor, err = generationtool.New(config); err != nil {
				t.Fatal(err)
			}
			result, _ := h.run("convert_model_format", `{"source_task_id":"model-1","format":"gltf"}`)
			if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != test.want || h.frozenInput(0)["source_asset"] != "model-1-glb" {
				t.Fatalf("route = %+v frozen=%+v; want %s", result, h.gateway.frozen, test.want)
			}
			if !slices.Equal(gateway.validated, test.checks) {
				t.Fatalf("validated %v; want %v", gateway.validated, test.checks)
			}
		})
	}
	// Another format names no generated route and keeps the ordinary order.
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	gateway := &validatingGateway{processingGateway: h.gateway}
	config := h.config
	config.Gateway = gateway
	var err error
	if h.executor, err = generationtool.New(config); err != nil {
		t.Fatal(err)
	}
	h.run("convert_model_format", `{"source_task_id":"model-1","format":"stl"}`)
	if slices.Contains(gateway.validated, "local/generated-glb/to-gltf") {
		t.Fatalf("an STL request tried the glTF package route: %v", gateway.validated)
	}
}
