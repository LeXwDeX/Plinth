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
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// fakeThreeViews knows one group: its views are the tasks side and back (and
// the front), and the front is the model source.
type fakeThreeViews struct {
	source  generationtool.ModelSource
	sources map[string]generationtool.ModelSource
	members map[string]bool
	asked   []string
}

func (f *fakeThreeViews) ModelSource(_ context.Context, owner, session, taskID string) (generationtool.ModelSource, bool, error) {
	f.asked = append(f.asked, owner+"/"+session+"/"+taskID)
	if owner != "owner-a" || session != "session-a" || !f.members[taskID] {
		return generationtool.ModelSource{}, false, nil
	}
	if source, ok := f.sources[taskID]; ok {
		return source, true, nil
	}
	return f.source, true, nil
}

type threeViewModelRun struct {
	result   chat.ToolResult
	err      error
	resolver *fakeReferences
	gateway  *fakeGateway
	content  map[string]any
}

func runModelFromImages(t *testing.T, views generationtool.ThreeViews, urls []string, preserve ...bool) threeViewModelRun {
	t.Helper()
	return runModelFromImagesWith(t, views, urls, func(offering *generation.Offering) {
		if len(preserve) > 0 && preserve[0] {
			offering.ReferenceSelectionPolicy = "preserve"
			offering.Parameters[1].MinItems = 1
			offering.Parameters[1].MaxItems = 4
		}
	})
}

func runModelFromImagesWith(t *testing.T, views generationtool.ThreeViews, urls []string, adjust func(*generation.Offering)) threeViewModelRun {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	if tool.Name == "" || tool.ResultFields.ReferenceNote == "" {
		t.Fatalf("the shipped image-to-3d tool must name a reference-note key: %+v", tool.ResultFields)
	}
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID: "configured/offering", ModelID: "configured-model", Capability: generation.ImageTo3D,
			Enabled: true, PollInterval: time.Second, Timeout: 5 * time.Second,
			Parameters: []generation.Parameter{
				{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
				{Name: "image", Label: "Reference images", Type: "asset-array", Semantic: generation.SemanticReferenceImage},
			},
		}},
		tasks: []generation.Task{
			{ID: "configured-task", State: generation.TaskSucceeded,
				Assets: []generation.Asset{{ID: "model-thumb", Role: "thumbnail"}}},
		},
	}
	// Catalog Offerings carry the runtime messages, the reference limit text among them.
	gateway.offerings[0].ExecutionMessages = application.Runtime.GenerationMessages
	adjust(&gateway.offerings[0])
	gateway.tasks[0].Offering = gateway.offerings[0]
	resolver := &fakeReferences{}
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: resolver, Chat: application.Chat, TaskStates: application.TaskStates,
		Clock: func() time.Time { return now },
		Sleep: func(_ context.Context, duration time.Duration) error { now = now.Add(duration); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	executor.SetThreeViews(views)
	history := []chat.Message{{Role: application.Chat.Roles.User, Content: "make it 3D", ImageURLs: urls}}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: tool, History: history,
		Call:    agents.ToolCall{ID: "call-3d", Name: tool.Name, Arguments: []byte(`{"prompt":"a character"}`)},
		Options: chat.TurnOptions{AIModel: "configured-model"},
	}, func(chat.ToolObservation) error { return nil })
	run := threeViewModelRun{result: result, err: err, resolver: resolver, gateway: gateway}
	if result.AgentContent != "" {
		_ = json.Unmarshal([]byte(result.AgentContent), &run.content)
	}
	return run
}

