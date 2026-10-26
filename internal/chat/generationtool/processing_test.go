package generationtool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// processingGateway serves the real default Offerings and a set of seeded
// tasks, and records what was frozen for each paid step.
type processingGateway struct {
	*grantGateway
	product *catalog.Catalog
	tasks   map[generation.TaskID]generation.Task
	frozen  []generation.PrepareInputCommand
	// prepareErr, when set, makes PrepareInput refuse (the kernel's own
	// input contract check).
	prepareErr       error
	normalize        bool
	parameterFixture func(generation.Offering) generation.Offering
	submitted        []generation.SubmitPreparedCommand
	// listAll makes List ignore the state filter, standing in for a lister
	// that does not apply it.
	listAll bool
}

func (g *processingGateway) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	g.submitted = append(g.submitted, cmd)
	return g.grantGateway.SubmitPrepared(ctx, cmd)
}

func (g *processingGateway) Offerings(capability generation.Capability) []generation.Offering {
	offerings := g.product.List(capability)
	if g.parameterFixture != nil {
		for i, o := range offerings {
			offerings[i] = g.parameterFixture(o)
		}
	}
	return offerings
}

func (g *processingGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	if g.prepareErr != nil {
		return generation.PreparedInput{}, g.prepareErr
	}
	if g.normalize {
		_, normalized, err := g.product.Normalize(cmd.Capability, cmd.OfferingID, cmd.Input)
		if err != nil {
			return generation.PreparedInput{}, err
		}
		cmd.Input = normalized
	}
	g.frozen = append(g.frozen, cmd)
	return g.grantGateway.PrepareInput(ctx, cmd)
}

func (g *processingGateway) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	if id == "accepted-agent-task" {
		// The task that the last frozen step produced, as the kernel would
		// have created it.
		last := g.frozen[len(g.frozen)-1]
		offering, _ := g.product.Get(last.OfferingID)
		if g.parameterFixture != nil {
			offering = g.parameterFixture(offering)
		}
		return generation.Task{ID: id, OwnerID: owner, SessionID: "session-a", State: generation.TaskSucceeded,
			Capability: last.Capability, Offering: offering, Input: last.Input,
			Assets: []generation.Asset{
				{ID: "out-model", Role: "model", Variant: "glb", StorageKey: "k1", MediaType: "model/gltf-binary"},
				{ID: "out-fbx", Role: "model", Variant: "fbx", StorageKey: "k2", MediaType: "application/octet-stream"},
				{ID: "out-thumb", Role: "thumbnail", StorageKey: "k3", MediaType: "image/png"},
			}}, nil
	}
	task, found := g.tasks[id]
	if !found || task.OwnerID != owner {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get"}
	}
	return task, nil
}

func (g *processingGateway) List(_ context.Context, scope generation.TaskListScope, query generation.ListQuery) (generation.TaskPage, error) {
	var page generation.TaskPage
	for _, task := range g.tasks {
		if task.OwnerID != scope.OwnerID() || (query.SessionID != "" && task.SessionID != query.SessionID) ||
			(!g.listAll && task.State != generation.TaskSucceeded) {
			continue
		}
		page.Tasks = append(page.Tasks, task)
	}
	page.Total = len(page.Tasks)
	return page, nil
}

type processingHarness struct {
	t            *testing.T
	gateway      *processingGateway
	executor     *generationtool.Executor
	app          catalog.ApplicationSchema
	meshy        string
	tripo        string
	calls        int
	known        []string
	manual       []string
	databasePath string
	// config rebuilds the executor around another gateway.
	config generationtool.Config
}

// policy is the approval policy the harness's executor uses.
func (h *processingHarness) policy() approvalpolicy.Policy {
	return approvalpolicy.New(h.app.Chat.ApprovalPolicy.DefaultMode, h.app.Chat.ApprovalPolicy.Modes, h.manual, h.known)
}

