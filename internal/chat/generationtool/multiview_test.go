package generationtool_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const multiviewOffering = "tripo/tripo-v3.1-multiview/image-to-3d"

// countingReferences gives every resolved picture its own asset, so the
// frozen input keeps one asset per view.
type countingReferences struct{ urls []string }

func (resolver *countingReferences) Resolve(_ context.Context, _ references.ReferenceScope, urls []string) ([]generation.AssetID, error) {
	result := make([]generation.AssetID, 0, len(urls))
	for _, url := range urls {
		resolver.urls = append(resolver.urls, url)
		result = append(result, generation.AssetID("asset-for-"+url))
	}
	return result, nil
}

type multiviewRun struct {
	result   chat.ToolResult
	err      error
	resolver *countingReferences
	gateway  *fakeGateway
	content  map[string]any
	input    map[string]any
	chat     catalog.ChatSchema
}

// multiviewScenario is what a run adds to the shipped one-Offering harness
// (#373): the session's explicit 3D lock, further Offerings of the shipped
// Catalog the gateway also serves, and those without runtime configuration.
type multiviewScenario struct {
	locked      string
	also        []string
	unavailable []string
}

const (
	rodinImageTo3D = "hyper3d/rodin-gen-2.5/image-to-3d"
	p2Multiview    = "tripo/tripo-p2-multiview/image-to-3d"
)

// runMultiview runs the shipped multiview tool on the shipped multiview
// Offering, with the given group views, arguments and structured request.
func runMultiview(t *testing.T, views *fakeThreeViews, arguments string, request *chat.MultiviewRequest) multiviewRun {
	t.Helper()
	return runMultiviewIn(t, multiviewScenario{}, views, arguments, request)
}

// runMultiviewIn is runMultiview inside a scenario.
func runMultiviewIn(t *testing.T, scenario multiviewScenario, views *fakeThreeViews, arguments string,
	request *chat.MultiviewRequest) multiviewRun {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == application.Chat.ThreeView.Multiview.Tool {
			tool = candidate
		}
	}
	offering, ok := product.Get(multiviewOffering)
	if tool.Name == "" || !ok {
		t.Fatalf("the shipped multiview tool %q or Offering is missing", application.Chat.ThreeView.Multiview.Tool)
	}
	// The direct submission path of this harness has no prepared authorization;
	// the frozen input is what these tests read.
	offering.NativePricing = nil
	offerings := []generation.Offering{offering}
	for _, id := range scenario.also {
		other, found := product.Get(generation.OfferingID(id))
		if !found {
			t.Fatalf("the shipped Catalog has no Offering %q", id)
		}
		// The harness submits directly, without a prepared authorization, so a
		// scenario Offering is quoted at its credits like the main one.
		other.NativePricing = nil
		offerings = append(offerings, other)
	}
	gateway := &fakeGateway{
		offerings: offerings, unavailable: map[generation.OfferingID]bool{},
		tasks: []generation.Task{{ID: "configured-task", State: generation.TaskSucceeded, Offering: offering,
			Assets: []generation.Asset{{ID: "model-thumb", Role: "thumbnail"}}}},
	}
	for _, id := range scenario.unavailable {
		gateway.unavailable[generation.OfferingID(id)] = true
	}
	resolver := &countingReferences{}
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: resolver, Chat: application.Chat, TaskStates: application.TaskStates,
		InputAssetRoute: application.Runtime.HTTP.Routes.AssetItem,
		Clock:           func() time.Time { return now },
		Sleep:           func(_ context.Context, duration time.Duration) error { now = now.Add(duration); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if views != nil {
		executor.SetThreeViews(views)
	}
	history := []chat.Message{{Role: application.Chat.Roles.User, Content: "make it 3D"}}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: tool, History: history,
		Call:    agents.ToolCall{ID: "call-views", Name: tool.Name, Arguments: []byte(arguments)},
		Options: chat.TurnOptions{Multiview: request, ExplicitModel3D: scenario.locked},
	}, func(chat.ToolObservation) error { return nil })
	run := multiviewRun{result: result, err: err, resolver: resolver, gateway: gateway, chat: application.Chat}
	if result.AgentContent != "" {
		_ = json.Unmarshal([]byte(result.AgentContent), &run.content)
	}
	if len(gateway.submitted.Input) > 0 {
		_ = json.Unmarshal(gateway.submitted.Input, &run.input)
	}
	return run
}

