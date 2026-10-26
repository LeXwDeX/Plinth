package chat_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func handlePolicyForTest(t *testing.T) (chat.HandlePolicy, catalog.ChatSchema) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	return chat.NewHandlePolicy(schema.Context), schema
}

func TestHandlePolicyParsesOnlyDeclaredHandles(t *testing.T) {
	policy, _ := handlePolicyForTest(t)
	for token, want := range map[string]struct {
		kind   string
		number int
		ok     bool
	}{
		"@img3":   {chat.AttachmentKindImage, 3, true},
		"@IMG12":  {chat.AttachmentKindImage, 12, true},
		" img4 ":  {chat.AttachmentKindImage, 4, true},
		"@model2": {chat.AttachmentKindModel, 2, true},
		"@img0":   {"", 0, false},
		"@img03":  {"", 0, false},
		"@img":    {"", 0, false},
		"@img3x":  {"", 0, false},
		"task-1":  {"", 0, false},
		"@sel":    {"", 0, false},
	} {
		kind, number, ok := policy.Parse(token)
		if kind != want.kind || number != want.number || ok != want.ok {
			t.Fatalf("Parse(%q) = %q %d %v; want %+v", token, kind, number, ok, want)
		}
	}
}

func TestHandlesAreNumberedPerKindAndNeverReused(t *testing.T) {
	policy, _ := handlePolicyForTest(t)
	history := []chat.Message{
		{Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage}, {Handle: "@img2", Kind: chat.AttachmentKindImage}}},
		{Attachments: []chat.Attachment{{Handle: "@model1", Kind: chat.AttachmentKindModel}}},
	}
	got := policy.Assign(history, []chat.Attachment{
		{Kind: chat.AttachmentKindImage}, {Kind: chat.AttachmentKindModel}, {Kind: chat.AttachmentKindImage},
		{Kind: chat.AttachmentKindImage}})
	handles := make([]string, len(got))
	for index, attachment := range got {
		handles[index] = attachment.Handle
	}
	if want := []string{"@img3", "@model2", "@img4", "@img5"}; !reflect.DeepEqual(handles, want) {
		t.Fatalf("handles = %v; want %v", handles, want)
	}
}

func TestResolveHandlesIsScopedToTheLoadedHistoryAndSelection(t *testing.T) {
	policy, schema := handlePolicyForTest(t)
	history := []chat.Message{{ImageURLs: []string{"a", "b"}, Attachments: []chat.Attachment{
		{Handle: "@img1", Kind: chat.AttachmentKindImage, ImageSlot: 2, TaskID: "t"},
		{Handle: "@model1", Kind: chat.AttachmentKindModel, TaskID: "m"}}}}
	selection := []chat.Attachment{{Handle: "@img2", Kind: chat.AttachmentKindImage, URL: "selected"}}

	resolved, err := policy.ResolveHandles(history, selection, []string{"@img1", "@img2"}, chat.AttachmentKindImage)
	if err != nil || len(resolved) != 2 || resolved[0].URL != "b" || resolved[1].URL != "selected" {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}
	resolved, err = policy.ResolveHandles(history, selection, []string{schema.Context.SelectionAlias}, chat.AttachmentKindImage)
	if err != nil || len(resolved) != 1 || resolved[0].Attachment.Handle != "@img2" {
		t.Fatalf("alias = %+v, %v", resolved, err)
	}
	var refusal *chat.UnknownHandleError
	if _, err = policy.ResolveHandles(history, selection, []string{"@img7"}, chat.AttachmentKindImage); !errors.As(err, &refusal) || refusal.Reason != "unknown" {
		t.Fatalf("unknown handle err = %v", err)
	}
	if _, err = policy.ResolveHandles(history, selection, []string{"@model1"}, chat.AttachmentKindImage); !errors.As(err, &refusal) || refusal.Reason != "kind" {
		t.Fatalf("wrong kind err = %v", err)
	}
}

