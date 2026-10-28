package pendingapproval_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// permissiveVerifier stands in for the production owner/session checks.
type permissiveVerifier struct{}

func (permissiveVerifier) VerifyAttachment(_ context.Context, _, _ string, ref chat.AttachmentRef) (chat.Attachment, error) {
	taskID := ref.TaskID
	if taskID == "" && strings.HasPrefix(ref.URL, "/api/v1/tasks/") {
		// A task picture names its Task in the URL, like the production verifier reads it.
		taskID = strings.Split(strings.TrimPrefix(ref.URL, "/api/v1/tasks/"), "/")[0]
	}
	return chat.Attachment{Kind: ref.Kind, Role: chat.AttachmentRoleGenerated, AssetID: ref.AssetID,
		TaskID: taskID, ImageSlot: ref.ImageSlot, URL: ref.URL}, nil
}

// fakeGateway is the task kernel: idempotent per key, with a task state the
// test controls.
type fakeGateway struct {
	lastSubmit generation.SubmitPreparedCommand

	repo     *sqlite.Repository
	offering generation.Offering
	// frozenMessages, when set, are the runtime messages frozen into the
	// task's Offering (an older Schema's texts).
	frozenMessages *generation.ExecutionMessages

	mu            sync.Mutex
	submits       int
	tasks         map[string]generation.TaskID
	state         generation.TaskState
	failure       *generation.Failure
	submitError   error
	preparedError error
	preparedReads int
	// taskInput is the input of the tasks it accepts; empty means a plain prompt.
	taskInput json.RawMessage
	// drawnPicture adds the second thumbnail a text-to-3D result carries: the
	// picture the provider drew before modelling, beside the render of the model.
	drawnPicture bool
	modelTasks   map[generation.TaskID]generation.Task
}

func (g *fakeGateway) Offerings(generation.Capability) []generation.Offering {
	return []generation.Offering{g.offering}
}
func (g *fakeGateway) OfferingAvailable(id generation.OfferingID) bool { return id == g.offering.ID }
func (g *fakeGateway) GetPreparedInput(ctx context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	g.preparedReads++
	if g.preparedError != nil {
		return generation.PreparedInput{}, g.preparedError
	}
	return g.repo.GetPreparedInput(ctx, owner, id)
}
func (g *fakeGateway) SubmitPrepared(_ context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.submits++
	g.lastSubmit = cmd
	if g.submitError != nil {
		return generation.Accepted{}, g.submitError
	}
	if g.tasks == nil {
		g.tasks = map[string]generation.TaskID{}
	}
	if _, exists := g.tasks[cmd.IdempotencyKey]; !exists {
		g.tasks[cmd.IdempotencyKey] = generation.TaskID("task-" + cmd.IdempotencyKey)
	}
	return generation.Accepted{TaskID: g.tasks[cmd.IdempotencyKey]}, nil
}
func (g *fakeGateway) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if task, ok := g.modelTasks[id]; ok {
		return task, nil
	}
	task := generation.Task{ID: id, OwnerID: owner, SessionID: "session-a", Offering: g.offering,
		Capability: g.offering.Capability, State: g.state, Failure: g.failure, Input: json.RawMessage(`{"prompt":"p"}`)}
	if len(g.taskInput) > 0 {
		task.Input = g.taskInput
	}
	if g.frozenMessages != nil {
		task.Offering.ExecutionMessages = *g.frozenMessages
	}
	if id == "task-model" {
		task.State = generation.TaskSucceeded
		// The 3D model the session shows as @model1: its file and its rendered preview.
		task.Assets = []generation.Asset{{ID: "model-glb", Role: "model", SHA256: "sha-model"}, {ID: "model-variant", Role: "model", SHA256: "sha-model"}, {ID: "model-thumb", Role: "thumbnail", SHA256: "sha-thumb"}, {ID: "model-texture", Role: "texture", SHA256: "sha-front", MediaType: "image/png"}}
	} else if id == "task-slot" {
		// The picture the person picked on the stage: the approval's input asset is a copy of it.
		task.Assets = []generation.Asset{{ID: "slot-asset", Role: "image", SHA256: "sha-front"}}
	} else if g.state == generation.TaskSucceeded {
		task.Assets = []generation.Asset{{ID: "asset-model", Role: "model"}, {ID: "asset-thumb", Role: "thumbnail"}}
		if g.drawnPicture {
			task.Assets = append(task.Assets, generation.Asset{ID: "asset-drawn", Role: "thumbnail"})
		}
	}
	return task, nil
}
func (g *fakeGateway) distinctTasks() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.tasks)
}

type world struct {
	binding  harness.Binding
	t        *testing.T
	path     string
	repo     *sqlite.Repository
	chat     *chat.Service
	gateway  *fakeGateway
	grants   *executiongrant.Service
	service  *pendingapproval.Service
	schema   catalog.ChatSchema
	operator executiongrant.Principal
	// normalized and extra are what the next approval freezes: the input and
	// the extra credits it incurs. Empty means a plain prompt without extras.
	normalized  json.RawMessage
	adjustments []generation.ParameterAdjustment
	extra       int
	// recharge is the earlier charged-but-unstored Task the next approval
	// repeats (#294); empty for an ordinary step.
	recharge string
	// locale is the UI locale frozen with the next approval's turn.
	locale string
	// sourceModel adds the session's 3D model (@model1, task-model) as the
	// next approval's source_asset: a copy of the model file, as a step that
	// works on a local model file freezes it (#324).
	sourceModel bool
	// tool is the tool of the next approval (create_model_3d when empty);
	// referencesUsed of referencesGiven are the pictures its step uses of the
	// ones the turn gave (#329 E), 0 of 0 when nothing was left out.
	tool                            string
	referencesUsed, referencesGiven int
	// capability is the capability frozen with the next prepared input. It is
	// TextTo3D unless a test names another one.
	capability generation.Capability
	// referenceSource is the source fact frozen with the next operation; zero
	// means an operation from before the fact was kept.
	referenceSource generationtool.ReferenceSourceFact
	// attachmentAsset is the session attachment's asset; the default is not
	// the input copy, so no handle is claimed without an exact identity.
	attachmentAsset  string
	extraAttachments []chat.Attachment
	modelAsset       string
	modelAttachment  *chat.Attachment
}

