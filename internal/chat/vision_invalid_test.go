package chat_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// The session's remembered refused pictures (#522) in the in-memory repository
// the chat tests share.
func (r *memoryChatRepository) VisionExclusions(
	_ context.Context, ownerID, sessionID string,
) ([]string, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return nil, false, nil
	}
	return append([]string{}, r.visionExcluded[sessionID]...), true, nil
}

func (r *memoryChatRepository) UpdateVisionExclusions(
	_ context.Context, ownerID, sessionID string, change func([]string) []string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return false, nil
	}
	if r.visionExcluded == nil {
		r.visionExcluded = map[string][]string{}
	}
	r.visionExcluded[sessionID] = change(append([]string{}, r.visionExcluded[sessionID]...))
	return true, nil
}

// pngDataURL is a real PNG of the given size as a data URI; seed makes two
// pictures of one size differ.
func pngDataURL(t *testing.T, width, height int, seed uint8) string {
	t.Helper()
	picture := image.NewGray(image.Rect(0, 0, width, height))
	picture.SetGray(0, 0, color.Gray{Y: seed})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

// refusingPictureAgent answers a request whose pictures include one that refuse
// names with that error, and every other request with a reply. Every Stream
// call is recorded; the first-turn title is answered apart.
type refusingPictureAgent struct {
	mu       sync.Mutex
	refuse   func(url string) error
	requests []agents.Request
}

func (agent *refusingPictureAgent) Stream(_ context.Context, request agents.Request, _ func(agents.Delta) error) (agents.Completion, error) {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.requests = append(agent.requests, request)
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.URL == "" {
				continue
			}
			if err := agent.refuse(part.URL); err != nil {
				return agents.Completion{}, err
			}
		}
	}
	return agents.Completion{Content: "configured reply"}, nil
}

func (agent *refusingPictureAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "title"}, nil
}

func (agent *refusingPictureAgent) snapshot() []agents.Request {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return append([]agents.Request(nil), agent.requests...)
}

func invalidPictureURLs(request agents.Request) []string {
	var urls []string
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.URL != "" {
				urls = append(urls, part.URL)
			}
		}
	}
	return urls
}

func invalidRequestText(request agents.Request) string {
	var builder strings.Builder
	for _, message := range request.Messages {
		builder.WriteString(message.Content)
		for _, part := range message.Parts {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func imageInvalidSchema(t *testing.T) catalog.ChatSchema {
	t.Helper()
	schema := configuredChatSchema(t)
	schema.Agent.InvalidImageNote = "configured invalid note"
	schema.Agent.ImagesUnsupportedNote = "configured no-vision note"
	schema.Presentation.Messages.AgentImageInvalidReasons = " reasons: {reasons}"
	schema.Presentation.Messages.AgentImageInvalidReasonLabels = map[string]string{"dimensions": "dims"}
	schema.Presentation.Messages.AgentImageInvalidReasonSeparator = ","
	if schema.Context.Vision == nil {
		t.Fatal("the default Schema declares no context.vision")
	}
	vision := *schema.Context.Vision
	vision.InvalidPicture = catalog.ChatVisionInvalidPictureSchema{
		MinimumEdgePixels: 33, MaximumAspectRatio: 200, MaximumExcluded: 3,
		SkippedTemplate: " configured skipped: {handles}",
	}
	schema.Context.Vision = &vision
	return schema
}

type invalidPictureHarness struct {
	t            *testing.T
	repository   *memoryChatRepository
	service      *chat.Service
	orchestrator *chat.Orchestrator
	agent        *refusingPictureAgent
	session      chat.Session
}

func newInvalidPictureHarness(t *testing.T, schema catalog.ChatSchema, refuse func(string) error) *invalidPictureHarness {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	agent := &refusingPictureAgent{refuse: refuse}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	return &invalidPictureHarness{t: t, repository: repository, service: service, orchestrator: orchestrator, agent: agent, session: session}
}

// turn sends one user message and runs the reply; it returns the reply.
func (harness *invalidPictureHarness) turn(content string, images ...string) chat.Message {
	harness.t.Helper()
	ctx := context.Background()
	assistant, err := harness.service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: harness.session.ID, Content: content, ImageURLs: images,
	})
	if err != nil {
		harness.t.Fatal(err)
	}
	if err := harness.orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: harness.session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		harness.t.Fatal(err)
	}
	harness.orchestrator.WaitForTitles()
	messages, err := harness.service.ListMessages(ctx, "owner-a", harness.session.ID)
	if err != nil {
		harness.t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == assistant.ID {
			return message
		}
	}
	harness.t.Fatalf("assistant %s not found", assistant.ID)
	return chat.Message{}
}

