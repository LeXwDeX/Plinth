package generationtool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// uploadedReferences resolves the chat's own upload URLs to their assets, in
// the order given, as the production resolver does.
type uploadedReferences struct{}

func (uploadedReferences) Resolve(_ context.Context, _ generation.OwnerID, urls []string) ([]generation.AssetID, error) {
	ids := make([]generation.AssetID, 0, len(urls))
	for _, url := range urls {
		ids = append(ids, generation.AssetID(strings.TrimPrefix(url, "/api/v1/assets/")))
	}
	return ids, nil
}

type pendingReferenceRun struct {
	result   chat.ToolResult
	content  map[string]any
	op       generationtool.Operation
	tool     catalog.ChatToolSchema
	prepared generation.PreparedInput
	grant    executiongrant.Grant
	repo     *sqlite.Repository
}

// runImageTo3DWithPictures asks the Agent's image-to-3D tool, with the given
// model picked, for a model from the given number of uploaded pictures, through
// the real kernel, grant service and operation store.
func runImageTo3DWithPictures(t *testing.T, mode, model string, pictures int) pendingReferenceRun {
	return runImageTo3DWithPicturesAndNote(t, mode, model, pictures, false)
}

func runImageTo3DWithPicturesAndNote(t *testing.T, mode, model string, pictures int, describe bool) pendingReferenceRun {
	t.Helper()
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	blobs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	c := qualifiedMultiviewCatalog(t)
	app := c.Application()
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", mock.New()); err != nil {
		t.Fatal(err)
	}
	m, err := generation.New(generation.Dependencies{Repository: repo, Catalog: c, Providers: registry, Inputs: inputs, Assets: blobs})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", OwnerName: "Owner", Title: "Pictures", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner", chat.Message{ID: "user", SessionID: "session", Role: "user", Content: "两张图一起做", CreatedAt: now},
		chat.Message{ID: "turn", SessionID: "session", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session", Status: "active"},
		chat.Turn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	urls := []string{}
	for index := range pictures {
		picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		picture.SetNRGBA(0, 0, color.NRGBA{R: uint8(30 * (index + 1)), G: 60, B: 90, A: 255})
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, picture); err != nil {
			t.Fatal(err)
		}
		asset, err := inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(encoded.Bytes()), MediaType: "image/png", AssetType: "image", MaxBytes: 20 << 20})
		if err != nil {
			t.Fatal(err)
		}
		urls = append(urls, "/api/v1/assets/"+string(asset.ID))
	}
	var manual, known []string
	for _, value := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(value))
	}
	for _, value := range app.Capabilities {
		known = append(known, string(value.ID))
	}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known), Modes: sessionMode{mode: mode}}
	executor, err := generationtool.New(generationtool.Config{Gateway: m, References: uploadedReferences{}, Operations: repo, Grants: grants,
		Chat: app.Chat, TaskStates: app.TaskStates, StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range app.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", Tool: tool,
		Call:    agents.ToolCall{ID: "call", Name: tool.Name, Arguments: json.RawMessage(`{"prompt":"a stool"}`)},
		Options: chat.TurnOptions{AIModel: model, CreativeDomain: "model-3d"},
		History: []chat.Message{{Role: app.Chat.Roles.User, ImageURLs: urls}}}
	if describe {
		_ = executor.ToolDescriptionNote(tool, command.Options)
	}
	result, err := executor.Execute(ctx, command, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := pendingReferenceRun{result: result, tool: tool, repo: repo}
	_ = json.Unmarshal([]byte(result.AgentContent), &run.content)
	if run.op, err = repo.LoadAgentOperation(ctx, "owner", "session", "turn", "call"); err != nil {
		t.Fatalf("operation: %v (result %+v)", err, result)
	}
	if run.prepared, err = m.GetPreparedInput(ctx, "owner", run.op.PreparedInputID); err != nil {
		t.Fatal(err)
	}
	if run.grant, err = repo.GetExecutionGrant(ctx, "owner", run.op.GrantRequestID); err != nil {
		t.Fatal(err)
	}
	return run
}

// oneReferenceLimitText is the Schema's note for a model that takes one
// picture (runtime.generation_messages.reference_limit_one, #329 note 99647).
func oneReferenceLimitText(t *testing.T) string {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	template := product.Application().Runtime.GenerationMessages.ReferenceLimitOne
	if !strings.Contains(template, "{model}") || !strings.Contains(template, "{total}") {
		t.Fatalf("no one-picture reference limit text: %q", template)
	}
	return template
}

// #329 E (dev-test 0518727d): with Meshy 7.1 picked and two pictures given, the
// step froze only the first, but the result the Agent read while the step
// waited on its approval card did not say so, and the reply claimed both
// pictures were sent. The waiting result now carries the same Schema note the
// finished result carries, and the operation keeps how many pictures were used
// of how many were given, so the card and the later result message can say it
// too.
func TestAStepWaitingOnItsCardSaysOnlyTheFirstPictureIsUsed(t *testing.T) {
	run := runImageTo3DWithPictures(t, "manual", "meshy-7.1", 2)
	if !run.result.NoSubmission || run.content["status"] != "awaiting_approval" {
		t.Fatalf("Meshy 7.1 image to 3D must wait on its card in manual mode: %+v", run.result)
	}
	template := oneReferenceLimitText(t)
	want := strings.NewReplacer("{model}", "Meshy 7.1", "{count}", "1", "{total}", "2").Replace(template)
	if got := run.content[run.tool.ResultFields.ReferenceNote]; got != want {
		t.Fatalf("waiting reference_note = %v, want %q (result %v)", got, want, run.content)
	}
	if run.op.ReferenceCount != 1 || run.op.ReferenceTotal != 2 {
		t.Fatalf("operation pictures = %d of %d, want 1 of 2", run.op.ReferenceCount, run.op.ReferenceTotal)
	}

	// One picture is used as given: no note, nothing recorded as left out.
	single := runImageTo3DWithPictures(t, "manual", "meshy-7.1", 1)
	if _, noted := single.content[single.tool.ResultFields.ReferenceNote]; noted || single.op.ReferenceTotal != 0 {
		t.Fatalf("one picture: result %v, operation %d of %d", single.content, single.op.ReferenceCount, single.op.ReferenceTotal)
	}

	// A multi-image model takes both: no note either.
	views := runImageTo3DWithPictures(t, "manual", "meshy-7.1-multiview", 2)
	if _, noted := views.content[views.tool.ResultFields.ReferenceNote]; noted || views.op.ReferenceTotal != 0 {
		t.Fatalf("multi-view: result %v, operation %d of %d", views.content, views.op.ReferenceCount, views.op.ReferenceTotal)
	}
}

// The same note reaches a step the session's automatic approval ran at once.
func TestAnAutomaticallyApprovedStepSaysOnlyTheFirstPictureIsUsed(t *testing.T) {
	run := runImageTo3DWithPictures(t, "auto", "meshy-7.1", 3)
	if run.result.NoSubmission || run.result.TaskID == "" {
		t.Fatalf("auto mode must submit: %+v", run.result)
	}
	if run.op.ReferenceCount != 1 || run.op.ReferenceTotal != 3 {
		t.Fatalf("operation pictures = %d of %d, want 1 of 3", run.op.ReferenceCount, run.op.ReferenceTotal)
	}
	if run.content["status"] == "awaiting_approval" {
		t.Fatalf("auto mode waited: %v", run.content)
	}
	if note, _ := run.content[run.tool.ResultFields.ReferenceNote].(string); note != "" &&
		note != strings.NewReplacer("{model}", "Meshy 7.1", "{count}", "1", "{total}", strconv.Itoa(3)).Replace(oneReferenceLimitText(t)) {
		t.Fatalf("auto reference_note = %q", note)
	}
}

// toolDescriptionNoteFixture returns the image-to-3D tool and an executor that
// resolves its description notes against the shipped Catalog.
func toolDescriptionNoteFixture(t *testing.T) (catalog.ChatToolSchema, *generationtool.Executor) {
	t.Helper()
	c := qualifiedMultiviewCatalog(t)
	app := c.Application()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "notes.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	registry := providers.NewRegistry()
	for _, adapter := range []string{"meshy.v2", "hyper3d.rodin.v1", "tripo.v3"} {
		_ = registry.Register(adapter, mock.New())
	}
	blobs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	kernel, err := generation.New(generation.Dependencies{Repository: repo, Catalog: c, Providers: registry, Inputs: inputs, Assets: blobs})
	if err != nil {
		t.Fatal(err)
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range app.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatal("the image-to-3d tool is not declared")
	}
	texts, err := catalog.NewTextLocalizer(app)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := generationtool.New(generationtool.Config{Gateway: kernel, References: &fakeReferences{}, Chat: app.Chat, TaskStates: app.TaskStates, Texts: texts})
	if err != nil {
		t.Fatal(err)
	}
	return tool, executor
}

// #329 E': before any call, an Agent said image to 3D treats the pictures as
// views of one object, which Meshy 7.1 does not do. The tool's description for
// a turn now states, in the Schema's words, how many pictures the model picked
// for 3D uses: one for a single-picture model, up to its limit for a
// multi-picture model.
func TestTheToolDescriptionFollowsThePickedModelsPictureLimit(t *testing.T) {
	tool, executor := toolDescriptionNoteFixture(t)
	notes := tool.ReferenceCapacityNote
	if notes.Single == "" || !strings.Contains(notes.Multiple, "{count}") {
		t.Fatalf("the image-to-3d tool declares no reference_capacity_note: %+v", notes)
	}
	// #356: a single-picture model points at list_offerings for other models'
	// capacities too, instead of letting the Agent infer them.
	if !strings.Contains(notes.Single, "list_offerings") || !strings.Contains(notes.Single, "reference_image_max") {
		t.Fatalf("the single-picture note does not point at list_offerings: %s", notes.Single)
	}
	fill := func(template, model string, count int) string {
		return strings.NewReplacer("{model}", model, "{count}", strconv.Itoa(count)).Replace(template)
	}
	for name, test := range map[string]struct {
		options chat.TurnOptions
		want    string
	}{
		"Meshy 7.1 in 3D":                 {chat.TurnOptions{AIModel: "meshy-7.1", CreativeDomain: "model-3d"}, fill(notes.Single, "Meshy 7.1", 1)},
		"Meshy 7.1 picked from 2D":        {chat.TurnOptions{AIModel: "gpt-image-2", CreativeDomain: "image", DomainModels: map[string]string{"model-3d": "meshy-7.1"}}, fill(notes.Single, "Meshy 7.1", 1)},
		"Meshy 7.1 multi-view":            {chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d", Locale: "zh-CN"}, fill(notes.Multiple, "Meshy 7.1 多视图", 4)},
		"Meshy 7.1 multi-view in English": {chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d", Locale: "en"}, fill(notes.Multiple, "Meshy 7.1 multi-view", 4)},
		"Rodin Gen-2.5":                   {chat.TurnOptions{AIModel: "rodin-gen-2.5", CreativeDomain: "model-3d"}, fill(notes.Multiple, "Rodin Gen-2.5", 5)},
		"Rodin Gen-2.5 in English":        {chat.TurnOptions{AIModel: "rodin-gen-2.5", CreativeDomain: "model-3d", Locale: "en"}, fill(notes.Multiple, "Rodin Gen-2.5", 5)},
		"no 3D model picked in 3D":        {chat.TurnOptions{CreativeDomain: "model-3d"}, ""},
		"a model of another domain":       {chat.TurnOptions{AIModel: "gpt-image-2", CreativeDomain: "model-3d"}, ""},
	} {
		if strings.HasPrefix(name, "Meshy") {
			model := "Meshy 7.1"
			if strings.Contains(name, "multi-view") {
				model = "Meshy 7.1 多视图"
				if test.options.Locale == "en" {
					model = "Meshy 7.1 multi-view"
				}
			}
			test.want += " " + fill(notes.NoPrompt, model, 0)
		}
		if got := executor.ToolDescriptionNote(tool, test.options); got != test.want {
			t.Errorf("%s: note = %q, want %q", name, got, test.want)
		}
	}
	// A tool without the declaration gets no note.
	plain := tool
	plain.ReferenceCapacityNote = catalog.ChatToolReferenceCapacityNote{}
	if got := executor.ToolDescriptionNote(plain, chat.TurnOptions{AIModel: "meshy-7.1", CreativeDomain: "model-3d"}); got != "" {
		t.Fatalf("undeclared note = %q", got)
	}
	old := tool
	old.ReferenceCapacityNote.NoPrompt = ""
	if got, want := executor.ToolDescriptionNote(old, chat.TurnOptions{AIModel: "meshy-7.1", CreativeDomain: "model-3d"}), fill(old.ReferenceCapacityNote.Single, "Meshy 7.1", 1); got != want {
		t.Fatalf("old note = %q, want %q", got, want)
	}
}

func TestReferenceToolNoteDoesNotChangePreparedInputOrApproval(t *testing.T) {
	for _, mode := range []string{"manual", "auto"} {
		t.Run(mode, func(t *testing.T) {
			run := runImageTo3DWithPicturesAndNote(t, mode, "meshy-7.1-multiview", 2, true)
			baseline := runImageTo3DWithPicturesAndNote(t, mode, "meshy-7.1-multiview", 2, false)
			var input map[string]any
			if err := json.Unmarshal(run.prepared.NormalizedInput, &input); err != nil {
				t.Fatal(err)
			}
			refs, ok := input["reference_assets"].([]any)
			if !ok || len(refs) != 2 || strings.Contains(string(run.prepared.NormalizedInput), "a stool") {
				t.Fatalf("prepared input = %s", run.prepared.NormalizedInput)
			}
			var baselineInput map[string]any
			if err := json.Unmarshal(baseline.prepared.NormalizedInput, &baselineInput); err != nil {
				t.Fatal(err)
			}
			baselineRefs, ok := baselineInput["reference_assets"].([]any)
			if !ok || len(baselineRefs) != len(refs) {
				t.Fatalf("baseline input = %s", baseline.prepared.NormalizedInput)
			}
			if len(run.prepared.Assets) != 2 || len(baseline.prepared.Assets) != 2 || run.prepared.Assets[0].SHA256 == run.prepared.Assets[1].SHA256 {
				t.Fatalf("expected two distinct reference assets: %+v", run.prepared.Assets)
			}
			for index := range run.prepared.Assets {
				actual, expected := run.prepared.Assets[index], baseline.prepared.Assets[index]
				actual.ID, expected.ID = "", ""
				if actual != expected {
					t.Fatalf("reference %d changed: %+v, baseline %+v", index, actual, expected)
				}
			}
			// The independent runs upload the same bytes under different asset IDs.
			delete(input, "reference_assets")
			delete(baselineInput, "reference_assets")
			if !reflect.DeepEqual(input, baselineInput) || run.grant.Status != baseline.grant.Status || run.result.NoSubmission != baseline.result.NoSubmission || (run.result.TaskID == "") != (baseline.result.TaskID == "") {
				t.Fatalf("note changed execution: input=%v baseline=%v, grant=%s baseline=%s", input, baselineInput, run.grant.Status, baseline.grant.Status)
			}
			if run.prepared.OfferingID != "meshy/meshy-7.1-multiview/image-to-3d" || run.grant.PreparedInputID != string(run.prepared.ID) {
				t.Fatalf("prepared=%+v grant=%+v", run.prepared, run.grant)
			}
			if mode == "manual" && (!run.result.NoSubmission || run.result.TaskID != "" || run.grant.Status != executiongrant.Pending) ||
				mode == "auto" && (run.result.NoSubmission || run.result.TaskID == "" || run.grant.Status != executiongrant.Bound) {
				t.Fatalf("result=%+v grant=%+v", run.result, run.grant)
			}
		})
	}
}

// #329: the multi-picture note guides the Agent; it is not a runtime rule. It
// scopes the limit to the picked model, does not infer other models' limits,
// leaves all-picture requests to the person, and never reports a refusal as a
// submission. The assertions use independent wording, not the Schema template.
func TestTheMultiPictureToolNoteScopesItsLimitToTheSelectedModel(t *testing.T) {
	tool, executor := toolDescriptionNoteFixture(t)
	for name, test := range map[string]struct {
		options chat.TurnOptions
		model   string
		count   int
	}{
		"Meshy 7.1 multi-view":            {chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d"}, "Meshy 7.1 多视图", 4},
		"Meshy 7.1 multi-view in English": {chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d", Locale: "en"}, "Meshy 7.1 multi-view", 4},
		"Rodin Gen-2.5":                   {chat.TurnOptions{AIModel: "rodin-gen-2.5", CreativeDomain: "model-3d"}, "Rodin Gen-2.5", 5},
	} {
		note := executor.ToolDescriptionNote(tool, test.options)
		limit := strconv.Itoa(test.count)
		for _, want := range []string{
			"The model picked for 3D (" + test.model + ") takes up to " + limit + " reference pictures",
			"This limit applies only to the selected model.",
			"Other models may have different limits; before claiming another model's limit, use list_offerings and read that offering's reference_image_max; do not infer it from this model or say that no model takes more, and do not switch the selected model on your own.",
			"If the person requires all the pictures and gives more than " + limit + ", do not choose or drop any yourself: explain this limit and ask the person to reduce the pictures or switch the selected model.",
			"A tool refusal is not a submission: do not describe refused pictures as submitted or truncated; only a successful or pending result's reference_note states which pictures were used.",
		} {
			if !strings.Contains(note, want) {
				t.Errorf("%s: note %q does not state %q", name, note, want)
			}
		}
		for _, claim := range []string{
			"views of the same object",
			"views of one object",
			"views of a single object",
		} {
			if strings.Contains(note, claim) {
				t.Errorf("%s: note keeps an unverified same-object claim %q: %q", name, claim, note)
			}
		}
	}
}

func TestReferenceToolDescriptionDoesNotPromiseUnsentText(t *testing.T) {
	tool, executor := toolDescriptionNoteFixture(t)
	const warning = "Do not promise that the person's text will affect this image-to-3D result"
	for _, tc := range []struct {
		model, locale string
		warn          bool
	}{
		{"meshy-7.1", "en", true}, {"meshy-7.1", "zh-CN", true},
		{"meshy-7.1-multiview", "en", true}, {"meshy-7.1-multiview", "zh-CN", true},
		{"rodin-gen-2.5", "en", false}, {"rodin-gen-2.5", "zh-CN", false},
		{"unknown", "en", false}, {"", "zh-CN", false},
	} {
		t.Run(tc.model+"/"+tc.locale, func(t *testing.T) {
			note := executor.ToolDescriptionNote(tool, chat.TurnOptions{AIModel: tc.model, CreativeDomain: "model-3d", Locale: tc.locale})
			if strings.Contains(note, warning) != tc.warn {
				t.Fatalf("warning presence = %v, want %v: %q", strings.Contains(note, warning), tc.warn, note)
			}
			if tc.warn && (!strings.Contains(note, "When this tool uses the selected model (") || !strings.Contains(note, ") for image-to-3D, the tool's text prompt is not sent to this service.")) {
				t.Fatalf("warning is not conditional on this tool's image-to-3D execution: %q", note)
			}
			for _, guidance := range []string{
				"parameter controls independently of the text prompt",
				"do not infer that changing reference pictures is the only way to influence the result",
				"Recommend a setting only when the selected Offering's facts support it, not from general provider knowledge",
			} {
				if strings.Contains(note, guidance) != tc.warn {
					t.Errorf("guidance %q presence = %v, want %v: %q", guidance, strings.Contains(note, guidance), tc.warn, note)
				}
			}
		})
	}
	for _, model := range []string{"meshy-7.1-multiview", "rodin-gen-2.5"} {
		note := executor.ToolDescriptionNote(tool, chat.TurnOptions{AIModel: model, CreativeDomain: "model-3d"})
		if !strings.Contains(note, "Do not claim that a collage sent as one picture is equivalent to sending each original picture separately unless the selected Offering's input contract explicitly supports it") {
			t.Errorf("%s: missing collage guidance: %s", model, note)
		}
	}
}

func TestReferenceToolDescriptionFollowsFrozenHarnessOffering(t *testing.T) {
	c := qualifiedMultiviewCatalog(t)
	app := c.Application()
	tool, _ := toolDescriptionNoteFixture(t)
	meshy, ok := c.Get("meshy/meshy-7.1-multiview/image-to-3d")
	if !ok {
		t.Fatal("missing Meshy offering")
	}
	rodin, ok := c.Get("hyper3d/rodin-gen-2.5/image-to-3d")
	if !ok {
		t.Fatal("missing Rodin offering")
	}
	gateway := &fakeGateway{offerings: []generation.Offering{meshy, rodin}, unavailable: map[generation.OfferingID]bool{}}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{}, Chat: app.Chat, TaskStates: app.TaskStates})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func(o generation.Offering) *resolve.Snapshot {
		return &resolve.Snapshot{Selection: harness.Selection{OperationID: "selected", OfferingID: string(o.ID), OfferingRevision: o.Revision}, ToolName: tool.Name, Capability: tool.Capability, OfferingRevision: o.Revision}
	}
	// Persisted snapshots do not restore Offering; a stale pointer must not win either.
	stalePointer := snapshot(meshy)
	stalePointer.Offering = &rodin
	for _, tc := range []struct {
		name, workspace string
		snap            *resolve.Snapshot
		want, absent    string
	}{
		{"Meshy not workspace Rodin", "rodin-gen-2.5", snapshot(meshy), "Do not promise that the person's text", "Rodin Gen-2.5"},
		{"stale offering pointer", "rodin-gen-2.5", stalePointer, "Do not promise that the person's text", "Rodin Gen-2.5"},
		{"Rodin not workspace Meshy", "meshy-7.1-multiview", snapshot(rodin), "Rodin Gen-2.5", "Do not promise that the person's text"},
		{"unknown", "rodin-gen-2.5", snapshot(generation.Offering{ID: "missing", Revision: "r1"}), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := executor.ToolDescriptionNote(tool, chat.TurnOptions{AIModel: tc.workspace, CreativeDomain: "model-3d", Harness: tc.snap})
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) || tc.absent != "" && strings.Contains(got, tc.absent) {
				t.Fatalf("note = %q", got)
			}
		})
	}
	for name, mutate := range map[string]func(*resolve.Snapshot){
		"different snapshot revision": func(s *resolve.Snapshot) { s.OfferingRevision = "old" },
		"different selected revision": func(s *resolve.Snapshot) { s.OfferingRevision = "old"; s.Selection.OfferingRevision = "old" },
		"different tool":              func(s *resolve.Snapshot) { s.ToolName = "other" },
		"different capability":        func(s *resolve.Snapshot) { s.Capability = generation.TextTo3D },
	} {
		t.Run(name, func(t *testing.T) {
			s := snapshot(meshy)
			mutate(s)
			if got := executor.ToolDescriptionNote(tool, chat.TurnOptions{AIModel: "rodin-gen-2.5", CreativeDomain: "model-3d", Harness: s}); got != "" {
				t.Fatalf("note = %q", got)
			}
		})
	}
	gateway.unavailable[meshy.ID] = true
	if got := executor.ToolDescriptionNote(tool, chat.TurnOptions{AIModel: "rodin-gen-2.5", CreativeDomain: "model-3d", Harness: snapshot(meshy)}); got != "" {
		t.Fatalf("unavailable offering note = %q", got)
	}
	gateway.unavailable[meshy.ID] = false
	gateway.offerings[0].Enabled = false
	if got := executor.ToolDescriptionNote(tool, chat.TurnOptions{AIModel: "rodin-gen-2.5", CreativeDomain: "model-3d", Harness: snapshot(meshy)}); got != "" {
		t.Fatalf("disabled offering note = %q", got)
	}
	gateway.offerings[0].Enabled = true
	argumentTool := tool
	argumentTool.OfferingArgument = "offering_id"
	if got := executor.ToolDescriptionNote(argumentTool, chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d"}); got != "" {
		t.Fatalf("argument choice note = %q", got)
	}
	argumentTool.OfferingArgument = ""
	argumentTool.OfferingFromSource = "source"
	if got := executor.ToolDescriptionNote(argumentTool, chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d"}); got != "" {
		t.Fatalf("source choice note = %q", got)
	}
	free := snapshot(rodin)
	free.Selection.OperationID = ""
	if got := executor.ToolDescriptionNote(tool, chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d", Harness: free}); !strings.Contains(got, "Do not promise that the person's text") {
		t.Fatalf("inactive harness did not use workspace model: %q", got)
	}
}

// #80: the source fact is frozen with the same prepared input. A direct,
// unfettered selection of two uploads and a one-picture model keeps the first
// picture, so only the prefix case may use the limit text; the fact records
// that and survives a reload from the operation store.
func TestImageTo3DReferenceSourceFactIsFrozenWithThePreparedInput(t *testing.T) {
	run := runImageTo3DWithPictures(t, "manual", "meshy-7.1", 2)
	fact := run.op.ReferenceSource
	if fact.Identity == "" || fact.Selected != 2 || fact.Resolved != 2 || fact.Frozen != 1 ||
		!fact.Prefix || fact.AllFront || fact.Replaced || fact.Deduped {
		t.Fatalf("source fact = %+v", fact)
	}
	if fact.FrontTaskID != "" || fact.FrontAssetID != "" {
		t.Fatalf("ordinary uploads named a front source: %+v", fact)
	}
	note, _ := run.content[run.tool.ResultFields.ReferenceNote].(string)
	if note == "" || !strings.Contains(note, "1") || !strings.Contains(note, "2") {
		t.Fatalf("waiting result note = %q", note)
	}
	reloaded, err := run.repo.LoadAgentOperation(context.Background(), "owner", "session", "turn", "call")
	if err != nil {
		t.Fatal(err)
	}
	if !sameReferenceSourceForTest(reloaded.ReferenceSource, fact) {
		t.Fatalf("reloaded fact = %+v, want %+v", reloaded.ReferenceSource, fact)
	}
}

func sameReferenceSourceForTest(a, b generationtool.ReferenceSourceFact) bool {
	a.Trusted, b.Trusted = false, false
	return a == b
}

// #80: an automatically approved image-to-3D step freezes the same source
// fact as a manual one; the sentence that reports it reads that fact on the
// stored operation, not today's groups.
func TestAutoApprovedImageTo3DFreezesTheSourceFact(t *testing.T) {
	run := runImageTo3DWithPictures(t, "auto", "meshy-7.1", 2)
	if run.result.TaskID == "" || run.op.ReferenceSource.Identity == "" {
		t.Fatalf("auto step = %+v fact=%+v", run.result, run.op.ReferenceSource)
	}
	fact := run.op.ReferenceSource
	if fact.Selected != 2 || fact.Frozen != 1 || !fact.Prefix || fact.AllFront {
		t.Fatalf("auto source fact = %+v", fact)
	}
	if run.result.AwaitTask {
		// The Task is running; this receipt is not a result and must not carry
		// a source sentence yet.
		if note, ok := run.content[run.tool.ResultFields.ReferenceNote]; ok {
			t.Fatalf("running receipt claimed a source: %v", note)
		}
	}
}
