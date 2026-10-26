package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
)

// lookupRig wires a scripted Agent to the real Agent tools: the history image
// lookup and the shipped refine_image tool, whose submission lands in a fake
// generation gateway. Nothing paid or remote is called.
type lookupRig struct {
	schema       catalog.ChatSchema
	repository   *memoryChatRepository
	service      *chat.Service
	agent        *scriptedAgent
	gateway      *lookupGateway
	resolver     *lookupReferences
	orchestrator *chat.Orchestrator
	inlined      []string
}

type lookupGateway struct {
	offering  generation.Offering
	submitted []generation.SubmitCommand
}

func (gateway *lookupGateway) Offerings(generation.Capability) []generation.Offering {
	return []generation.Offering{gateway.offering}
}

func (*lookupGateway) OfferingAvailable(generation.OfferingID) bool { return true }

func (gateway *lookupGateway) Submit(_ context.Context, command generation.SubmitCommand) (generation.Accepted, error) {
	gateway.submitted = append(gateway.submitted, command)
	return generation.Accepted{TaskID: "task-new"}, nil
}

func (*lookupGateway) Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error) {
	return generation.Task{ID: "task-new", State: generation.TaskRunning}, nil
}

type lookupReferences struct{ urls []string }

func (resolver *lookupReferences) Resolve(_ context.Context, _ generation.OwnerID, urls []string) ([]generation.AssetID, error) {
	resolver.urls = append(resolver.urls, urls...)
	assets := make([]generation.AssetID, len(urls))
	for index := range urls {
		assets[index] = generation.AssetID(fmt.Sprintf("asset-%d", index))
	}
	return assets, nil
}

func newLookupRig(t *testing.T, responses []agents.Completion, adjust func(*catalog.ChatSchema)) *lookupRig {
	t.Helper()
	rig := &lookupRig{repository: newMemoryChatRepository(), agent: &scriptedAgent{responses: responses}}
	rig.service = newChatService(t, rig.repository)
	rig.schema = configuredChatSchema(t)
	rig.schema.Agent.Vision = true
	rig.schema.Context.TextPartKind = "configured-text-part"
	rig.schema.Context.ImagePartKind = "configured-image-part"
	if adjust != nil {
		adjust(&rig.schema)
	}
	refine := rig.schema.Tools[0]
	rig.gateway = &lookupGateway{offering: generation.Offering{
		ID: "configured/edit", ModelID: "configured-model", Capability: refine.Capability, Enabled: true,
		PollInterval: time.Second, Timeout: 5 * time.Second,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "images", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 4},
		},
	}}
	rig.resolver = &lookupReferences{}
	generator, err := generationtool.New(generationtool.Config{
		Gateway: rig.gateway, References: rig.resolver, Chat: rig.schema,
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := agenttools.New(agenttools.Config{
		Context: rig.schema.Context, HistoryLookup: chat.NewHistoryLookupPolicy(rig.schema),
		Params: agentparams.NewStore(), Generate: generator,
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.orchestrator, err = chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: rig.service, Schema: rig.schema, Model: "configured-model", Agent: rig.agent, Tools: tools,
		VisionInline: func(_ context.Context, owner generation.OwnerID, url string) (string, bool) {
			rig.inlined = append(rig.inlined, string(owner)+" "+url)
			if strings.Contains(url, "gone") {
				return "", false
			}
			return "data:image/png;base64," + strings.NewReplacer("/", "_").Replace(url), true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rig
}

// seed stores a session's earlier messages and starts the turn.
func (rig *lookupRig) seed(t *testing.T, history []chat.Message, request string) (chat.Session, chat.Message) {
	t.Helper()
	ctx := context.Background()
	session, err := rig.service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	rig.repository.mu.Lock()
	for index := range history {
		history[index].SessionID = session.ID
		history[index].ID = fmt.Sprintf("%s-seed-%d", session.ID, index)
	}
	rig.repository.messages[session.ID] = history
	rig.repository.mu.Unlock()
	if request == "" {
		return session, chat.Message{}
	}
	assistant, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: request,
		Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "configured-model"}})
	if err != nil {
		t.Fatal(err)
	}
	return session, assistant
}

func (rig *lookupRig) process(t *testing.T, session chat.Session, assistant chat.Message) chat.Message {
	t.Helper()
	ctx := context.Background()
	if err := rig.orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "configured-model"}}); err != nil {
		t.Fatal(err)
	}
	messages, err := rig.service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return messages[len(messages)-1]
}