func (harness *invalidPictureHarness) remembered() []string {
	harness.t.Helper()
	keys, _, err := harness.repository.VisionExclusions(context.Background(), "owner-a", harness.session.ID)
	if err != nil {
		harness.t.Fatal(err)
	}
	return keys
}

// #522: a session whose picture the upstream refuses is not blocked. The first
// request is refused as a bad picture and sent again without it, the session
// remembers the picture, and no later turn carries it: not one more request
// holds it, so the upstream is refused no more than once.
func TestInvalidPictureIsStrippedOnceAndSkippedByLaterTurns(t *testing.T) {
	bad := pngDataURL(t, 64, 64, 1)
	harness := newInvalidPictureHarness(t, imageInvalidSchema(t), func(url string) error {
		if url == bad {
			return errors.Join(&agents.ImageInvalidity{Reasons: []string{"dimensions"}}, &agents.StatusError{Status: 400})
		}
		return nil
	})

	reply := harness.turn("look at this", bad)
	if reply.Content != "configured reply" {
		t.Fatalf("first reply = %q, want the retried request to answer", reply.Content)
	}
	requests := harness.agent.snapshot()
	if len(requests) != 2 || len(invalidPictureURLs(requests[0])) != 1 || len(invalidPictureURLs(requests[1])) != 0 {
		t.Fatalf("requests = %d, pictures = %v / %v, want the refused one and its picture-free retry",
			len(requests), len(invalidPictureURLs(requests[0])), len(invalidPictureURLs(requests[len(requests)-1])))
	}
	if text := invalidRequestText(requests[1]); !strings.Contains(text, "configured invalid note reasons: dims") {
		t.Fatalf("retried request lacks the note with the reasons: %q", text)
	}
	if len(harness.remembered()) != 1 {
		t.Fatalf("remembered = %v, want the one refused picture", harness.remembered())
	}

	reply = harness.turn("and now make it red")
	if reply.Content != "configured reply" {
		t.Fatalf("second reply = %q", reply.Content)
	}
	requests = harness.agent.snapshot()
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want the second turn to need one request", len(requests))
	}
	if len(invalidPictureURLs(requests[2])) != 0 {
		t.Fatal("the later turn sent the refused picture again")
	}
	if !strings.Contains(invalidRequestText(requests[2]), "configured skipped:") {
		t.Fatalf("the later turn does not say a picture was skipped: %q", invalidRequestText(requests[2]))
	}
}

// #522: the remembered refusal outlives the orchestrator (a restart): a new
// orchestrator on the same repository still skips the picture.
func TestRefusedPictureStaysSkippedAfterARestart(t *testing.T) {
	bad := pngDataURL(t, 64, 64, 2)
	refuse := func(url string) error {
		if url == bad {
			return errors.Join(&agents.ImageInvalidity{Reasons: []string{"dimensions"}}, &agents.StatusError{Status: 400})
		}
		return nil
	}
	harness := newInvalidPictureHarness(t, imageInvalidSchema(t), refuse)
	harness.turn("look at this", bad)

	restarted := &refusingPictureAgent{refuse: refuse}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: harness.service, Schema: imageInvalidSchema(t), Model: "configured-model", Agent: restarted, Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.orchestrator, harness.agent = orchestrator, restarted
	harness.turn("one more thing")
	requests := restarted.snapshot()
	if len(requests) != 1 || len(invalidPictureURLs(requests[0])) != 0 {
		t.Fatalf("after a restart: requests = %d with pictures %v, want one request without the picture",
			len(requests), len(invalidPictureURLs(requests[0])))
	}
}

// #522: only a refusal of the picture itself strips pictures. A refused tool
// declaration, or any other 400, fails as before: nothing is stripped or
// remembered.
func TestOtherRefusalsDoNotStripPictures(t *testing.T) {
	picture := pngDataURL(t, 64, 64, 3)
	for name, refusal := range map[string]error{
		"tool declaration": errors.Join(&agents.RequestRejection{Param: "tools[0].function.parameters"}, &agents.StatusError{Status: 400}),
		"plain 400":        &agents.StatusError{Status: 400},
		"too long":         &agents.StatusError{Status: 413},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newInvalidPictureHarness(t, imageInvalidSchema(t), func(string) error { return refusal })
			reply := harness.turn("look at this", picture)
			if reply.Content == "configured reply" {
				t.Fatalf("reply = %q, want the failure", reply.Content)
			}
			if requests := harness.agent.snapshot(); len(requests) != 1 || len(invalidPictureURLs(requests[0])) != 1 {
				t.Fatalf("requests = %d: the refusal must not be retried without pictures", len(requests))
			}
			if len(harness.remembered()) != 0 {
				t.Fatalf("remembered = %v, want nothing", harness.remembered())
			}
		})
	}
}

