package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
)

// cropAssetRoute is the Schema's input asset route the crops are shown by.
const cropAssetRoute = "/api/v1/assets/{assetID}"

// sheetCrops stands in for the listcrop service: it cuts the 2048x2048 sheet
// into vertical strips and names each stored crop crop_N, without bytes.
type sheetCrops struct{ calls int }

func (crops *sheetCrops) CropRegions(_ context.Context, request listcrop.Request) ([]listcrop.Result, error) {
	crops.calls++
	results := make([]listcrop.Result, 0, len(request.Regions))
	for index, region := range request.Regions {
		x0 := int(region.X * 2048)
		x1 := int((region.X + region.Width) * 2048)
		results = append(results, listcrop.Result{
			InputAssetID:     generation.AssetID(fmt.Sprintf("crop_%d", index+1)),
			NormalizedRegion: region,
			PixelRect:        listcrop.PixelRect{X0: x0, Y0: 0, X1: x1, Y1: 2048},
			SourceWidth:      2048, SourceHeight: 2048,
			SHA256: fmt.Sprintf("sha-%d", index+1),
		})
	}
	return results, nil
}

// cropPictureVerifier is the attachment verifier of the test: an owned input
// asset route is a stored crop (the resolver reads that from provenance), and
// a rejected id is a picture the platform cannot show.
type cropPictureVerifier struct{ rejected map[string]bool }

func (verifier *cropPictureVerifier) VerifyAttachment(_ context.Context, _, _ string, ref chat.AttachmentRef) (chat.Attachment, error) {
	assetID, found := strings.CutPrefix(ref.URL, "/api/v1/assets/")
	if !found || verifier.rejected[assetID] {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	return chat.Attachment{Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCrop,
		AssetID: assetID, ImageSlot: ref.ImageSlot}, nil
}

type cropRig struct {
	schema   catalog.ChatSchema
	service  *chat.Service
	agent    *scriptedAgent
	resolver *lookupReferences
	crops    *sheetCrops
	process  func(t *testing.T) chat.Message
}

// newCropRig wires a scripted Agent to the real crop_image_regions tool, the
// history image lookup and the shipped refine_image tool (whose
// reference_handles resolve like every picture argument), over a session whose
// @img1 is a four-view sheet. Nothing paid or remote is called.
func newCropRig(t *testing.T, responses []agents.Completion, rejected ...string) *cropRig {
	t.Helper()
	rig := &cropRig{schema: configuredChatSchema(t), agent: &scriptedAgent{responses: responses}, crops: &sheetCrops{}}
	policy := chat.NewHandlePolicy(rig.schema.Context)
	verifier := &cropPictureVerifier{rejected: map[string]bool{}}
	for _, id := range rejected {
		verifier.rejected[id] = true
	}
	repository := newMemoryChatRepository()
	rig.service = newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = verifier
	})
	refine := rig.schema.Tools[0]
	gateway := &lookupGateway{offering: generation.Offering{
		ID: "configured/edit", ModelID: "configured-model", Capability: refine.Capability, Enabled: true,
		PollInterval: time.Second, Timeout: 5 * time.Second,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "images", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 4},
		},
	}}
	rig.resolver = &lookupReferences{}
	generator, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: rig.resolver, Chat: rig.schema,
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	cropTool, err := agenttools.NewCropTool(agenttools.CropToolConfig{
		Context: rig.schema.Context, Schema: rig.schema.ImageCrop, Crops: rig.crops,
		AssetItemRoute: cropAssetRoute,
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := agenttools.New(agenttools.Config{
		Context: rig.schema.Context, HistoryLookup: chat.NewHistoryLookupPolicy(rig.schema),
		Params: agentparams.NewStore(), Generate: generator, CropImageRegions: cropTool.Execute,
	})
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: rig.service, Schema: rig.schema, Model: "configured-model", Agent: rig.agent, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	options := chat.TurnOptions{CreativeDomain: "image", AIModel: "configured-model"}
	session, err := rig.service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	repository.mu.Lock()
	repository.messages[session.ID] = []chat.Message{
		{ID: "sheet-user", SessionID: session.ID, Role: rig.schema.Roles.User, Content: "画圣母玛丽亚四视图"},
		{ID: "sheet-reply", SessionID: session.ID, Role: rig.schema.Roles.Assistant,
			ImageURLs:   []string{"/api/v1/tasks/t-sheet/assets/sheet"},
			ToolCalls:   []chat.ToolCall{{Name: "refine_image", TaskID: "t-sheet", Arguments: []byte(`{"prompt":"four views"}`)}},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "t-sheet", AssetID: "sheet", ImageSlot: 1}}},
	}
	repository.mu.Unlock()
	assistant, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
		Content: "我需要多张图，你现在的这个我没办法拿给3D模型", Options: options})
	if err != nil {
		t.Fatal(err)
	}
	rig.process = func(t *testing.T) chat.Message {
		t.Helper()
		if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID,
			AssistantMessageID: assistant.ID, Options: options}); err != nil {
			t.Fatal(err)
		}
		messages, err := rig.service.ListMessages(ctx, "owner-a", session.ID)
		if err != nil {
			t.Fatal(err)
		}
		return messages[len(messages)-1]
	}
	return rig
}