// gunmanHistory is the #279 scenario: the adjusted pose of the white-clad
// gunman (@img2, generated) and an outfit upload (@img3), followed by more
// than a model window of chat, so neither picture is in the Agent's context
// any more. A third, similar gunman (@img1) makes "the gunman" alone unclear.
func gunmanHistory(schema catalog.ChatSchema) []chat.Message {
	history := []chat.Message{
		{Role: schema.Roles.User, Content: "画一个白衣持枪的人"},
		{Role: schema.Roles.Assistant, ImageURLs: []string{"/api/v1/tasks/t1/assets/first"},
			ToolCalls:   []chat.ToolCall{{Name: "refine_image", TaskID: "t1", Arguments: []byte(`{"prompt":"a man in white holding a gun"}`)}},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "t1", AssetID: "first", ImageSlot: 1}}},
		{Role: schema.Roles.User, Content: "调整一下他的姿势，双手举枪"},
		{Role: schema.Roles.Assistant, ImageURLs: []string{"/api/v1/tasks/t2/assets/pose"},
			ToolCalls:   []chat.ToolCall{{Name: "refine_image", TaskID: "t2", Arguments: []byte(`{"prompt":"same man in white, now aiming with both hands"}`)}},
			Attachments: []chat.Attachment{{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "t2", AssetID: "pose", ImageSlot: 1}}},
		{Role: schema.Roles.User, Content: "这件盔甲是服装参考", ImageURLs: []string{"/api/v1/input-assets/armor"},
			Attachments: []chat.Attachment{{Handle: "@img3", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "armor", ImageSlot: 1}}},
	}
	for index := range 24 {
		role := schema.Roles.User
		if index%2 == 1 {
			role = schema.Roles.Assistant
		}
		history = append(history, chat.Message{Role: role, Content: fmt.Sprintf("chat about lighting %d", index)})
	}
	return history
}

func lookupCall(id, arguments string) agents.ToolCall {
	return agents.ToolCall{ID: id, Name: agenttools.ToolFindHistoryImages, Arguments: []byte(arguments)}
}