func chatService(t *testing.T, repo *sqlite.Repository, schema catalog.ChatSchema) *chat.Service {
	t.Helper()
	service, err := chat.NewService(chat.ServiceConfig{
		Repository: repo, DefaultTitle: "default", UserRole: "user", AssistantRole: "assistant",
		PendingContent: "pending", StreamActive: "active", StreamDone: "done", StreamError: "error",
		TurnQueued: "queued", TurnRunning: "running", TurnWaitingTask: "waiting_task",
		TurnDone: "done", TurnError: "error",
		PlaceholderHealing: chat.PlaceholderHealingPolicy{StaleAfter: time.Minute, PromptArgument: "prompt",
			PromptPreviewCharacters: 8, InterruptedMessage: "interrupted", InterruptedPromptTemplate: "{prompt}"},
		EventFields:          chat.EventFieldPolicy{Type: "type", MessageID: "message_id"},
		EventTypes:           chat.EventTypePolicy{MessageCreated: "created"},
		StreamCleanupTTL:     time.Minute,
		SubscriberBufferSize: 1,
		Handles:              chat.NewHandlePolicy(schema.Context),
		Attachments:          permissiveVerifier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func openWorld(t *testing.T, path string, ttl time.Duration) *world {
	t.Helper()
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	gateway := &fakeGateway{repo: repo, state: generation.TaskQueued, offering: generation.Offering{
		ID: "fake/offering", Revision: "rev-a", ModelID: "fake-model", ModelName: "Fake Model", ProviderName: "Fake", ProviderID: "fake-provider",
		Capability: generation.TextTo3D, Enabled: true, EstimatedCredits: 25,
		Parameters:        []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt}},
		ExecutionMessages: product.Application().Runtime.GenerationMessages,
	}}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, PendingTTL: ttl}
	chats := chatService(t, repo, schema)
	service, err := pendingapproval.New(pendingapproval.Config{
		// This fixture supplies historical calls only. Agent-source submission is
		// exercised through the production App verifier, never this legacy stub.
		VerifyPreparedSource: func(ctx context.Context, op generationtool.Operation, p generation.PreparedInput) error {
			turn, err := repo.GetTurn(ctx, op.OwnerID, op.SessionID, op.AssistantMessageID)
			if err != nil {
				return err
			}
			if turn.Options.Harness != nil && resolve.AgentSource(turn.Options.Harness.Selection) {
				return pendingapproval.ErrUnavailable
			}
			return nil
		},
		Operations: repo, Grants: grants, Gateway: gateway, Chat: chats, Schema: schema, Texts: texts,
		DefaultLocale: product.Application().Runtime.Frontend.DisplayLocale,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, path: path, repo: repo, chat: chats, gateway: gateway, grants: grants, service: service,
		schema: schema, operator: executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}}
}

// pending creates a session, a finished turn, and one detached approval.
func (w *world) pending(callID string) (generationtool.Operation, executiongrant.Grant) {
	w.t.Helper()
	return w.approval(callID, true)
}

// undetached is the state a Stop leaves behind when it wins the race against
// the detach: a pending grant whose operation never left its turn.
func (w *world) undetached(callID string) (generationtool.Operation, executiongrant.Grant) {
	w.t.Helper()
	return w.approval(callID, false)
}

func (w *world) approval(callID string, detach bool) (generationtool.Operation, executiongrant.Grant) {
	w.t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, found, err := w.repo.Session(ctx, "owner-a", "session-a"); err != nil {
		w.t.Fatal(err)
	} else if !found {
		if _, err := w.repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
			Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
			w.t.Fatal(err)
		}
	}
	turnID := "turn-" + callID
	if _, _, err := w.repo.StartTurn(ctx, "owner-a",
		chat.Message{ID: "user-" + callID, SessionID: "session-a", Role: "user", Content: "make it", CreatedAt: now,
			Attachments: w.attachments()},
		chat.Message{ID: turnID, SessionID: "session-a", Role: "assistant", Content: "answer",
			ToolCalls: []chat.ToolCall{{ID: callID, Name: w.toolName(), Arguments: []byte(`{"prompt":"p"}`)}}, CreatedAt: now},
		chat.Event{SessionID: "session-a", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session-a", Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: turnID,
			Options: chat.TurnOptions{Locale: w.locale},
			State:   "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		w.t.Fatal(err)
	}
	if err := w.repo.UpdateTurnState(ctx, turnID, "done", now); err != nil {
		w.t.Fatal(err)
	}
	if err := w.repo.FinishStream(ctx, "session-a", turnID, "done", now.Add(time.Hour)); err != nil {
		w.t.Fatal(err)
	}
	capability := w.capability
	if capability == "" {
		capability = generation.TextTo3D
	}
	prepared := generation.PreparedInput{ID: generation.PreparedInputID("prepared-" + callID), OwnerID: "owner-a",
		SessionID: "session-a", Capability: capability, OfferingID: w.gateway.offering.ID,
		OfferingRevision: "rev-a", SchemaVersion: "1", NormalizedInput: w.frozenInput(),
		ParameterAdjustments: w.adjustments, ExtraCredits: w.extra, InputHash: "hash-" + callID, CreatedAt: now,
		Assets: w.preparedAssets()}
	if err := w.repo.CreatePreparedInput(ctx, prepared); err != nil {
		w.t.Fatal(err)
	}
	operation, err := w.repo.InsertAgentOperation(ctx, generationtool.Operation{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: turnID, CallID: callID,
		ToolName: w.toolName(), CommandHash: "hash-" + callID, IdempotencyKey: "key-" + callID,
		PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash,
		OfferingID: prepared.OfferingID, OfferingRevision: prepared.OfferingRevision, Harness: w.binding,
		RechargeTaskID: w.recharge, ReferenceCount: w.referencesUsed, ReferenceTotal: w.referencesGiven,
		ReferenceSource: w.referenceSource})
	if err != nil {
		w.t.Fatal(err)
	}
	grant, err := w.grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: executiongrant.Principal{OwnerID: "owner-a", ActorID: "agent:" + turnID, Source: executiongrant.AgentPending},
		SessionID: "session-a", OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: operation.IdempotencyKey, OperationID: operation.OperationID, TriggerTurnID: turnID, Harness: w.binding})
	if err != nil {
		w.t.Fatal(err)
	}
	if operation, err = w.repo.AttachAgentGrantRequest(ctx, operation, grant.RequestID); err != nil {
		w.t.Fatal(err)
	}
	if !detach {
		return operation, grant
	}
	operation, detached, err := w.repo.DetachAgentOperation(ctx, operation)
	if err != nil || !detached {
		w.t.Fatalf("detach: %v %v", detached, err)
	}
	return operation, grant
}

func (w *world) toolName() string {
	if w.tool != "" {
		return w.tool
	}
	return "create_model_3d"
}