// cropSheetCall cuts the four views out of @img1 with their labels.
func cropSheetCall() agents.ToolCall {
	return agents.ToolCall{ID: "crop-1", Name: agenttools.ToolCropImageRegions, Arguments: []byte(
		`{"image":"@img1","regions":[` +
			`{"label":"正面","region":{"x":0,"y":0,"width":0.25,"height":1}},` +
			`{"label":"侧面","region":{"x":0.25,"y":0,"width":0.25,"height":1}},` +
			`{"label":"背面","region":{"x":0.5,"y":0,"width":0.25,"height":1}},` +
			`{"label":"四分之三","region":{"x":0.75,"y":0,"width":0.25,"height":1}}]}`)}
}

// toolReply returns the latest tool result in the request the Agent made
// right after a call: the result of that call. The orchestrator renames call
// IDs per reply, so the result is found by role and order.
func toolReply(t *testing.T, schema catalog.ChatSchema, agent *scriptedAgent, request int) string {
	t.Helper()
	if len(agent.requests) <= request {
		t.Fatalf("agent saw %d requests, want more than %d", len(agent.requests), request)
	}
	reply := ""
	for _, message := range agent.requests[request].Messages {
		if message.Role == schema.Roles.Tool {
			reply = message.Content
		}
	}
	if reply == "" {
		t.Fatalf("request %d carries no tool result", request)
	}
	return reply
}