// groupOfFour is a group whose front and left were generated, whose back the
// person replaced with an upload, and which still holds a view of the time
// before views followed the multiview keys (side).
func groupOfFour() *fakeThreeViews {
	return &fakeThreeViews{groups: map[string][]generationtool.GroupView{
		"owner-a/session-a/run-1": {
			{View: "front", TaskID: "task-front", AssetID: "asset-front"},
			{View: "left", TaskID: "task-left", AssetID: "asset-left"},
			{View: "back", AssetID: "upload-back"},
			{View: "side", TaskID: "task-side", AssetID: "asset-side"},
		},
	}}
}

func TestMultiviewFromANamedGroupBindsTheGroupsViews(t *testing.T) {
	run := runMultiview(t, groupOfFour(), `{"model":"tripo-v3.1-multiview","three_view_group":"run-1",
		"reference_handles":["@img9","@img10"],"reference_views":["back","front"]}`, nil)
	if run.err != nil || run.result.NoSubmission || run.gateway.submitted.OfferingID != multiviewOffering {
		t.Fatalf("multiview from group: %+v %v", run.result, run.err)
	}
	want := []string{"/api/v1/tasks/task-front/assets/asset-front", "/api/v1/tasks/task-left/assets/asset-left", "/api/v1/assets/upload-back"}
	if !reflect.DeepEqual(run.resolver.urls, want) {
		t.Fatalf("the pictures came from %v, want the group's finished views %v", run.resolver.urls, want)
	}
	if got := run.input["reference_views"]; !reflect.DeepEqual(got, []any{"front", "left", "back"}) {
		t.Fatalf("reference_views = %v, want the group's view keys (the Agent's own views and the legacy side view dropped)", got)
	}
	note, _ := run.content["reference_note"].(string)
	if !strings.Contains(note, "正面 / 左侧 / 背面") {
		t.Fatalf("the result must say which views it was made from: %q", note)
	}
}

func TestMultiviewFromTheButtonMustMatchTheGroup(t *testing.T) {
	request := chat.MultiviewRequest{Group: "run-1", Model: "tripo-v3.1-multiview", Views: []chat.MultiviewView{
		{View: "front", TaskID: "task-front", AssetID: "asset-front"},
		{View: "back", AssetID: "upload-back"},
	}}
	// The Agent names another model and no group: the person's request decides.
	run := runMultiview(t, groupOfFour(), `{"model":"other"}`, &request)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("button request: %+v %v", run.result, run.err)
	}
	if got := run.input["reference_views"]; !reflect.DeepEqual(got, []any{"front", "back"}) {
		t.Fatalf("reference_views = %v, want only the views the person pressed the button with", got)
	}

	changed := request
	changed.Views = []chat.MultiviewView{{View: "front", TaskID: "task-front", AssetID: "asset-front"}, {View: "back", AssetID: "older-upload"}}
	stale := runMultiview(t, groupOfFour(), `{"model":"tripo-v3.1-multiview"}`, &changed)
	if stale.err != nil || !stale.result.NoSubmission || stale.gateway.submitted.OfferingID != "" ||
		!strings.Contains(stale.result.AgentContent, stale.chat.ThreeView.Messages.ViewsChanged) {
		t.Fatalf("a replaced view must refuse the step: %+v %v", stale.result, stale.err)
	}

	missing := request
	missing.Views = append(missing.Views, chat.MultiviewView{View: "right", TaskID: "task-right", AssetID: "asset-right"})
	gone := runMultiview(t, groupOfFour(), `{"model":"tripo-v3.1-multiview"}`, &missing)
	if !gone.result.NoSubmission || !strings.Contains(gone.result.AgentContent, gone.chat.ThreeView.Messages.ViewsChanged) {
		t.Fatalf("a view the group no longer has finished must refuse the step: %+v", gone.result)
	}
}