func (w *world) attachments() []chat.Attachment {
	asset := w.attachmentAsset
	if asset == "" {
		asset = "slot-asset"
	}
	attachments := []chat.Attachment{{Handle: "@img2", Kind: chat.AttachmentKindImage, AssetID: asset, TaskID: "task-slot"}}
	if w.sourceModel {
		// The stage selection names the model file itself, as the browser sends it.
		model := chat.Attachment{Handle: "@model1", Kind: chat.AttachmentKindModel, TaskID: "task-model", AssetID: "model-glb"}
		if w.modelAttachment != nil {
			model = *w.modelAttachment
		}
		attachments = append(attachments, model)
	}
	attachments = append(attachments, w.extraAttachments...)
	return attachments
}

func (w *world) preparedAssets() []generation.PreparedInputAsset {
	assets := []generation.PreparedInputAsset{{Parameter: "reference_image", ID: "ref-asset", Role: "reference", SHA256: "sha-front", MediaType: "image/png"}}
	if w.sourceModel {
		id := w.modelAsset
		if id == "" {
			id = "source-copy"
		}
		assets = append(assets, generation.PreparedInputAsset{Parameter: "source_asset", ID: generation.AssetID(id), Role: "source_asset", SHA256: "sha-model", MediaType: "model/gltf-binary"})
	}
	return assets
}

func TestApprovalModelRequiresExactFrozenAssetAndHandle(t *testing.T) {
	for _, tc := range []struct {
		name, frozen, attachment string
		wantTask                 bool
	}{
		{"independent upload with same bytes", "uploaded-model", "model-glb", false},
		{"different variant of same task", "model-glb", "model-variant", false},
		{"legacy attachment without asset", "model-glb", "", false},
		{"exact task asset", "model-glb", "model-glb", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := openWorld(t, filepath.Join(t.TempDir(), "approval.db"), time.Hour)
			w.sourceModel, w.modelAsset = true, tc.frozen
			w.modelAttachment = &chat.Attachment{Handle: "@model1", Kind: chat.AttachmentKindModel, TaskID: "task-model", AssetID: tc.attachment}
			w.gateway.offering.Parameters = append(w.gateway.offering.Parameters, generation.Parameter{Name: "source_asset", Type: "asset", Semantic: generation.SemanticSourceAsset, MediaTypes: []string{"model/gltf-binary"}})
			w.pending("call")
			items, err := w.service.List(context.Background(), "owner-a", "session-a")
			if err != nil || len(items) != 1 || len(items[0].Inputs) != 2 {
				t.Fatalf("list=%+v err=%v", items, err)
			}
			input := items[0].Inputs[1]
			if tc.wantTask {
				if input.Kind != pendingapproval.InputTask || input.TaskID != "task-model" || input.Handle != "@model1" || input.AssetID != "model-thumb" {
					t.Fatalf("exact model: %+v", input)
				}
			} else if input.Kind != pendingapproval.InputAsset || input.AssetID != tc.frozen || input.Handle != "" || input.TaskID != "" {
				t.Fatalf("unproved model: %+v", input)
			}
		})
	}
	t.Run("another variant cannot supply missing handle", func(t *testing.T) {
		w := openWorld(t, filepath.Join(t.TempDir(), "approval.db"), time.Hour)
		w.sourceModel, w.modelAsset = true, "model-glb"
		w.modelAttachment = &chat.Attachment{Kind: chat.AttachmentKindModel, TaskID: "task-model", AssetID: "model-glb"}
		w.extraAttachments = []chat.Attachment{{Handle: "@variant", Kind: chat.AttachmentKindModel, TaskID: "task-model", AssetID: "model-variant"}}
		w.gateway.offering.Parameters = append(w.gateway.offering.Parameters, generation.Parameter{Name: "source_asset", Type: "asset", Semantic: generation.SemanticSourceAsset, MediaTypes: []string{"model/gltf-binary"}})
		w.pending("call")
		items, err := w.service.List(context.Background(), "owner-a", "session-a")
		if err != nil || len(items) != 1 || len(items[0].Inputs) != 2 {
			t.Fatalf("list=%+v err=%v", items, err)
		}
		input := items[0].Inputs[1]
		if input.Kind != pendingapproval.InputAsset || input.AssetID != "model-glb" || input.Handle != "" || input.TaskID != "" {
			t.Fatalf("another variant supplied the handle: %+v", input)
		}
	})
}

func (w *world) frozenInput() json.RawMessage {
	if len(w.normalized) > 0 {
		return w.normalized
	}
	return json.RawMessage(`{"prompt":"p"}`)
}

func (w *world) message(id string) (chat.Message, bool) {
	w.t.Helper()
	messages, err := w.chat.ListMessages(context.Background(), "owner-a", "session-a")
	if err != nil {
		w.t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == id {
			return message, true
		}
	}
	return chat.Message{}, false
}

func TestFinishedTaskBecomesAResultCardWithPictureAndModelHandle(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	op, grant := w.pending("call-1")

	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
	item := items[0]
	if item.RequestID != grant.RequestID || item.Status != pendingapproval.StatusPending || item.Model != "Fake Model" ||
		item.Provider != "Fake" || item.EstimatedCredits != 25 || item.Prompt != "p" || item.MessageID != op.AssistantMessageID ||
		len(item.Inputs) != 1 || item.Inputs[0].AssetID != "ref-asset" || item.Inputs[0].Parameter != "reference_image" ||
		item.Inputs[0].Kind != pendingapproval.InputAsset ||
		// The input copy is the picture address; the source Task and handle are
		// not mixed into it without an exact asset identity (#80).
		item.Inputs[0].Handle != "" || item.Inputs[0].TaskID != "" || item.ProviderID != "fake-provider" {
		t.Fatalf("item = %+v", item)
	}
	if got := item.ExpiresAt.Sub(item.CreatedAt); got != time.Hour {
		t.Fatalf("expiry window = %v", got)
	}

	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil || !handled || outcome.TaskID == "" || outcome.MessageID != pendingapproval.MessageID(grant.RequestID) {
		t.Fatalf("approve = %+v handled=%v err=%v", outcome, handled, err)
	}
	started, ok := w.message(outcome.MessageID)
	if !ok || strings.Contains(started.Content, "{model}") || !strings.Contains(started.Content, "Fake Model") ||
		len(started.ToolCalls) != 1 || started.ToolCalls[0].TaskID != outcome.TaskID ||
		string(started.ToolCalls[0].Arguments) != `{"prompt":"p"}` || started.ToolCalls[0].EstimatedCredits != 25 {
		t.Fatalf("started = %+v ok=%v", started, ok)
	}
	if len(started.ImageURLs) != 0 {
		t.Fatal("a running task has no picture yet")
	}

	w.gateway.state = generation.TaskSucceeded
	if settled, err := w.service.Sweep(context.Background()); err != nil || settled != 1 {
		t.Fatalf("sweep settled=%d err=%v", settled, err)
	}
	result, _ := w.message(outcome.MessageID)
	if len(result.ImageURLs) != 1 || result.ImageURLs[0] != "/api/v1/tasks/"+outcome.TaskID+"/assets/asset-thumb" {
		t.Fatalf("picture = %v", result.ImageURLs)
	}
	var handle string
	for _, attachment := range result.Attachments {
		if attachment.Kind == chat.AttachmentKindModel && attachment.TaskID == outcome.TaskID {
			handle = attachment.Handle
		}
	}
	if handle == "" || !strings.Contains(result.Content, "Fake Model") || strings.Contains(result.Content, "开始") {
		t.Fatalf("result = %+v", result)
	}
	// Settled work is left alone however often the sweep or an approve repeats.
	for range 3 {
		if _, err := w.service.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil {
			t.Fatal(err)
		}
	}
	if w.gateway.distinctTasks() != 1 {
		t.Fatalf("tasks = %d", w.gateway.distinctTasks())
	}
	messages, _ := w.chat.ListMessages(context.Background(), "owner-a", "session-a")
	posted := 0
	for _, message := range messages {
		if message.ID == outcome.MessageID {
			posted++
		}
	}
	if posted != 1 {
		t.Fatalf("result posted %d times", posted)
	}
}

