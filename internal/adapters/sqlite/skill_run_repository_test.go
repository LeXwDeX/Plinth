package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

type testRecipeResolver struct{ recipe skillrun.Recipe }

func (r testRecipeResolver) ResolveRecipe(_ context.Context, id string, offering generation.OfferingID) (skillrun.Recipe, bool, error) {
	return r.recipe, r.recipe.ID == id && offering == "offering-i2i", nil
}

func skillFixture(t *testing.T) (*Repository, *skillrun.Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skill.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	for _, pair := range [][2]string{{"owner-a", "session-a"}, {"owner-b", "session-b"}} {
		_, err = repo.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at)
			VALUES (?,?,?,'skill','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, pair[1], pair[0], pair[0])
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"owner-a", "source-a"}, {"owner-b", "source-b"}} {
		_, err = repo.db.Exec(`INSERT INTO generation_input_assets
			(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at)
			VALUES (?,?,?,'image/png',4,'abcd','2026-01-01T00:00:00Z')`, pair[1], pair[0], "blobs/"+pair[1])
		if err != nil {
			t.Fatal(err)
		}
	}
	service, err := skillrun.NewService(repo, testRecipeResolver{skillrun.Recipe{
		ID: "three-view", Revision: "r1", Steps: []skillrun.StepSpec{{
			Key: "front", Label: "Front", Capability: "image-to-image",
			OfferingID: "offering-i2i", OfferingRevision: "r7",
			PromptTemplate: "front view of source", OutputRole: "image",
		}, {
			Key: "side", Label: "Side", Capability: "image-to-image",
			OfferingID: "offering-i2i", OfferingRevision: "r7",
			PromptTemplate: "side view of source", OutputRole: "image",
		}}}})
	if err != nil {
		t.Fatal(err)
	}
	return repo, service, path
}

func skillCreateCommand() skillrun.CreateCommand {
	return skillrun.CreateCommand{OwnerID: "owner-a", SessionID: "session-a",
		RecipeID: "three-view", ExpectedRevision: "r1", SelectedOfferingID: "offering-i2i", SourceAssetID: "source-a",
		IdempotencyKey: "create-key"}
}

func TestSkillRunFreezesRecipeSourceAndRecoversIdempotently(t *testing.T) {
	repo, service, path := skillFixture(t)
	ctx := context.Background()
	cmd := skillCreateCommand()
	cmd.SourceAssetID = "source-b"
	if _, err := service.Create(ctx, cmd); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("cross-owner source accepted: %v", err)
	}
	cmd = skillCreateCommand()
	cmd.SessionID = "session-b"
	if _, err := service.Create(ctx, cmd); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("cross-owner session accepted: %v", err)
	}
	cmd = skillCreateCommand()
	run, err := service.Create(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if run.RecipeRevision != "r1" || run.RecipeHash == "" || len(run.Steps) != 2 ||
		run.Steps[0].Phase != skillrun.Planned {
		t.Fatalf("incomplete frozen recipe: %+v", run)
	}
	stepPlan, err := service.ResolveExecution(ctx, "owner-a", run.ID, "front")
	if err != nil || stepPlan.SourceAssetID != "source-a" || stepPlan.OperationID == "" ||
		stepPlan.PlanStepID == "" {
		t.Fatalf("step execution identity: %+v %v", stepPlan, err)
	}
	if _, err = service.Get(ctx, "owner-b", run.ID); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("cross-owner run read: %v", err)
	}
	replay, err := service.Create(ctx, cmd)
	if err != nil || replay.ID != run.ID {
		t.Fatalf("create replay: %+v %v", replay, err)
	}
	secondCommand := cmd
	secondCommand.IdempotencyKey = "create-key-2"
	secondRun, err := service.Create(ctx, secondCommand)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.List(ctx, "owner-a", "session-a", "", 1)
	if err != nil || len(page) != 1 || page[0].ID != secondRun.ID {
		t.Fatalf("newest run page: %+v %v", page, err)
	}
	page, err = service.List(ctx, "owner-a", "session-a", secondRun.ID, 1)
	if err != nil || len(page) != 1 || page[0].ID != run.ID {
		t.Fatalf("older run page: %+v %v", page, err)
	}
	if _, err = service.List(ctx, "owner-b", "session-b", secondRun.ID, 1); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("cross-owner history cursor accepted: %v", err)
	}
	cmd.SourceAssetID = "source-b"
	if _, err = service.Create(ctx, cmd); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("changed source under same key: %v", err)
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	service, err = skillrun.NewService(reopened, serviceRecipe())
	if err != nil {
		t.Fatal(err)
	}
	replay, err = service.Create(ctx, skillCreateCommand())
	if err != nil || replay.ID != run.ID {
		t.Fatalf("restart replay: %+v %v", replay, err)
	}
}

