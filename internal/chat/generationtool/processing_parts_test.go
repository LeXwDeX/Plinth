package generationtool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// savedGLBTask is a succeeded 3D task whose saved file carries the platform
// GLB media type the local model-parts Offerings take.
func savedGLBTask(id string) generation.Task {
	return generation.Task{
		ID: generation.TaskID(id), OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.TextTo3D, State: generation.TaskSucceeded,
		Offering: generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy",
			ProviderName: "Meshy AI", ModelName: "Meshy 7.1"},
		CreatedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
		Assets: []generation.Asset{
			{ID: generation.AssetID(id + "-glb"), TaskID: generation.TaskID(id), Role: "model", Variant: "glb",
				Ordinal: 1, StorageKey: "key/" + id + "-glb", MediaType: "model/gltf-binary", Size: 4096},
			{ID: generation.AssetID(id + "-thumb"), TaskID: generation.TaskID(id), Role: "thumbnail",
				StorageKey: "key/" + id + "-thumb", MediaType: "image/png", Size: 128},
		},
	}
}

func partsManifestJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(schema.ModelPartsManifest{
		Version: schema.ModelPartsManifestVersion,
		Mode:    schema.ModelSplitModeConnectedComponent,
		Parts: []schema.ModelPart{
			{Ordinal: 1, Name: "cube_a", Triangles: 12,
				Bounds: schema.ModelPartBounds{Min: [3]float64{-4, -1, -1}, Max: [3]float64{-2, 1, 1}},
				Source: schema.ModelPartSource{Nodes: []string{"toy"}}},
			{Ordinal: 2, Name: "cube_b", Triangles: 12,
				Bounds: schema.ModelPartBounds{Min: [3]float64{-0.5, -0.5, -0.5}, Max: [3]float64{0.5, 0.5, 0.5}},
				Source: schema.ModelPartSource{Nodes: []string{"toy"}}},
			{Ordinal: 3, Name: "fragment", Triangles: 1,
				Bounds: schema.ModelPartBounds{Min: [3]float64{1.2, 2.4, 0}, Max: [3]float64{1.8, 2.8, 0}},
				Source: schema.ModelPartSource{Nodes: []string{"toy"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// splitTask is what the kernel persists after a successful split: one model
// asset per part in ordinal order plus the manifest of the same result. The
// task ID prefixes the asset IDs so two fixtures never collide.
func splitTask(id string, manifest []byte) generation.Task {
	taskID := generation.TaskID(id)
	task := generation.Task{
		ID: taskID, OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.ModelSplit, State: generation.TaskSucceeded,
		Offering: generation.Offering{ID: "local/model-parts/split", ProviderID: "local",
			ProviderName: "本地模型处理", ModelName: "拆解模型（本地）"},
		CreatedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
	}
	for ordinal := 1; ordinal <= 3; ordinal++ {
		task.Assets = append(task.Assets, generation.Asset{
			ID: generation.AssetID(fmt.Sprintf("%s-model-%d", id, ordinal)), TaskID: taskID,
			Role: "model", Variant: "glb", Ordinal: ordinal,
			StorageKey: fmt.Sprintf("key/%s-%d", id, ordinal), MediaType: "model/gltf-binary", Size: 512,
		})
	}
	task.Assets = append(task.Assets, generation.Asset{
		ID: generation.AssetID(id + "-manifest"), TaskID: taskID, Role: "parts_manifest",
		Variant: "json", Ordinal: 1, StorageKey: "key/" + id + "-manifest",
		MediaType: "application/json", Size: int64(len(manifest)),
	})
	return task
}

func splitHistory() []chat.Message {
	return []chat.Message{{Role: "assistant", Attachments: []chat.Attachment{
		{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated,
			TaskID: "split-1", AssetID: "split-1-model-1"},
	}}}
}

type listedPart struct {
	Ordinal   int                    `json:"ordinal"`
	AssetID   string                 `json:"asset_id"`
	Name      string                 `json:"name"`
	Triangles int                    `json:"triangles"`
	Bounds    schema.ModelPartBounds `json:"bounds"`
	NextSteps []struct {
		Tool string `json:"tool"`
		Step string `json:"step"`
	} `json:"next_steps"`
}

type listedModel struct {
	TaskID      string `json:"task_id"`
	ModelAssets []struct {
		AssetID string `json:"asset_id"`
		Format  string `json:"format"`
	} `json:"model_assets"`
	Parts []listedPart `json:"parts"`
}

// TestLocalModelPartsAgentChainSplitsPartRetexturesAndMerges walks the Agent
// chain the feature promises: the split is a public Task, a part is named by
// @modelN#k for a later step, and merge takes several saved GLBs from different
// tasks. The fake gateway stands in for the kernel; every step still freezes
// its exact input and goes through preparation and submission.
func TestLocalModelPartsAgentChainSplitsPartRetexturesAndMerges(t *testing.T) {
	h := newProcessingHarness(t, "auto", func(*processingHarness) []generation.Task {
		return []generation.Task{savedGLBTask("model-1"), savedGLBTask("model-2")}
	})
	result, _ := h.run("process_model",
		`{"step":"model-split","source_task_id":"model-1","mode":"primitive","min_part_ratio":0.01,"max_parts":8}`)
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("split was not submitted: %s", result.AgentContent)
	}
	if frozen := h.gateway.frozen[0]; frozen.OfferingID != "local/model-parts/split" ||
		frozen.Capability != generation.ModelSplit {
		t.Fatalf("split frozen step = %+v", frozen)
	}
	splitInput := h.frozenInput(0)
	if splitInput["source_asset"] != "model-1-glb" || splitInput["mode"] != "primitive" ||
		splitInput["min_part_ratio"] != 0.01 || splitInput["max_parts"] != float64(8) {
		t.Fatalf("split input = %v", splitInput)
	}
	if content := h.content(result); content["success"] != true || !zeroCredits(content) {
		t.Fatalf("split result = %v", content)
	}

	// The kernel persists the split result; the Agent can then read the parts.
	manifest := partsManifestJSON(t)
	h.gateway.tasks["split-1"] = splitTask("split-1", manifest)
	h.config.ModelAssets = newReviewAssets(map[generation.AssetID][]byte{"split-1-manifest": manifest})
	executor, err := generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.executor = executor
	listed, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var models struct {
		Models []listedModel `json:"models"`
	}
	if err := json.Unmarshal(listed, &models); err != nil {
		t.Fatal(err)
	}
	var splitView *listedModel
	for index := range models.Models {
		if models.Models[index].TaskID == "split-1" {
			splitView = &models.Models[index]
		}
	}
	if splitView == nil || len(splitView.Parts) != 3 {
		t.Fatalf("split parts = %+v; want three parts", splitView)
	}
	part := splitView.Parts[1]
	if part.Ordinal != 2 || part.AssetID != "split-1-model-2" || part.Name != "cube_b" || part.Triangles != 12 ||
		part.Bounds.Min != [3]float64{-0.5, -0.5, -0.5} || part.Bounds.Max != [3]float64{0.5, 0.5, 0.5} {
		t.Fatalf("part 2 = %+v", part)
	}
	retextures := 0
	for _, step := range part.NextSteps {
		if step.Tool == "retexture_model" || step.Step == "retexture" {
			retextures++
		}
	}
	if retextures != 1 {
		t.Fatalf("a part must offer retexture as a next step: %+v", part.NextSteps)
	}

	// @modelN#k names the saved file of ordinal k, and the Meshy retexture
	// Offering takes it as its saved source (the providers differ, so no
	// upstream job can be bound).
	result, _ = h.runAs("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"@model1#2","offering_id":"meshy/processing/retexture","prompt":"mossy"}`,
		splitHistory())
	if result.NoSubmission || h.gateway.submitCount != 2 {
		t.Fatalf("part retexture was not submitted: %s", result.AgentContent)
	}
	if frozen := h.gateway.frozen[1]; frozen.OfferingID != "meshy/processing/retexture" ||
		frozen.Capability != generation.Retexture {
		t.Fatalf("retexture frozen step = %+v", frozen)
	}
	retextureInput := h.frozenInput(1)
	if retextureInput["source_asset"] != "split-1-model-2" || retextureInput["prompt"] != "mossy" {
		t.Fatalf("part handle bound %v; want the second part", retextureInput)
	}
	if _, viaTask := retextureInput["source_task"]; viaTask {
		t.Fatalf("a local split task must not be passed as an upstream source task: %v", retextureInput)
	}

	// The explicit asset argument binds the same part without a handle.
	result, _ = h.run("retexture_model",
		`{"source_task_id":"split-1","source_asset_id":"split-1-model-2","offering_id":"meshy/processing/retexture","prompt":"mossy"}`)
	if result.NoSubmission || h.gateway.submitCount != 3 || h.frozenInput(2)["source_asset"] != "split-1-model-2" {
		t.Fatalf("explicit part asset was not bound: %s", result.AgentContent)
	}

	// Merge takes saved GLBs from different finished tasks by asset ID.
	result, _ = h.run("process_model",
		`{"step":"model-merge","source_assets":["split-1-model-1","model-2-glb"],"name":"assembly"}`)
	if result.NoSubmission || h.gateway.submitCount != 4 {
		t.Fatalf("merge was not submitted: %s", result.AgentContent)
	}
	if frozen := h.gateway.frozen[3]; frozen.OfferingID != "local/model-parts/merge" ||
		frozen.Capability != generation.ModelMerge {
		t.Fatalf("merge frozen step = %+v", frozen)
	}
	mergeInput := h.frozenInput(3)
	sources, _ := mergeInput["source_assets"].([]any)
	if len(sources) != 2 || sources[0] != "split-1-model-1" || sources[1] != "model-2-glb" ||
		mergeInput["name"] != "assembly" {
		t.Fatalf("merge input = %v", mergeInput)
	}
	if content := h.content(result); content["success"] != true || !zeroCredits(content) {
		t.Fatalf("merge result = %v", content)
	}
}

// TestProcessModelRefusesUnknownPartsAndUnusableSources keeps the part handle
// as strictly scoped as a task ID: an ordinal the result does not have, a task
// of another owner, and a source that has not finished are all refused before
// anything is prepared.
func TestProcessModelRefusesUnknownPartsAndUnusableSources(t *testing.T) {
	manifest := partsManifestJSON(t)
	pending := savedGLBTask("pending-1")
	pending.State = generation.TaskRunning
	otherOwner := splitTask("split-other", manifest)
	otherOwner.OwnerID = "owner-b"
	h := newProcessingHarness(t, "auto", func(*processingHarness) []generation.Task {
		return []generation.Task{savedGLBTask("model-1"), splitTask("split-1", manifest), pending, otherOwner}
	})
	for _, test := range []struct {
		name    string
		args    string
		history []chat.Message
		want    string
	}{
		{"unknown ordinal", `{"step":"model-split","source_task_id":"@model1#9"}`, splitHistory(), "@model1#9"},
		{"part of an image handle", `{"step":"model-split","source_task_id":"@img1#2"}`, splitHistory(), "No finished 3D model"},
		{"another owner's task", `{"step":"model-split","source_task_id":"@model1#2"}`, []chat.Message{{
			Role: "assistant", Attachments: []chat.Attachment{
				{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated,
					TaskID: "split-other", AssetID: "split-other-model-1"}}}}, "No finished 3D model"},
		{"unfinished source", `{"step":"model-split","source_task_id":"pending-1"}`, nil, "has not finished"},
		{"manifest is not a model", `{"step":"model-split","source_task_id":"split-1","source_asset_id":"split-1-manifest"}`,
			nil, "No finished 3D model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, _ := h.runAs("owner-a", "session-a", "process_model", test.args, test.history)
			h.expectRejected(result, test.want)
			if len(h.gateway.frozen) != 0 {
				t.Fatalf("refused call froze an input: %+v", h.gateway.frozen)
			}
		})
	}
}

func TestProcessModelMergeRefusesInvalidSourceLists(t *testing.T) {
	manifest := partsManifestJSON(t)
	otherOwner := splitTask("split-other", manifest)
	otherOwner.OwnerID = "owner-b"
	h := newProcessingHarness(t, "auto", func(*processingHarness) []generation.Task {
		return []generation.Task{splitTask("split-1", manifest), savedGLBTask("model-1"), otherOwner}
	})
	tooMany := make([]string, 0, 65)
	for index := 0; index < 65; index++ {
		tooMany = append(tooMany, fmt.Sprintf(`"part-%d"`, index))
	}
	for _, test := range []struct {
		name string
		args string
		want string
	}{
		{"one source", `{"step":"model-merge","source_assets":["split-1-model-1"]}`, "between 2 and 64"},
		{"too many sources", `{"step":"model-merge","source_assets":[` + strings.Join(tooMany, ",") + `]}`, "between 2 and 64"},
		{"duplicate source", `{"step":"model-merge","source_assets":["split-1-model-1","split-1-model-1"]}`, "distinct"},
		{"empty source", `{"step":"model-merge","source_assets":["split-1-model-1",""]}`, "distinct"},
		{"unknown source", `{"step":"model-merge","source_assets":["split-1-model-1","ghost"]}`, "No finished 3D model"},
		{"another owner's source", `{"step":"model-merge","source_assets":["split-1-model-1","split-other-model-1"]}`, "No finished 3D model"},
		{"single-source argument", `{"step":"model-merge","source_assets":["split-1-model-1","split-1-model-2"],"source_task_id":"split-1"}`, "not supported"},
		{"explicit asset argument", `{"step":"model-merge","source_assets":["split-1-model-1","split-1-model-2"],"source_asset_id":"split-1-model-1"}`, "not supported"},
		{"merge arguments on another step", `{"step":"model-split","source_task_id":"split-1","source_assets":["split-1-model-1","split-1-model-2"]}`, "not supported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, _ := h.run("process_model", test.args)
			h.expectRejected(result, test.want)
			if len(h.gateway.frozen) != 0 {
				t.Fatalf("refused merge froze an input: %+v", h.gateway.frozen)
			}
		})
	}
}

// A missing or unreadable parts manifest must not take the whole model out of
// list_session_models: the model and its saved files stay visible.
func TestListSessionModelsKeepsTheModelWhenThePartsManifestIsUnreadable(t *testing.T) {
	manifest := partsManifestJSON(t)
	for name, assets := range map[string]*reviewAssets{
		"missing manifest": newReviewAssets(nil),
		"corrupt manifest": newReviewAssets(map[generation.AssetID][]byte{
			"split-1-manifest": []byte(`{"version":1,"mode":"node","parts":[]}`),
		}),
	} {
		t.Run(name, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", func(*processingHarness) []generation.Task {
				return []generation.Task{splitTask("split-1", manifest)}
			})
			h.config.ModelAssets = assets
			executor, err := generationtool.New(h.config)
			if err != nil {
				t.Fatal(err)
			}
			listing, err := executor.ListSessionModels(context.Background(), "owner-a", "session-a")
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Models []listedModel `json:"models"`
			}
			if err := json.Unmarshal(listing, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Models) != 1 || decoded.Models[0].TaskID != "split-1" || len(decoded.Models[0].Parts) != 0 {
				t.Fatalf("listing = %s; want the split model without parts", listing)
			}
			if len(decoded.Models[0].ModelAssets) != 3 {
				t.Fatalf("model assets = %+v; want all three saved parts", decoded.Models[0].ModelAssets)
			}
		})
	}
}