// #552: in a conversation, the crops crop_image_regions cuts are pictures of
// the reply the person can see: the final message shows all four with their
// own @imgN handles, the Agent reads each handle beside its label and size
// with the Schema's note that only what the person sees is delivered,
// find_history_images lists the new handles in the same turn, and a later
// tool's reference_handles resolve them to the crop pictures.
func TestConversationCropsArePicturesOfTheReplyWithHandles(t *testing.T) {
	rig := newCropRig(t, []agents.Completion{
		{ToolCalls: []agents.ToolCall{cropSheetCall()}},
		{ToolCalls: []agents.ToolCall{lookupCall("find-1", `{}`)}},
		{ToolCalls: []agents.ToolCall{{ID: "refine-1", Name: "refine_image", Arguments: []byte(
			`{"prompt":"clean front view","reference_handles":["@img2"]}`)}}},
		{Content: "四张裁图是 @img2 @img3 @img4 @img5。"},
	})
	final := rig.process(t)

	wantURLs := []string{"/api/v1/assets/crop_1", "/api/v1/assets/crop_2", "/api/v1/assets/crop_3", "/api/v1/assets/crop_4"}
	if !reflect.DeepEqual(final.ImageURLs, wantURLs) {
		t.Fatalf("final image_urls = %v, want the four crops", final.ImageURLs)
	}
	var handles []string
	for index, attachment := range final.Attachments {
		if attachment.Kind != chat.AttachmentKindImage || attachment.Role != chat.AttachmentRoleCrop ||
			attachment.AssetID != fmt.Sprintf("crop_%d", index+1) || attachment.ImageSlot != index+1 {
			t.Fatalf("attachment %d = %+v", index, attachment)
		}
		handles = append(handles, attachment.Handle)
	}
	if !reflect.DeepEqual(handles, []string{"@img2", "@img3", "@img4", "@img5"}) {
		t.Fatalf("final handles = %v", handles)
	}

	var result struct {
		Success bool     `json:"success"`
		Handles []string `json:"handles"`
		Note    string   `json:"note"`
		Crops   []struct {
			Handle       string `json:"handle"`
			Label        string `json:"label"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			InputAssetID string `json:"input_asset_id"`
		} `json:"crops"`
	}
	told := toolReply(t, rig.schema, rig.agent, 1)
	if err := json.Unmarshal([]byte(told), &result); err != nil {
		t.Fatalf("crop result %q: %v", told, err)
	}
	if !result.Success || !reflect.DeepEqual(result.Handles, handles) || len(result.Crops) != 4 {
		t.Fatalf("crop result = %s", told)
	}
	for index, label := range []string{"正面", "侧面", "背面", "四分之三"} {
		crop := result.Crops[index]
		if crop.Handle != handles[index] || crop.Label != label || crop.Width != 512 || crop.Height != 2048 ||
			crop.InputAssetID != fmt.Sprintf("crop_%d", index+1) {
			t.Fatalf("crop %d = %+v", index, crop)
		}
	}
	if want := strings.ReplaceAll(rig.schema.ImageCrop.Texts.PresentedNote, "{count}", "4"); want == "" || result.Note != want {
		t.Fatalf("crop note = %q, want the Schema's presented note %q", result.Note, want)
	}

	listed := toolReply(t, rig.schema, rig.agent, 2)
	for _, handle := range handles {
		if !strings.Contains(listed, `"`+handle+`"`) {
			t.Fatalf("find_history_images does not list %s: %s", handle, listed)
		}
	}
	if !reflect.DeepEqual(rig.resolver.urls, []string{"/api/v1/assets/crop_1"}) {
		t.Fatalf("@img2 resolved to %v, want the first crop", rig.resolver.urls)
	}
}

// #552: a crop the platform cannot show the person is no delivery. When not
// every crop becomes a handled picture of the reply, the Agent reads the
// Schema's not-presented refusal instead of a success with asset ids.
func TestConversationCropsThatCannotAllBeShownAreAFailedCall(t *testing.T) {
	rig := newCropRig(t, []agents.Completion{
		{ToolCalls: []agents.ToolCall{cropSheetCall()}},
		{Content: "裁图没能展示给你。"},
	}, "crop_3")
	rig.process(t)

	var result struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Error   string `json:"error"`
	}
	told := toolReply(t, rig.schema, rig.agent, 1)
	if err := json.Unmarshal([]byte(told), &result); err != nil {
		t.Fatalf("crop result %q: %v", told, err)
	}
	prefix, _, _ := strings.Cut(rig.schema.ImageCrop.Texts.NotPresented, "{detail}")
	if result.Success || result.Code != "not_presented" || prefix == "" || !strings.HasPrefix(result.Error, prefix) {
		t.Fatalf("crop result = %s, want the not-presented refusal", told)
	}
	if strings.Contains(told, `"crops"`) || strings.Contains(told, `"handles"`) {
		t.Fatalf("a refused crop result still reports crops or handles: %s", told)
	}
}