func TestFailedTaskPostsTheSchemaFailureTextAndNoPicture(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-fail")
	outcome, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil {
		t.Fatal(err)
	}
	w.gateway.state = generation.TaskFailed
	w.gateway.failure = &generation.Failure{Class: "insufficient_balance"}
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	message, _ := w.message(outcome.MessageID)
	if message.Content != w.schema.Presentation.Messages.GenerationFailedByClass["insufficient_balance"] || len(message.ImageURLs) != 0 {
		t.Fatalf("failure message = %+v", message)
	}
}

// #326 N2: the card of an approved step whose source's original task the
// provider no longer keeps says so and names the way forward, in the turn's
// locale, instead of a bare 生成失败.
func TestASourceExpiredFailurePostsTheExpiredSourceText(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-expired")
	outcome, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil {
		t.Fatal(err)
	}
	w.gateway.state = generation.TaskFailed
	w.gateway.failure = &generation.Failure{Class: generation.FailureSourceExpired, SafeDetail: "Task not found"}
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	message, _ := w.message(outcome.MessageID)
	want := w.schema.Presentation.Messages.GenerationFailedByClass[string(generation.FailureSourceExpired)]
	if want == "" || message.Content != want || strings.Contains(message.Content, "Task not found") {
		t.Fatalf("failure message = %q; want %q", message.Content, want)
	}
	for _, part := range []string{"原任务", "已保存的模型文件"} {
		if !strings.Contains(message.Content, part) {
			t.Fatalf("failure message %q lacks %q", message.Content, part)
		}
	}
}

func TestSubmitFailureAfterApprovalIsReportedOnce(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-submit")
	w.gateway.submitError = &generation.Error{Code: generation.ErrUnknownOffering, Operation: "submit", Detail: "gone"}
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil || !handled {
		t.Fatalf("approve = %+v %v %v", outcome, handled, err)
	}
	message, ok := w.message(pendingapproval.MessageID(grant.RequestID))
	if !ok || message.Content != w.schema.PendingApprovals.Messages.SubmitFailed {
		t.Fatalf("message = %+v ok=%v", message, ok)
	}
	submits := w.gateway.submits
	if _, err := w.service.Sweep(context.Background()); err != nil || w.gateway.submits != submits {
		t.Fatalf("a settled failure was retried: submits %d -> %d err=%v", submits, w.gateway.submits, err)
	}
}

// A frozen input that no longer matches the catalog (the Schema, the Offering
// revision or an input asset changed under the approval) can never be
// submitted. The approval is settled once with the submit-failed text instead
// of being retried by every sweep forever; a concurrent-update conflict of the
// repository is still a retry.
func TestFrozenInputConflictAfterApprovalIsSettledNotRetriedForever(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-conflict")
	w.gateway.submitError = &generation.Error{Code: generation.ErrConflict, Operation: "submit_prepared",
		Detail: "Product Schema 已变化，请重新准备输入"}
	if _, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil || !handled {
		t.Fatalf("approve = %v %v", handled, err)
	}
	message, ok := w.message(pendingapproval.MessageID(grant.RequestID))
	if !ok || message.Content != w.schema.PendingApprovals.Messages.SubmitFailed {
		t.Fatalf("message = %+v ok=%v", message, ok)
	}
	submits := w.gateway.submits
	w.gateway.submitError = nil
	if _, err := w.service.Sweep(context.Background()); err != nil || w.gateway.submits != submits {
		t.Fatalf("a settled conflict was retried: submits %d -> %d err=%v", submits, w.gateway.submits, err)
	}
	if w.gateway.distinctTasks() != 0 {
		t.Fatalf("a settled conflict accepted %d tasks", w.gateway.distinctTasks())
	}

	w = openWorld(t, filepath.Join(t.TempDir(), "b.db"), time.Hour)
	_, racing := w.pending("call-racing")
	w.gateway.submitError = &generation.Error{Code: generation.ErrConflict, Operation: "submit_prepared",
		Detail: "任务已被并发更新，请重试", Cause: generation.RepositoryConflict}
	if _, handled, err := w.service.Decide(context.Background(), w.operator, racing.RequestID, executiongrant.Approve); err != nil || !handled {
		t.Fatalf("approve = %v %v", handled, err)
	}
	if _, ok := w.message(pendingapproval.MessageID(racing.RequestID)); ok {
		t.Fatal("a concurrent-update conflict was reported as a permanent failure")
	}
	w.gateway.submitError = nil
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.gateway.distinctTasks() != 1 {
		t.Fatalf("the retried approval accepted %d tasks", w.gateway.distinctTasks())
	}
	if _, ok := w.message(pendingapproval.MessageID(racing.RequestID)); !ok {
		t.Fatal("the retried approval did not post its message")
	}
}

func TestTransientSubmitFailureIsRetriedBySweepWithoutDuplicates(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-retry")
	w.gateway.submitError = errors.New("database is busy")
	if _, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil || !handled {
		t.Fatalf("a durable decision must not fail on a transient submit error: %v %v", handled, err)
	}
	if w.gateway.distinctTasks() != 0 {
		t.Fatal("nothing was accepted yet")
	}
	w.gateway.submitError = nil
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.gateway.distinctTasks() != 1 {
		t.Fatalf("tasks = %d", w.gateway.distinctTasks())
	}
	if _, ok := w.message(pendingapproval.MessageID(grant.RequestID)); !ok {
		t.Fatal("the swept approval did not post its message")
	}
}