// plan_model_processing runs the same local split Offering through the plan
// worker: the step is normalized from Catalog defaults and submitted only with
// its frozen source.
func TestAgentPlanRunsALocalSplitStep(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	source := h.gateway.tasks["model-1"]
	for index := range source.Assets {
		if source.Assets[index].Role == "model" {
			source.Assets[index].MediaType = "model/gltf-binary"
		}
	}
	h.gateway.tasks["model-1"] = source
	_, plan := h.propose(`{"source_task_id":"model-1","steps":[{"capability":"model-split","parameters":{"mode":"primitive"}}]}`)
	if len(plan.Steps) != 1 || plan.Steps[0].OfferingID != "local/model-parts/split" ||
		plan.Steps[0].Capability != generation.ModelSplit {
		t.Fatalf("plan step = %+v", plan.Steps)
	}
	var proposed map[string]any
	if err := json.Unmarshal(plan.Steps[0].Input, &proposed); err != nil {
		t.Fatal(err)
	}
	if proposed["mode"] != "primitive" || proposed["min_part_ratio"] != 0.002 || proposed["max_parts"] != float64(32) {
		t.Fatalf("plan step input = %v; want Catalog defaults", proposed)
	}
	h.tick()
	plan = h.get(plan.ID)
	if plan.State != processing.Running || len(h.gateway.commands) != 1 || plan.Steps[0].TaskID != "plan-task-1" {
		t.Fatalf("plan = %+v submits=%d", plan, len(h.gateway.commands))
	}
	prepared, err := h.gateway.repo.GetPreparedInput(context.Background(), "owner-a", h.gateway.commands[0].PreparedInputID)
	if err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if prepared.Capability != generation.ModelSplit || prepared.OfferingID != "local/model-parts/split" ||
		json.Unmarshal(prepared.NormalizedInput, &submitted) != nil || submitted["source_asset"] != "model-1-glb" ||
		submitted["mode"] != "primitive" {
		t.Fatalf("plan submission = %+v input=%s", prepared, prepared.NormalizedInput)
	}
}