func TestModelFromAThreeViewGroupUsesTheFrontViewOnly(t *testing.T) {
	product, _ := catalog.LoadDefault()
	chatSchema := product.Application().Chat
	front := generationtool.ModelSource{Available: true, TaskID: "task-front", AssetID: "asset-front"}
	views := &fakeThreeViews{source: front, members: map[string]bool{"task-front": true, "task-side": true, "task-back": true}}

	// Pointing at the side and the back view still sends the front view, once.
	run := runModelFromImages(t, views, []string{
		"/api/v1/tasks/task-side/assets/asset-side?v=1", "/api/v1/tasks/task-back/assets/asset-back",
	})
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("model from group: %+v %v", run.result, run.err)
	}
	if len(run.resolver.urls) != 1 || run.resolver.urls[0] != "/api/v1/tasks/task-front/assets/asset-front" {
		t.Fatalf("the model was made from %v, not from the front view alone", run.resolver.urls)
	}
	if got := run.content["reference_note"]; got != chatSchema.ThreeView.ModelSourceNote {
		t.Fatalf("the result must say that only the front view is used: %v", run.content)
	}

	// A picture that is not a view of a group is sent as it is, with no note.
	other := runModelFromImages(t, views, []string{"/api/v1/tasks/task-other/assets/asset-other"})
	if other.err != nil || len(other.resolver.urls) != 1 || other.resolver.urls[0] != "/api/v1/tasks/task-other/assets/asset-other" {
		t.Fatalf("ordinary picture: %+v %v", other.resolver.urls, other.err)
	}
	if _, noted := other.content["reference_note"]; noted {
		t.Fatalf("an ordinary picture got the three-view note: %v", other.content)
	}
	upload := runModelFromImages(t, views, []string{"/api/v1/assets/own-upload"})
	if upload.err != nil || len(upload.resolver.urls) != 1 || upload.resolver.urls[0] != "/api/v1/assets/own-upload" {
		t.Fatalf("uploaded picture: %+v %v", upload.resolver.urls, upload.err)
	}
}

func TestModelReferenceNoteMatchesSelectedImageAtSingleImageLimit(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	frontNote := product.Application().Chat.ThreeView.ModelSourceNote
	if frontNote == "" {
		t.Fatal("the three-view source note must not be empty")
	}
	ordinary := "/api/v1/tasks/task-other/assets/asset-other"
	side := "/api/v1/tasks/task-side/assets/asset-side"
	front := "/api/v1/tasks/task-front/assets/asset-front"
	for _, tc := range []struct {
		name      string
		urls      []string
		wantURL   string
		wantFront bool
	}{
		{name: "ordinary first", urls: []string{ordinary, side}, wantURL: ordinary},
		{name: "side first", urls: []string{side, ordinary}, wantURL: front, wantFront: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			views := &fakeThreeViews{
				source:  generationtool.ModelSource{Available: true, TaskID: "task-front", AssetID: "asset-front"},
				members: map[string]bool{"task-side": true},
			}
			run := runModelFromImagesWith(t, views, tc.urls, func(offering *generation.Offering) {
				offering.Parameters[1].MaxItems = 1
			})
			if run.err != nil || run.result.NoSubmission || run.gateway.submitted.OfferingID == "" {
				t.Fatalf("model execution: result=%+v err=%v", run.result, run.err)
			}
			if !reflect.DeepEqual(run.resolver.urls, []string{tc.wantURL}) {
				t.Fatalf("resolved images = %v, want [%s]", run.resolver.urls, tc.wantURL)
			}
			note, _ := run.content["reference_note"].(string)
			if tc.wantFront && note != frontNote {
				t.Errorf("front selected but reference_note = %q, want %q", note, frontNote)
			}
			if !tc.wantFront && strings.Contains(note, frontNote) {
				t.Errorf("ordinary image selected but reference_note claims the front view: %q", note)
			}
		})
	}
}

func TestModelFromAThreeViewGroupWaitsForTheFrontView(t *testing.T) {
	product, _ := catalog.LoadDefault()
	chatSchema := product.Application().Chat
	views := &fakeThreeViews{source: generationtool.ModelSource{}, members: map[string]bool{"task-side": true}}
	run := runModelFromImages(t, views, []string{"/api/v1/tasks/task-side/assets/asset-side"})
	if run.err != nil || !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" {
		t.Fatalf("an unfinished front view still produced a paid task: %+v %v", run.result, run.err)
	}
	if want := chatSchema.ThreeView.Messages.ModelSourceNotYet; want == "" || !strings.Contains(run.result.AgentContent, want) {
		t.Fatalf("refusal = %q, want the Schema text %q", run.result.AgentContent, want)
	}
}