// #279, unambiguous referents: the Agent finds the pose and outfit pictures
// in this session's history although they left its context, looks at them,
// and adds both as references with their purposes, in the same turn and
// without another confirmation. The frozen generation input has the two
// pictures in order and a prompt that says which one is for what.
func TestOrchestratorFindsHistoryPicturesAndFreezesThemWithTheirPurposes(t *testing.T) {
	rig := newLookupRig(t, []agents.Completion{
		{ToolCalls: []agents.ToolCall{lookupCall("find-1", `{}`)}},
		{ToolCalls: []agents.ToolCall{lookupCall("find-2", `{"handles":["@img2","@img3"]}`)}},
		{Content: "姿势取自 @img2，服装取自 @img3。", ToolCalls: []agents.ToolCall{{ID: "refine-1", Name: "refine_image", Arguments: []byte(
			`{"prompt":"the man in white in armor","reference_handles":["@img2","@img3"],` +
				`"reference_purposes":{"@img3":"take the armor outfit from this image","@img2":"keep this man and his two-handed aiming pose"}}`)}}},
	}, nil)
	// Another session of the same owner has a picture under the same handle.
	rig.seed(t, []chat.Message{{Role: rig.schema.Roles.User, Content: "elsewhere", ImageURLs: []string{"/api/v1/input-assets/elsewhere"},
		Attachments: []chat.Attachment{{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "elsewhere", ImageSlot: 1}}}}, "")
	session, assistant := rig.seed(t, gunmanHistory(rig.schema), "保留白衣持枪图调整后的姿势，参考另一张图换衣服")
	rig.process(t, session, assistant)

	if len(rig.agent.requests) != 3 {
		t.Fatalf("agent requests = %d", len(rig.agent.requests))
	}
	// The pictures are outside the model window of the first request.
	for _, message := range rig.agent.requests[0].Messages[1:] {
		if strings.Contains(message.Content, "@img2") || strings.Contains(message.Content, "@img3") {
			t.Fatalf("test history must push the pictures out of the window: %q", message.Content)
		}
	}
	listing := rig.agent.requests[1].Messages[len(rig.agent.requests[1].Messages)-1]
	var listed chat.HistoryLookupResult
	if listing.Role != rig.schema.Roles.Tool || json.Unmarshal([]byte(listing.Content), &listed) != nil {
		t.Fatalf("listing reply = %+v", listing)
	}
	if got := handlesOf(listed.Images); !slices.Equal(got, []string{"@img3", "@img2", "@img1"}) ||
		listed.Images[1].Prompt != "same man in white, now aiming with both hands" || listed.Images[0].UserText != "这件盔甲是服装参考" {
		t.Fatalf("listed = %+v", listed.Images)
	}

	// Looking at two pictures: the tool reply stays text, the pictures follow
	// it in one message, each read for this owner and labelled by handle.
	view := rig.agent.requests[2].Messages
	reply, pictures := view[len(view)-2], view[len(view)-1]
	if reply.Role != rig.schema.Roles.Tool || len(reply.Parts) != 0 || !strings.Contains(reply.Content, `"picture_requested":true`) {
		t.Fatalf("view reply = %+v", reply)
	}
	if pictures.Role != rig.schema.Roles.User || len(pictures.Parts) != 5 || pictures.Parts[0].Text != rig.schema.Context.HistoryImageViewIntro {
		t.Fatalf("pictures message = %+v", pictures)
	}
	if pictures.Parts[1].URL != "data:image/png;base64,_api_v1_tasks_t2_assets_pose" ||
		pictures.Parts[2].Text != strings.ReplaceAll(rig.schema.Context.HistoryImageViewLabelTemplate, "{handle}", "@img2") ||
		pictures.Parts[3].URL != "data:image/png;base64,_api_v1_input-assets_armor" {
		t.Fatalf("pictures = %+v", pictures.Parts)
	}
	for _, read := range rig.inlined {
		if !strings.HasPrefix(read, "owner-a ") || strings.Contains(read, "elsewhere") {
			t.Fatalf("a picture was read outside the turn's owner and session: %q", read)
		}
	}

	// The same turn submitted the generation with both pictures and purposes.
	if len(rig.gateway.submitted) != 1 {
		t.Fatalf("submissions = %d", len(rig.gateway.submitted))
	}
	if !slices.Equal(rig.resolver.urls, []string{"/api/v1/tasks/t2/assets/pose", "/api/v1/input-assets/armor"}) {
		t.Fatalf("frozen references = %v", rig.resolver.urls)
	}
	var input map[string]any
	if err := json.Unmarshal(rig.gateway.submitted[0].Input, &input); err != nil {
		t.Fatal(err)
	}
	refine := rig.schema.Tools[0]
	item := func(position, purpose string) string {
		return strings.NewReplacer("{position}", position, "{purpose}", purpose).Replace(refine.ReferencePurposeItemTemplate)
	}
	want := strings.NewReplacer(
		"{purposes}", item("1", "keep this man and his two-handed aiming pose")+item("2", "take the armor outfit from this image"),
		"{prompt}", "the man in white in armor").Replace(refine.PromptWithReferencePurposesTemplate)
	if input["prompt"] != want {
		t.Fatalf("frozen prompt = %q\nwant           %q", input["prompt"], want)
	}
}

