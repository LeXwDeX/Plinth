package lineage

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #330 review of !341: a session made before conversions were folded already
// names its versions in the chat history. A model handle (@model2) is bound
// to its Task in the stored attachment, but the version names ("3D v3") are
// written into tool results and Agent replies as text, computed from the
// version numbers of that moment. Folding an existing conversion would
// renumber the later versions and make that text point at another model, so
// only conversions made from the fold's start on are folded; earlier ones
// keep their node and number and are marked as conversions.
func TestHistoryKeepsNamingTheSameVersionsAfterTheFold330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	versionName := func(version int) string {
		return strings.ReplaceAll(chatSchema.Context.ModelVersionTemplate, "{version}", strconv.Itoa(version))
	}
	policy := chat.NewHandlePolicy(chatSchema.Context)

	// 生成 → 转换 → 再生成, all before the fold started (minute 10).
	first := task("gen-1", 1, modelOffering, nil, modelAsset("gen-1-a", "g1"))
	conversion := task("convert-1", 2, convertOffering, input(t, map[string]any{"source_task": "gen-1"}), modelAsset("convert-1-a", "c1"))
	second := task("gen-2", 3, modelOffering, nil, modelAsset("gen-2-a", "g2"))
	tasks := []generation.Task{second, conversion, first}
	ctx := context.Background()

	// What the session looked like before: three versions, and the history
	// names the second generation both by its handle and by its version.
	before, err := New(&fakeTasks{tasks: tasks}, fakeCaptures{}, fakeInputs{}, Config{MaxTasks: 50})
	if err != nil {
		t.Fatal(err)
	}
	old, err := before.SessionVersions(ctx, "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if old["gen-1"] != 1 || old["convert-1"] != 2 || old["gen-2"] != 3 {
		t.Fatalf("versions before = %v", old)
	}
	history := []chat.Message{
		{Role: "user", Attachments: []chat.Attachment{{Handle: "@model1", Kind: chat.AttachmentKindModel, TaskID: "gen-1"}}},
		{Role: "user", Attachments: []chat.Attachment{{Handle: "@model2", Kind: chat.AttachmentKindModel, TaskID: "gen-2"}}},
		{Role: "assistant", Content: "已为 @model2（" + versionName(old["gen-2"]) + "）换好贴图。"},
	}

	// After the upgrade: the fold started at minute 10.
	since := start.Add(10 * time.Minute)
	after, err := New(&fakeTasks{tasks: tasks}, fakeCaptures{}, fakeInputs{}, Config{MaxTasks: 50, Conversions: isConversion, FoldConversionsSince: since})
	if err != nil {
		t.Fatal(err)
	}
	now, err := after.SessionVersions(ctx, "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	for id, version := range old {
		if now[id] != version {
			t.Fatalf("%s was %s and is now %s: the history would name another model", id, versionName(version), versionName(now[id]))
		}
	}
	resolved, err := policy.ResolveHandles(history, nil, []string{"@model2"}, chat.AttachmentKindModel)
	if err != nil || len(resolved) != 1 || resolved[0].Attachment.TaskID != "gen-2" {
		t.Fatalf("@model2 = %+v, %v", resolved, err)
	}
	if said := history[2].Content; !strings.Contains(said, versionName(now[generation.TaskID(resolved[0].Attachment.TaskID)])) {
		t.Fatalf("the reply %q no longer names the version @model2 shows (%s)", said, versionName(now["gen-2"]))
	}
	page, err := after.SessionPage(ctx, "owner-a", "session-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, node := range page.Nodes {
		nodes[node.ID] = node
	}
	if node, ok := nodes["convert-1"]; !ok || !node.Conversion || node.Version != 2 {
		t.Fatalf("the earlier conversion must stay version 2, marked as a conversion: %#v", node)
	}
	if nodes["gen-2"].Version != 3 || nodes["gen-2"].Conversion {
		t.Fatalf("gen-2 = %#v", nodes["gen-2"])
	}
	if len(page.Conversions) != 0 {
		t.Fatalf("nothing made before the fold is folded: %v", page.Conversions)
	}

	// Folding every conversion would have renumbered the history's version.
	everything, _ := New(&fakeTasks{tasks: tasks}, fakeCaptures{}, fakeInputs{}, Config{MaxTasks: 50, Conversions: isConversion})
	if renumbered, _ := everything.SessionVersions(ctx, "owner-a", "session-1"); renumbered["gen-2"] == old["gen-2"] {
		t.Fatalf("the case must exercise a renumbering fold: %v", renumbered)
	}

	// Work after the upgrade: a new conversion is folded and takes no
	// number; the next version follows the existing ones.
	newer := task("convert-2", 11, convertOffering, input(t, map[string]any{"source_task": "gen-2"}), modelAsset("convert-2-a", "c2"))
	third := task("gen-3", 12, modelOffering, nil, modelAsset("gen-3-a", "g3"))
	later, _ := New(&fakeTasks{tasks: append([]generation.Task{third, newer}, tasks...)}, fakeCaptures{}, fakeInputs{}, Config{MaxTasks: 50, Conversions: isConversion, FoldConversionsSince: since})
	latest, err := later.SessionVersions(ctx, "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if latest["gen-1"] != 1 || latest["convert-1"] != 2 || latest["gen-2"] != 3 || latest["gen-3"] != 4 {
		t.Fatalf("versions after new work = %v", latest)
	}
	if _, numbered := latest["convert-2"]; numbered {
		t.Fatalf("a conversion made after the upgrade must be folded: %v", latest)
	}
	page, _ = later.SessionPage(ctx, "owner-a", "session-1", "", 10)
	if page.Conversions["convert-2"] != "gen-2" {
		t.Fatalf("conversions = %v", page.Conversions)
	}
}
