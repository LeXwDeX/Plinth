package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

// #283: the views of one character are compared with the front view for their
// colours, their detached parts and their mirrored sides, with the thresholds
// and the words the default Schema ships.

type lookBlock struct {
	x0, y0, x1, y1 int // [x0, x1) x [y0, y1)
	fill           color.NRGBA
}

var (
	lookCoatBlue  = color.NRGBA{R: 50, G: 80, B: 160, A: 255}
	lookCoatRed   = color.NRGBA{R: 170, G: 50, B: 50, A: 255}
	lookSkin      = color.NRGBA{R: 230, G: 190, B: 160, A: 255}
	lookHair      = color.NRGBA{R: 70, G: 45, B: 30, A: 255}
	lookBoots     = color.NRGBA{R: 40, G: 40, B: 50, A: 255}
	lookSteel     = color.NRGBA{R: 150, G: 155, B: 165, A: 255}
	lookOrange    = color.NRGBA{R: 225, G: 120, B: 40, A: 255}
	lookBackdrop  = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	lookWidth     = 128
	lookHeight    = 160
	lookMarginTop = 8
)

// lookFront is a character with a coat, a head, boots and two detached
// accessories: a floating sword on one side and a pet on the other.
func lookFront(head, coat color.NRGBA) []lookBlock {
	return []lookBlock{
		{40, 30, 88, 120, coat},
		{52, lookMarginTop, 76, 30, head},
		{44, 120, 84, 150, lookBoots},
		{100, 40, 107, 110, lookSteel},
		{8, 125, 30, 148, lookOrange},
	}
}

// lookSide is the same character from the side: narrower, with the same
// accessories and the same height in the frame.
func lookSide(head, coat color.NRGBA) []lookBlock {
	return []lookBlock{
		{44, 30, 84, 120, coat},
		{52, lookMarginTop, 76, 30, head},
		{46, 120, 82, 150, lookBoots},
		{96, 30, 104, 110, lookSteel},
		{6, 120, 30, 150, lookOrange},
	}
}

// lookMirror is the scene seen from the opposite side.
func lookMirror(blocks []lookBlock) []lookBlock {
	result := make([]lookBlock, len(blocks))
	for index, item := range blocks {
		result[index] = lookBlock{lookWidth - item.x1, item.y0, lookWidth - item.x0, item.y1, item.fill}
	}
	return result
}