func newProcessingHarness(t *testing.T, mode string, seed func(h *processingHarness) []generation.Task) *processingHarness {
	t.Helper()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "processing.db")
	repo, err := sqlitetest.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner-a",
		chat.Message{ID: "user-a", SessionID: "session-a", Role: "user", Content: "go", CreatedAt: now},
		chat.Message{ID: "turn-a", SessionID: "session-a", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session-a", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session-a", Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
			State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// These executor tests explicitly enable the legacy Tripo conversion path.
	// Its Provider identity must not depend on the order of other converters.
	product := catalogtest.LoadDefaultWithTripoConversion(t)
	tripo, ok := product.Get(catalogtest.TripoFormatConversionID)
	if !ok {
		t.Fatal("legacy Tripo conversion fixture is missing")
	}
	application := product.Application()
	harness := &processingHarness{t: t, app: application, databasePath: databasePath,
		meshy: product.List("retexture")[0].ProviderID, tripo: tripo.ProviderID}
	harness.gateway = &processingGateway{
		grantGateway: &grantGateway{fakeGateway: &fakeGateway{}, repo: repo},
		product:      product, tasks: map[generation.TaskID]generation.Task{}}
	harness.gateway.grantGateway.offering = product.Get
	for _, task := range seed(harness) {
		harness.gateway.tasks[task.ID] = task
	}
	var known, manual []string
	for _, capability := range application.Capabilities {
		known = append(known, string(capability.ID))
	}
	for _, capability := range application.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	harness.known, harness.manual = known, manual
	policy := harness.policy()
	service := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: policy, Modes: sessionMode{mode: mode}}
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	harness.config = generationtool.Config{Gateway: harness.gateway, References: &fakeReferences{},
		Operations: repo, Grants: service, Chat: application.Chat, ModelDelivery: application.ModelDelivery,
		Texts:         texts,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }}
	harness.executor, err = generationtool.New(harness.config)
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

// modelTask is a succeeded 3D task with saved model files, created an hour
// ago: a Provider still keeps its upstream job (#325 judges that by age).
// fixtureOfferingID names the default Catalog Offering a fixture task came
// from: Meshy processing takes only the source task types its documentation
// lists, by exact Offering (#329).
func fixtureOfferingID(provider string, capability generation.Capability) generation.OfferingID {
	if provider != "meshy" {
		return ""
	}
	switch capability {
	case generation.TextTo3D, generation.ImageTo3D:
		return generation.OfferingID("meshy/meshy-7.1/" + string(capability))
	case generation.Remesh, generation.Retexture, generation.Rigging, generation.Animation:
		return generation.OfferingID("meshy/processing/" + string(capability))
	}
	return ""
}

func modelTask(id, owner, session, provider string, capability generation.Capability, variants ...string) generation.Task {
	task := generation.Task{ID: generation.TaskID(id), OwnerID: generation.OwnerID(owner), SessionID: session,
		Capability: capability, State: generation.TaskSucceeded,
		Offering:     generation.Offering{ID: fixtureOfferingID(provider, capability), ProviderID: provider, ProviderName: "Provider", ModelName: "Model"},
		CreatedAt:    time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
		ProviderData: json.RawMessage(`{"upstream_job":"UPSTREAM-SECRET"}`)}
	for _, variant := range variants {
		task.Assets = append(task.Assets, generation.Asset{ID: generation.AssetID(id + "-" + variant), TaskID: task.ID,
			Role: "model", Variant: variant, StorageKey: "key/" + variant, MediaType: "model/" + variant, Size: 1024})
	}
	task.Assets = append(task.Assets, generation.Asset{ID: generation.AssetID(id + "-thumb"), TaskID: task.ID,
		Role: "thumbnail", StorageKey: "key/thumb", MediaType: "image/png"})
	return task
}

func (h *processingHarness) tool(name string) catalog.ChatToolSchema {
	for _, tool := range h.app.Chat.Tools {
		if tool.Name == name {
			return tool
		}
	}
	h.t.Fatalf("tool %s missing", name)
	return catalog.ChatToolSchema{}
}

func (h *processingHarness) run(name, arguments string) (chat.ToolResult, []chat.ToolObservation) {
	return h.runAs("owner-a", "session-a", name, arguments, nil)
}

func (h *processingHarness) runAs(owner, session, name, arguments string, history []chat.Message) (chat.ToolResult, []chat.ToolObservation) {
	h.t.Helper()
	return h.runWith(owner, session, name, arguments, history, chat.TurnOptions{})
}

func (h *processingHarness) runWith(owner, session, name, arguments string, history []chat.Message, options chat.TurnOptions) (chat.ToolResult, []chat.ToolObservation) {
	h.t.Helper()
	h.calls++
	command := chat.ToolCommand{OwnerID: owner, SessionID: session, AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: fmt.Sprintf("call-%d", h.calls), Name: name, Arguments: []byte(arguments)},
		Tool: h.tool(name), History: history, Options: options}
	var observed []chat.ToolObservation
	result, err := h.executor.Execute(context.Background(), command, func(o chat.ToolObservation) error {
		observed = append(observed, o)
		return nil
	})
	if err != nil {
		h.t.Fatalf("execute %s: %v", name, err)
	}
	return result, observed
}

