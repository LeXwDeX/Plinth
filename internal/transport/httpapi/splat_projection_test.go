package httpapi

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/routing"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"strings"
	"testing"
)

func TestSplatNativeTaskListAndLineageProjection(t *testing.T) {
	t.Parallel()
	route, err := routing.ParseTwo("/api/v1/tasks/{taskID}/assets/{assetID}", "taskID", "assetID")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{generation: &generation.Module{}, taskAssetRoute: route}
	task := generation.Task{ID: "task", Capability: schema.ImageToSplat, Offering: generation.Offering{Outputs: []generation.AssetRequirement{{Role: schema.GaussianSplatRole, Variant: schema.GaussianSplatVariant, Delivery: schema.AssetDeliveryRequired}}}, Assets: []generation.Asset{{ID: "asset", Role: schema.GaussianSplatRole, Variant: schema.GaussianSplatVariant, MediaType: schema.GaussianSplatMediaType, DeliveryProfile: schema.DeliveryProfileGaussianSplat32V1, StorageKey: "private-cas"}}}
	graph := lineage.Graph{Nodes: []lineage.Node{{ID: "task", Kind: lineage.KindTask, TaskID: "task", Assets: []lineage.Asset{{ID: "asset", Role: schema.GaussianSplatRole, Variant: schema.GaussianSplatVariant, MediaType: schema.GaussianSplatMediaType, DeliveryProfile: schema.DeliveryProfileGaussianSplat32V1}}}}}
	for name, payload := range map[string]any{"detail": s.newTaskResponse("", task), "list": s.taskPageResponse("", generation.TaskPage{Tasks: []generation.Task{task}}), "lineage": s.lineageWire("", graph)} {
		t.Run(name, func(t *testing.T) {
			raw, e := json.Marshal(payload)
			if e != nil {
				t.Fatal(e)
			}
			body := string(raw)
			for _, expected := range []string{`"delivery_profile":"gaussian-splat-32-v1"`, `"role":"gaussian-splat"`, `"variant":"splat"`, `"media_type":"application/vnd.art-ai.gaussian-splat"`, `"url":"/api/v1/tasks/task/assets/asset"`} {
				if !strings.Contains(body, expected) {
					t.Fatalf("missing %s in %s", expected, body)
				}
			}
			for _, private := range []string{"private-cas", "private-owner", "opaque", "file_token", "consumed_credit"} {
				if strings.Contains(body, private) {
					t.Fatalf("private data leaked %s", private)
				}
			}
		})
	}
}