// recordingVerifier accepts a claim only when the owner holds the Task or
// capture in exactly this session, like the production verifier.
type recordingVerifier struct {
	// held maps "owner/session" to the IDs that scope may use.
	held map[string]map[string]bool
	// models names the held Tasks that are 3D models: only they verify as a
	// model claim, like the production verifier, which needs a saved model asset.
	models map[string]bool
	calls  []chat.AttachmentRef
	owners []string
}

func (v *recordingVerifier) VerifyAttachment(_ context.Context, owner, session string, ref chat.AttachmentRef) (chat.Attachment, error) {
	v.calls = append(v.calls, ref)
	v.owners = append(v.owners, owner+"/"+session)
	held := v.held[owner+"/"+session]
	for _, id := range []string{ref.TaskID, ref.AssetID, ref.CaptureID} {
		if id != "" && !held[id] {
			return chat.Attachment{}, chat.ErrAttachmentRejected
		}
	}
	if ref.TaskID == "" && ref.AssetID == "" && ref.CaptureID == "" && !strings.HasPrefix(ref.URL, "data:") {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	if ref.Kind == chat.AttachmentKindModel && !v.models[ref.TaskID] {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	attachment := chat.Attachment{Kind: ref.Kind, Role: chat.AttachmentRoleGenerated, AssetID: ref.AssetID,
		TaskID: ref.TaskID, CaptureID: ref.CaptureID, ImageSlot: ref.ImageSlot, URL: ref.URL}
	switch {
	case ref.CaptureID != "":
		attachment.Role = chat.AttachmentRoleCapture
	case ref.TaskID == "":
		attachment.Role = chat.AttachmentRoleUpload
	}
	return attachment, nil
}

func newHandleService(t *testing.T, verifier chat.AttachmentVerifier) (*chat.Service, *memoryChatRepository, catalog.ChatSchema) {
	t.Helper()
	policy, schema := handlePolicyForTest(t)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = verifier
	})
	return service, repository, schema
}

