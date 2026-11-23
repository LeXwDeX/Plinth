package modeldelivery

// coverage_cross_offering_478_test.go 锁定 #478 P1-7：跨 Offering 修订的旧计划
// 结果不明时不得被当成从未转换。旧计划按旧修订的幂等键存放，键路径按当前修订
// 查不到，只能由 ListBySource 找到；record() 对 Failed 计划只看免费 refetch，
// ambiguous 的旧计划既不标 active 也不标 obtained，格式被当成从未转换 →
// 自动补齐/补齐格式会再发一次付费转换。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// TestCrossOfferingAmbiguousPlanHoldsFormats478：旧修订下失败且结果不明的计划
// 持有的格式必须读作 converting，自动补齐不得再发一次付费转换。
func TestCrossOfferingAmbiguousPlanHoldsFormats478(t *testing.T) {
	f := newAutoFixture(t)
	task := f.meshyGlbOnly("meshy-task", "owner")
	// 旧修订：计划按旧 Offering 修订的幂等键存放（键路径按当前修订查不到）。
	offering, ok := f.catalog.Get("meshy/processing/convert")
	if !ok {
		t.Fatal("默认目录缺少 meshy 转换 offering")
	}
	oldKey := planKey(task, offering, "")
	plan := processing.Plan{
		ID: processing.PlanID("owner", oldKey), OwnerID: "owner",
		SessionID: task.SessionID, SourceTaskID: task.ID,
		State: processing.Failed, Failure: processing.ReasonSubmitUnknown,
		Steps: []processing.Step{{
			OfferingID: "meshy/processing/convert",
			Input:      json.RawMessage(`{"source_asset":"meshy-task-a0"}`),
			State:      processing.Failed, Failure: processing.ReasonSubmitUnknown, TaskID: "convert-old",
		}},
	}
	f.store.plans[plan.ID] = plan
	// Catalog 发布新修订：当前候选键与旧计划 ID 不同，键路径找不到它。
	f.revisions["meshy/processing/convert"] = "2026-11-01.999.1"

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj", "usdz", "stl", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status != StatusConverting {
			t.Fatalf("%s = %#v；want converting（结果不明的旧计划可能已付费，不得当成从未转换）", id, row)
		}
	}
	// 自动补齐不得为这些格式再发一次付费转换。
	f.candidate(task)
	f.reconcile(t)
	for _, p := range f.plansOf(t, task.ID) {
		if p.ID == plan.ID {
			continue
		}
		if len(p.Steps) > 0 && p.Steps[0].OfferingID == "meshy/processing/convert" {
			t.Fatalf("为一个被旧计划持有的格式又建了付费计划: %#v", p)
		}
	}
}