// #522: a one-pixel strip never reaches the upstream: it is skipped before the
// request, so even an upstream that would refuse it is not asked, and the
// picture beside it still travels. The Agent is told which was skipped.
func TestTooSmallPictureIsSkippedBeforeTheRequest(t *testing.T) {
	strip := pngDataURL(t, 300, 1, 4)
	good := pngDataURL(t, 64, 64, 5)
	harness := newInvalidPictureHarness(t, imageInvalidSchema(t), func(url string) error {
		if url == strip {
			return errors.Join(&agents.ImageInvalidity{Reasons: []string{"dimensions"}}, &agents.StatusError{Status: 400})
		}
		return nil
	})
	reply := harness.turn("two pictures", strip, good)
	if reply.Content != "configured reply" {
		t.Fatalf("reply = %q", reply.Content)
	}
	requests := harness.agent.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want one: the strip must not be sent", len(requests))
	}
	if urls := invalidPictureURLs(requests[0]); len(urls) != 1 || urls[0] != good {
		t.Fatalf("pictures sent = %d, want only the 64x64 one", len(urls))
	}
	if !strings.Contains(invalidRequestText(requests[0]), "configured skipped:") {
		t.Fatalf("the Agent is not told a picture was skipped: %q", invalidRequestText(requests[0]))
	}
	if len(harness.remembered()) != 0 {
		t.Fatal("a picture skipped by size is not a refusal to remember")
	}
}

// #522: a ratio beyond the Schema's bound is skipped too, and a picture whose
// bytes cannot be read here is never judged (the upstream decides).
func TestPictureShapeBoundsComeFromTheSchema(t *testing.T) {
	cases := map[string]struct {
		url     string
		skipped bool
	}{
		"extreme ratio":       {pngDataURL(t, 10000, 40, 6), true},
		"within the ratio":    {pngDataURL(t, 4000, 40, 7), false},
		"undecodable payload": {"data:image/png;base64,Y29uZmlndXJlZA==", false},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			harness := newInvalidPictureHarness(t, imageInvalidSchema(t), func(string) error { return nil })
			harness.turn("look", test.url)
			requests := harness.agent.snapshot()
			sent := len(invalidPictureURLs(requests[0])) == 1
			if sent == test.skipped {
				t.Fatalf("picture sent = %v, want skipped = %v", sent, test.skipped)
			}
		})
	}
}

// #522: a model that takes no picture input is answered without the pictures
// once, and that is not remembered: the person may pick a model that reads
// them, and the pictures must come back.
func TestModelWithoutPictureInputIsRetriedButNotRemembered(t *testing.T) {
	picture := pngDataURL(t, 64, 64, 8)
	refusing := true
	var mu sync.Mutex
	harness := newInvalidPictureHarness(t, imageInvalidSchema(t), func(string) error {
		mu.Lock()
		defer mu.Unlock()
		if refusing {
			return errors.Join(agents.ErrImagesRejected, &agents.StatusError{Status: 400})
		}
		return nil
	})
	reply := harness.turn("look", picture)
	if reply.Content != "configured reply" {
		t.Fatalf("reply = %q, want the picture-free retry to answer", reply.Content)
	}
	requests := harness.agent.snapshot()
	if len(requests) != 2 || len(invalidPictureURLs(requests[1])) != 0 ||
		!strings.Contains(invalidRequestText(requests[1]), "configured no-vision note") {
		t.Fatalf("requests = %d, retry = %q", len(requests), invalidRequestText(requests[len(requests)-1]))
	}
	if len(harness.remembered()) != 0 {
		t.Fatalf("remembered = %v, want nothing", harness.remembered())
	}
	mu.Lock()
	refusing = false
	mu.Unlock()
	harness.turn("again")
	requests = harness.agent.snapshot()
	if len(invalidPictureURLs(requests[len(requests)-1])) != 1 {
		t.Fatal("the picture did not come back for a model that reads it")
	}
}