func TestStartTurnVerifiesClaimsAndGivesHandles(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{
		"owner-a/session": {"task-1": true, "asset-1": true, "capture-1": true, "task-3d": true, "asset-3d": true},
	}, models: map[string]bool{"task-3d": true}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = verifier.held["owner-a/session"]

	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID, Content: "edit these",
		ImageURLs: []string{"/api/v1/tasks/task-1/assets/asset-1", "data:image/png;base64,QQ==", "/api/v1/tasks/foreign/assets/foreign-asset"},
		Attachments: []chat.AttachmentRef{
			{ImageSlot: 1, TaskID: "task-1", AssetID: "asset-1"},
			// A claim about a Task the owner does not hold in this session.
			{ImageSlot: 3, TaskID: "foreign", AssetID: "foreign-asset"},
		},
		Selection: []chat.AttachmentRef{
			{Kind: chat.AttachmentKindModel, TaskID: "task-3d", AssetID: "asset-3d", URL: "/thumb-3d"},
			{Kind: chat.AttachmentKindImage, TaskID: "foreign", AssetID: "foreign-asset"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	user := messages[0]
	if len(user.Attachments) != 4 {
		t.Fatalf("attachments = %+v", user.Attachments)
	}
	first, second, third, selected := user.Attachments[0], user.Attachments[1], user.Attachments[2], user.Attachments[3]
	if first.Handle != "@img1" || first.TaskID != "task-1" || first.AssetID != "asset-1" || first.ImageSlot != 1 || first.URL != "" {
		t.Fatalf("verified claim = %+v", first)
	}
	if second.Handle != "@img2" || second.Role != chat.AttachmentRoleUpload || second.TaskID != "" {
		t.Fatalf("plain upload = %+v", second)
	}
	// The foreign claim keeps its picture but none of the claimed IDs.
	if third.Handle != "@img3" || third.TaskID != "" || third.AssetID != "" || third.Role != chat.AttachmentRoleUpload {
		t.Fatalf("foreign claim was trusted: %+v", third)
	}
	if selected.Handle != "@model1" || selected.Kind != chat.AttachmentKindModel || selected.TaskID != "task-3d" || selected.ImageSlot != 0 {
		t.Fatalf("selected model = %+v", selected)
	}
	for _, attachment := range user.Attachments {
		if attachment.TaskID == "foreign" || attachment.AssetID == "foreign-asset" {
			t.Fatalf("foreign ID persisted: %+v", attachment)
		}
	}
	for _, owner := range verifier.owners {
		if owner != "owner-a/"+session.ID {
			t.Fatalf("verified for the wrong scope %q", owner)
		}
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Options.Selection) != 1 || turn.Options.Selection[0].Handle != "@model1" || turn.Options.Selection[0].TaskID != "task-3d" {
		t.Fatalf("turn selection = %+v (the foreign selected image must be dropped)", turn.Options.Selection)
	}
}

func TestSelectionOfAShownObjectReusesItsHandle(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	service, repository, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"task-1": true, "asset-1": true, "task-2": true, "asset-2": true}
	first, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "one",
		ImageURLs:   []string{"/api/v1/tasks/task-1/assets/asset-1"},
		Attachments: []chat.AttachmentRef{{ImageSlot: 1, TaskID: "task-1", AssetID: "asset-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.FinishTurn(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := service.FinishStream(ctx, session.ID, first.ID, false); err != nil {
		t.Fatal(err)
	}
	second, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "make this bigger",
		Selection: []chat.AttachmentRef{
			{Kind: chat.AttachmentKindImage, TaskID: "task-1", AssetID: "asset-1"},
			{Kind: chat.AttachmentKindImage, TaskID: "task-2", AssetID: "asset-2", URL: "/api/v1/tasks/task-2/assets/asset-2"},
			// The same object twice is one object.
			{Kind: chat.AttachmentKindImage, TaskID: "task-2", AssetID: "asset-2"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Options.Selection) != 2 || turn.Options.Selection[0].Handle != "@img1" || turn.Options.Selection[1].Handle != "@img2" {
		t.Fatalf("selection = %+v", turn.Options.Selection)
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	stored := repository.messages[session.ID]
	last := stored[len(stored)-2]
	if len(last.Attachments) != 1 || last.Attachments[0].Handle != "@img2" {
		t.Fatalf("only the new selected object may be recorded: %+v", last.Attachments)
	}
}

func TestSelectionIsLimitedBySchema(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	service, _, schema := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]bool{}
	var selection []chat.AttachmentRef
	for index := 0; index < schema.Context.MaximumSelection+5; index++ {
		id := "task-" + string(rune('a'+index))
		held[id], held["asset-"+id] = true, true
		selection = append(selection, chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: id, AssetID: "asset-" + id, URL: "/x/" + id})
	}
	verifier.held["owner-a/"+session.ID] = held
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "x", Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Options.Selection) != schema.Context.MaximumSelection {
		t.Fatalf("selection size = %d; Schema allows %d", len(turn.Options.Selection), schema.Context.MaximumSelection)
	}
}

// identitylessVerifier accepts every claim the way the real verifier accepts a
// pasted data URI: as a plain upload without a platform ID or a picture.
type identitylessVerifier struct{}

func (identitylessVerifier) VerifyAttachment(_ context.Context, _, _ string, ref chat.AttachmentRef) (chat.Attachment, error) {
	return chat.Attachment{Kind: ref.Kind, Role: chat.AttachmentRoleUpload, ImageSlot: ref.ImageSlot}, nil
}

func TestSelectionWithoutIdentityOrPictureIsDropped(t *testing.T) {
	service, _, _ := newHandleService(t, identitylessVerifier{})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "this one",
		ImageURLs: []string{"data:image/png;base64,QQ=="},
		Selection: []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, URL: "data:image/png;base64,QQ=="}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Options.Selection) != 0 {
		t.Fatalf("a selected object that resolves to nothing must not get a handle: %+v", turn.Options.Selection)
	}
	messages, _ := service.ListMessages(ctx, "owner-a", session.ID)
	// The message picture itself keeps its handle: its slot is the picture.
	if len(messages[0].Attachments) != 1 || messages[0].Attachments[0].Handle != "@img1" || messages[0].Attachments[0].ImageSlot != 1 {
		t.Fatalf("message attachments = %+v", messages[0].Attachments)
	}
}

