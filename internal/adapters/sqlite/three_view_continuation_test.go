package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

// continuationGateway is the smallest Generation gateway that creates a real
// Task through the grant claim: every view gets its own Task and no Provider is
// contacted.
type continuationGateway struct {
	repo     *Repository
	prepares int
	submits  int
}

func (g *continuationGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	g.prepares++
	id := generation.PreparedInputID("prepared-cont-" + strconv.Itoa(g.prepares))
	var input map[string]json.RawMessage
	if err := json.Unmarshal(cmd.Input, &input); err != nil {
		return generation.PreparedInput{}, err
	}
	var sources []generation.AssetID
	if raw, ok := input["reference_assets"]; ok {
		_ = json.Unmarshal(raw, &sources)
	}
	assets := make([]generation.PreparedInputAsset, 0, len(sources))
	for _, source := range sources {
		assets = append(assets, generation.PreparedInputAsset{ID: source, SHA256: "sha", Size: 1, MediaType: "image/png"})
	}
	prepared := generation.PreparedInput{ID: id, OwnerID: cmd.OwnerID, SessionID: cmd.SessionID,
		Capability: cmd.Capability, OfferingID: cmd.OfferingID, OfferingRevision: "r7",
		NormalizedInput: cmd.Input, InputHash: "hash-" + string(id), Assets: assets,
		EstimatedCredits: 4, CreatedAt: time.Now().UTC()}
	if err := g.repo.CreatePreparedInput(ctx, prepared); err != nil {
		return generation.PreparedInput{}, err
	}
	return prepared, nil
}

func (g *continuationGateway) GetPreparedInput(ctx context.Context, owner generation.OwnerID,
	id generation.PreparedInputID,
) (generation.PreparedInput, error) {
	return g.repo.GetPreparedInput(ctx, owner, id)
}

func (g *continuationGateway) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	g.submits++
	prepared, err := g.GetPreparedInput(ctx, cmd.OwnerID, cmd.PreparedInputID)
	if err != nil {
		return generation.Accepted{}, err
	}
	identity := executiongrant.Identity{OwnerID: string(cmd.OwnerID), SessionID: cmd.SessionID,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		IdempotencyKey: cmd.IdempotencyKey, OperationID: cmd.OperationID, PlanStepID: cmd.PlanStepID, Harness: cmd.Harness}
	conn, err := g.repo.grantWriteConn(ctx)
	if err != nil {
		return generation.Accepted{}, err
	}
	defer rollbackGrantConn(conn)
	taskID, err := g.repo.ClaimExecutionGrantOnConn(ctx, conn, cmd.AuthorizationRef, identity)
	if err != nil {
		return generation.Accepted{}, err
	}
	if taskID == "" {
		taskID = "task-cont-" + strconv.Itoa(g.submits)
		offering, _ := json.Marshal(generation.Offering{ID: prepared.OfferingID, Revision: prepared.OfferingRevision,
			ModelID: "image-model", Capability: prepared.Capability, Timeout: time.Hour, CreditsMetered: true})
		digest := generation.ExpectedRequestHash(prepared.OfferingID, prepared.NormalizedInput)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = conn.ExecContext(ctx, `INSERT INTO generation_tasks
			(id,owner_id,session_id,capability,offering_json,input_json,request_hash,idempotency_key,state,version,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,'QUEUED',1,?,?)`, taskID, cmd.OwnerID, cmd.SessionID,
			prepared.Capability, string(offering), string(prepared.NormalizedInput), digest, cmd.IdempotencyKey, now, now)
		if err != nil {
			return generation.Accepted{}, err
		}
		if err := g.repo.BindExecutionGrantOnConn(ctx, conn, cmd.AuthorizationRef, identity, taskID); err != nil {
			return generation.Accepted{}, err
		}
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return generation.Accepted{}, err
	}
	return generation.Accepted{TaskID: generation.TaskID(taskID)}, nil
}

func (g *continuationGateway) VerifyPreparedTaskReplay(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Task, error) {
	task, err := g.repo.GetByIdempotency(ctx, cmd.OwnerID, cmd.IdempotencyKey)
	if err != nil {
		return generation.Task{}, err
	}
	prepared, err := g.GetPreparedInput(ctx, cmd.OwnerID, cmd.PreparedInputID)
	if err != nil {
		return generation.Task{}, err
	}
	err = g.repo.VerifyTaskGrantReplay(ctx, task, &generation.ExecutionAdmission{
		RequestID: cmd.AuthorizationRef, PreparedInputID: cmd.PreparedInputID,
		PreparedInputHash: prepared.InputHash, OperationID: cmd.OperationID, PlanStepID: cmd.PlanStepID, Harness: cmd.Harness})
	return task, err
}