func TestModelFromAnotherUsersViewIsNotTreatedAsAGroup(t *testing.T) {
	views := &fakeThreeViews{
		source:  generationtool.ModelSource{Available: true, TaskID: "task-front", AssetID: "asset-front"},
		members: map[string]bool{"task-side": true},
	}
	run := runModelFromImages(t, views, []string{"/api/v1/tasks/task-side/assets/asset-side"})
	// The fake only answers for owner-a/session-a; ask as someone else.
	if _, member, _ := views.ModelSource(context.Background(), "owner-b", "session-a", "task-side"); member {
		t.Fatal("another owner's lookup found the group")
	}
	if run.err != nil || len(run.resolver.urls) != 1 || run.resolver.urls[0] != "/api/v1/tasks/task-front/assets/asset-front" {
		t.Fatalf("own group: %+v %v", run.resolver.urls, run.err)
	}
}

func TestMultiviewPreservesSelectedThreeViewIdentityOrderAndDuplicates(t *testing.T) {
	views := &fakeThreeViews{source: generationtool.ModelSource{Available: false}, members: map[string]bool{"task-side": true, "task-back": true}}
	urls := []string{"/api/v1/tasks/task-side/assets/asset-side", "/api/v1/tasks/task-back/assets/asset-back", "/api/v1/tasks/task-side/assets/asset-side"}
	run := runModelFromImages(t, views, urls, true)
	if run.err != nil || run.result.NoSubmission || !reflect.DeepEqual(run.resolver.urls, urls) {
		t.Fatalf("preserved references=%v result=%+v err=%v", run.resolver.urls, run.result, run.err)
	}
	if len(views.asked) != 0 {
		t.Fatalf("preserve mode consulted front substitution: %v", views.asked)
	}
	if _, ok := run.content["reference_note"]; ok {
		t.Fatalf("preserve mode claimed front-only: %v", run.content)
	}
}
func TestMultiviewOverLimitDoesNotResolveOrSubmit(t *testing.T) {
	run := runModelFromImages(t, nil, []string{"a", "b", "c", "d", "e"}, true)
	// Preserving the selected views means refusing the step, never cutting it
	// to four; the reason is Schema copy with the public label and both counts,
	// not the internal parameter name or a generic failure (#329 note 100006).
	if run.err != nil || !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" || len(run.resolver.urls) != 0 {
		t.Fatalf("over-limit transformed or submitted: %+v err=%v refs=%v", run.result, run.err, run.resolver.urls)
	}
	for _, fact := range []string{"Reference images", "5", "4"} {
		if !strings.Contains(run.result.Error, fact) || !strings.Contains(run.result.AgentContent, fact) {
			t.Fatalf("refusal %q / %q must state %q", run.result.Error, run.result.AgentContent, fact)
		}
	}
	if strings.Contains(run.result.Error, `parameter "image"`) || strings.Contains(run.result.Error, "at most 4 images") {
		t.Fatalf("refusal reads as an internal validation error: %q", run.result.Error)
	}
}

// #80: when a three-view view is replaced by its front and an ordinary picture
// is retained beside it, the retained pictures are not the first ones of the
// turn. The result must use the neutral count, not the front-only sentence and
// not the "only the first N" sentence.
func TestModelReferenceNoteUsesANeutralCountWhenASourceWasReplaced(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	neutral := application.Runtime.GenerationMessages.ReferenceSelectionNote
	frontNote := application.Chat.ThreeView.ModelSourceNote
	if neutral == "" || !strings.Contains(neutral, "{model}") || !strings.Contains(neutral, "{count}") {
		t.Fatalf("no neutral reference-selection text: %q", neutral)
	}
	side := "/api/v1/tasks/task-side/assets/asset-side"
	ordinary := "/api/v1/tasks/task-other/assets/asset-other"
	views := &fakeThreeViews{
		source:  generationtool.ModelSource{Available: true, TaskID: "task-front", AssetID: "asset-front"},
		members: map[string]bool{"task-side": true},
	}
	run := runModelFromImagesWith(t, views, []string{side, ordinary}, func(offering *generation.Offering) {
		offering.Parameters[1].MaxItems = 2
	})
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("model execution: result=%+v err=%v", run.result, run.err)
	}
	note, _ := run.content["reference_note"].(string)
	want := strings.NewReplacer("{model}", "configured-model", "{count}", "2").Replace(neutral)
	if note != want {
		t.Fatalf("reference note = %q, want neutral %q", note, want)
	}
	if strings.Contains(note, frontNote) {
		t.Fatalf("neutral note claims the front-only source: %q", note)
	}
}