func TestStartTurnWithoutVerifierKeepsLegacyMessages(t *testing.T) {
	service, _, _ := newHandleService(t, nil)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "x",
		ImageURLs: []string{"/a.png"}, Selection: []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, TaskID: "t"}}}); err != nil {
		t.Fatal(err)
	}
	messages, _ := service.ListMessages(ctx, "owner-a", session.ID)
	if len(messages[0].Attachments) != 0 {
		t.Fatalf("attachments without a verifier: %+v", messages[0].Attachments)
	}
}

func TestAttachResultsRecordsGeneratedImagesAndModels(t *testing.T) {
	verifier := &resultVerifier{}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	message := chat.Message{SessionID: session.ID, ImageURLs: []string{"/api/v1/tasks/img-task/assets/a", "/api/v1/tasks/model-task/assets/thumb", "/api/v1/tasks/lost/assets/x"}}
	service.AttachResults(ctx, "owner-a", &message, map[string]bool{"model-task": true, "faceless-model": true})
	var summary []string
	for _, attachment := range message.Attachments {
		summary = append(summary, attachment.Handle+"/"+attachment.Kind+"/"+attachment.TaskID+"/"+string(rune('0'+attachment.ImageSlot)))
	}
	want := []string{"@img1/image/img-task/1", "@model1/model/model-task/2", "@model2/model/faceless-model/0"}
	if !reflect.DeepEqual(summary, want) {
		t.Fatalf("attachments = %v; want %v", summary, want)
	}
	// Repeating the call is harmless: handles do not change.
	before := message.Attachments
	service.AttachResults(ctx, "owner-a", &message, map[string]bool{"model-task": true, "faceless-model": true})
	if !reflect.DeepEqual(before, message.Attachments) {
		t.Fatalf("second attach changed %+v to %+v", before, message.Attachments)
	}
}

func attachmentSummary(attachments []chat.Attachment) []string {
	var summary []string
	for _, attachment := range attachments {
		summary = append(summary, attachment.Handle+"/"+attachment.Kind+"/"+attachment.TaskID+"/"+string(rune('0'+attachment.ImageSlot)))
	}
	return summary
}

func TestAttachResultsKeepsThePicturesOfAModelTaskOutOfTheImageHandles(t *testing.T) {
	verifier := &resultVerifier{}
	service, repository, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	modelTasks := map[string]bool{"model-task": true}

	// A text-to-3D result carries two thumbnails: the render of the model and
	// the picture the provider drew before modelling. Both preview the model.
	model := chat.Message{SessionID: session.ID, ImageURLs: []string{
		"/api/v1/tasks/model-task/assets/rendered", "/api/v1/tasks/model-task/assets/drawn"}}
	service.AttachResults(ctx, "owner-a", &model, modelTasks)
	if want := []string{"@model1/model/model-task/1"}; !reflect.DeepEqual(attachmentSummary(model.Attachments), want) {
		t.Fatalf("attachments = %v; want %v", attachmentSummary(model.Attachments), want)
	}
	if _, found := chat.AttachmentForSlot(model, 2); found {
		t.Fatalf("the second picture of a model Task became an attachment: %+v", model.Attachments)
	}
	// Repeating the call neither adds the skipped picture nor renumbers.
	before := model.Attachments
	service.AttachResults(ctx, "owner-a", &model, modelTasks)
	if !reflect.DeepEqual(before, model.Attachments) {
		t.Fatalf("second attach changed %+v to %+v", before, model.Attachments)
	}

	// A 2D picture in the same message still gets the next @img number: the
	// skipped picture does not use one up.
	mixed := chat.Message{SessionID: session.ID, ImageURLs: []string{
		"/api/v1/tasks/flat-1/assets/a",
		"/api/v1/tasks/model-task/assets/rendered", "/api/v1/tasks/model-task/assets/drawn",
		"/api/v1/tasks/flat-2/assets/b"}}
	service.AttachResults(ctx, "owner-a", &mixed, modelTasks)
	want := []string{"@img1/image/flat-1/1", "@model1/model/model-task/2", "@img2/image/flat-2/4"}
	if !reflect.DeepEqual(attachmentSummary(mixed.Attachments), want) {
		t.Fatalf("mixed attachments = %v; want %v", attachmentSummary(mixed.Attachments), want)
	}

	// A later message continues the numbering of the session.
	repository.mu.Lock()
	repository.messages[session.ID] = []chat.Message{mixed}
	repository.mu.Unlock()
	later := chat.Message{SessionID: session.ID, ImageURLs: []string{
		"/api/v1/tasks/model-task-2/assets/rendered", "/api/v1/tasks/model-task-2/assets/drawn", "/api/v1/tasks/flat-3/assets/c"}}
	service.AttachResults(ctx, "owner-a", &later, map[string]bool{"model-task-2": true})
	if want := []string{"@model2/model/model-task-2/1", "@img3/image/flat-3/3"}; !reflect.DeepEqual(attachmentSummary(later.Attachments), want) {
		t.Fatalf("later attachments = %v; want %v", attachmentSummary(later.Attachments), want)
	}
}