// #279, unclear referents: when the lookup leaves two similar candidates, the
// Agent asks with the candidates instead of guessing, and nothing is
// generated. An unreadable picture is reported as unreadable, apart from the
// question of meaning.
func TestOrchestratorAsksInsteadOfGeneratingWhenTheLookupStaysAmbiguous(t *testing.T) {
	question := "有两张白衣持枪图：@img1（初版）和 @img2（双手举枪），要保留哪一张的姿势？"
	rig := newLookupRig(t, []agents.Completion{
		{ToolCalls: []agents.ToolCall{lookupCall("find-1", `{"handles":["@img1","@img2"]}`)}},
		{Content: question},
	}, nil)
	history := gunmanHistory(rig.schema)
	history[3].ImageURLs[0] = "/api/v1/tasks/t2/assets/gone"
	session, assistant := rig.seed(t, history, "用那张白衣持枪图的姿势")
	stored := rig.process(t, session, assistant)

	if len(rig.gateway.submitted) != 0 || len(rig.resolver.urls) != 0 {
		t.Fatalf("an unclear request was generated: %+v", rig.gateway.submitted)
	}
	if stored.Content != question || len(stored.ToolCalls) != 1 {
		t.Fatalf("stored reply = %+v", stored)
	}
	view := rig.agent.requests[1].Messages
	reply, pictures := view[len(view)-2], view[len(view)-1]
	if !strings.Contains(reply.Content, strings.ReplaceAll(rig.schema.Context.HistoryImageUnreadableTemplate, "{handle}", "@img2")) {
		t.Fatalf("the unreadable picture must be named: %q", reply.Content)
	}
	if len(pictures.Parts) != 3 || pictures.Parts[1].URL != "data:image/png;base64,_api_v1_tasks_t1_assets_first" {
		t.Fatalf("only the readable picture is shown: %+v", pictures.Parts)
	}
}

// The lookup calls count toward the reply's tool budget (#275) like any tool,
// and the history bound (#279) holds within one batch: the second call of a
// batch sees what the first one surfaced.
func TestHistoryLookupBudgetsInterplayWithTheReplyToolBudget(t *testing.T) {
	rig := newLookupRig(t, []agents.Completion{
		{ToolCalls: []agents.ToolCall{lookupCall("find-1", `{"handles":["@img2"]}`), lookupCall("find-2", `{"handles":["@img3"]}`)}},
		{ToolCalls: []agents.ToolCall{lookupCall("find-3", `{}`), {ID: "refine-1", Name: "refine_image",
			Arguments: []byte(`{"prompt":"p","reference_handles":["@img2"]}`)}}},
		{Content: "summary"},
	}, func(schema *catalog.ChatSchema) {
		schema.Context.HistoryImageLookupLimit = 1
		schema.Agent.MaxToolCallsPerReply = 3
	})
	session, assistant := rig.seed(t, gunmanHistory(rig.schema), "姿势和衣服")
	stored := rig.process(t, session, assistant)

	if len(stored.ToolCalls) != 3 || len(rig.gateway.submitted) != 0 {
		t.Fatalf("the generation beyond the reply budget ran: calls=%d submitted=%d", len(stored.ToolCalls), len(rig.gateway.submitted))
	}
	// Call IDs are renumbered per turn; the replies keep the call order.
	var replies []chat.HistoryLookupResult
	for _, message := range rig.agent.requests[len(rig.agent.requests)-1].Messages {
		if message.Role != rig.schema.Roles.Tool {
			continue
		}
		var result chat.HistoryLookupResult
		if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
			t.Fatalf("tool reply %q: %v", message.Content, err)
		}
		replies = append(replies, result)
	}
	if len(replies) != 3 {
		t.Fatalf("tool replies = %+v", replies)
	}
	first, second, third := replies[0], replies[1], replies[2]
	if first.Used != 1 || first.Note == "" || len(first.Images) != 1 {
		t.Fatalf("first = %+v", first)
	}
	if !slices.Equal(second.OverBudget, []string{"@img3"}) || len(second.Images) != 0 {
		t.Fatalf("second = %+v", second)
	}
	if got := handlesOf(third.Images); !slices.Equal(got, []string{"@img2"}) || !third.OlderNotListed {
		t.Fatalf("third = %+v", third)
	}
}

// The shipped Schema offers the lookup and replaces the old "ask the user to
// point again at older pictures" rule with the find / add / ask rule.
func TestShippedPromptTellsTheAgentToFindHistoryPicturesOrAsk(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	prompt := product.Application().Chat.Agent.SystemPrompt
	for _, want := range []string{"find_history_images", "reference_purposes", "不要猜、不要生成"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "需要时让用户重新指定") {
		t.Fatal("the old re-specify rule is still in the system prompt")
	}
}