func TestMultiviewRefusesAGroupThatIsNotReadyOrNotTheOwners(t *testing.T) {
	onlyFront := &fakeThreeViews{groups: map[string][]generationtool.GroupView{
		"owner-a/session-a/run-1": {{View: "front", TaskID: "task-front", AssetID: "asset-front"}, {View: "side", TaskID: "task-side", AssetID: "asset-side"}},
	}}
	run := runMultiview(t, onlyFront, `{"model":"tripo-v3.1-multiview","three_view_group":"run-1"}`, nil)
	if run.err != nil || !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" ||
		!strings.Contains(run.result.AgentContent, run.chat.ThreeView.Messages.GroupNotReady) {
		t.Fatalf("a group with only its front view must refuse: %+v %v", run.result, run.err)
	}
	noFront := &fakeThreeViews{groups: map[string][]generationtool.GroupView{
		"owner-a/session-a/run-1": {{View: "left", TaskID: "task-left", AssetID: "asset-left"}, {View: "back", TaskID: "task-back", AssetID: "asset-back"}},
	}}
	if run := runMultiview(t, noFront, `{"model":"tripo-v3.1-multiview","three_view_group":"run-1"}`, nil); !run.result.NoSubmission ||
		!strings.Contains(run.result.AgentContent, run.chat.ThreeView.Messages.GroupNotReady) {
		t.Fatalf("a group without its front view must refuse: %+v", run.result)
	}
	// Another person's group (the fake knows it only for owner-b) reads as missing.
	foreign := &fakeThreeViews{groups: map[string][]generationtool.GroupView{
		"owner-b/session-a/run-1": groupOfFour().groups["owner-a/session-a/run-1"],
	}}
	for _, arguments := range []string{
		`{"model":"tripo-v3.1-multiview","three_view_group":"run-1"}`,
		`{"model":"tripo-v3.1-multiview","three_view_group":""}`,
	} {
		run := runMultiview(t, foreign, arguments, nil)
		if run.err != nil || !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" || len(run.resolver.urls) != 0 ||
			!strings.Contains(run.result.AgentContent, run.chat.ThreeView.Messages.GroupNotFound) {
			t.Fatalf("%s: a foreign group must refuse before any picture is read: %+v %v", arguments, run.result, run.err)
		}
	}
	request := chat.MultiviewRequest{Group: "run-1", Model: "tripo-v3.1-multiview", Views: []chat.MultiviewView{
		{View: "front", TaskID: "task-front", AssetID: "asset-front"}, {View: "left", TaskID: "task-left", AssetID: "asset-left"}}}
	if run := runMultiview(t, foreign, `{"model":"tripo-v3.1-multiview"}`, &request); !run.result.NoSubmission ||
		!strings.Contains(run.result.AgentContent, run.chat.ThreeView.Messages.GroupNotFound) {
		t.Fatalf("a structured request for a foreign group must refuse: %+v", run.result)
	}
}

func TestMultiviewFromLabelledUploads(t *testing.T) {
	request := chat.MultiviewRequest{Model: "tripo-v3.1-multiview", Views: []chat.MultiviewView{
		{View: "front", AssetID: "upload-1"}, {View: "right", AssetID: "upload-2"}, {View: "back", AssetID: "upload-3"}}}
	run := runMultiview(t, nil, `{"model":"tripo-v3.1-multiview","reference_handles":["@img1","@img2"]}`, &request)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("labelled uploads: %+v %v", run.result, run.err)
	}
	if want := []string{"/api/v1/assets/upload-1", "/api/v1/assets/upload-2", "/api/v1/assets/upload-3"}; !reflect.DeepEqual(run.resolver.urls, want) {
		t.Fatalf("the pictures came from %v, want the labelled uploads %v", run.resolver.urls, want)
	}
	if got := run.input["reference_views"]; !reflect.DeepEqual(got, []any{"front", "right", "back"}) {
		t.Fatalf("reference_views = %v, want the person's labels in their order", got)
	}
	for _, views := range [][]chat.MultiviewView{
		{{View: "front", AssetID: "upload-1"}, {View: "top", AssetID: "upload-2"}},
		{{View: "left", AssetID: "upload-1"}, {View: "back", AssetID: "upload-2"}},
	} {
		bad := chat.MultiviewRequest{Model: "tripo-v3.1-multiview", Views: views}
		run := runMultiview(t, nil, `{"model":"tripo-v3.1-multiview"}`, &bad)
		if run.err != nil || !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" ||
			!strings.Contains(run.result.AgentContent, run.chat.ThreeView.Messages.ViewsInvalid) {
			t.Fatalf("labels %+v must refuse: %+v %v", views, run.result, run.err)
		}
	}
}

func TestMultiviewWithoutAGroupKeepsTheHandlePath(t *testing.T) {
	run := runMultiview(t, groupOfFour(), `{"model":"tripo-v3.1-multiview","reference_handles":["@img1","@img2"]}`, nil)
	// The handles name no picture of this chat: the ordinary handle refusal, not a group one.
	if !run.result.NoSubmission || strings.Contains(run.result.AgentContent, run.chat.ThreeView.Messages.GroupNotFound) {
		t.Fatalf("a call without a group must use the handle path: %+v", run.result)
	}
}