func TestAttachResultsGivesNoImageHandleToAModelTaskThatDoesNotVerify(t *testing.T) {
	verifier := &resultVerifier{notModels: map[string]bool{"broken-model": true}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	message := chat.Message{SessionID: session.ID, ImageURLs: []string{
		"/api/v1/tasks/broken-model/assets/rendered", "/api/v1/tasks/broken-model/assets/drawn",
		"/api/v1/tasks/flat/assets/a"}}
	service.AttachResults(ctx, "owner-a", &message, map[string]bool{"broken-model": true})
	// The pictures stay in the message, but neither of them is offered as a 2D image.
	if want := []string{"@img1/image/flat/3"}; !reflect.DeepEqual(attachmentSummary(message.Attachments), want) {
		t.Fatalf("attachments = %v; want %v", attachmentSummary(message.Attachments), want)
	}
	if len(message.ImageURLs) != 3 {
		t.Fatalf("the pictures were changed: %v", message.ImageURLs)
	}
}

// resultVerifier verifies the pictures and models of generated results by the
// Task in their URL. notModels lists Tasks that cannot be verified as a model
// (for example one without a saved model file).
type resultVerifier struct{ notModels map[string]bool }

func (v *resultVerifier) VerifyAttachment(_ context.Context, _, _ string, ref chat.AttachmentRef) (chat.Attachment, error) {
	taskID := ref.TaskID
	if taskID == "" {
		taskID = strings.Split(strings.TrimPrefix(ref.URL, "/api/v1/tasks/"), "/")[0]
	}
	if taskID == "lost" || (ref.Kind == chat.AttachmentKindModel && v.notModels[taskID]) {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	return chat.Attachment{Kind: ref.Kind, Role: chat.AttachmentRoleGenerated, TaskID: taskID, ImageSlot: ref.ImageSlot}, nil
}

func TestOrchestratorNamesPicturesBySchemaLabelAndListsTheSelection(t *testing.T) {
	policy, schema := handlePolicyForTest(t)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) { config.Handles = policy })
	schema.Stream.EventFields.Type = "configured-type"
	schema.Stream.EventFields.Content = "configured-content"
	schema.Stream.EventTypes.Text = "configured-text"
	schema.Stream.EventTypes.Done = "configured-done"
	schema.Roles.System, schema.Roles.User, schema.Roles.Assistant, schema.Roles.Tool =
		"configured-system", "configured-user", "configured-assistant", "configured-tool"
	schema.State.PendingContent = "configured-pending"
	schema.Defaults.Title = "configured default title"
	schema.Agent.SystemPrompt = "base prompt"
	schema.Agent.DomainContext = map[string]string{}
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	// Seed a finished earlier exchange that already carries handles.
	repository.mu.Lock()
	repository.messages[session.ID] = []chat.Message{
		{ID: "old-user", SessionID: session.ID, Role: schema.Roles.User, Content: "earlier", ImageURLs: []string{"/up.png"},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, ImageSlot: 1}}},
		{ID: "old-assistant", SessionID: session.ID, Role: schema.Roles.Assistant, Content: "made", ImageURLs: []string{"/gen.png", "/thumb.png"},
			Attachments: []chat.Attachment{
				{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-9", ImageSlot: 1},
				{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "task-3d", ImageSlot: 2}}},
	}
	repository.mu.Unlock()
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "retexture this",
		Options: chat.TurnOptions{CreativeDomain: "image"}})
	if err != nil {
		t.Fatal(err)
	}
	// The selection is whatever the service stored with the turn.
	repository.mu.Lock()
	turn := repository.turns[assistant.ID]
	turn.Options.Selection = []chat.Attachment{{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "task-3d"}}
	repository.turns[assistant.ID] = turn
	repository.mu.Unlock()
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		// A selection smuggled into the command is ignored.
		Options: chat.TurnOptions{CreativeDomain: "image", Selection: []chat.Attachment{{Handle: "@img99", Kind: chat.AttachmentKindImage}}}}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("requests = %d", len(agent.requests))
	}
	request := agent.requests[0]
	system := request.Messages[0].Content
	if !strings.Contains(system, schema.Context.SelectionAlias) || !strings.Contains(system, "@model1") ||
		!strings.Contains(system, "task-3d") || strings.Contains(system, "@img99") {
		t.Fatalf("selection block = %q", system)
	}
	var transcript strings.Builder
	for _, message := range request.Messages[1:] {
		transcript.WriteString(message.Content)
		for _, part := range message.Parts {
			transcript.WriteString(part.Text)
		}
	}
	for _, want := range []string{"@img1", "@img2", "task-9", "@model1", "task-3d"} {
		if !strings.Contains(transcript.String(), want) {
			t.Fatalf("history labels lack %q: %q", want, transcript.String())
		}
	}
}