func TestConcurrentApprovalsSubmitOnce(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-race")
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if w.gateway.distinctTasks() != 1 {
		t.Fatalf("tasks = %d", w.gateway.distinctTasks())
	}
	messages, _ := w.chat.ListMessages(context.Background(), "owner-a", "session-a")
	posted := 0
	for _, message := range messages {
		if message.ID == pendingapproval.MessageID(grant.RequestID) {
			posted++
		}
	}
	if posted != 1 {
		t.Fatalf("message posted %d times", posted)
	}
}

func TestApprovalIsRefusedWhenItCanNeverRun(t *testing.T) {
	t.Run("turn stopped after the card was detached", func(t *testing.T) {
		w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
		_, grant := w.pending("call-stopped")
		// The user pressed Stop while the agent streamed its closing text: the card
		// was already detached, so the stop must not invalidate it.
		if _, found, err := w.repo.RequestTurnStop(context.Background(), "owner-a", "session-a", []string{"done"}, time.Now()); err != nil || !found {
			t.Fatalf("stop: %v %v", found, err)
		}
		items, err := w.service.List(context.Background(), "owner-a", "session-a")
		if err != nil || len(items) != 1 || items[0].RequestID != grant.RequestID {
			t.Fatalf("the detached card must stay listed after Stop: %+v err=%v", items, err)
		}
		outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
		if err != nil || !handled || outcome.TaskID == "" {
			t.Fatalf("approve after stop = %+v %v %v", outcome, handled, err)
		}
		// A second click must not create another task (the kernel's claim makes the
		// replay a no-op; here the idempotent fake kernel stands in for it).
		if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil {
			t.Fatal(err)
		}
		if w.gateway.distinctTasks() != 1 {
			t.Fatalf("tasks=%d, want exactly one", w.gateway.distinctTasks())
		}
		if _, ok := w.message(pendingapproval.MessageID(grant.RequestID)); !ok {
			t.Fatal("the approved card posted no result message")
		}
	})
	t.Run("offering changed", func(t *testing.T) {
		w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
		_, grant := w.pending("call-changed")
		w.gateway.offering.Revision = "rev-b"
		if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); !errors.Is(err, pendingapproval.ErrUnavailable) {
			t.Fatalf("approve against a changed offering err=%v", err)
		}
		stored, err := w.repo.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
		if err != nil || stored.Status != executiongrant.Pending {
			t.Fatalf("grant = %+v %v", stored, err)
		}
		// Declining stays possible.
		if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Deny); err != nil {
			t.Fatal(err)
		}
	})
}

func TestApprovalExpiresAfterTheSchemaDuration(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Second)
	_, grant := w.pending("call-ttl")
	time.Sleep(1100 * time.Millisecond)
	if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); !errors.Is(err, executiongrant.ErrExpired) {
		t.Fatalf("approve after expiry err=%v", err)
	}
}

// Defect 2: an approval that expired without a decision leaves the pending
// list at once, so neither the list nor its count (and the MaximumListed
// window) keeps it.
func TestExpiredApprovalLeavesThePendingList(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Second)
	w.pending("call-old")
	time.Sleep(1100 * time.Millisecond)
	_, fresh := w.pending("call-new")
	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 || items[0].RequestID != fresh.RequestID || items[0].Status != pendingapproval.StatusPending {
		t.Fatalf("expired approval is still listed: %+v err=%v", items, err)
	}
}

func TestOtherOwnersAreInvisible(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-owner")
	if _, err := w.service.List(context.Background(), "owner-b", "session-a"); !errors.Is(err, pendingapproval.ErrSessionNotFound) {
		t.Fatalf("foreign list err=%v", err)
	}
	foreign := executiongrant.Principal{OwnerID: "owner-b", ActorID: "owner-b", Source: executiongrant.HumanBrowser}
	if _, handled, err := w.service.Decide(context.Background(), foreign, grant.RequestID, executiongrant.Approve); handled || err != nil {
		t.Fatalf("a foreign grant must not be handled: handled=%v err=%v", handled, err)
	}
	if session, ok, err := w.service.DetachedSession(context.Background(), "owner-b", grant.RequestID); ok || session != "" || err != nil {
		t.Fatalf("foreign probe = %q %v %v", session, ok, err)
	}
	if w.gateway.distinctTasks() != 0 {
		t.Fatal("a task was created")
	}
}

func TestPendingApprovalSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	first := openWorld(t, path, time.Hour)
	first.adjustments = []generation.ParameterAdjustment{{Name: "quality", Label: "质量", RecommendedValue: json.RawMessage(`"max"`), Value: json.RawMessage(`"low"`), Reason: "先验证草稿构图", Advice: "默认最高质量", Source: "agent"}}
	_, grant := first.pending("call-restart")
	if err := first.repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openWorld(t, path, time.Hour)
	items, err := reopened.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 || items[0].RequestID != grant.RequestID || len(items[0].ParameterAdjustments) != 1 || items[0].ParameterAdjustments[0].Reason != "先验证草稿构图" || string(items[0].ParameterAdjustments[0].Value) != `"low"` {
		t.Fatalf("after restart: %+v err=%v", items, err)
	}
	if outcome, handled, err := reopened.service.Decide(context.Background(), reopened.operator, grant.RequestID, executiongrant.Approve); err != nil || !handled || outcome.TaskID == "" {
		t.Fatalf("approve after restart = %+v %v %v", outcome, handled, err)
	}
	// A decision committed just before a second crash is finished by the sweep.
	reopened.gateway.state = generation.TaskSucceeded
	if err := reopened.repo.Close(); err != nil {
		t.Fatal(err)
	}
	again := openWorld(t, path, time.Hour)
	again.gateway.state = generation.TaskSucceeded
	again.gateway.tasks = reopened.gateway.tasks
	if settled, err := again.service.Sweep(context.Background()); err != nil || settled != 1 {
		t.Fatalf("sweep after second restart settled=%d err=%v", settled, err)
	}
	message, _ := again.message(pendingapproval.MessageID(grant.RequestID))
	if len(message.ImageURLs) != 1 {
		t.Fatalf("result after restarts = %+v", message)
	}
}

