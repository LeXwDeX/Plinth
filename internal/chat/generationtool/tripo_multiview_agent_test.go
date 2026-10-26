package generationtool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func enabledImageGroupCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile("../../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, item := range document["offerings"].([]any) {
		o := item.(map[string]any)
		if o["id"] == "tripo/image-to-multiview" || o["id"] == "tripo/edit-multiview" {
			o["enabled"] = true
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestImageGroupAgentUsesSchemaGrantAndFrozenGroupSource(t *testing.T) {
	for _, name := range []string{"create_multiview_images", "edit_multiview_images"} {
		modes := []string{"auto"}
		if name == "create_multiview_images" {
			modes = append(modes, "manual")
		}
		for _, mode := range modes {
			t.Run(name+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				executor, command, gateway, repo := splatAgentHarness(t, mode)
				c := enabledImageGroupCatalog(t)
				gateway.catalog = c
				app := c.Application()
				for _, tool := range app.Chat.Tools {
					if tool.Name == name {
						command.Tool = tool
						command.Call.Name = name
						break
					}
				}
				if command.Tool.Name != name {
					t.Fatal("tool missing")
				}
				offeringID := generation.OfferingID("tripo/image-to-multiview")
				command.Call.Arguments = json.RawMessage(`{}`)
				if name == "edit_multiview_images" {
					offeringID = "tripo/edit-multiview"
					command.History = nil
					command.Options.Selection = []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleSelection, TaskID: "source-public", AssetID: "front-asset", URL: "/api/v1/tasks/source-public/assets/front-asset"}}
					command.Call.Arguments = json.RawMessage(`{"source_task":"@selected","front_prompt":"green"}`)
				}
				o, ok := c.Get(offeringID)
				if !ok || !o.Enabled {
					t.Fatal("candidate unavailable")
				}
				gateway.offerings = []generation.Offering{o}
				result, err := executor.Execute(ctx, command, nil)
				if err != nil {
					t.Fatalf("execute: %v input=%s", err, gateway.lastInput)
				}
				op, err := repo.LoadAgentOperation(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID, command.Call.ID)
				if err != nil {
					t.Fatal(err)
				}
				prepared, err := repo.GetPreparedInput(ctx, "owner-a", op.PreparedInputID)
				if err != nil {
					t.Fatal(err)
				}
				if prepared.OfferingID != offeringID {
					t.Fatalf("prepared=%+v", prepared)
				}
				var input map[string]any
				if err := json.Unmarshal(prepared.NormalizedInput, &input); err != nil {
					t.Fatal(err)
				}
				if name == "edit_multiview_images" {
					if input["source_task"] != "source-public" || input["front_prompt"] != "green" {
						t.Fatalf("edit input=%s", prepared.NormalizedInput)
					}
				} else if input["image"] == nil {
					t.Fatalf("create input=%s", prepared.NormalizedInput)
				}
				grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
				if err != nil {
					t.Fatal(err)
				}
				if grant.Status != executiongrant.Approved || gateway.submitCount != 1 || result.TaskID != "accepted-agent-task" {
					t.Fatalf("2D policy result=%+v grant=%+v submits=%d", result, grant, gateway.submitCount)
				}
			})
		}
	}
}