// #266: the canvas source is frozen only with its verified snapshot. An
// undeclared source or a canvas turn whose snapshot did not verify does not
// start, so it can never run from another picture or none.
func TestCanvasInputSourceIsVerifiedAndFrozenWithTheTurn(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	policy, schema := handlePolicyForTest(t)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = verifier
		config.CanvasInputSource = schema.Context.CanvasInputSource
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"snapshot": true}
	snapshot := []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, AssetID: "snapshot", URL: "/api/v1/input-assets/snapshot"}}
	canvas := chat.TurnOptions{InputSource: schema.Context.CanvasInputSource}

	if _, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "x",
		Options: chat.TurnOptions{InputSource: "history"}, Selection: snapshot}); !errors.Is(err, chat.ErrInvalidInputSource) {
		t.Fatalf("undeclared source = %v", err)
	}
	if _, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "x",
		Options: canvas}); !errors.Is(err, chat.ErrCanvasSourceMissing) {
		t.Fatalf("canvas without snapshot = %v", err)
	}
	foreign := []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, AssetID: "someone-else", URL: "/x"}}
	if _, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "x",
		Options: canvas, Selection: foreign}); !errors.Is(err, chat.ErrCanvasSourceMissing) {
		t.Fatalf("canvas with an unverified snapshot = %v", err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "x",
		Options: canvas, Selection: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Options.InputSource != schema.Context.CanvasInputSource || len(turn.Options.Selection) != 1 || turn.Options.SelectionPicked {
		t.Fatalf("frozen options = %+v", turn.Options)
	}
}

// uploadHintRequest runs one turn whose user message carries two uploads and
// returns the text the Agent receives for the earlier and the latest user
// message. withHandles gives the uploads structured attachments (@img3, @img4).
func uploadHintRequest(t *testing.T, withHandles bool) (string, string, catalog.ChatSchema) {
	t.Helper()
	policy, schema := handlePolicyForTest(t)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) { config.Handles = policy })
	schema.Stream.EventFields.Type = "configured-type"
	schema.Stream.EventFields.Content = "configured-content"
	schema.Stream.EventTypes.Text = "configured-text"
	schema.Stream.EventTypes.Done = "configured-done"
	schema.Roles.System, schema.Roles.User, schema.Roles.Assistant, schema.Roles.Tool =
		"configured-system", "configured-user", "configured-assistant", "configured-tool"
	schema.State.PendingContent = "configured-pending"
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	repository.mu.Lock()
	repository.messages[session.ID] = []chat.Message{
		{ID: "old-user", SessionID: session.ID, Role: schema.Roles.User, Content: "earlier", ImageURLs: []string{"/up.png"},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, ImageSlot: 1}}},
		{ID: "old-assistant", SessionID: session.ID, Role: schema.Roles.Assistant, Content: "made", ImageURLs: []string{"/gen.png"},
			Attachments: []chat.Attachment{{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-9", ImageSlot: 1}}},
	}
	repository.mu.Unlock()
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "what is in these pictures?",
		Options: chat.TurnOptions{CreativeDomain: "image"}})
	if err != nil {
		t.Fatal(err)
	}
	// The uploads of this turn, as the service stores them once verified.
	repository.mu.Lock()
	messages := repository.messages[session.ID]
	for index := range messages {
		if messages[index].Role != schema.Roles.User || messages[index].ID == "old-user" {
			continue
		}
		messages[index].ImageURLs = []string{"/now-1.png", "/now-2.png"}
		if withHandles {
			messages[index].Attachments = []chat.Attachment{
				{Handle: "@img3", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, ImageSlot: 1},
				{Handle: "@img4", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, ImageSlot: 2},
			}
		}
	}
	repository.mu.Unlock()
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		Options: chat.TurnOptions{CreativeDomain: "image"}}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("requests = %d", len(agent.requests))
	}
	var users []string
	for _, message := range agent.requests[0].Messages[1:] {
		if message.Role != schema.Roles.User {
			continue
		}
		text := message.Content
		for _, part := range message.Parts {
			text += part.Text
		}
		users = append(users, text)
	}
	if len(users) != 2 {
		t.Fatalf("user messages = %q", users)
	}
	return users[0], users[1], schema
}