func TestFinishedModelTaskWithTwoPicturesShowsOnlyTheCoverAndTheModelAttachment(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	w.gateway.drawnPicture = true
	_, grant := w.pending("call-two-pictures")
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil || !handled || outcome.TaskID == "" {
		t.Fatalf("approve = %+v handled=%v err=%v", outcome, handled, err)
	}
	w.gateway.state = generation.TaskSucceeded
	if settled, err := w.service.Sweep(context.Background()); err != nil || settled != 1 {
		t.Fatalf("sweep settled=%d err=%v", settled, err)
	}
	// The Task keeps both thumbnails among its assets ...
	task, err := w.gateway.Get(context.Background(), "owner-a", generation.TaskID(outcome.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	thumbnails := 0
	for _, asset := range task.Assets {
		if asset.Role == "thumbnail" {
			thumbnails++
		}
	}
	if thumbnails != 2 {
		t.Fatalf("the Task should hold the render and the drawn picture: %+v", task.Assets)
	}
	// ... but the message shows the model's cover only, and the model is its only attachment.
	result, _ := w.message(outcome.MessageID)
	if len(result.ImageURLs) != 1 || result.ImageURLs[0] != "/api/v1/tasks/"+outcome.TaskID+"/assets/asset-thumb" {
		t.Fatalf("the message should show the cover only: %v", result.ImageURLs)
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("attachments = %+v", result.Attachments)
	}
	model := result.Attachments[0]
	if model.Kind != chat.AttachmentKindModel || model.TaskID != outcome.TaskID || model.ImageSlot != 1 || model.Handle == "" {
		t.Fatalf("model attachment = %+v", model)
	}
}

func TestDenialPostsOneMessageAndIsFinal(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-denied")
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Deny)
	if err != nil || !handled || outcome.Status != executiongrant.Denied || outcome.TaskID != "" {
		t.Fatalf("deny = %+v %v %v", outcome, handled, err)
	}
	message, ok := w.message(outcome.MessageID)
	if !ok || message.Content != w.schema.PendingApprovals.Messages.Denied {
		t.Fatalf("message = %+v ok=%v", message, ok)
	}
	if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err == nil {
		t.Fatal("a denied approval was approved")
	}
	if w.gateway.submits != 0 {
		t.Fatal("a denied approval was submitted")
	}
}

func (w *world) messagesWith(id string) int {
	w.t.Helper()
	messages, err := w.chat.ListMessages(context.Background(), "owner-a", "session-a")
	if err != nil {
		w.t.Fatal(err)
	}
	count := 0
	for _, message := range messages {
		if message.ID == id {
			count++
		}
	}
	return count
}

// Defect 2: the sweep marks an approval that expired without a decision: one
// message, once, the operation is settled, it never comes back to the list, and
// approving it is still refused as expired.
func TestSweepPostsTheExpiryOnceAndKeepsTheApprovalRefused(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Second)
	op, grant := w.pending("call-expiring")
	if settled, err := w.service.Sweep(context.Background()); err != nil || settled != 0 {
		t.Fatalf("sweep before expiry settled=%d err=%v", settled, err)
	}
	time.Sleep(1100 * time.Millisecond)
	for range 2 {
		if _, err := w.service.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	message, ok := w.message(pendingapproval.MessageID(grant.RequestID))
	if !ok || message.Content != w.schema.PendingApprovals.Messages.Expired || w.messagesWith(message.ID) != 1 {
		t.Fatalf("expiry message = %+v ok=%v", message, ok)
	}
	stored, err := w.repo.LoadAgentOperationByGrant(context.Background(), "owner-a", op.GrantRequestID)
	if err != nil || !stored.Settled {
		t.Fatalf("operation = %+v err=%v", stored, err)
	}
	if items, _ := w.service.List(context.Background(), "owner-a", "session-a"); len(items) != 0 {
		t.Fatalf("listed after expiry: %+v", items)
	}
	if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); !errors.Is(err, executiongrant.ErrExpired) {
		t.Fatalf("approve after the expiry was posted err=%v", err)
	}
	if w.gateway.distinctTasks() != 0 {
		t.Fatal("a task was created")
	}
}

// Defect 1: a Stop that won the race against the detach ends in a visible state.
func TestSweepCancelsAnApprovalWhoseTurnWasStoppedBeforeTheDetach(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.undetached("call-stopped-early")
	if settled, err := w.service.Sweep(context.Background()); err != nil || settled != 0 {
		t.Fatalf("a live turn's undetached approval was touched: settled=%d err=%v", settled, err)
	}
	if _, found, err := w.repo.RequestTurnStop(context.Background(), "owner-a", "session-a", []string{"done"}, time.Now()); err != nil || !found {
		t.Fatalf("stop: %v %v", found, err)
	}
	for range 2 {
		if _, err := w.service.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := w.repo.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Denied || !stored.CancelledByStop() {
		t.Fatalf("grant = %+v err=%v", stored, err)
	}
	message, ok := w.message(pendingapproval.MessageID(grant.RequestID))
	if !ok || message.Content != w.schema.PendingApprovals.Messages.Cancelled || w.messagesWith(message.ID) != 1 {
		t.Fatalf("cancellation message = %+v ok=%v", message, ok)
	}
	if message.Content == w.schema.PendingApprovals.Messages.Denied {
		t.Fatal("a cancellation is not a denial")
	}
	// Whatever the person clicks later, nothing is submitted.
	if _, _, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err == nil {
		t.Fatal("a cancelled approval was approved")
	}
	if w.gateway.submits != 0 {
		t.Fatal("submitted")
	}
}

// extraExportWorld is a world whose Offering charges 5 credits for each extra
// export format, as a Tripo text-to-3D Offering does, and whose next approval
// asks for two of them.
func extraExportWorld(t *testing.T) *world {
	t.Helper()
	w := openWorld(t, filepath.Join(t.TempDir(), "extra.db"), time.Hour)
	w.gateway.offering.EstimatedCredits = 20
	w.gateway.offering.Parameters = []generation.Parameter{
		{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt},
		{Name: "output_formats", Label: "附加导出格式", Type: "string-array", Options: []generation.ParameterOption{
			{Value: "FBX", Label: "FBX"}, {Value: "USDZ", Label: "USDZ"}, {Value: "OBJ", Label: "OBJ"}}},
		{Name: "authorize_extra_credits", Type: "boolean"},
	}
	w.gateway.offering.Cost = &generation.CostDeclaration{ExtraCredits: 5, SelectionParameter: "output_formats",
		ConsentParameter: "authorize_extra_credits", ExtraLabel: "附加导出格式"}
	w.normalized = json.RawMessage(`{"prompt":"a stool","output_formats":["FBX","USDZ"],"authorize_extra_credits":true}`)
	w.extra = 10
	w.gateway.taskInput = w.normalized
	return w
}

// The card says what the extra credits are for, what the step costs without
// them and the total, from the frozen input alone.
func TestApprovalCardStatesTheExtraCreditsAndWhatTheyAreFor(t *testing.T) {
	w := extraExportWorld(t)
	_, grant := w.pending("call-extra")
	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
	item := items[0]
	if item.RequestID != grant.RequestID || item.EstimatedCredits != 30 || item.BaseCredits != 20 || item.ExtraCredits != 10 ||
		item.ExtraLabel != "附加导出格式" || len(item.ExtraItems) != 2 || item.ExtraItems[0] != "FBX" || item.ExtraItems[1] != "USDZ" {
		t.Fatalf("extra-credit card = %+v", item)
	}
	// A card without extra credits carries no extra-credit part at all.
	plain := openWorld(t, filepath.Join(t.TempDir(), "plain.db"), time.Hour)
	plain.pending("call-plain")
	plainItems, _ := plain.service.List(context.Background(), "owner-a", "session-a")
	if len(plainItems) != 1 || plainItems[0].ExtraCredits != 0 || plainItems[0].BaseCredits != 0 || plainItems[0].ExtraLabel != "" || plainItems[0].ExtraItems != nil {
		t.Fatalf("plain card = %+v", plainItems)
	}
}

// A step that repeats an earlier attempt that was charged upstream but whose
// result was not stored names that attempt, so the card can say approving
// charges again (#294). An ordinary card carries no such mark.
func TestApprovalCardNamesTheChargedAttemptItRepeats(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "recharge.db"), time.Hour)
	w.recharge = "task_old"
	w.pending("call-recharge")
	w.recharge = ""
	w.pending("call-plain")
	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 2 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
	marks := map[string]string{}
	for _, item := range items {
		marks[item.MessageID] = item.RechargeTaskID
	}
	if marks["turn-call-recharge"] != "task_old" || marks["turn-call-plain"] != "" {
		t.Fatalf("recharge marks = %v", marks)
	}
}