func (h *processingHarness) content(result chat.ToolResult) map[string]any {
	h.t.Helper()
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		h.t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	return content
}

func (h *processingHarness) frozenInput(index int) map[string]any {
	h.t.Helper()
	if len(h.gateway.frozen) <= index {
		h.t.Fatalf("frozen inputs = %d; want index %d", len(h.gateway.frozen), index)
	}
	var input map[string]any
	if err := json.Unmarshal(h.gateway.frozen[index].Input, &input); err != nil {
		h.t.Fatal(err)
	}
	return input
}

func (h *processingHarness) expectRejected(result chat.ToolResult, want string) {
	h.t.Helper()
	if !result.NoSubmission || result.Suspend || h.gateway.submitCount != 0 || len(h.gateway.frozen) != 0 {
		h.t.Fatalf("rejected step reached the paid path: %+v submits=%d frozen=%d", result, h.gateway.submitCount, len(h.gateway.frozen))
	}
	content := h.content(result)
	message, _ := content["error"].(string)
	if content["success"] != false || !strings.Contains(message, want) {
		h.t.Fatalf("rejection = %v; want text containing %q", content, want)
	}
	if strings.Contains(result.AgentContent, "UPSTREAM-SECRET") {
		h.t.Fatalf("rejection leaked upstream data: %s", result.AgentContent)
	}
}

func meshyCatalogTasks(h *processingHarness) []generation.Task {
	return []generation.Task{
		modelTask("model-1", "owner-a", "session-a", h.meshy, "text-to-3d", "glb"),
		modelTask("rig-1", "owner-a", "session-a", h.meshy, "rigging", "glb", "fbx"),
		modelTask("tripo-1", "owner-a", "session-a", h.tripo, "image-to-3d", "glb"),
		modelTask("other-owner", "owner-b", "session-a", h.meshy, "text-to-3d", "glb"),
		modelTask("other-session", "owner-a", "session-z", h.meshy, "text-to-3d", "glb"),
	}
}