func serviceRecipe() testRecipeResolver {
	return testRecipeResolver{skillrun.Recipe{ID: "three-view", Revision: "r1", Steps: []skillrun.StepSpec{
		{Key: "front", Label: "Front", Capability: "image-to-image", OfferingID: "offering-i2i", OfferingRevision: "r7", PromptTemplate: "front view of source", OutputRole: "image"},
		{Key: "side", Label: "Side", Capability: "image-to-image", OfferingID: "offering-i2i", OfferingRevision: "r7", PromptTemplate: "side view of source", OutputRole: "image"},
	}}}
}

func TestSkillRunStepRecoveryUsesGenerationEvidence(t *testing.T) {
	repo, service, path := skillFixture(t)
	ctx := context.Background()
	run, err := service.Create(ctx, skillCreateCommand())
	if err != nil {
		t.Fatal(err)
	}
	op, key, planStep := skillrun.StepIdentity(run.ID, "front")
	stepCommand := skillrun.StepCommand{OwnerID: "owner-a", RunID: run.ID, StepKey: "front", OperationID: op, IdempotencyKey: key}
	prepared := generation.PreparedInput{ID: "prepared-front", OwnerID: "owner-a", SessionID: "session-a",
		Capability: "image-to-image", OfferingID: "offering-i2i", OfferingRevision: "r7",
		InputHash: "input-hash", Assets: []generation.PreparedInputAsset{{ID: "source-a"}}, CreatedAt: time.Now().UTC()}
	if err = repo.CreatePreparedInput(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	wrongSource := prepared
	wrongSource.ID = "prepared-other-source"
	wrongSource.InputHash = "other-source-hash"
	wrongSource.Assets = []generation.PreparedInputAsset{{ID: "source-b"}}
	if err = repo.CreatePreparedInput(ctx, wrongSource); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AttachPrepared(ctx, skillrun.PreparedCommand{StepCommand: stepCommand,
		PreparedInputID: wrongSource.ID, PreparedInputHash: wrongSource.InputHash}); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("prepared input lost source binding: %v", err)
	}
	wrong := skillrun.PreparedCommand{StepCommand: stepCommand, PreparedInputID: prepared.ID, PreparedInputHash: "wrong"}
	if _, err = service.AttachPrepared(ctx, wrong); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("wrong input hash: %v", err)
	}
	freeze := skillrun.PreparedCommand{StepCommand: stepCommand, PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash}
	run, err = service.AttachPrepared(ctx, freeze)
	if err != nil || run.Steps[0].Phase != skillrun.Prepared {
		t.Fatalf("prepare: %+v %v", run, err)
	}
	if _, err = service.AttachPrepared(ctx, freeze); err != nil {
		t.Fatalf("prepare replay: %v", err)
	}
	_, err = repo.db.Exec(`INSERT INTO execution_grants
		(request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,
		prepared_input_hash,idempotency_key,operation_id,plan_step_id,status,source,
		request_version,created_at,updated_at)
		VALUES ('grant-front','owner-a','session-a','offering-i2i','r7','prepared-front',
		'input-hash',?,?,?,'pending','human_browser',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, key, op, planStep)
	if err != nil {
		t.Fatal(err)
	}
	grant := skillrun.GrantCommand{StepCommand: stepCommand, GrantRequestID: "grant-front"}
	run, err = service.AttachGrant(ctx, grant)
	if err != nil || run.Steps[0].Phase != skillrun.GrantPending {
		t.Fatalf("grant: %+v %v", run, err)
	}
	pending, err := service.PendingGrant(ctx, "owner-a", "session-a")
	if err != nil || pending != (skillrun.PendingGrant{
		RunID: run.ID, StepKey: "front", GrantRequestID: "grant-front",
		PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash,
		OfferingID: "offering-i2i", OfferingRevision: "r7", OperationID: op,
		IdempotencyKey: key,
	}) {
		t.Fatalf("pending consent identity: %+v %v", pending, err)
	}
	if _, err = service.PendingGrant(ctx, "owner-b", "session-b"); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("cross-owner pending grant exposed: %v", err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := second.GetSkillRun(ctx, "owner-a", run.ID)
	_ = second.Close()
	if err != nil || recovered.Steps[0].GrantRequestID != "grant-front" ||
		recovered.Steps[0].PreparedInputHash != "input-hash" {
		t.Fatalf("restart lost exact step checkpoint: %+v %v", recovered, err)
	}
	bound := skillrun.TaskCommand{StepCommand: stepCommand, TaskID: "task-front"}
	if _, err = service.BindTask(ctx, bound); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("pending grant bound task: %v", err)
	}
	_, err = repo.db.Exec(`INSERT INTO generation_tasks
		(id,owner_id,capability,offering_json,input_json,request_hash,idempotency_key,state,
		version,session_id,created_at,updated_at)
		VALUES ('task-front','owner-a','image-to-image','{}','{}','hash',?,'RUNNING',1,
		'session-a','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.Exec(`UPDATE execution_grants SET status='bound',task_id='task-front' WHERE request_id='grant-front'`)
	if err != nil {
		t.Fatal(err)
	}
	run, err = service.BindTask(ctx, bound)
	if err != nil || run.Steps[0].Phase != skillrun.TaskBound {
		t.Fatalf("task binding: %+v %v", run, err)
	}
	if _, err = service.BindTask(ctx, bound); err != nil {
		t.Fatalf("task replay: %v", err)
	}
	output := skillrun.OutputCommand{OwnerID: "owner-a", RunID: run.ID, StepKey: "front", AssetID: "output-front"}
	if _, err = service.RecordOutput(ctx, output); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("unfinished task output accepted: %v", err)
	}
	_, err = repo.db.Exec(`INSERT INTO generation_attempts
		(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at)
		VALUES ('attempt-front','task-front',1,'{}','SUCCEEDED','s','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.Exec(`INSERT INTO generation_assets
		(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES ('output-front','task-front','attempt-front','image',1,1,'blobs/output-front',
		'image/png',4,'abcd','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RecordOutput(ctx, output); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("non-terminal task output accepted: %v", err)
	}
	_, err = repo.db.Exec(`UPDATE generation_tasks SET state='SUCCEEDED' WHERE id='task-front'`)
	if err != nil {
		t.Fatal(err)
	}
	run, err = service.RecordOutput(ctx, output)
	if err != nil || run.Steps[0].OutputAssetID != "output-front" {
		t.Fatalf("output: %+v %v", run, err)
	}
	decision := skillrun.ReviewCommand{OwnerID: "owner-a", RunID: run.ID, StepKey: "front", Decision: skillrun.Accepted}
	run, err = service.Review(ctx, decision)
	if err != nil || run.Steps[0].Review != skillrun.Accepted {
		t.Fatalf("review: %+v %v", run, err)
	}
	if _, err = service.Review(ctx, decision); err != nil {
		t.Fatalf("review replay: %v", err)
	}
	decision.Decision = skillrun.Rejected
	if _, err = service.Review(ctx, decision); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("review rewrite: %v", err)
	}
}

func TestSkillRunSourceTaskRequiresSameSessionMaterializedAsset(t *testing.T) {
	repo, service, _ := skillFixture(t)
	ctx := context.Background()
	_, err := repo.db.Exec(`INSERT INTO generation_tasks
		(id,owner_id,capability,offering_json,input_json,request_hash,state,version,session_id,created_at,updated_at)
		VALUES ('source-task','owner-a','text-to-image','{}','{}','source-hash','SUCCEEDED',1,
		'session-a','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.Exec(`INSERT INTO generation_attempts
		(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at)
		VALUES ('source-attempt','source-task',1,'{}','SUCCEEDED','s',
		'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.Exec(`INSERT INTO generation_assets
		(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES ('task-source-asset','source-task','source-attempt','image',1,1,
		'blobs/source','image/png',4,'abcd','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	cmd := skillCreateCommand()
	cmd.SourceTaskID = "source-task"
	cmd.SourceAssetID = "source-a"
	if _, err = service.Create(ctx, cmd); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("source task and asset mismatch: %v", err)
	}
	cmd.SourceAssetID = "task-source-asset"
	run, err := service.Create(ctx, cmd)
	if err != nil || run.SourceTaskID != "source-task" {
		t.Fatalf("same-session source: %+v %v", run, err)
	}
	cmd.IdempotencyKey = "source-with-unbound-prepared"
	cmd.SourcePreparedInputID = "unbound-prepared"
	if _, err = service.Create(ctx, cmd); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("unbound source prepared input accepted: %v", err)
	}
	_, err = repo.db.Exec(`UPDATE generation_tasks SET state='FAILED' WHERE id='source-task'`)
	if err != nil {
		t.Fatal(err)
	}
	cmd.IdempotencyKey = "failed-source-task"
	cmd.SourcePreparedInputID = ""
	if _, err = service.Create(ctx, cmd); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("failed source task accepted: %v", err)
	}
}

// boundSkillStep drives one step to task_bound with a task in the given state.
func boundSkillStep(t *testing.T, repo *Repository, service *skillrun.Service, run skillrun.Run, stepKey, taskState string) skillrun.StepCommand {
	t.Helper()
	ctx := context.Background()
	execution, err := service.ResolveExecution(ctx, "owner-a", run.ID, stepKey)
	if err != nil {
		t.Fatal(err)
	}
	command := skillrun.StepCommand{OwnerID: "owner-a", RunID: run.ID, StepKey: stepKey,
		OperationID: execution.OperationID, IdempotencyKey: execution.IdempotencyKey}
	prepared := generation.PreparedInput{ID: generation.PreparedInputID("prepared-" + execution.OperationID[:12]),
		OwnerID: "owner-a", SessionID: "session-a", Capability: "image-to-image", OfferingID: "offering-i2i",
		OfferingRevision: "r7", InputHash: "hash-" + execution.OperationID[:12],
		Assets: []generation.PreparedInputAsset{{ID: "source-a"}}, CreatedAt: time.Now().UTC()}
	if err = repo.CreatePreparedInput(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AttachPrepared(ctx, skillrun.PreparedCommand{StepCommand: command,
		PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash}); err != nil {
		t.Fatal(err)
	}
	grantID := "grant-" + execution.OperationID[:12]
	taskID := "task-" + execution.OperationID[:12]
	if _, err = repo.db.Exec(`INSERT INTO execution_grants
		(request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,
		prepared_input_hash,idempotency_key,operation_id,plan_step_id,status,source,
		request_version,created_at,updated_at)
		VALUES (?,'owner-a','session-a','offering-i2i','r7',?,?,?,?,?,'pending','human_browser',1,
		'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, grantID, prepared.ID, prepared.InputHash,
		execution.IdempotencyKey, execution.OperationID, execution.PlanStepID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AttachGrant(ctx, skillrun.GrantCommand{StepCommand: command, GrantRequestID: grantID}); err != nil {
		t.Fatal(err)
	}
	if taskState == "" {
		return command // left waiting on its grant
	}
	if _, err = repo.db.Exec(`INSERT INTO generation_tasks
		(id,owner_id,capability,offering_json,input_json,request_hash,idempotency_key,state,
		version,session_id,created_at,updated_at)
		VALUES (?,'owner-a','image-to-image','{}','{}','hash',?,?,1,'session-a',
		'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, taskID, execution.IdempotencyKey, taskState); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.db.Exec(`UPDATE execution_grants SET status='bound',task_id=? WHERE request_id=?`, taskID, grantID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.BindTask(ctx, skillrun.TaskCommand{StepCommand: command, TaskID: generation.TaskID(taskID)}); err != nil {
		t.Fatal(err)
	}
	return command
}

func TestSkillStepRestartStartsANewAttemptOnlyAfterFailureOrDenial(t *testing.T) {
	repo, service, _ := skillFixture(t)
	ctx := context.Background()
	run, err := service.Create(ctx, skillCreateCommand())
	if err != nil {
		t.Fatal(err)
	}
	front := boundSkillStep(t, repo, service, run, "front", "RUNNING")
	failedTask := generation.TaskID("task-" + front.OperationID[:12])
	restart := skillrun.RestartCommand{OwnerID: "owner-a", RunID: run.ID, StepKey: "front", FailedTaskID: failedTask}
	if _, err = service.RestartSkillStep(ctx, restart); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("a running task was restarted: %v", err)
	}
	if _, err = repo.db.Exec(`UPDATE generation_tasks SET state='FAILED' WHERE id=?`, failedTask); err != nil {
		t.Fatal(err)
	}
	wrong := restart
	wrong.FailedTaskID = "task-other"
	if _, err = service.RestartSkillStep(ctx, wrong); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("a task that is not the step's was accepted: %v", err)
	}
	foreign := restart
	foreign.OwnerID = "owner-b"
	if _, err = service.RestartSkillStep(ctx, foreign); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("another owner restarted the step: %v", err)
	}
	restarted, err := service.RestartSkillStep(ctx, restart)
	if err != nil {
		t.Fatal(err)
	}
	step := restarted.Steps[0]
	if step.Attempt != 1 || step.Phase != skillrun.Planned || step.TaskID != "" || step.GrantRequestID != "" ||
		step.PreparedInputID != "" || step.OutputAssetID != "" {
		t.Fatalf("restarted step = %+v", step)
	}
	if restarted.Steps[1].Attempt != 0 {
		t.Fatalf("the other step moved: %+v", restarted.Steps[1])
	}
	// The same request again changes nothing: the attempt already moved on.
	again, err := service.RestartSkillStep(ctx, restart)
	if err != nil || again.Steps[0].Attempt != 1 {
		t.Fatalf("repeat restarted twice: %+v %v", again.Steps[0], err)
	}
	// The failed attempt's task is still there, and the new attempt has its own identity.
	var count int
	if err = repo.db.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE id=?`, failedTask).Scan(&count); err != nil || count != 1 {
		t.Fatalf("the failed task was touched: %d %v", count, err)
	}
	execution, err := service.ResolveExecution(ctx, "owner-a", run.ID, "front")
	if err != nil || execution.OperationID == front.OperationID || execution.IdempotencyKey == front.IdempotencyKey ||
		execution.PlanStepID == "" {
		t.Fatalf("new attempt reused the paid identity: %+v %v", execution, err)
	}
	if _, err = service.AttachPrepared(ctx, skillrun.PreparedCommand{StepCommand: front,
		PreparedInputID: "x", PreparedInputHash: "y"}); !errors.Is(err, skillrun.ErrInvalid) {
		t.Fatalf("the replaced attempt's identity still writes: %v", err)
	}
	next := boundSkillStep(t, repo, service, again, "front", "")
	if _, err = service.RestartSkillStep(ctx, skillrun.RestartCommand{OwnerID: "owner-a", RunID: run.ID,
		StepKey: "front", ExpectedAttempt: 1, DeniedGrantID: "grant-" + next.OperationID[:12]}); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("a pending grant was restarted: %v", err)
	}
	if _, err = repo.db.Exec(`UPDATE execution_grants SET status='denied' WHERE request_id=?`, "grant-"+next.OperationID[:12]); err != nil {
		t.Fatal(err)
	}
	denied, err := service.RestartSkillStep(ctx, skillrun.RestartCommand{OwnerID: "owner-a", RunID: run.ID,
		StepKey: "front", ExpectedAttempt: 1, DeniedGrantID: "grant-" + next.OperationID[:12]})
	if err != nil || denied.Steps[0].Attempt != 2 || denied.Steps[0].Phase != skillrun.Planned {
		t.Fatalf("denied restart: %+v %v", denied.Steps[0], err)
	}
}

// taskReader answers Get from a table; the runtime's restart reads nothing else.
type taskReader struct {
	skillrun.GenerationGateway
	tasks map[generation.TaskID]generation.Task
}

func (r taskReader) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	task, ok := r.tasks[id]
	if !ok || task.OwnerID != owner {
		return generation.Task{}, skillrun.ErrMissing
	}
	return task, nil
}

func TestRuntimeRestartFollowsTheRetryRulesOfAStandaloneTask(t *testing.T) {
	repo, service, _ := skillFixture(t)
	ctx := context.Background()
	run, err := service.Create(ctx, skillCreateCommand())
	if err != nil {
		t.Fatal(err)
	}
	front := boundSkillStep(t, repo, service, run, "front", "FAILED")
	side := boundSkillStep(t, repo, service, run, "side", "FAILED")
	frontTask := generation.TaskID("task-" + front.OperationID[:12])
	sideTask := generation.TaskID("task-" + side.OperationID[:12])
	gateway := taskReader{tasks: map[generation.TaskID]generation.Task{
		frontTask: {ID: frontTask, OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskFailed,
			Failure: &generation.Failure{Class: generation.FailureRejected, Retryable: false}},
		sideTask: {ID: sideTask, OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskFailed,
			Failure: &generation.Failure{Retryable: true, Ambiguous: true}},
	}}
	runtime, err := skillrun.NewRuntime(service, gateway, &executiongrant.Service{Grants: repo, Prepared: repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.RestartStep(ctx, "owner-a", run.ID, "front", 0); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("a failure that may not be retried was restarted: %v", err)
	}
	if _, err = runtime.RestartStep(ctx, "owner-a", run.ID, "side", 0); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("an ambiguous failure (the upstream may have the job) was restarted: %v", err)
	}
	if _, err = runtime.RestartStep(ctx, "owner-b", run.ID, "side", 0); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("another owner restarted the step: %v", err)
	}
	gateway.tasks[sideTask] = generation.Task{ID: sideTask, OwnerID: "owner-a", SessionID: "session-a",
		State: generation.TaskFailed, Failure: &generation.Failure{Retryable: true}}
	restarted, err := runtime.RestartStep(ctx, "owner-a", run.ID, "side", 0)
	if err != nil || restarted.Steps[1].Attempt != 1 || restarted.Steps[0].Attempt != 0 {
		t.Fatalf("retryable failure: %+v %v", restarted.Steps, err)
	}
	replay, err := runtime.RestartStep(ctx, "owner-a", run.ID, "side", 0)
	if err != nil || replay.Steps[1].Attempt != 1 {
		t.Fatalf("a repeated restart moved the step again: %+v %v", replay.Steps, err)
	}
	if _, err = runtime.RestartStep(ctx, "owner-a", run.ID, "side", 3); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("restart of an attempt the step never had: %v", err)
	}
	if _, err = runtime.RestartStep(ctx, "owner-a", run.ID, "side", -1); !errors.Is(err, skillrun.ErrInvalid) {
		t.Fatalf("negative attempt: %v", err)
	}
}