// #80: two groups each bring their own front; the one-picture model keeps the
// first retained front and says so. The other group is not renamed or merged.
func TestTwoThreeViewGroupsKeepTheFirstFrontAndItsSource(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	frontNote := product.Application().Chat.ThreeView.ModelSourceNote
	views := &fakeThreeViews{
		sources: map[string]generationtool.ModelSource{
			"task-side-a": {Available: true, TaskID: "task-front-a", AssetID: "asset-front-a"},
			"task-side-b": {Available: true, TaskID: "task-front-b", AssetID: "asset-front-b"},
		},
		members: map[string]bool{"task-side-a": true, "task-side-b": true},
	}
	run := runModelFromImagesWith(t, views, []string{
		"/api/v1/tasks/task-side-a/assets/asset-side-a",
		"/api/v1/tasks/task-side-b/assets/asset-side-b",
	}, func(offering *generation.Offering) { offering.Parameters[1].MaxItems = 1 })
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("model execution: result=%+v err=%v", run.result, run.err)
	}
	if !reflect.DeepEqual(run.resolver.urls, []string{"/api/v1/tasks/task-front-a/assets/asset-front-a"}) {
		t.Fatalf("resolved images = %v", run.resolver.urls)
	}
	if note, _ := run.content["reference_note"].(string); note != frontNote {
		t.Fatalf("reference note = %q, want %q", note, frontNote)
	}
}

// #80: the same view repeated is one front picture; the fact may name the
// front but must not count the duplicate as a second source.
func TestRepeatedThreeViewViewUsesOneFront(t *testing.T) {
	side := "/api/v1/tasks/task-side/assets/asset-side"
	views := &fakeThreeViews{
		source:  generationtool.ModelSource{Available: true, TaskID: "task-front", AssetID: "asset-front"},
		members: map[string]bool{"task-side": true},
	}
	run := runModelFromImagesWith(t, views, []string{side, side}, func(offering *generation.Offering) {
		offering.Parameters[1].MaxItems = 1
	})
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("model execution: result=%+v err=%v", run.result, run.err)
	}
	if !reflect.DeepEqual(run.resolver.urls, []string{"/api/v1/tasks/task-front/assets/asset-front"}) {
		t.Fatalf("resolved images = %v", run.resolver.urls)
	}
}

// #80: a repeated ordinary picture is one picture, not the first of two. The
// neutral text says how many are used without claiming the original order.
func TestRepeatedOrdinaryPictureUsesTheNeutralCount(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	neutral := product.Application().Runtime.GenerationMessages.ReferenceSelectionNote
	ordinary := "/api/v1/tasks/task-other/assets/asset-other"
	views := &fakeThreeViews{members: map[string]bool{"task-side": true}}
	run := runModelFromImagesWith(t, views, []string{ordinary, ordinary}, func(offering *generation.Offering) {
		offering.Parameters[1].MaxItems = 2
	})
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("model execution: result=%+v err=%v", run.result, run.err)
	}
	if !reflect.DeepEqual(run.resolver.urls, []string{ordinary}) {
		t.Fatalf("resolved images = %v, want the one repeated picture", run.resolver.urls)
	}
	want := strings.NewReplacer("{model}", "configured-model", "{count}", "1").Replace(neutral)
	if note, _ := run.content["reference_note"].(string); note != want {
		t.Fatalf("reference note = %q, want %q", note, want)
	}
}