// #522: a Schema without the notes keeps the earlier behavior for both
// refusals: the turn fails with the Schema's text.
func TestWithoutNotesTheRefusalsFailAsBefore(t *testing.T) {
	picture := pngDataURL(t, 64, 64, 9)
	schema := imageInvalidSchema(t)
	schema.Agent.InvalidImageNote = ""
	schema.Agent.ImagesUnsupportedNote = ""
	schema.Presentation.Messages.AgentImageInvalid = "configured invalid text"
	for name, refusal := range map[string]error{
		"invalid":     errors.Join(&agents.ImageInvalidity{Reasons: []string{"dimensions"}}, &agents.StatusError{Status: 400}),
		"unsupported": errors.Join(agents.ErrImagesRejected, &agents.StatusError{Status: 400}),
	} {
		t.Run(name, func(t *testing.T) {
			harness := newInvalidPictureHarness(t, schema, func(string) error { return refusal })
			reply := harness.turn("look", picture)
			if reply.Content == "configured reply" {
				t.Fatalf("reply = %q, want the failure", reply.Content)
			}
			if requests := harness.agent.snapshot(); len(requests) != 1 {
				t.Fatalf("requests = %d, want no retry", len(requests))
			}
		})
	}
}

// #522: what a session remembers is bounded by the Schema, newest kept.
func TestRememberedRefusalsAreBoundedBySchema(t *testing.T) {
	harness := newInvalidPictureHarness(t, imageInvalidSchema(t), func(string) error { return nil })
	ctx := context.Background()
	for _, keys := range [][]string{{"a", "b"}, {"c", "d"}, {"b"}} {
		if _, err := harness.service.ExcludeVisionPictures(ctx, "owner-a", harness.session.ID, keys, 3); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(harness.remembered(), ","); got != "c,d,b" {
		t.Fatalf("remembered = %q, want the newest three without repeats (c,d,b)", got)
	}
	if stored, err := harness.service.ExcludeVisionPictures(ctx, "owner-b", harness.session.ID, []string{"x"}, 3); err != nil || stored {
		t.Fatalf("a foreign owner stored = %v, err = %v", stored, err)
	}
}

// toolLoopAgent calls the tool on its first two replies and ends on the third;
// a request that carries a picture from the second reply on is refused as a
// bad picture (#522).
type toolLoopAgent struct {
	mu       sync.Mutex
	tool     string
	requests []agents.Request
	replies  int
}

func (agent *toolLoopAgent) Stream(_ context.Context, request agents.Request, _ func(agents.Delta) error) (agents.Completion, error) {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.requests = append(agent.requests, request)
	if agent.replies >= 1 && len(invalidPictureURLs(request)) > 0 {
		return agents.Completion{}, errors.Join(&agents.ImageInvalidity{Reasons: []string{"dimensions"}}, &agents.StatusError{Status: 400})
	}
	agent.replies++
	if agent.replies <= 2 {
		return agents.Completion{ToolCalls: []agents.ToolCall{{
			ID: "call-" + string(rune('0'+agent.replies)), Name: agent.tool, Arguments: []byte(`{"prompt":"p"}`),
		}}}, nil
	}
	return agents.Completion{Content: "configured final"}, nil
}

func (agent *toolLoopAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "title"}, nil
}

type okToolExecutor struct{}

func (okToolExecutor) Execute(context.Context, chat.ToolCommand, func(chat.ToolObservation) error) (chat.ToolResult, error) {
	return chat.ToolResult{TaskID: "task-ok", AgentContent: `{"success":true}`}, nil
}

// #522: a refusal in the middle of a tool loop strips the picture for the rest
// of the reply too: the round after the retry does not send it again, so the
// upstream refuses it exactly once.
func TestPictureRefusedInAToolRoundStaysStrippedForTheRestOfTheReply(t *testing.T) {
	schema := imageInvalidSchema(t)
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	agent := &toolLoopAgent{tool: schema.Tools[0].Name}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: okToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
		Content: "look and make", ImageURLs: []string{pngDataURL(t, 64, 64, 10)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	orchestrator.WaitForTitles()
	agent.mu.Lock()
	defer agent.mu.Unlock()
	// Requests: first reply, refused follow-up, its retry, the round after it.
	if len(agent.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(agent.requests))
	}
	for index, want := range []int{1, 1, 0, 0} {
		if got := len(invalidPictureURLs(agent.requests[index])); got != want {
			t.Fatalf("request %d carries %d pictures, want %d", index, got, want)
		}
	}
	if !strings.Contains(invalidRequestText(agent.requests[3]), "configured invalid note") {
		t.Fatal("the later round lost the note that says the picture was skipped")
	}
}