func (g *continuationGateway) Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	return g.repo.Get(ctx, owner, id)
}

// continuationAssets serves the deterministic fake pictures. A view whose
// asset is in bad gets a tiny off-center subject instead.
type continuationAssets struct {
	good []byte
	bad  map[generation.AssetID][]byte
}

func (a continuationAssets) OpenOwned(context.Context, generation.OwnerID,
	generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	return generation.InputAsset{ID: "input"}, io.NopCloser(bytes.NewReader(a.good)), nil
}

func (a continuationAssets) OpenAsset(_ context.Context, _ generation.OwnerID, _ generation.TaskID,
	asset generation.AssetID,
) (generation.AssetContent, error) {
	data := a.good
	if bad, found := a.bad[asset]; found {
		data = bad
	}
	return generation.AssetContent{Body: io.NopCloser(bytes.NewReader(data)), MediaType: "image/png"}, nil
}

func continuationPicture(t *testing.T, width, height, x, y, size int) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		picture.Pix[i*4] = 255
		picture.Pix[i*4+1] = 255
		picture.Pix[i*4+2] = 255
		picture.Pix[i*4+3] = 255
	}
	for row := y; row < y+size; row++ {
		for column := x; column < x+size; column++ {
			picture.SetNRGBA(column, row, color.NRGBA{R: 40, G: 50, B: 60, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type continuationFixture struct {
	repo    *Repository
	runtime *skillrun.Runtime
	gateway *continuationGateway
	assets  continuationAssets
	views   *threeview.Service
	run     skillrun.Run
}

func newContinuationFixture(t *testing.T, withPlan bool) *continuationFixture {
	t.Helper()
	return newContinuationFixtureWithMode(t, withPlan, "budget")
}

func newContinuationFixtureWithMode(t *testing.T, withPlan bool, mode string) *continuationFixture {
	t.Helper()
	ctx := context.Background()
	repo, _, _ := skillFixture(t)
	insertGrantTurn(t, repo, "turn-a", "owner-a", "session-a")
	recipe := skillrun.Recipe{ID: "image-three-view", Revision: "r1", Steps: []skillrun.StepSpec{
		{Key: "front", Label: "Front", Capability: generation.ImageToImage, OfferingID: "offering-i2i",
			OfferingRevision: "r7", PromptTemplate: "front view", PromptParameter: "prompt",
			SourceParameter: "reference_assets", OutputRole: "image"},
		{Key: "left", Label: "Left", Capability: generation.ImageToImage, OfferingID: "offering-i2i",
			OfferingRevision: "r7", PromptTemplate: "left view", PromptParameter: "prompt",
			SourceParameter: "reference_assets", OutputRole: "image", SourceStepKey: "front"},
		{Key: "back", Label: "Back", Capability: generation.ImageToImage, OfferingID: "offering-i2i",
			OfferingRevision: "r7", PromptTemplate: "back view", PromptParameter: "prompt",
			SourceParameter: "reference_assets", OutputRole: "image", SourceStepKey: "front"},
		{Key: "right", Label: "Right", Capability: generation.ImageToImage, OfferingID: "offering-i2i",
			OfferingRevision: "r7", PromptTemplate: "right view", PromptParameter: "prompt",
			SourceParameter: "reference_assets", OutputRole: "image", SourceStepKey: "front", Optional: true},
	}}
	runs, err := skillrun.NewService(repo, testRecipeResolver{recipe})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &continuationGateway{repo: repo}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Budgets: repo,
		Policy: budgetContinuationPolicy(), Modes: fixedModes{mode: mode}}
	runtime, err := skillrun.NewRuntime(runs, gateway, grants)
	if err != nil {
		t.Fatal(err)
	}
	good, err := fakeupstream.ImageFile("png")
	if err != nil {
		t.Fatal(err)
	}
	assets := continuationAssets{good: good.Data, bad: map[generation.AssetID][]byte{}}
	schema := catalog.ChatThreeViewSchema{RecipeID: recipe.ID, ModelSourceStepKey: "front",
		Label: "views", MaximumListed: 5, ModelSourceNote: "front",
		DefaultSubjectType: "generic", SubjectArgument: "subject_type",
		SubjectTypes: []catalog.ChatThreeViewSubjectType{
			{ID: "generic", Label: "Generic", FrontConstraints: "generic front", ViewConstraints: "generic view"},
			{ID: "humanoid", Label: "Humanoid", FrontConstraints: "humanoid front", ViewConstraints: "humanoid view"},
		},
		Consistency: catalog.ChatThreeViewConsistencySchema{MaxSubjectRatioDelta: 0.08,
			MaxBackgroundSpread: 32, MaxBBoxHeightDelta: 0.12, MaxAttemptsPerView: 2},
		Continuation: catalog.ChatThreeViewContinuationSchema{Enabled: true,
			Reasons: map[string]string{"budget_not_covered": "budget does not cover it",
				"view_not_finished": "view did not finish", "view_already_started": "another view runs",
				"consistency_retries_exhausted": "retries exhausted"}},
		Messages: catalog.ChatThreeViewMessages{
			AgentStarted:  "run {run} view {view} {approval} {credits} {task}",
			ApprovalHuman: "by a person", ApprovalPolicy: "by policy",
			ReferenceRequired: "one image",
			AgentCompleted:    "completed {run}: {views}",
			AgentPaused:       "paused {run} at {view}: {reason}",
			ConsistencyRetry:  "retry {view} {attempt}/{maximum}: {reasons}",
			ConsistencyFailed: "failed {view}: {reasons}",
			SubjectInvalid:    "bad subject {subject}; choices {choices}",
		},
		Multiview: catalog.ChatThreeViewMultiviewSchema{ViewOrder: []string{"front", "left", "back", "right"},
			ViewSeparator: " / "}}
	views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo},
		Schema: schema, Assets: assets, Review: catalog.ChatImageReviewSchema{
			Limits: catalog.ImageInspectLimitsSchema{MaxBytes: 1 << 20, MaxPixels: 1 << 20}}})
	if err != nil {
		t.Fatal(err)
	}
	if withPlan {
		now := time.Now().UTC()
		production, err := repo.CreateProductionBudget(ctx, budget.ProductionBudget{
			ID: "pb-cont", OwnerID: "owner-a", SessionID: "session-a",
			Plan: budget.Plan{Title: "three views", Steps: []budget.PlanStep{
				{Capability: "image-to-image", OfferingID: "offering-i2i", Estimate: budget.Legacy(4)},
				{Capability: "image-to-image", OfferingID: "offering-i2i", Estimate: budget.Legacy(4)},
				{Capability: "image-to-image", OfferingID: "offering-i2i", Estimate: budget.Legacy(4)},
			}},
			Limits: []budget.Amount{budget.Legacy(100)}, State: budget.StatePending, CreatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.DecideProductionBudget(ctx, "owner-a", production.ID, "owner-a", true, now); err != nil {
			t.Fatal(err)
		}
	}
	run, err := runtime.Create(ctx, skillrun.CreateCommand{OwnerID: "owner-a", SessionID: "session-a",
		RecipeID: recipe.ID, ExpectedRevision: recipe.Revision, SelectedOfferingID: "offering-i2i",
		SourceAssetID: "source-a", IdempotencyKey: "cont-create"})
	if err != nil {
		t.Fatal(err)
	}
	return &continuationFixture{repo: repo, runtime: runtime, gateway: gateway, assets: assets,
		views: views, run: run}
}

func budgetContinuationPolicy() approvalpolicy.Policy {
	return approvalpolicy.New("budget", []string{"auto", "manual", "budget"},
		[]string{"text-to-3d", "image-to-3d", "retexture"},
		[]string{"text-to-image", "image-to-image", "image-to-3d", "text-to-3d", "retexture"})
}

func (f *continuationFixture) startFront(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	_, grant, err := f.runtime.RequestAgentGrantWithBudget(ctx, "owner-a", f.run.ID, "front", "turn-a")
	if err != nil || grant.Status != executiongrant.Approved || !grant.ApprovedByBudget() {
		t.Fatalf("front grant = %+v err=%v", grant, err)
	}
	_, accepted, err := f.runtime.SubmitStep(ctx, "owner-a", f.run.ID, "front")
	if err != nil {
		t.Fatal(err)
	}
	return string(accepted.TaskID)
}

func (f *continuationFixture) finish(t *testing.T, taskID, assetID string) {
	t.Helper()
	if _, err := f.repo.db.Exec(`UPDATE generation_tasks SET state='SUCCEEDED' WHERE id=?`, taskID); err != nil {
		t.Fatal(err)
	}
	attempt := "attempt-" + taskID
	if _, err := f.repo.db.Exec(`INSERT INTO generation_attempts
		(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at)
		VALUES (?,?,1,'{}','SUCCEEDED','s','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		attempt, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.db.Exec(`INSERT INTO generation_assets
		(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES (?,?,?,'image',1,1,?,'image/png',4,'sha','2026-01-01T00:00:00Z')`,
		assetID, taskID, attempt, "blobs/"+assetID); err != nil {
		t.Fatal(err)
	}
}

func (f *continuationFixture) group(t *testing.T) threeview.Group {
	t.Helper()
	groups, err := f.views.List(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.RunID == f.run.ID {
			return group
		}
	}
	t.Fatalf("group %s missing from the listing", f.run.ID)
	return threeview.Group{}
}

func slotOf(group threeview.Group, key string) threeview.Slot {
	for _, slot := range group.Slots {
		if slot.Key == key {
			return slot
		}
	}
	return threeview.Slot{}
}

func (f *continuationFixture) step(t *testing.T, key string) skillrun.Step {
	t.Helper()
	run, err := f.runtime.Get(context.Background(), "owner-a", f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range run.Steps {
		if step.Spec.Key == key {
			return step
		}
	}
	t.Fatalf("step %q missing", key)
	return skillrun.Step{}
}

func TestBudgetContinuationFinishesTheRequiredViewsInOrder(t *testing.T) {
	f := newContinuationFixture(t, true)
	ctx := context.Background()
	if ok, err := f.views.CanContinue(ctx, "owner-a", "session-a", f.run.ID); err != nil || !ok {
		t.Fatalf("covered plan cannot continue: %v %v", ok, err)
	}
	frontTask := f.startFront(t)
	f.finish(t, frontTask, "asset-front")
	result, err := f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, frontTask, "turn-a")
	if err != nil || result.Paused || result.TaskID == "" || result.View != "left" {
		t.Fatalf("after front: %+v err=%v", result, err)
	}
	leftTask := result.TaskID
	f.finish(t, leftTask, "asset-left")
	result, err = f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, leftTask, "turn-a")
	if err != nil || result.Paused || result.TaskID == "" || result.View != "back" {
		t.Fatalf("after left: %+v err=%v", result, err)
	}
	backTask := result.TaskID
	f.finish(t, backTask, "asset-back")
	result, err = f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, backTask, "turn-a")
	if err != nil || result.Paused || result.TaskID != "" || len(result.Completed) != 3 {
		t.Fatalf("after back: %+v err=%v", result, err)
	}
	if !result.Group.Complete || result.Group.Slots[3].Status != threeview.NotStarted {
		t.Fatalf("group after completion = %+v", result.Group)
	}
	// A left and back view really used the front output as their source.
	for _, key := range []string{"left", "back"} {
		step := f.step(t, key)
		execution, err := f.runtime.Runs.ResolveExecution(ctx, "owner-a", f.run.ID, key)
		if err != nil || execution.SourceAssetID != "asset-front" {
			t.Fatalf("%s source = %q err=%v", key, execution.SourceAssetID, err)
		}
		if step.Attempt != 0 {
			t.Fatalf("%s attempt = %d", key, step.Attempt)
		}
	}
	if f.gateway.submits != 3 {
		t.Fatalf("submissions = %d, want one per required view", f.gateway.submits)
	}
	if right := f.step(t, "right"); right.TaskID != "" || right.Phase != skillrun.Planned {
		t.Fatalf("the optional right view was started: %+v", right)
	}
}

func TestRejectedViewIsRedrawnAloneWithinTheAttemptLimit(t *testing.T) {
	f := newContinuationFixture(t, true)
	ctx := context.Background()
	frontTask := f.startFront(t)
	f.finish(t, frontTask, "asset-front")
	result, err := f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, frontTask, "turn-a")
	if err != nil || result.TaskID == "" {
		t.Fatalf("left submit: %+v %v", result, err)
	}
	leftTask := result.TaskID
	f.finish(t, leftTask, "asset-left")
	result, err = f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, leftTask, "turn-a")
	if err != nil || result.TaskID == "" {
		t.Fatalf("back submit: %+v %v", result, err)
	}
	backTask := result.TaskID
	f.finish(t, backTask, "asset-back")
	f.assets.bad["asset-back"] = continuationPicture(t, 64, 64, 28, 20, 8)
	// The view just finished is rejected: only it is restarted, as attempt 1.
	result, err = f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, backTask, "turn-a")
	if err != nil || !result.Retried || result.Attempt != 1 || result.TaskID == "" || result.Paused {
		t.Fatalf("rejected back: %+v err=%v", result, err)
	}
	if front, left := f.step(t, "front"), f.step(t, "left"); front.Attempt != 0 || left.Attempt != 0 ||
		string(front.TaskID) != frontTask || string(left.TaskID) != leftTask {
		t.Fatalf("accepted views were touched: front=%+v left=%+v", front, left)
	}
	if right := f.step(t, "right"); right.TaskID != "" {
		t.Fatalf("the optional right view was started: %+v", right)
	}
	retryTask := result.TaskID
	f.finish(t, retryTask, "asset-back-2")
	f.assets.bad["asset-back-2"] = continuationPicture(t, 64, 64, 28, 20, 8)
	result, err = f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, retryTask, "turn-a")
	if err != nil || !result.Paused || result.Reason != threeview.ReasonConsistencyExhausted || result.TaskID != "" {
		t.Fatalf("exhausted back: %+v err=%v", result, err)
	}
	if back := f.step(t, "back"); back.Attempt != 1 || back.Review != skillrun.Rejected {
		t.Fatalf("back after exhaustion = %+v", back)
	}
	if f.gateway.submits != 4 {
		t.Fatalf("submissions = %d, want front, left, back and one redraw", f.gateway.submits)
	}
	if result.FailedReasons == "" || !strings.Contains(result.FailedReasons, "subject_size_delta") {
		t.Fatalf("failed reasons = %q", result.FailedReasons)
	}
}

// Replacing a front view the consistency check rejected makes it stand for the
// view, so the dependent views reopen: the projection must agree with the
// execution path, which accepts a replacement whatever the rejected attempt
// recorded (#243).
func TestReplacingARejectedFrontViewReopensTheDependentViews(t *testing.T) {
	f := newContinuationFixture(t, true)
	ctx := context.Background()
	frontTask := f.startFront(t)
	f.finish(t, frontTask, "asset-front-rejected")
	// A subject in the corner fails the front view's own checklist.
	f.assets.bad["asset-front-rejected"] = continuationPicture(t, 64, 64, 0, 0, 8)
	if _, err := f.runtime.SyncOutput(ctx, "owner-a", f.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.Review(ctx, skillrun.ReviewCommand{OwnerID: "owner-a", RunID: f.run.ID,
		StepKey: "front", Decision: skillrun.Rejected}); err != nil {
		t.Fatal(err)
	}
	group := f.group(t)
	front, left, back := slotOf(group, "front"), slotOf(group, "left"), slotOf(group, "back")
	if front.Review != string(skillrun.Rejected) || left.Startable || left.Retryable || back.Startable {
		t.Fatalf("a rejected front must keep the dependent views closed: front=%+v left=%+v back=%+v",
			front, left, back)
	}

	if _, err := f.repo.db.Exec(`INSERT INTO generation_input_assets
		(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES ('asset-front-upload','owner-a','blobs/asset-front-upload','image/png',4,'abcd',
		'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	group, err := f.views.Replace(ctx, executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a",
		Source: executiongrant.HumanBrowser}, "session-a", f.run.ID, "front", "asset-front-upload")
	if err != nil {
		t.Fatal(err)
	}
	front, left, back = slotOf(group, "front"), slotOf(group, "left"), slotOf(group, "back")
	if front.Status != threeview.Succeeded || !front.Replaced ||
		front.OutputAssetID != "asset-front-upload" || front.Startable || front.Retryable {
		t.Fatalf("replaced front = %+v", front)
	}
	if !left.Startable || !back.Startable || left.Retryable || back.Retryable {
		t.Fatalf("a replaced front must reopen left and back: left=%+v back=%+v", left, back)
	}

	// Starting left goes through the same execution path and really uses the
	// uploaded picture as its source.
	started, err := f.views.Advance(ctx, executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a",
		Source: executiongrant.HumanBrowser}, "session-a", f.run.ID, "left", threeview.Start, 0)
	if err != nil {
		t.Fatal(err)
	}
	if left = slotOf(started, "left"); left.TaskID == "" || left.Status != threeview.Running {
		t.Fatalf("left after the start = %+v", left)
	}
	execution, err := f.runtime.Runs.ResolveExecution(ctx, "owner-a", f.run.ID, "left")
	if err != nil || execution.SourceAssetID != "asset-front-upload" {
		t.Fatalf("left source = %q err=%v", execution.SourceAssetID, err)
	}
	if f.gateway.submits != 2 {
		t.Fatalf("submissions = %d, want front and the started left view", f.gateway.submits)
	}
}

// Strict (manual) mode never continues the views from an approved plan, even
// when the plan covers them: the person still starts each view. The front view
// itself keeps the shipped 2D policy (an image step is auto-approved), so the
// per-view human gate is the three-view card action, not a budget grant.
func TestStrictModeNeverContinuesViewsByBudget(t *testing.T) {
	f := newContinuationFixtureWithMode(t, true, "manual")
	ctx := context.Background()
	if ok, err := f.views.CanContinue(ctx, "owner-a", "session-a", f.run.ID); err != nil || ok {
		t.Fatalf("strict mode reported a budget continuation: %v %v", ok, err)
	}
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	_, grant, err := f.runtime.RequestAgentGrant(ctx, "owner-a", f.run.ID, "front", "turn-a")
	if err != nil || grant.Status != executiongrant.Approved || grant.ApprovedByBudget() {
		t.Fatalf("front grant = %+v err=%v", grant, err)
	}
	_, accepted, err := f.runtime.SubmitStep(ctx, "owner-a", f.run.ID, "front")
	if err != nil {
		t.Fatal(err)
	}
	f.finish(t, string(accepted.TaskID), "asset-front")
	result, err := f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, string(accepted.TaskID), "turn-a")
	if err != nil || !result.Paused || result.Reason != threeview.ReasonBudgetNotCovered || result.TaskID != "" {
		t.Fatalf("strict continuation = %+v err=%v", result, err)
	}
	if left := f.step(t, "left"); left.TaskID != "" || left.Phase != skillrun.Planned {
		t.Fatalf("strict mode submitted a later view without a person: %+v", left)
	}
	if f.gateway.submits != 1 {
		t.Fatalf("submissions = %d, want only the front view", f.gateway.submits)
	}
}

func TestContinuationWithoutAnActivePlanPausesWithoutSubmitting(t *testing.T) {
	f := newContinuationFixture(t, false)
	ctx := context.Background()
	if ok, err := f.views.CanContinue(ctx, "owner-a", "session-a", f.run.ID); err != nil || ok {
		t.Fatalf("no plan reported covered: %v %v", ok, err)
	}
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	// The front view still runs through the ordinary Agent grant (image steps
	// are auto-approved in manual mode); the continuation must not submit more.
	_, grant, err := f.runtime.RequestAgentGrant(ctx, "owner-a", f.run.ID, "front", "turn-a")
	if err != nil || grant.Status != executiongrant.Approved {
		t.Fatalf("front grant = %+v err=%v", grant, err)
	}
	_, accepted, err := f.runtime.SubmitStep(ctx, "owner-a", f.run.ID, "front")
	if err != nil {
		t.Fatal(err)
	}
	f.finish(t, string(accepted.TaskID), "asset-front")
	result, err := f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, string(accepted.TaskID), "turn-a")
	if err != nil || !result.Paused || result.Reason != threeview.ReasonBudgetNotCovered || result.TaskID != "" {
		t.Fatalf("continuation without plan = %+v err=%v", result, err)
	}
	if left := f.step(t, "left"); left.TaskID != "" || left.Phase != skillrun.Planned {
		t.Fatalf("left was submitted without a plan: %+v", left)
	}
	if f.gateway.submits != 1 {
		t.Fatalf("submissions = %d, want only the front view", f.gateway.submits)
	}
}

// The Agent tool starts the front view, then reuses its own call to finish the
// required views inside the approved plan and returns one completed receipt
// with the accepted pictures (#243). Strict mode never enters this path.
func TestAgentToolContinuesTheBudgetViewsInOneCall(t *testing.T) {
	f := newContinuationFixture(t, true)
	executor, err := agenttools.New(agenttools.Config{Params: agentparams.NewStore(),
		Generate: unusedSkillGenerator{}, TaskObservationTimeout: time.Minute,
		Context:                 catalog.ChatContextSchema{AssetURLTemplate: "/api/v1/tasks/{task_id}/assets/{asset_id}"},
		ConsentReconcileContent: "Task observation unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.SetSkillRuntime(f.runtime, &skillRecoveryReferences{},
		&skillRecoveryOfferings{}, []catalog.ChatSkillRecipeSchema{{ID: "image-three-view", Revision: "r1"}},
		f.repo.TurnStopRequested); err != nil {
		t.Fatal(err)
	}
	executor.SetThreeView(catalog.ChatThreeViewSchema{
		RecipeID: "image-three-view", ModelSourceStepKey: "front", Label: "views", MaximumListed: 5,
		ModelSourceNote: "front", DefaultSubjectType: "generic", SubjectArgument: "subject_type",
		SubjectTypes: []catalog.ChatThreeViewSubjectType{
			{ID: "generic", Label: "Generic"}, {ID: "humanoid", Label: "Humanoid"}},
		Messages: catalog.ChatThreeViewMessages{
			AgentStarted: "started {run} {view} {approval} {credits} {task}", ApprovalHuman: "person",
			ApprovalPolicy: "policy", ReferenceRequired: "one image",
			AgentCompleted: "completed {run}: {views}", AgentPaused: "paused {run} {view} {reason}",
			ConsistencyRetry:  "retry {view} {attempt}/{maximum} {reasons}",
			ConsistencyFailed: "failed {view} {reasons}", SubjectInvalid: "bad {subject} {choices}",
		},
		Continuation: catalog.ChatThreeViewContinuationSchema{Enabled: true,
			Reasons: map[string]string{"budget_not_covered": "budget", "view_not_finished": "unfinished",
				"view_already_started": "busy", "consistency_retries_exhausted": "exhausted"}},
		Multiview: catalog.ChatThreeViewMultiviewSchema{ViewOrder: []string{"front", "left", "back", "right"},
			ViewSeparator: " / "},
	})
	executor.SetThreeViewService(f.views)
	// The fixture made a run already; give the tool a fresh session turn key.
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call:    agents.ToolCall{ID: "continuation-call", Name: "create_three_view", Arguments: []byte(`{"subject_type":"humanoid"}`)},
		Options: chat.TurnOptions{AIModel: "original-model", CreativeDomain: "image"},
		History: []chat.Message{{Role: "user", ImageURLs: []string{"/owned/source-a"}}}}
	ctx := context.Background()
	var observed []chat.ToolObservation
	observe := func(observation chat.ToolObservation) error {
		observed = append(observed, observation)
		return nil
	}
	first, err := executor.Execute(ctx, command, observe)
	if err != nil || !first.AwaitTask || first.TaskID == "" {
		t.Fatalf("front: %+v err=%v", first, err)
	}
	run, err := f.runtime.Runs.GetByIdempotency(ctx, "owner-a", threeViewRunKeyForTest("session-a", "continuation-call"))
	if err != nil {
		t.Fatal(err)
	}
	if run.Steps[0].Spec.Key != "front" || run.Steps[0].GrantRequestID == "" {
		t.Fatalf("front run = %+v", run.Steps[0])
	}
	command.BoundTaskID = first.TaskID
	command.FrozenIdempotencyKey = ""
	for range 2 {
		f.finish(t, command.BoundTaskID, "asset-"+command.BoundTaskID)
		next, err := executor.Execute(ctx, command, observe)
		if err != nil || !next.AwaitTask || next.TaskID == "" {
			t.Fatalf("next view after %s: %+v err=%v", command.BoundTaskID, next, err)
		}
		command.BoundTaskID = next.TaskID
	}
	f.finish(t, command.BoundTaskID, "asset-back")
	final, err := executor.Execute(ctx, command, observe)
	if err != nil || final.AwaitTask || !strings.Contains(final.AgentContent, "completed") ||
		!strings.Contains(final.AgentContent, run.ID) || len(final.Pictures) != 3 {
		t.Fatalf("final: %+v err=%v", final, err)
	}
	if f.gateway.submits != 3 || len(observed) != 3 {
		t.Fatalf("submissions = %d observations = %d", f.gateway.submits, len(observed))
	}
	if right := f.step(t, "right"); right.TaskID != "" {
		t.Fatalf("optional right started: %+v", right)
	}
}

func threeViewRunKeyForTest(session, call string) string {
	return "chat-skill:" + session + ":" + call
}