// #293: the hint after this turn's uploads says which pictures they are and
// that an edit uses them by default, but it does not tell the Agent to
// generate: only looking at a picture needs no tool.
func TestCurrentUploadHintNamesThisTurnsPicturesWithoutPushingGeneration(t *testing.T) {
	earlier, latest, schema := uploadHintRequest(t, true)
	tool := schema.Tools[0].Name
	for _, want := range []string{"本轮用户上传了 2 张图片（句柄 @img3 @img4）", "不调用生成工具", "要求据此生成或编辑时", tool, "默认使用本轮这些图片"} {
		if !strings.Contains(latest, want) {
			t.Fatalf("latest user message lacks %q: %q", want, latest)
		}
	}
	for _, pushed := range []string{"请使用", "最终选图与裁剪由系统完成"} {
		if strings.Contains(latest, pushed) {
			t.Fatalf("hint still pushes generation (%q): %q", pushed, latest)
		}
	}
	if strings.Contains(earlier, "本轮用户上传") {
		t.Fatalf("an earlier user message carries the hint: %q", earlier)
	}

	// Pictures without handles (older messages) keep the count and drop the
	// handle phrase instead of leaving a placeholder.
	_, legacy, _ := uploadHintRequest(t, false)
	if !strings.Contains(legacy, "本轮用户上传了 2 张图片。") || strings.Contains(legacy, "句柄 @img3") ||
		strings.Contains(legacy, "{handles}") {
		t.Fatalf("hint without handles = %q", legacy)
	}
}