// Approving the card is the whole consent: exactly one task, however often the
// card is approved, carrying the input the card showed (its consent flag set,
// the extra credits equal to the card's), and the posted note shows the total.
func TestApprovingAnExtraCreditCardSubmitsExactlyOnceWithTheShownConsent(t *testing.T) {
	w := extraExportWorld(t)
	_, grant := w.pending("call-extra")
	for range 3 {
		outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
		if err != nil || !handled || outcome.TaskID == "" {
			t.Fatalf("approve = %+v handled=%v err=%v", outcome, handled, err)
		}
	}
	if w.gateway.distinctTasks() != 1 {
		t.Fatalf("approving three times accepted %d tasks", w.gateway.distinctTasks())
	}
	prepared, err := w.repo.GetPreparedInput(context.Background(), "owner-a", "prepared-call-extra")
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal(prepared.NormalizedInput, &input); err != nil || input["authorize_extra_credits"] != true {
		t.Fatalf("the approved input must carry the consent: %v %v", input, err)
	}
	if shown := w.gateway.offering.ExtraCreditDetail(prepared.NormalizedInput).Credits; shown != 10 || prepared.ExtraCredits != shown {
		t.Fatalf("approved extra credits %d (frozen %d) must equal the 10 the card showed", shown, prepared.ExtraCredits)
	}
	grantAfter, err := w.repo.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || grantAfter.Status != executiongrant.Approved || grantAfter.DecidedBy != "owner-a" || grantAfter.ApprovedByPolicy() {
		t.Fatalf("grant = %+v %v", grantAfter, err)
	}
	started, ok := w.message(pendingapproval.MessageID(grant.RequestID))
	if !ok || len(started.ToolCalls) != 1 || started.ToolCalls[0].EstimatedCredits != 30 {
		t.Fatalf("started message = %+v ok=%v", started, ok)
	}
}

// Refusing the card spends nothing and the chat says so with an alternative:
// the same step without the extra part, or a model without extra charges.
func TestRefusingAnExtraCreditCardSubmitsNothingAndProposesAnAlternative(t *testing.T) {
	w := extraExportWorld(t)
	_, grant := w.pending("call-extra")
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Deny)
	if err != nil || !handled || outcome.TaskID != "" || outcome.Status != executiongrant.Denied {
		t.Fatalf("deny = %+v handled=%v err=%v", outcome, handled, err)
	}
	if w.gateway.submits != 0 || w.gateway.distinctTasks() != 0 {
		t.Fatalf("a refused card submitted %d times", w.gateway.submits)
	}
	message, ok := w.message(pendingapproval.MessageID(grant.RequestID))
	if !ok {
		t.Fatal("the refusal was not posted")
	}
	for _, want := range []string{"Fake Model", "附加导出格式", "FBX、USDZ", "10", "20", "30"} {
		if !strings.Contains(message.Content, want) {
			t.Fatalf("refusal %q must mention %q", message.Content, want)
		}
	}
	if strings.ContainsAny(message.Content, "{}") || strings.Contains(message.Content, "勾选") {
		t.Fatalf("refusal has unfilled fields or points at a checkbox: %q", message.Content)
	}
	if message.Content == w.schema.PendingApprovals.Messages.Denied {
		t.Fatal("a refused extra-credit step must propose an alternative, not just say it was refused")
	}
	// A refused plain step keeps the plain message.
	plain := openWorld(t, filepath.Join(t.TempDir(), "plain.db"), time.Hour)
	_, plainGrant := plain.pending("call-plain")
	if _, _, err := plain.service.Decide(context.Background(), plain.operator, plainGrant.RequestID, executiongrant.Deny); err != nil {
		t.Fatal(err)
	}
	plainMessage, _ := plain.message(pendingapproval.MessageID(plainGrant.RequestID))
	if plainMessage.Content != plain.schema.PendingApprovals.Messages.Denied {
		t.Fatalf("plain refusal = %q", plainMessage.Content)
	}
}

// #324: an exact frozen Task model asset keeps its session handle and preview.
func TestApprovalNamesTheSourceModelFileByItsSessionModel(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	w.sourceModel = true
	w.modelAsset = "model-glb"
	w.gateway.offering.Parameters = append(w.gateway.offering.Parameters, generation.Parameter{Name: "source_asset", Type: "asset", Semantic: generation.SemanticSourceAsset, MediaTypes: []string{"model/gltf-binary"}})
	w.pending("call-1")
	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 || len(items[0].Inputs) != 2 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
	var source pendingapproval.Input
	for _, input := range items[0].Inputs {
		if input.Parameter == "source_asset" {
			source = input
		}
	}
	if source.Kind != pendingapproval.InputTask || source.TaskID != "task-model" || source.Handle != "@model1" || source.AssetID != "model-thumb" || source.MediaType != "" {
		t.Fatalf("source input = %+v; want the session model @model1 with its preview", source)
	}
	if reference := items[0].Inputs[0]; reference.Handle != "" || reference.TaskID != "" ||
		reference.Kind != pendingapproval.InputAsset || reference.AssetID != "ref-asset" || reference.MediaType != "image/png" {
		t.Fatalf("reference input = %+v; want the frozen input copy without a guessed source", reference)
	}
}