func TestMultiviewRequestAcceptsTheOfferingID(t *testing.T) {
	request := chat.MultiviewRequest{Group: "run-1", Model: multiviewOffering, Views: []chat.MultiviewView{
		{View: "front", TaskID: "task-front", AssetID: "asset-front"},
		{View: "back", AssetID: "upload-back"},
	}}
	run := runMultiview(t, groupOfFour(), `{"model":"tripo-v3.1-multiview"}`, &request)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("a request naming the Offering ID was refused: %+v %v", run.result, run.err)
	}
}

// #373: the model the person picked on the multiview panel is the pick of this
// turn. A session lock on a model that takes no views (Rodin) or on another
// multiview model neither refuses the step nor replaces the model: the Task is
// frozen on the requested Offering, and the result says the person chose it.
func TestMultiviewRequestRunsItsModelWhateverTheSessionLocked(t *testing.T) {
	request := chat.MultiviewRequest{Model: "tripo-v3.1-multiview", Views: []chat.MultiviewView{
		{View: "front", AssetID: "upload-1"}, {View: "left", AssetID: "upload-2"}, {View: "back", AssetID: "upload-3"}}}
	for _, test := range []struct{ name, locked string }{
		{"a lock on a model that takes no views", "rodin-gen-2.5"},
		{"a lock on another multiview model", "tripo-p2-multiview"},
		{"a lock on the requested model", "tripo-v3.1-multiview"},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario := multiviewScenario{locked: test.locked, also: []string{rodinImageTo3D, p2Multiview}}
			// The Agent follows the turn directive: it names the requested model.
			run := runMultiviewIn(t, scenario, nil, `{"model":"tripo-v3.1-multiview"}`, &request)
			if run.err != nil || run.result.NoSubmission || run.gateway.submitted.OfferingID != multiviewOffering {
				t.Fatalf("the requested model must run under lock %q: %+v %v (submitted %q)",
					test.locked, run.result, run.err, run.gateway.submitted.OfferingID)
			}
			if got := run.input["reference_views"]; !reflect.DeepEqual(got, []any{"front", "left", "back"}) {
				t.Fatalf("reference_views = %v, want the person's labels", got)
			}
			if choice, _ := run.content["model_choice"].(map[string]any); choice["source"] != "user" {
				t.Fatalf("model_choice = %v, want the person's choice", run.content["model_choice"])
			}
		})
	}
}

// Without a structured request the session's lock still outranks the Agent's
// model: a call that names a group and another model is refused with the
// confirmation text and submits nothing (#103).
func TestMultiviewWithoutARequestStillFollowsTheLock(t *testing.T) {
	scenario := multiviewScenario{locked: "rodin-gen-2.5", also: []string{rodinImageTo3D}}
	run := runMultiviewIn(t, scenario, groupOfFour(), `{"model":"tripo-v3.1-multiview","three_view_group":"run-1"}`, nil)
	if run.err != nil || !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" ||
		run.content["service_confirmation_required"] != true {
		t.Fatalf("the Agent's model must not replace the lock: %+v %v", run.result, run.err)
	}
}

// A request for a model that cannot run now is refused. The lock does not
// stand in for it: running Rodin on pictures the person labelled for a
// multiview model would spend credits on a step they did not choose.
func TestMultiviewRequestForAnUnavailableModelIsRefusedNotReplacedByTheLock(t *testing.T) {
	request := chat.MultiviewRequest{Model: "tripo-p2-multiview", Views: []chat.MultiviewView{
		{View: "front", AssetID: "upload-1"}, {View: "left", AssetID: "upload-2"}}}
	scenario := multiviewScenario{locked: "rodin-gen-2.5", also: []string{rodinImageTo3D, p2Multiview}, unavailable: []string{p2Multiview}}
	run := runMultiviewIn(t, scenario, nil, `{"model":"tripo-p2-multiview"}`, &request)
	if run.err != nil || !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" ||
		!strings.Contains(run.result.Error, "当前不可用") {
		t.Fatalf("an unavailable requested model must refuse the step as unavailable: %+v %v (submitted %q)",
			run.result, run.err, run.gateway.submitted.OfferingID)
	}
}