func TestRetextureAutoModeSubmitsFrozenSourceAndReportsCredits(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	// resolution and enable_pbr stay tool arguments mapped to the offering's
	// parameters; the Schema fixes both, so normalization replaces the values.
	result, observed := h.run("retexture_model", `{"source_task_id":"model-1","prompt":"mossy stone","resolution":"2k","enable_pbr":false}`)
	if result.Suspend || h.gateway.submitCount != 1 {
		t.Fatalf("auto retexture did not submit: %+v submits=%d", result, h.gateway.submitCount)
	}
	input := h.frozenInput(0)
	if h.gateway.frozen[0].Capability != "retexture" || input["source_task"] != "model-1" || input["prompt"] != "mossy stone" ||
		input["texture_resolution"] != "2k" || input["enable_pbr"] != false || input["resolution"] != nil {
		t.Fatalf("frozen retexture = %+v %v", h.gateway.frozen[0], input)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, normalized, err := product.Normalize("retexture", h.gateway.frozen[0].OfferingID, h.gateway.frozen[0].Input)
	if err != nil {
		t.Fatal(err)
	}
	var fixed map[string]any
	if err := json.Unmarshal(normalized, &fixed); err != nil || fixed["texture_resolution"] != "8k" || fixed["enable_pbr"] != true {
		t.Fatalf("normalized retexture = %s, want the Schema-fixed 8k and PBR", normalized)
	}
	if _, leaked := input["source_url"]; leaked {
		t.Fatalf("source URL must never be passed: %v", input)
	}
	content := h.content(result)
	if content["success"] != true || content["approval"] != "policy" || content["estimated_credits"] != float64(15) {
		t.Fatalf("result = %v", content)
	}
	formats, _ := content["model_formats"].([]any)
	if len(formats) != 2 || formats[0] != "glb" {
		t.Fatalf("model formats = %v", content["model_formats"])
	}
	if len(observed) == 0 || observed[0].ApprovalSource != "policy" || observed[0].EstimatedCredits != 15 {
		t.Fatalf("observation = %+v", observed)
	}
}

func TestRetextureManualModeWaitsForTheHuman(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	// resolution and enable_pbr stay tool arguments mapped to the offering's
	// parameters; the Schema fixes both, so normalization replaces the values.
	result, observed := h.run("retexture_model", `{"source_task_id":"model-1","prompt":"mossy stone","resolution":"2k","enable_pbr":false}`)
	if result.Suspend || !result.NoSubmission || h.gateway.submitCount != 0 || len(observed) != 0 {
		t.Fatalf("manual retexture must wait for approval: %+v submits=%d observed=%d", result, h.gateway.submitCount, len(observed))
	}
	if len(h.gateway.frozen) != 1 || h.frozenInput(0)["source_task"] != "model-1" {
		t.Fatalf("the exact input must be frozen before the human decides: %+v", h.gateway.frozen)
	}
}

func TestRetextureStyleImageUsesTheReferenceAssetWithoutPrompt(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	history := []chat.Message{{Role: "user", ImageURLs: []string{"/api/v1/input-assets/style"}}}
	result, _ := h.runAs("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1","reference_image_indices":[1]}`, history)
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("style retexture rejected: %+v", result)
	}
	input := h.frozenInput(0)
	if input["style_asset"] != "configured-input-asset" {
		t.Fatalf("style input = %v", input)
	}
	if _, hasPrompt := input["prompt"]; hasPrompt {
		t.Fatalf("a style-image step must not carry a prompt: %v", input)
	}
}

func TestImplicitImageInTheTurnIsNotAStyleReference(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	history := []chat.Message{{Role: "user", ImageURLs: []string{"/api/v1/input-assets/style"}}}
	h.runAs("owner-a", "session-a", "retexture_model", `{"source_task_id":"model-1","prompt":"mossy"}`, history)
	if _, hasStyle := h.frozenInput(0)["style_asset"]; hasStyle {
		t.Fatalf("an unrequested image became a second retexture input: %v", h.frozenInput(0))
	}
}

func TestProcessingRejectsSourcesOfAnotherOwnerOrSession(t *testing.T) {
	for _, source := range []string{"other-owner", "other-session", "does-not-exist"} {
		t.Run(source, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", meshyCatalogTasks)
			result, _ := h.run("retexture_model", `{"source_task_id":"`+source+`","prompt":"x"}`)
			h.expectRejected(result, "list_session_models")
			if strings.Contains(result.AgentContent, "owner-b") || strings.Contains(result.AgentContent, "session-z") {
				t.Fatalf("rejection exposed another scope: %s", result.AgentContent)
			}
		})
	}
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.runAs("owner-b", "session-a", "retexture_model", `{"source_task_id":"model-1","prompt":"x"}`, nil)
	h.expectRejected(result, "list_session_models")
}

func TestProcessingRejectsIncompatibleProvider(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	// Format conversion and animation continue the Provider's own job. Meshy
	// Convert takes a Meshy result (#330), but not an animation task, and
	// this one's saved file is over its 70 MiB limit.
	animated := modelTask("anim-1", "owner-a", "session-a", h.meshy, "animation", "glb")
	animated.Assets[0].Size = 71 << 20
	h.gateway.tasks[animated.ID] = animated
	result, _ := h.run("convert_model_format", `{"source_task_id":"anim-1","format":"FBX"}`)
	h.expectRejected(result, "accepts this model")
	result, _ = h.run("process_model", `{"step":"animation","source_task_id":"tripo-1","action_id":3}`)
	// #326: each service's reason, as the 加工 panel words it.
	h.expectRejected(result, "only takes Tripo3D rigging results")
}

func TestRetextureAcceptsAForeignModelOnlyThroughItsSavedFile(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.run("retexture_model", `{"source_task_id":"tripo-1","prompt":"rusty"}`)
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("foreign model rejected although the offering takes a saved model file: %+v", result)
	}
	input := h.frozenInput(0)
	if input["source_asset"] != "tripo-1-glb" {
		t.Fatalf("source must be the platform asset, never an upstream reference: %v", input)
	}
	if _, viaTask := input["source_task"]; viaTask {
		t.Fatalf("a foreign task must not be passed as a source task: %v", input)
	}
}

func TestAnimationNeedsARiggingSource(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	result, _ := h.run("process_model", `{"step":"animation","source_task_id":"model-1","action_id":3}`)
	// #326: each service's reason, and that rigging can run on this model first
	// (the 加工 panel brings that step in).
	h.expectRejected(result, "only takes Meshy AI rigging results")
	h.expectRejected(result, "rigging can run on model-1 first")

	// The rigging task itself is a valid animation source and, being neither
	// text/image-to-3D nor retexture, is approved by policy even in manual mode.
	result, _ = h.run("process_model", `{"step":"animation","source_task_id":"rig-1","action_id":3}`)
	if result.Suspend || h.gateway.submitCount != 1 {
		t.Fatalf("animation on a rigging task did not run: %+v submits=%d", result, h.gateway.submitCount)
	}
	input := h.frozenInput(0)
	if h.gateway.frozen[0].Capability != "animation" || input["source_task"] != "rig-1" || input["action_id"] != float64(3) {
		t.Fatalf("frozen animation = %+v %v", h.gateway.frozen[0], input)
	}
	content := h.content(result)
	if content["approval"] != "policy" || content["estimated_credits"] != float64(3) {
		t.Fatalf("animation result = %v", content)
	}
}

func TestRiggingAndRemeshAreAutoApprovedInManualMode(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	result, _ := h.run("process_model", `{"step":"rigging","source_task_id":"model-1","height_meters":1.8}`)
	if result.Suspend || h.gateway.submitCount != 1 || h.gateway.frozen[0].Capability != "rigging" {
		t.Fatalf("rigging: %+v submits=%d", result, h.gateway.submitCount)
	}
	if h.content(result)["estimated_credits"] != float64(5) {
		t.Fatalf("rigging credits = %v", h.content(result))
	}
	h.run("process_model", `{"step":"remesh","source_task_id":"model-1","topology":"triangle","target_polycount":20000}`)
	if h.gateway.submitCount != 2 || h.frozenInput(1)["topology"] != "triangle" {
		t.Fatalf("remesh was not submitted with its options: submits=%d", h.gateway.submitCount)
	}
}

func TestFormatConversionWithoutExtraCreditsIsAutoApprovedInManualMode(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	result, _ := h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"FBX"}`)
	if result.Suspend || h.gateway.submitCount != 1 {
		t.Fatalf("plain conversion: %+v", result)
	}
	content := h.content(result)
	if content["estimated_credits"] != float64(5) || content["approval"] != "policy" {
		t.Fatalf("plain conversion result = %v", content)
	}
	if _, extra := content["extra_credits"]; extra {
		t.Fatalf("a step without extra credits must not report any: %v", content)
	}
}