// #80: the card shows the session handle only when the attachment is the exact
// frozen input asset. Even then the source Task is not put beside that asset.
func TestFrozenInputHandleRequiresTheExactAsset(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "exact.db"), time.Hour)
	w.attachmentAsset = "ref-asset"
	w.pending("call-1")
	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 || len(items[0].Inputs) != 1 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
	input := items[0].Inputs[0]
	if input.Handle != "@img2" || input.TaskID != "" || input.AssetID != "ref-asset" ||
		input.Kind != pendingapproval.InputAsset {
		t.Fatalf("input = %+v; want the exact handle without the source Task", input)
	}
}

func TestApprovalModelContentRequiresAnUnambiguousModelSource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		wantModel bool
	}{
		{"unique model", "unique", true},
		{"same task repeated", "repeated", true},
		{"exactly 24 candidates with one match", "at-limit", true},
		{"repeated task exceeds candidate limit", "repeated-too-many", false},
		{"candidate has wrong owner", "wrong-owner", false},
		{"candidate has wrong session", "wrong-session", false},
		{"candidate has wrong returned task ID", "wrong-id", false},
		{"different tasks with same model bytes", "ambiguous", true},
		{"bounded history is not proof of uniqueness", "too-many", false},
		{"only a texture has matching bytes", "texture", false},
		{"missing offering", "missing", false},
		{"parameter is not a model source", "not-source", false},
		{"model media type is not supported", "unsupported", false},
		{"glb extension declares the model format", "glb-extension", true},
		{"model format is not declared", "unknown-format", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := openWorld(t, filepath.Join(t.TempDir(), "approval.db"), time.Hour)
			w.sourceModel = true
			w.modelAsset = "model-glb"
			parameter := generation.Parameter{
				Name: "source_asset", Type: "asset", Semantic: generation.SemanticSourceAsset,
				MediaTypes: []string{"model/gltf-binary"},
			}
			if tc.mode == "not-source" {
				parameter.Semantic = generation.SemanticReferenceImage
			}
			if tc.mode == "unsupported" {
				parameter.MediaTypes = []string{"model/obj"}
			}
			if tc.mode == "glb-extension" || tc.mode == "unknown-format" {
				parameter.MediaTypes = nil
				if tc.mode == "glb-extension" {
					parameter.Extensions = []string{".glb"}
				}
			}
			w.gateway.offering.Parameters = append(w.gateway.offering.Parameters, parameter)
			w.gateway.modelTasks = map[generation.TaskID]generation.Task{}
			if tc.mode == "texture" {
				w.gateway.modelTasks["task-model"] = generation.Task{ID: "task-model", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded,
					Assets: []generation.Asset{{ID: "other-model", Role: "model", SHA256: "different"}, {ID: "model-glb", Role: "texture", SHA256: "sha-model"}, {ID: "thumb", Role: "thumbnail"}}}
			}
			if tc.mode == "repeated" {
				w.extraAttachments = append(w.extraAttachments, chat.Attachment{Handle: "@model1", Kind: chat.AttachmentKindModel, TaskID: "task-model"})
			}
			if tc.mode == "repeated-too-many" {
				for i := 0; i < 24; i++ {
					w.extraAttachments = append(w.extraAttachments, chat.Attachment{Kind: chat.AttachmentKindModel, TaskID: "task-model"})
				}
			}
			if tc.mode == "wrong-owner" || tc.mode == "wrong-session" || tc.mode == "wrong-id" {
				task := generation.Task{ID: "other", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded,
					Assets: []generation.Asset{{ID: "other-asset", Role: "model", SHA256: "different"}}}
				switch tc.mode {
				case "wrong-owner":
					task.OwnerID = "owner-b"
				case "wrong-session":
					task.SessionID = "session-b"
				case "wrong-id":
					task.ID = "wrong-id"
				}
				w.extraAttachments = append(w.extraAttachments, chat.Attachment{Kind: chat.AttachmentKindModel, TaskID: "other"})
				w.gateway.modelTasks["other"] = task
			}
			if tc.mode == "ambiguous" || tc.mode == "too-many" || tc.mode == "at-limit" {
				w.extraAttachments = append(w.extraAttachments, chat.Attachment{Handle: "@model-copy", Kind: chat.AttachmentKindModel, AssetID: "source-copy"})
				n := 1
				if tc.mode == "too-many" {
					n = 24
				} else if tc.mode == "at-limit" {
					n = 23
				}
				for i := 0; i < n; i++ {
					id := generation.TaskID(fmt.Sprintf("other-%d", i))
					sha := "different"
					if tc.mode == "ambiguous" {
						sha = "sha-model"
					}
					w.extraAttachments = append(w.extraAttachments, chat.Attachment{Handle: "@" + string(id), Kind: chat.AttachmentKindModel, TaskID: string(id)})
					w.gateway.modelTasks[id] = generation.Task{ID: id, OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded, Assets: []generation.Asset{{ID: "other-asset", Role: "model", SHA256: sha}}}
				}
			}
			w.pending("call")
			if tc.mode == "missing" {
				w.gateway.offering.ID = "unavailable"
			}
			items, err := w.service.List(context.Background(), "owner-a", "session-a")
			if err != nil || len(items) != 1 || len(items[0].Inputs) != 2 {
				t.Fatalf("list=%+v err=%v", items, err)
			}
			picture, model := items[0].Inputs[0], items[0].Inputs[1]
			if picture.Kind != pendingapproval.InputAsset || picture.AssetID != "ref-asset" || picture.MediaType != "image/png" || picture.Handle != "" || picture.TaskID != "" {
				t.Fatalf("texture-matching reference misidentified: %+v", picture)
			}
			if tc.wantModel {
				if model.Kind != pendingapproval.InputTask || model.TaskID != "task-model" || model.Handle != "@model1" || model.AssetID != "model-thumb" {
					t.Fatalf("unique model: %+v", model)
				}
			} else if model.Kind != pendingapproval.InputAsset || model.AssetID != "model-glb" || model.Handle != "" || model.TaskID != "" {
				t.Fatalf("unproved model source: %+v", model)
			}
			if _, err := w.service.List(context.Background(), "owner-b", "session-a"); !errors.Is(err, pendingapproval.ErrSessionNotFound) {
				t.Fatalf("foreign list: %v", err)
			}
		})
	}
}
