package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #492: a quad P2 task delivers one FBX and no GLB, so no read-only review tool
// can open it. The whole stack must let the result be delivered without a paid
// conversion: inspect_model refuses with the Schema's not-GLB text, that refusal
// is the failed evidence the record keeps, record_self_check then accepts
// not_inspectable, and none of it adds a Task or a credit to the ledger.
func TestQuadFBXResultIsDeliveredAsNotInspectableWithoutSpendingCredits(t *testing.T) {
	harness, product, _, _ := fakeStackHarnessWithInputs(t, func(origin string) map[string]string {
		return map[string]string{
			"tripo_base_url": origin + "/tripo/v3",
			"tripo_api_key":  fakeUpstreamKeyPrefix + "quad-492",
		}
	})
	accepted := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-p2/text-to-3d",
		`{"prompt":"a game-ready knight","quad":true,"authorize_extra_credits":true}`, "uncheckable-492",
		map[string]string{"quad": "用户明确要四边面 FBX"})
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
	}
	var model generation.Asset
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.StorageKey != "" {
			model = asset
		}
	}
	if model.ID == "" {
		t.Fatalf("the quad task delivered no model file: %+v", task.Assets)
	}
	if model.MediaType == "model/gltf-binary" || strings.HasSuffix(model.StorageKey, ".glb") {
		t.Fatalf("the quad task delivered a GLB: %+v", model)
	}

	selfChecks := chat.NewSelfCheckService(harness.repository, product.Application().Chat.SelfCheck)
	executor, err := generationtool.New(generationtool.Config{
		Gateway: harness.module, References: noReferences{},
		ModelAssets: harness.module, Chat: product.Application().Chat,
		TaskStates: product.Application().TaskStates, SelfChecks: selfChecks,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The ledger of the whole session: the one generation task, and nothing the
	// check path could add on top of it.
	ledger := func() (credits, tasks int) {
		t.Helper()
		buckets, err := harness.repository.QueryAdministrationUsage(context.Background(),
			administration.UsageQuery{Since: time.Unix(0, 0)})
		if err != nil {
			t.Fatal(err)
		}
		for _, bucket := range buckets {
			credits += bucket.EstimatedCredits
			tasks += bucket.TaskCount
		}
		return credits, tasks
	}
	creditsBefore, tasksBefore := ledger()
	// The ledger is not empty: the generation itself is in it, so unchanged
	// counts below mean the check path added no Task and no credit.
	if tasksBefore != 1 {
		t.Fatalf("ledger after the generation = %d task(s), want the one P2 task", tasksBefore)
	}

	command := chat.ToolCommand{
		OwnerID: "owner-acme", SessionID: "acme-session",
		Call: agents.ToolCall{ID: "call-1", Name: "inspect_model",
			Arguments: json.RawMessage(`{"model":"` + string(task.ID) + `"}`)},
		Tool: catalog.ChatToolSchema{Name: "inspect_model"},
	}
	refused, err := executor.InspectModel(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	// The store keeps the CDN's opaque label, so the bytes decide and the
	// bytes-not-GLB text is the one that applies.
	want := strings.NewReplacer("{asset}", string(model.ID), "{media}", model.MediaType).
		Replace(application.Chat.ModelReview.Source.SourceBytesNotGLB)
	if refused.Error != want || !refused.NoSubmission || refused.TaskID != "" {
		t.Fatalf("inspect_model = %+v, want the not-GLB refusal %q", refused, want)
	}
	// The outward MCP core reports the stable not_glb kind for the same file.
	_, err = executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
		Owner: "owner-acme", TaskID: task.ID, AssetID: model.ID,
	})
	var refusal *generationtool.ModelInspectRefusal
	if !errors.As(err, &refusal) || refusal.Kind != generationtool.ModelInspectNotGLB || refusal.Message != want {
		t.Fatalf("core inspection = %v, want kind %s with %q", err, generationtool.ModelInspectNotGLB, want)
	}

	// The refusal is stored as failed evidence, which is what makes the
	// uncheckable conclusion provable rather than a claim.
	record, found, err := selfChecks.Get(context.Background(), "owner-acme", "acme-session", string(task.ID))
	if err != nil || !found {
		t.Fatalf("self-check record = %+v found=%v err=%v", record, found, err)
	}
	if len(record.Checks) != 1 || record.Checks[0].Kind != chat.SelfCheckKindInspect ||
		record.Checks[0].OK || record.Checks[0].Error != want {
		t.Fatalf("stored evidence = %+v, want one failed inspect of %q", record.Checks, want)
	}

	recorded, err := executor.RecordSelfCheck(context.Background(), chat.ToolCommand{
		OwnerID: "owner-acme", SessionID: "acme-session",
		Call: agents.ToolCall{ID: "call-2", Name: "record_self_check",
			Arguments: json.RawMessage(`{"model":"` + string(task.ID) +
				`","status":"not_inspectable","summary":"交付的是 FBX，平台只能检查 GLB，未做模型检查"}`)},
		Tool: catalog.ChatToolSchema{Name: "record_self_check"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Error != "" || !recorded.NoSubmission {
		t.Fatalf("record_self_check = %+v, want a saved conclusion", recorded)
	}
	var payload struct {
		OK         bool   `json:"ok"`
		Status     string `json:"status"`
		StatusRole string `json:"status_role"`
	}
	if err := json.Unmarshal([]byte(recorded.AgentContent), &payload); err != nil {
		t.Fatalf("record_self_check result is not JSON: %v", err)
	}
	if !payload.OK || payload.Status != "not_inspectable" || payload.StatusRole != chat.SelfCheckRoleUncheckable {
		t.Fatalf("recorded conclusion = %+v, want not_inspectable", payload)
	}

	// Zero credits: the refusal and the conclusion added no Task and no credit.
	// The alternative the not-GLB text rules out is a paid GLB conversion, which
	// is a new Task; the session still holds only the generation.
	creditsAfter, tasksAfter := ledger()
	if creditsAfter != creditsBefore || tasksAfter != tasksBefore {
		t.Fatalf("the check path spent: tasks %d -> %d, credits %d -> %d",
			tasksBefore, tasksAfter, creditsBefore, creditsAfter)
	}
	page, err := harness.module.List(context.Background(), generation.OwnTasks("owner-acme"),
		generation.ListQuery{SessionID: "acme-session"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 1 || page.Tasks[0].ID != task.ID {
		t.Fatalf("the session's tasks = %+v, want only the quad generation", page.Tasks)
	}
}