// A step that spends extra credits is the person's to approve in manual mode,
// even on a capability that is otherwise automatic: it ends at an approval card
// (nothing is submitted), the frozen input records the exact extra credits the
// card shows, and the model never supplied the consent flag.
func TestFormatConversionExtraCreditsNeedAnApprovalCardInManualMode(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	result, observed := h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"FBX","texture_format":"PNG"}`)
	if result.Suspend || !result.NoSubmission || awaitedMessage(result.AgentContent) != h.app.Chat.Agent.ConsentWaitingContent ||
		h.gateway.submitCount != 0 || len(observed) != 0 {
		t.Fatalf("manual extra-credit step must wait on a card without submitting: %+v submits=%d", result, h.gateway.submitCount)
	}
	if h.frozenInput(0)["authorize_extra_credits"] != true {
		t.Fatalf("the frozen input must carry the consent the approval decides: %v", h.frozenInput(0))
	}
	prepared, err := h.gateway.repo.GetPreparedInput(context.Background(), "owner-a", "prepared-1")
	if err != nil || prepared.ExtraCredits != 5 {
		t.Fatalf("frozen extra credits = %d, %v", prepared.ExtraCredits, err)
	}
	op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-1")
	if err != nil || !op.Detached {
		t.Fatalf("operation = %+v, %v", op, err)
	}
	grant, err := h.gateway.repo.GetExecutionGrant(context.Background(), "owner-a", op.GrantRequestID)
	if err != nil || grant.Status != executiongrant.Pending || grant.PreparedInputID != string(prepared.ID) {
		t.Fatalf("grant = %+v, %v", grant, err)
	}
}

func TestFormatConversionExtraCreditsAreApprovedByAutoMode(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"FBX","texture_format":"PNG"}`)
	content := h.content(result)
	if result.Suspend || h.gateway.submitCount != 1 || content["approval"] != "policy" {
		t.Fatalf("auto mode must approve the extra credits itself: %+v submits=%d", result, h.gateway.submitCount)
	}
	if content["estimated_credits"] != float64(10) || content["extra_credits"] != float64(5) {
		t.Fatalf("the result must show the extra part of the credits: %v", content)
	}
	if h.frozenInput(0)["authorize_extra_credits"] != true {
		t.Fatalf("policy-approved extra credits must be marked: %v", h.frozenInput(0))
	}
}