// zeroCredits reports a result that names no credits (the field is omitted
// for a zero-credit step) or names zero.
func zeroCredits(content map[string]any) bool {
	credits, present := content["estimated_credits"]
	return !present || credits == float64(0)
}

// catalogWithTwoMergeOfferings adds a second local merge Offering so the
// multi-source path can be tested with an actual choice.
func catalogWithTwoMergeOfferings(t *testing.T) *catalog.Catalog {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &document); err != nil {
		t.Fatal(err)
	}
	var offerings []map[string]json.RawMessage
	if err := json.Unmarshal(document["offerings"], &offerings); err != nil {
		t.Fatal(err)
	}
	var clone map[string]json.RawMessage
	for _, offering := range offerings {
		var id string
		_ = json.Unmarshal(offering["id"], &id)
		if id == "local/model-parts/merge" {
			clone = maps.Clone(offering)
		}
	}
	if clone == nil {
		t.Fatal("merge offering missing")
	}
	clone["id"] = json.RawMessage(`"local/model-parts/merge-alt"`)
	clone["model_id"] = json.RawMessage(`"local-model-merge-alt"`)
	clone["revision"] = json.RawMessage(`"2026-11-05.2"`)
	clone["display_order"] = json.RawMessage(`2`)
	offerings = append(offerings, clone)
	var err error
	if document["offerings"], err = json.Marshal(offerings); err != nil {
		t.Fatal(err)
	}
	var application map[string]json.RawMessage
	if err := json.Unmarshal(document["application"], &application); err != nil {
		t.Fatal(err)
	}
	var discovery map[string]json.RawMessage
	if err := json.Unmarshal(application["provider_discovery"], &discovery); err != nil {
		t.Fatal(err)
	}
	var legacy []map[string]json.RawMessage
	if err := json.Unmarshal(discovery["legacy_models"], &legacy); err != nil {
		t.Fatal(err)
	}
	legacy = append(legacy, map[string]json.RawMessage{
		"model_id": json.RawMessage(`"local-model-merge-alt"`), "provider_key": json.RawMessage(`"local"`),
	})
	if discovery["legacy_models"], err = json.Marshal(legacy); err != nil {
		t.Fatal(err)
	}
	if application["provider_discovery"], err = json.Marshal(discovery); err != nil {
		t.Fatal(err)
	}
	if document["application"], err = json.Marshal(application); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestMergeWithSeveralServicesOpensAChoiceCard(t *testing.T) {
	h, repo := choiceHarness(t, "auto")
	product := catalogWithTwoMergeOfferings(t)
	h.gateway.product = product
	h.gateway.grantGateway.offering = product.Get
	executor, err := generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.executor = executor
	// The choice is asked before the source files are checked, but the
	// answered call must still bind real model files of this session.
	for _, id := range []generation.TaskID{"model-1", "rig-1"} {
		task := h.gateway.tasks[id]
		for index := range task.Assets {
			if task.Assets[index].Role == "model" {
				task.Assets[index].MediaType = "model/gltf-binary"
				task.Assets[index].Size = 4096
			}
		}
		h.gateway.tasks[id] = task
	}
	result, _ := h.run("process_model", `{"step":"model-merge","source_assets":["model-1-glb","rig-1-glb"]}`)
	if !result.NoSubmission || len(h.gateway.frozen) != 0 || h.gateway.submitCount != 0 {
		t.Fatalf("a merge with two services ran one: %+v frozen=%d", result, len(h.gateway.frozen))
	}
	content := h.content(result)
	options, _ := content["offering_choices"].([]any)
	if content["choice_required"] != true || len(options) != 2 {
		t.Fatalf("choice result = %v", content)
	}
	cards := h.cards(repo)
	if len(cards) != 1 || cards[0].State != chat.ChoicePending || cards[0].Capability != "model-merge" ||
		cards[0].ToolName != "process_model" {
		t.Fatalf("stored card = %+v", cards)
	}
	result, _ = h.run("process_model",
		`{"step":"model-merge","source_assets":["model-1-glb","rig-1-glb"],"offering_id":"local/model-parts/merge-alt"}`)
	if result.NoSubmission || len(h.gateway.frozen) != 1 ||
		h.gateway.frozen[0].OfferingID != "local/model-parts/merge-alt" || h.gateway.submitCount != 1 {
		t.Fatalf("chosen merge = %+v frozen=%v submits=%d", h.content(result), h.gateway.frozen, h.gateway.submitCount)
	}
	cards = h.cards(repo)
	if cards[0].State != chat.ChoiceAnswered || cards[0].AnswerOptionID != "local/model-parts/merge-alt" ||
		cards[0].AnswerSource != chat.ChoiceAnsweredByAgent {
		t.Fatalf("answered card = %+v", cards[0])
	}
}