func lookPNG(t *testing.T, blocks []lookBlock) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, lookWidth, lookHeight))
	for y := 0; y < lookHeight; y++ {
		for x := 0; x < lookWidth; x++ {
			picture.SetNRGBA(x, y, lookBackdrop)
		}
	}
	for _, item := range blocks {
		for y := item.y0; y < item.y1; y++ {
			for x := item.x0; x < item.x1; x++ {
				picture.SetNRGBA(x, y, item.fill)
			}
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// useShippedChecks makes the fixture judge with the default Schema's
// thresholds, frame rule, subject types, checklist and reasons.
func (f *continuationFixture) useShippedChecks(t *testing.T) {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	shipped := c.Application().Chat.ThreeView
	f.useSchema(t, func(schema *catalog.ChatThreeViewSchema) {
		schema.Consistency = shipped.Consistency
		schema.Frame = shipped.Frame
		schema.Review = shipped.Review
		schema.SubjectTypes = shipped.SubjectTypes
		schema.Messages.AgentCompleted = "completed {run}: {views}{checklist}"
	})
}

// complete finishes the Task of the view that just ran with the given picture
// and asks the continuation what happens next.
func (f *continuationFixture) complete(t *testing.T, taskID, assetID string, picture []byte) threeview.ContinuationResult {
	t.Helper()
	f.assets.bad[generation.AssetID(assetID)] = picture
	f.finish(t, taskID, assetID)
	result, err := f.views.Continue(context.Background(), "owner-a", "session-a", f.run.ID, taskID, "turn-a")
	if err != nil {
		t.Fatalf("continue after %s: %v", assetID, err)
	}
	return result
}

// reachBack finishes the front and left views with the given pictures and
// returns the Task of the back view the continuation started.
func (f *continuationFixture) reachBack(t *testing.T, front, left []byte) string {
	t.Helper()
	next := f.complete(t, f.startFront(t), "asset-front", front)
	if next.TaskID == "" || next.View != "left" {
		t.Fatalf("the front view did not lead to the left view: %+v", next)
	}
	next = f.complete(t, next.TaskID, "asset-left", left)
	if next.TaskID == "" || next.View != "back" {
		t.Fatalf("the left view did not lead to the back view: %+v", next)
	}
	return next.TaskID
}

func requireViewRedraw(t *testing.T, result threeview.ContinuationResult, view string, reasons ...string) {
	t.Helper()
	if !result.Retried || result.View != view || result.Attempt != 1 || result.TaskID == "" || result.Paused {
		t.Fatalf("%s must be redrawn alone: %+v", view, result)
	}
	t.Logf("%s is redrawn because: %s", view, result.FailedReasons)
	for _, reason := range reasons {
		if !strings.Contains(result.FailedReasons, reason) {
			t.Fatalf("the redraw of %s does not say %q: %q", view, reason, result.FailedReasons)
		}
	}
}

func TestShippedChecksAcceptTheNormalViewsOfACharacter(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	backTask := f.reachBack(t, lookPNG(t, lookFront(lookSkin, lookCoatBlue)), lookPNG(t, lookSide(lookSkin, lookCoatBlue)))
	// From behind the face is hidden by hair, and the picture is mirrored.
	done := f.complete(t, backTask, "asset-back", lookPNG(t, lookMirror(lookFront(lookHair, lookCoatBlue))))
	if done.Paused || done.Retried || done.TaskID != "" || len(done.Completed) != 3 || !done.Group.Complete {
		t.Fatalf("the normal views were not accepted: %+v", done)
	}
	for _, key := range []string{"front", "left", "back"} {
		if step := f.step(t, key); step.Review != skillrun.Accepted || step.Attempt != 0 {
			t.Fatalf("%s = %+v", key, step)
		}
	}
	// The optional right view is the mirror image of the left one.
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	group, err := f.views.Advance(context.Background(), principal, "session-a", f.run.ID, "right", threeview.Start, 0)
	if err != nil {
		t.Fatal(err)
	}
	rightTask := slotOf(group, "right").TaskID
	f.assets.bad["asset-right"] = lookPNG(t, lookMirror(lookSide(lookHair, lookCoatBlue)))
	f.finish(t, rightTask, "asset-right")
	if _, err := f.views.Views(context.Background(), "owner-a", "session-a", f.run.ID); err != nil {
		t.Fatal(err)
	}
	if right := f.step(t, "right"); right.Review != skillrun.Accepted {
		t.Fatalf("the mirrored right view was refused: %+v", right)
	}
}

func TestColourDriftInTheBackViewIsRedrawnWithItsReason(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	backTask := f.reachBack(t, lookPNG(t, lookFront(lookSkin, lookCoatBlue)), lookPNG(t, lookSide(lookSkin, lookCoatBlue)))
	drifted := f.complete(t, backTask, "asset-back", lookPNG(t, lookMirror(lookFront(lookHair, lookCoatRed))))
	requireViewRedraw(t, drifted, "back", "主色分布")
	for _, unrelated := range []string{"左右互换", "配件或独立部件"} {
		if strings.Contains(drifted.FailedReasons, unrelated) {
			t.Fatalf("colour drift also reported %q: %q", unrelated, drifted.FailedReasons)
		}
	}
	if front, left := f.step(t, "front"), f.step(t, "left"); front.Attempt != 0 || left.Attempt != 0 ||
		front.Review != skillrun.Accepted || left.Review != skillrun.Accepted {
		t.Fatalf("accepted views were touched: front=%+v left=%+v", front, left)
	}
	// The limits stay as they were: a second drifted picture ends the redraws
	// and the reason travels with the pause.
	again := f.complete(t, drifted.TaskID, "asset-back-2", lookPNG(t, lookMirror(lookFront(lookHair, lookCoatRed))))
	if !again.Paused || again.Reason != threeview.ReasonConsistencyExhausted || !strings.Contains(again.FailedReasons, "主色分布") {
		t.Fatalf("exhausted redraws: %+v", again)
	}
	if f.gateway.submits != 4 {
		t.Fatalf("submissions = %d, want front, left, back and one redraw", f.gateway.submits)
	}
}

func TestASwappedBackViewIsRedrawnWithItsReason(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	backTask := f.reachBack(t, lookPNG(t, lookFront(lookSkin, lookCoatBlue)), lookPNG(t, lookSide(lookSkin, lookCoatBlue)))
	// The back view kept the front view's layout instead of mirroring it: the
	// sword stayed on the same side of the picture.
	swapped := f.complete(t, backTask, "asset-back", lookPNG(t, lookFront(lookHair, lookCoatBlue)))
	requireViewRedraw(t, swapped, "back", "左右互换", "Front")
}

func TestAMissingAccessoryIsRedrawnWithItsReason(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	backTask := f.reachBack(t, lookPNG(t, lookFront(lookSkin, lookCoatBlue)), lookPNG(t, lookSide(lookSkin, lookCoatBlue)))
	bare := lookFront(lookHair, lookCoatBlue)[:3] // no sword, no pet
	missing := f.complete(t, backTask, "asset-back", lookPNG(t, lookMirror(bare)))
	requireViewRedraw(t, missing, "back", "配件或独立部件")
}

func TestASwappedRightViewIsRefusedAgainstTheLeftView(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	backTask := f.reachBack(t, lookPNG(t, lookFront(lookSkin, lookCoatBlue)), lookPNG(t, lookSide(lookSkin, lookCoatBlue)))
	done := f.complete(t, backTask, "asset-back", lookPNG(t, lookMirror(lookFront(lookHair, lookCoatBlue))))
	if len(done.Completed) != 3 {
		t.Fatalf("the required views were not accepted: %+v", done)
	}
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	group, err := f.views.Advance(context.Background(), principal, "session-a", f.run.ID, "right", threeview.Start, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A right view that faces the same way as the left one.
	f.assets.bad["asset-right"] = lookPNG(t, lookSide(lookHair, lookCoatBlue))
	f.finish(t, slotOf(group, "right").TaskID, "asset-right")
	if _, err := f.views.Views(context.Background(), "owner-a", "session-a", f.run.ID); err != nil {
		t.Fatal(err)
	}
	right := f.step(t, "right")
	if right.Review != skillrun.Rejected {
		t.Fatalf("a right view that faces the left view's way was accepted: %+v", right)
	}
	if slot := slotOf(f.group(t), "right"); slot.Status != threeview.Rejected || !slot.Retryable {
		t.Fatalf("the rejected right view must be retryable alone: %+v", slot)
	}
}

// A character with a floating weapon or a pet is one subject in several
// pieces: the front view is no longer refused for it (the piece count is a
// Schema bound), while a sheet of several figures still is.
func TestTheFrontViewMayCarryFloatingAccessories(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	next := f.complete(t, f.startFront(t), "asset-front", lookPNG(t, lookFront(lookSkin, lookCoatBlue)))
	if next.Retried || next.Paused || next.View != "left" || next.TaskID == "" {
		t.Fatalf("a character with a sword and a pet was refused: %+v", next)
	}
	if front := f.step(t, "front"); front.Review != skillrun.Accepted || front.Attempt != 0 {
		t.Fatalf("front = %+v", front)
	}
	// The same picture met the former fixed bound of one piece and was
	// refused for its accessories, which cost a paid redraw.
	g := newContinuationFixture(t, true)
	g.useShippedChecks(t)
	g.useSchema(t, func(schema *catalog.ChatThreeViewSchema) { schema.Consistency.Front.MaxComponents = 1 })
	former := g.complete(t, g.startFront(t), "asset-front", lookPNG(t, lookFront(lookSkin, lookCoatBlue)))
	requireViewRedraw(t, former, "front", "主体被分成过多块")
}

func TestAFrontViewWithSeveralFiguresIsStillRefused(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	sheet := []lookBlock{{10, 20, 50, 140, lookCoatBlue}, {78, 20, 118, 140, lookCoatRed}}
	redrawn := f.complete(t, f.startFront(t), "asset-front", lookPNG(t, sheet))
	requireViewRedraw(t, redrawn, "front", "不止一个主体")
}

func TestTheFrontPieceBoundIsTheSchemas(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	f.useSchema(t, func(schema *catalog.ChatThreeViewSchema) { schema.Consistency.Front.MaxComponents = 2 })
	redrawn := f.complete(t, f.startFront(t), "asset-front", lookPNG(t, lookFront(lookSkin, lookCoatBlue)))
	requireViewRedraw(t, redrawn, "front", "主体被分成过多块")
}

// The appearance comparison is off when the Schema declares no bound: the
// views are judged by geometry alone, as before.
func TestWithoutAppearanceBoundsOnlyTheGeometryDecides(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	f.useSchema(t, func(schema *catalog.ChatThreeViewSchema) {
		schema.Consistency.Appearance = catalog.ChatThreeViewAppearanceSchema{}
	})
	backTask := f.reachBack(t, lookPNG(t, lookFront(lookSkin, lookCoatBlue)), lookPNG(t, lookSide(lookSkin, lookCoatBlue)))
	done := f.complete(t, backTask, "asset-back", lookPNG(t, lookMirror(lookFront(lookHair, lookCoatRed))))
	if done.Retried || done.Paused || len(done.Completed) != 3 {
		t.Fatalf("a colour change alone must pass when no appearance bound is declared: %+v", done)
	}
}

// threeViewFramedOfferings is an image-to-image model whose output size is a ratio the
// person may choose, like the Meshy image models.
type threeViewFramedOfferings struct{}

func (threeViewFramedOfferings) Offerings(generation.Capability) []generation.Offering {
	return []generation.Offering{{ID: "offering-i2i", ModelID: "original-model", Enabled: true,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt},
			{Name: "aspect_ratio", Type: "select", Semantic: generation.SemanticOutputSize,
				Default: json.RawMessage(`"1:1"`), Options: []generation.ParameterOption{
					{Value: "1:1"}, {Value: "4:3"}, {Value: "3:4"}, {Value: "16:9"}, {Value: "9:16"}}},
		}}}
}
func (threeViewFramedOfferings) OfferingAvailable(generation.OfferingID) bool { return true }

// startThreeViewTool creates a run through the Agent tool, as a turn does, and
// returns the run.
func (f *continuationFixture) startThreeViewTool(t *testing.T, call, subjectType string) skillrun.Run {
	t.Helper()
	executor, err := agenttools.New(agenttools.Config{Params: agentparams.NewStore(),
		Generate: unusedSkillGenerator{}, TaskObservationTimeout: time.Minute,
		Context:                 catalog.ChatContextSchema{AssetURLTemplate: "/api/v1/tasks/{task_id}/assets/{asset_id}"},
		ConsentReconcileContent: "Task observation unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.SetSkillRuntime(f.runtime, &skillRecoveryReferences{}, threeViewFramedOfferings{},
		[]catalog.ChatSkillRecipeSchema{{ID: "image-three-view", Revision: "r1"}}, f.repo.TurnStopRequested); err != nil {
		t.Fatal(err)
	}
	executor.SetThreeView(f.schema)
	executor.SetThreeViewService(f.views)
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: call, Name: "create_three_view",
			Arguments: []byte(`{"subject_type":"` + subjectType + `"}`)},
		Options: chat.TurnOptions{AIModel: "original-model", CreativeDomain: "image"},
		History: []chat.Message{{Role: "user", ImageURLs: []string{"/owned/source-a"}}}}
	if _, err := executor.Execute(context.Background(), command, func(chat.ToolObservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	run, err := f.runtime.Runs.GetByIdempotency(context.Background(), "owner-a", threeViewRunKeyForTest("session-a", call))
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func threeViewFrameOf(run skillrun.Run) []string {
	frames := make([]string, 0, len(run.Steps))
	for _, step := range run.Steps {
		frames = append(frames, string(step.Spec.StaticInput["aspect_ratio"]))
	}
	return frames
}

// #283: the frame of every view follows the source subject's shape instead of
// the Offering's default square, inside the bounds of the subject type.
func TestAgentToolFramesTheViewsLikeTheSourceSubject(t *testing.T) {
	// A tall character on a plain background: 32 wide and 160 high in a
	// 96x192 picture.
	portrait := image.NewNRGBA(image.Rect(0, 0, 96, 192))
	for y := 0; y < 192; y++ {
		for x := 0; x < 96; x++ {
			portrait.SetNRGBA(x, y, lookBackdrop)
			if x >= 32 && x < 64 && y >= 16 && y < 176 {
				portrait.SetNRGBA(x, y, lookCoatBlue)
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, portrait); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		subject string
		want    string
	}{
		// 32x160 plus the margin is 0.31 wide over high: the tallest option.
		{"generic", `"9:16"`},
		// A humanoid asked for an A-pose is not as narrow as a source with the
		// arms down: the frame is at least 0.66 wide over high.
		{"humanoid", `"3:4"`},
		// A quadruped's side view is wider than its front view.
		{"quadruped", `"1:1"`},
	} {
		t.Run(test.subject, func(t *testing.T) {
			f := newContinuationFixture(t, true)
			f.useShippedChecks(t)
			f.assets.owned["source-a"] = encoded.Bytes()
			run := f.startThreeViewTool(t, "frame-"+test.subject, test.subject)
			frames := threeViewFrameOf(run)
			if len(frames) != 4 {
				t.Fatalf("steps = %d", len(frames))
			}
			for index, frame := range frames {
				if frame != test.want {
					t.Fatalf("step %s frame = %s, want %s", run.Steps[index].Spec.Key, frame, test.want)
				}
			}
			// The frozen input of the front view carries it to the model.
			prepared, err := f.gateway.GetPreparedInput(context.Background(), "owner-a", run.Steps[0].PreparedInputID)
			if err != nil || !strings.Contains(string(prepared.NormalizedInput), `"aspect_ratio":`+test.want) {
				t.Fatalf("prepared input = %s (%v)", prepared.NormalizedInput, err)
			}
		})
	}
}

func TestTheFrameFollowsTheSourceAndFallsBackToTheDefault(t *testing.T) {
	// The fake picture is 64x64 with a 24x48 subject: a tall subject, so the
	// frame is portrait; a nearly square subject gets the nearly square frame;
	// a source that cannot be read, or a Schema without the rule, keeps the
	// Offering's default.
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	if frames := threeViewFrameOf(f.startThreeViewTool(t, "frame-fake", "generic")); frames[0] != `"9:16"` {
		t.Fatalf("the fake portrait character gave %v", frames)
	}
	square := lookPNG(t, []lookBlock{{24, 30, 104, 130, lookCoatBlue}}) // 80x100 subject: nearly square
	g := newContinuationFixture(t, true)
	g.useShippedChecks(t)
	g.assets.owned["source-a"] = square
	if frames := threeViewFrameOf(g.startThreeViewTool(t, "frame-square", "generic")); frames[0] != `"3:4"` {
		t.Fatalf("a nearly square subject gave %v", frames)
	}
	broken := newContinuationFixture(t, true)
	broken.useShippedChecks(t)
	broken.assets.owned["source-a"] = []byte("not a picture")
	if frames := threeViewFrameOf(broken.startThreeViewTool(t, "frame-broken", "generic")); frames[0] != "" {
		t.Fatalf("an unreadable source must keep the Offering's default frame, got %v", frames)
	}
	disabled := newContinuationFixture(t, true)
	disabled.useShippedChecks(t)
	disabled.useSchema(t, func(schema *catalog.ChatThreeViewSchema) { schema.Frame = catalog.ChatThreeViewFrameSchema{} })
	if frames := threeViewFrameOf(disabled.startThreeViewTool(t, "frame-off", "generic")); frames[0] != "" {
		t.Fatalf("a Schema without the frame rule must keep the default frame, got %v", frames)
	}
}

// The receipt of a finished group carries the Agent's visual checklist, worded
// by the Schema: each view other than the front, item by item against the
// front.
func TestTheCompletionReceiptCarriesTheVisualChecklist(t *testing.T) {
	f := newContinuationFixture(t, true)
	f.useShippedChecks(t)
	executor, err := agenttools.New(agenttools.Config{Params: agentparams.NewStore(),
		Generate: unusedSkillGenerator{}, TaskObservationTimeout: time.Minute,
		Context:                 catalog.ChatContextSchema{AssetURLTemplate: "/api/v1/tasks/{task_id}/assets/{asset_id}"},
		ConsentReconcileContent: "Task observation unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.SetSkillRuntime(f.runtime, &skillRecoveryReferences{}, &skillRecoveryOfferings{},
		[]catalog.ChatSkillRecipeSchema{{ID: "image-three-view", Revision: "r1"}}, f.repo.TurnStopRequested); err != nil {
		t.Fatal(err)
	}
	executor.SetThreeView(f.schema)
	executor.SetThreeViewService(f.views)
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call:    agents.ToolCall{ID: "checklist-call", Name: "create_three_view", Arguments: []byte(`{"subject_type":"humanoid"}`)},
		Options: chat.TurnOptions{AIModel: "original-model", CreativeDomain: "image"},
		History: []chat.Message{{Role: "user", ImageURLs: []string{"/owned/source-a"}}}}
	observe := func(chat.ToolObservation) error { return nil }
	first, err := executor.Execute(context.Background(), command, observe)
	if err != nil || first.TaskID == "" {
		t.Fatalf("front: %+v err=%v", first, err)
	}
	command.BoundTaskID = first.TaskID
	command.FrozenIdempotencyKey = ""
	for range 2 {
		f.finish(t, command.BoundTaskID, "asset-"+command.BoundTaskID)
		next, err := executor.Execute(context.Background(), command, observe)
		if err != nil || !next.AwaitTask || next.TaskID == "" {
			t.Fatalf("next view after %s: %+v err=%v", command.BoundTaskID, next, err)
		}
		command.BoundTaskID = next.TaskID
	}
	f.finish(t, command.BoundTaskID, "asset-back")
	final, err := executor.Execute(context.Background(), command, observe)
	if err != nil || final.AwaitTask || len(final.Pictures) != 3 {
		t.Fatalf("final: %+v err=%v", final, err)
	}
	for _, want := range []string{"三视图复核", "Left / Back", "服装", "配色", "配件", "左右",
		"- Back：从主体身后看", "- Left：从主体自己的左侧看过去"} {
		if !strings.Contains(final.AgentContent, want) {
			t.Fatalf("the receipt lacks %q:\n%s", want, final.AgentContent)
		}
	}
	if strings.Contains(final.AgentContent, "{") || strings.Contains(final.AgentContent, "- Front：") {
		t.Fatalf("unfilled field or a note for the front view in the receipt:\n%s", final.AgentContent)
	}
}