func TestExtraCreditConsentCannotComeFromToolArguments(t *testing.T) {
	// The model passes the consent flag directly: it is not a declared argument.
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"FBX","texture_format":"PNG","authorize_extra_credits":true}`)
	h.expectRejected(result, "authorize_extra_credits")

	// A Schema that maps a tool argument onto the consent parameter is refused
	// at runtime as well, so only the approval policy can set the flag.
	h = newProcessingHarness(t, "auto", meshyCatalogTasks)
	for index := range h.app.Chat.Tools {
		if h.app.Chat.Tools[index].Name == "convert_model_format" {
			h.app.Chat.Tools[index].Processing.ParameterArguments["texture_format"] = "authorize_extra_credits"
		}
	}
	result, _ = h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"FBX","texture_format":true}`)
	h.expectRejected(result, "texture_format")
}

func TestListSessionModelsSkipsUnfinishedTasksWhateverTheStoreReturns(t *testing.T) {
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		running := modelTask("running-1", "owner-a", "session-a", h.meshy, "text-to-3d", "glb")
		running.State = generation.TaskMaterializing
		return append(meshyCatalogTasks(h), running)
	})
	// The fake store ignores the States filter below, like a lister that
	// does not implement it would.
	h.gateway.listAll = true
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "running-1") {
		t.Fatalf("an unfinished task was offered as a processing source: %s", raw)
	}
	result, _ := h.run("retexture_model", `{"source_asset_id":"running-1-glb","prompt":"x"}`)
	h.expectRejected(result, "list_session_models")
}

func TestProcessingRejectsUnknownStepAndArguments(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.run("process_model", `{"step":"explode","source_task_id":"model-1"}`)
	h.expectRejected(result, "explode")
	result, _ = h.run("process_model", `{"step":"rigging","source_task_id":"model-1","source_url":"https://example.invalid/m.glb"}`)
	h.expectRejected(result, "source_url")
	// action_id exists for animation only; a rigging step must not drop it silently.
	result, _ = h.run("process_model", `{"step":"rigging","source_task_id":"model-1","action_id":3}`)
	h.expectRejected(result, "action_id")
	result, _ = h.run("retexture_model", `{}`)
	h.expectRejected(result, "source_task_id")
}

func TestProcessingReportsKernelInputRuleRefusalsWithoutSubmitting(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.prepareErr = &generation.Error{Code: generation.ErrInvalidInput, Operation: "prepare", Detail: "exactly one of prompt or style_asset"}
	result, _ := h.run("retexture_model", `{"source_task_id":"model-1"}`)
	if !result.NoSubmission || h.gateway.submitCount != 0 {
		t.Fatalf("refused input was submitted: %+v", result)
	}
	message := h.content(result)["error"].(string)
	if strings.Contains(message, "exactly one of prompt or style_asset") {
		t.Fatalf("kernel diagnostic leaked: %q", message)
	}
	if message != h.app.Chat.Presentation.Messages.InvalidParameters {
		t.Fatalf("public fallback = %q", message)
	}
}

func TestListSessionModelsIsScopedAndHidesUpstreamData(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"UPSTREAM-SECRET", "other-owner", "other-session", "key/", "storage"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("listing leaked %q: %s", forbidden, text)
		}
	}
	var listing struct {
		Models []struct {
			TaskID     string   `json:"task_id"`
			Capability string   `json:"capability"`
			Provider   string   `json:"provider"`
			Model      string   `json:"model"`
			CreatedAt  string   `json:"created_at"`
			Formats    []string `json:"formats"`
			NextSteps  []struct {
				Tool string `json:"tool"`
				Step string `json:"step"`
			} `json:"next_steps"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, model := range listing.Models {
		seen[model.TaskID] = true
		if model.CreatedAt == "" || model.Provider == "" || model.Model == "" || len(model.Formats) == 0 {
			t.Fatalf("incomplete model entry: %+v", model)
		}
	}
	if len(listing.Models) != 3 || !seen["model-1"] || !seen["rig-1"] || !seen["tripo-1"] {
		t.Fatalf("listed models = %+v", listing.Models)
	}
	steps := map[string][]string{}
	for _, model := range listing.Models {
		for _, step := range model.NextSteps {
			steps[model.TaskID] = append(steps[model.TaskID], step.Tool+":"+step.Step)
		}
	}
	has := func(task, step string) bool {
		for _, candidate := range steps[task] {
			if candidate == step {
				return true
			}
		}
		return false
	}
	if !has("model-1", "retexture_model:") || has("model-1", "process_model:animation") || !has("rig-1", "process_model:animation") ||
		!has("tripo-1", "convert_model_format:") || !has("model-1", "convert_model_format:") {
		t.Fatalf("next steps = %v", steps)
	}
	empty, err := h.executor.ListSessionModels(context.Background(), "owner-b", "session-empty")
	if err != nil || strings.Contains(string(empty), "model-1") {
		t.Fatalf("another scope sees models: %s %v", empty, err)
	}
}

func TestProcessingReplayedCallReusesFrozenInputAndIdempotencyKey(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.run("process_model", `{"step":"rigging","source_task_id":"model-1"}`)
	h.calls = 0 // the same tool call is delivered again, e.g. after a restart
	h.run("process_model", `{"step":"rigging","source_task_id":"model-1"}`)
	if len(h.gateway.frozen) != 1 {
		t.Fatalf("a replayed call froze its input again: %d", len(h.gateway.frozen))
	}
	for _, submission := range h.gateway.submitted {
		if submission.IdempotencyKey != "chat:session-a:call-1" || submission.PreparedInputID != h.gateway.submitted[0].PreparedInputID {
			t.Fatalf("replay is not idempotent: %+v", h.gateway.submitted)
		}
	}
}

func modelHistory() []chat.Message {
	return []chat.Message{{Role: "assistant", ImageURLs: []string{"/thumb"}, Attachments: []chat.Attachment{
		{Handle: "@model3", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "model-1", ImageSlot: 1}}}}
}

func TestProcessingAcceptsAModelHandleForTheSourceTask(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.runAs("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"@model3","prompt":"mossy stone"}`, modelHistory())
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("model handle rejected: %+v", result)
	}
	if h.frozenInput(0)["source_task"] != "model-1" {
		t.Fatalf("frozen input = %v", h.frozenInput(0))
	}
}

func TestProcessingModelHandleStillFailsForAnotherOwnerOrSession(t *testing.T) {
	// The handle only names a Task; the Task is still checked against the
	// owner and the session like one named by its ID.
	for _, who := range [][2]string{{"owner-b", "session-a"}, {"owner-a", "session-z"}} {
		h := newProcessingHarness(t, "auto", meshyCatalogTasks)
		result, _ := h.runAs(who[0], who[1], "retexture_model", `{"source_task_id":"@model3","prompt":"x"}`, modelHistory())
		h.expectRejected(result, "list_session_models")
	}
}

func TestProcessingRefusesUnknownOrImageHandles(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.runAs("owner-a", "session-a", "retexture_model", `{"source_task_id":"@model9","prompt":"x"}`, modelHistory())
	h.expectRejected(result, "@model9")
	history := append(modelHistory(), chat.Message{Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage}}})
	h = newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ = h.runAs("owner-a", "session-a", "retexture_model", `{"source_task_id":"@img1","prompt":"x"}`, history)
	h.expectRejected(result, "@img1")
}

func TestProcessingDefaultsToTheSelectedModel(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	options := chat.TurnOptions{Selection: []chat.Attachment{
		{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "model-1"}}}
	result, _ := h.runWith("owner-a", "session-a", "retexture_model", `{"prompt":"mossy stone"}`, nil, options)
	if result.NoSubmission || h.frozenInput(0)["source_task"] != "model-1" {
		t.Fatalf("selected model not used: %+v", result)
	}
	// The alias names it as well, and a selected model of another session is
	// rejected exactly like a typed task ID.
	h = newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ = h.runWith("owner-a", "session-a", "retexture_model", `{"source_task_id":"@selected","prompt":"x"}`, nil, options)
	if result.NoSubmission || h.frozenInput(0)["source_task"] != "model-1" {
		t.Fatalf("alias not used: %+v", result)
	}
	h = newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ = h.runWith("owner-a", "session-z", "retexture_model", `{"prompt":"x"}`, nil, options)
	h.expectRejected(result, "list_session_models")
}
