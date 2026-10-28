package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	sqliteDriver "modernc.org/sqlite"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

type sourcePages struct {
	*Repository
	older skillrun.Run
}

func (s sourcePages) GetSkillRunByTask(_ context.Context, owner generation.OwnerID, session string, taskID generation.TaskID) (skillrun.Run, error) {
	if owner == s.older.OwnerID && session == s.older.SessionID && taskID == "side-task" {
		return s.older, nil
	}
	return skillrun.Run{}, skillrun.ErrMissing
}

func (s sourcePages) ListSkillRuns(_ context.Context, _ generation.OwnerID, _, after string, limit int) ([]skillrun.Run, error) {
	start := 0
	if after != "" {
		_, _ = fmt.Sscanf(after, "new-%d", &start)
		start++
	}
	var runs []skillrun.Run
	for i := start; i < start+limit && i <= 1000; i++ {
		if i == 1000 {
			runs = append(runs, s.older)
		} else {
			runs = append(runs, skillrun.Run{ID: fmt.Sprintf("new-%d", i), RecipeID: "another-recipe"})
		}
	}
	return runs, nil
}

type sourceGateway struct{ *skillRecoveryGateway }

// interceptConnector performs the real binding at the exact fallback query boundary.
type interceptConnector struct {
	path                    string
	beforeFallback          func(context.Context) error
	firstSeen, fallbackSeen bool
}

func (c *interceptConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&sqliteDriver.Driver{}).Open(c.path)
	if err != nil {
		return nil, err
	}
	return &interceptConn{Conn: conn, connector: c}, nil
}

func (c *interceptConnector) Driver() driver.Driver { return &sqliteDriver.Driver{} }

type interceptConn struct {
	driver.Conn
	connector *interceptConnector
}

func (c *interceptConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if query == skillRunByStepTask {
		c.connector.firstSeen = true
	}
	if query == skillRunByGrantTask {
		c.connector.fallbackSeen = true
		if err := c.connector.beforeFallback(ctx); err != nil {
			return nil, err
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestSQLiteSourceOfDuringGrantToStepBinding(t *testing.T) {
	ctx := context.Background()
	repo, runs, path := skillFixture(t)
	run, err := runs.Create(ctx, skillCreateCommand())
	if err != nil {
		t.Fatal(err)
	}
	front := boundSkillStep(t, repo, runs, run, "front", "SUCCEEDED")
	side := boundSkillStep(t, repo, runs, run, "side", "RUNNING")
	frontID := generation.TaskID("task-" + front.OperationID[:12])
	sideID := generation.TaskID("task-" + side.OperationID[:12])
	if _, err := repo.db.Exec(`UPDATE skill_run_steps SET phase='grant_pending',task_id=NULL WHERE run_id=? AND step_key='side'`, run.ID); err != nil {
		t.Fatal(err)
	}
	gateway := taskReader{tasks: map[generation.TaskID]generation.Task{
		frontID: {ID: frontID, OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded, Assets: []generation.Asset{{ID: "front-asset", Role: "image", StorageKey: "blob"}}},
		sideID:  {ID: sideID, OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskRunning},
	}}
	makeViews := func(service *skillrun.Service) *threeview.Service {
		t.Helper()
		runtime, err := skillrun.NewRuntime(service, gateway, &executiongrant.Service{Grants: repo, Prepared: repo})
		if err != nil {
			t.Fatal(err)
		}
		views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo}, Schema: catalog.ChatThreeViewSchema{RecipeID: "three-view", ModelSourceStepKey: "front", MaximumListed: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return views
	}
	check := func(label string, views *threeview.Service) {
		t.Helper()
		source, member, err := views.SourceOf(ctx, "owner-a", "session-a", string(sideID))
		if err != nil || !member || !source.Available || source.TaskID != string(frontID) || source.AssetID != "front-asset" {
			t.Fatalf("%s: %+v member=%v err=%v", label, source, member, err)
		}
	}
	check("before binding", makeViews(runs))
	for _, scope := range [][2]string{{"owner-a", "another-session"}, {"owner-b", "session-b"}} {
		if _, err := runs.GetByTask(ctx, generation.OwnerID(scope[0]), scope[1], sideID); !errors.Is(err, skillrun.ErrMissing) {
			t.Fatalf("grant scope %v: %v", scope, err)
		}
	}
	connector := &interceptConnector{path: path}
	connector.beforeFallback = func(ctx context.Context) error {
		if !connector.firstSeen {
			return errors.New("fallback preceded step lookup")
		}
		_, err := runs.BindTask(ctx, skillrun.TaskCommand{StepCommand: side, TaskID: sideID})
		return err
	}
	lookupDB := sql.OpenDB(connector)
	t.Cleanup(func() { _ = lookupDB.Close() })
	lookupRepo := *repo
	lookupRepo.db = lookupDB
	lookupRuns, err := skillrun.NewService(&lookupRepo, testRecipeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	check("binding between queries", makeViews(lookupRuns))
	if !connector.firstSeen || !connector.fallbackSeen {
		t.Fatal("did not exercise both real lookup queries")
	}
	check("after binding", makeViews(runs))
	if _, err := repo.db.Exec(`INSERT INTO generation_tasks
		(id,owner_id,session_id,capability,offering_json,input_json,request_hash,idempotency_key,state,version,created_at,updated_at)
		VALUES ('different-task','owner-a','session-a','image-to-image','{}','{}','different','different','RUNNING',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE skill_run_steps SET task_id='different-task' WHERE run_id=? AND step_key='side'`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.GetByTask(ctx, "owner-a", "session-a", sideID); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("stale grant with different current task: %v", err)
	}
}

func TestSourceLookupUsesTaskIndexes(t *testing.T) {
	repo, _, _ := skillFixture(t)
	queries := []struct {
		query, alias string
		args         []any
	}{
		{"EXPLAIN QUERY PLAN " + skillRunByStepTask, "ss", []any{"task", "owner-a", "session-a"}},
		{"EXPLAIN QUERY PLAN " + skillRunByGrantTask, "g", []any{"task", "owner-a", "owner-a", "session-a"}},
	}
	for _, q := range queries {
		rows, err := repo.db.Query(q.query, q.args...)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SEARCH "+q.alias+" USING") && strings.Contains(detail, "task_id=?") {
				found = true
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatalf("%s lookup did not use a task index", q.alias)
		}
	}
}

func (g sourceGateway) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	return generation.Task{ID: id, OwnerID: owner, SessionID: "session-a", State: generation.TaskSucceeded,
		Assets: []generation.Asset{{ID: generation.AssetID(id + "-asset"), Role: "image", StorageKey: "blob"}}}, nil
}

func TestSourceOfBeyondThousandRuns(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := skillFixture(t)
	older := skillrun.Run{ID: "older", OwnerID: "owner-a", SessionID: "session-a", RecipeID: "three-view", Steps: []skillrun.Step{
		{Spec: skillrun.StepSpec{Key: "front", OutputRole: "image"}, TaskID: "front-task"},
		{Spec: skillrun.StepSpec{Key: "side", OutputRole: "image"}, TaskID: "side-task"},
	}}
	runs, err := skillrun.NewService(sourcePages{Repository: repo, older: older}, testRecipeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := skillrun.NewRuntime(runs, sourceGateway{&skillRecoveryGateway{repo: repo}}, &executiongrant.Service{Grants: repo, Prepared: repo})
	if err != nil {
		t.Fatal(err)
	}
	views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo}, Schema: catalog.ChatThreeViewSchema{
		RecipeID: "three-view", ModelSourceStepKey: "front", MaximumListed: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	source, member, err := views.SourceOf(ctx, "owner-a", "session-a", "side-task")
	if err != nil || !member || !source.Available || source.TaskID != "front-task" || source.AssetID != "front-task-asset" {
		t.Fatalf("old side source: %+v member=%v err=%v", source, member, err)
	}
}

func TestSQLiteSourceOfBeyondThousandRuns(t *testing.T) {
	ctx := context.Background()
	repo, runs, _ := skillFixture(t)
	run, err := runs.Create(ctx, skillCreateCommand())
	if err != nil {
		t.Fatal(err)
	}
	front := boundSkillStep(t, repo, runs, run, "front", "SUCCEEDED")
	side := boundSkillStep(t, repo, runs, run, "side", "RUNNING")
	frontID := "task-" + front.OperationID[:12]
	sideID := "task-" + side.OperationID[:12]
	if _, err := repo.db.Exec(`UPDATE skill_runs SET created_at='2020-01-01T00:00:00Z' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<1001)
		INSERT INTO skill_runs (id,owner_id,session_id,recipe_id,recipe_revision,recipe_hash,selected_offering_id,
			source_asset_id,idempotency_key,request_hash,created_at,updated_at,harness_revision,harness_digest)
		SELECT 'history-'||i,owner_id,session_id,'other-recipe',recipe_revision,recipe_hash,selected_offering_id,
			source_asset_id,'history-key-'||i,request_hash,'2030-01-01T00:00:00Z',updated_at,harness_revision,harness_digest
		FROM skill_runs CROSS JOIN n WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<1001)
		INSERT INTO skill_run_steps (run_id,ordinal,step_key,spec_json,phase,operation_id,idempotency_key,plan_step_id,review)
		SELECT 'history-'||i,0,step_key,spec_json,'planned','history-op-'||i,'history-submit-'||i,'history-step-'||i,'unreviewed'
		FROM skill_run_steps CROSS JOIN n WHERE run_id=? AND step_key='front'`, run.ID); err != nil {
		t.Fatal(err)
	}
	var newer int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM skill_runs WHERE owner_id=? AND session_id=? AND created_at>'2020-01-01T00:00:00Z'`, "owner-a", "session-a").Scan(&newer); err != nil || newer != 1001 {
		t.Fatalf("newer runs: %d %v", newer, err)
	}
	runtime, err := skillrun.NewRuntime(runs, sourceGateway{&skillRecoveryGateway{repo: repo}}, &executiongrant.Service{Grants: repo, Prepared: repo})
	if err != nil {
		t.Fatal(err)
	}
	views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo}, Schema: catalog.ChatThreeViewSchema{RecipeID: "three-view", ModelSourceStepKey: "front", MaximumListed: 1}})
	if err != nil {
		t.Fatal(err)
	}
	source, member, err := views.SourceOf(ctx, "owner-a", "session-a", sideID)
	if err != nil || !member || !source.Available || source.TaskID != frontID || source.AssetID != frontID+"-asset" {
		t.Fatalf("old SQLite run: %+v %v %v", source, member, err)
	}
}

func TestSQLiteSourceOfCurrentStepAndIsolation(t *testing.T) {
	ctx := context.Background()
	repo, runs, _ := skillFixture(t)
	if _, err := repo.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at)
		VALUES ('session-other','owner-a','owner-a','other','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	cmd := skillCreateCommand()
	run, err := runs.Create(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := skillrun.NewRuntime(runs, sourceGateway{&skillRecoveryGateway{repo: repo}}, &executiongrant.Service{Grants: repo, Prepared: repo})
	if err != nil {
		t.Fatal(err)
	}
	views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo}, Schema: catalog.ChatThreeViewSchema{
		RecipeID: "three-view", ModelSourceStepKey: "front", MaximumListed: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"front-task", "side-task", "retry-task", "other-front", "other-side"} {
		_, err = repo.db.Exec(`INSERT INTO generation_tasks
			(id,owner_id,session_id,capability,offering_json,input_json,request_hash,idempotency_key,state,version,created_at,updated_at)
			VALUES (?,'owner-a','session-a','image-to-image','{}','{}',?,?,'SUCCEEDED',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, id, id, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	bind := func(key, id string) {
		t.Helper()
		if _, err := repo.db.Exec(`UPDATE skill_run_steps SET task_id=? WHERE run_id=? AND step_key=?`, id, run.ID, key); err != nil {
			t.Fatal(err)
		}
	}
	bind("side", "side-task")
	source, member, err := views.SourceOf(ctx, "owner-a", "session-a", "side-task")
	if err != nil || !member || source.Available || source.Slot != "front" {
		t.Fatalf("front not ready: %+v %v %v", source, member, err)
	}
	bind("front", "front-task")
	source, member, err = views.SourceOf(ctx, "owner-a", "session-a", "side-task")
	if err != nil || !member || !source.Available || source.TaskID != "front-task" || source.AssetID != "front-task-asset" {
		t.Fatalf("side source: %+v %v %v", source, member, err)
	}
	secondCommand := skillCreateCommand()
	secondCommand.IdempotencyKey = "second-run"
	second, err := runs.Create(ctx, secondCommand)
	if err != nil {
		t.Fatal(err)
	}
	for key, id := range map[string]string{"front": "other-front", "side": "other-side"} {
		if _, err := repo.db.Exec(`UPDATE skill_run_steps SET task_id=? WHERE run_id=? AND step_key=?`, id, second.ID, key); err != nil {
			t.Fatal(err)
		}
	}
	if source, member, err := views.SourceOf(ctx, "owner-a", "session-a", "other-side"); err != nil || !member || source.TaskID != "other-front" {
		t.Fatalf("second run: %+v %v %v", source, member, err)
	}
	if source, member, err := views.SourceOf(ctx, "owner-a", "session-a", "side-task"); err != nil || !member || source.TaskID != "front-task" {
		t.Fatalf("first run: %+v %v %v", source, member, err)
	}
	for _, args := range [][3]string{{"owner-a", "session-other", "side-task"}, {"owner-b", "session-b", "side-task"}, {"owner-a", "session-a", "missing"}} {
		if _, member, err := views.SourceOf(ctx, args[0], args[1], args[2]); err != nil || member {
			t.Fatalf("scope %v: %v %v", args, member, err)
		}
	}
	bind("side", "retry-task")
	if _, member, err := views.SourceOf(ctx, "owner-a", "session-a", "side-task"); err != nil || member {
		t.Fatalf("old attempt: %v %v", member, err)
	}
	if source, member, err := views.SourceOf(ctx, "owner-a", "session-a", "retry-task"); err != nil || !member || source.TaskID != "front-task" {
		t.Fatalf("new attempt: %+v %v %v", source, member, err)
	}
	if _, err := repo.db.Exec(`UPDATE skill_runs SET recipe_id='other' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, member, err := views.SourceOf(ctx, "owner-a", "session-a", "retry-task"); err != nil || member {
		t.Fatalf("other recipe: %v %v", member, err)
	}
	if _, err := repo.db.Exec(`UPDATE skill_runs SET recipe_id='three-view' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE skill_run_steps SET spec_json='"invalid"' WHERE run_id=? AND step_key='side'`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, member, err := views.SourceOf(ctx, "owner-a", "session-a", "retry-task"); err == nil || member {
		t.Fatalf("invalid step DTO: %v %v", member, err)
	}
	if _, err := repo.db.Exec(`UPDATE skill_run_steps SET spec_json='{}' WHERE run_id=? AND step_key='side'`, run.ID); err != nil {
		t.Fatal(err)
	}
	// A missing grant must not be converted to a non-member result.
	if _, err := repo.db.Exec(`UPDATE skill_run_steps SET grant_request_id='missing-grant' WHERE run_id=? AND step_key='side'`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, member, err := views.SourceOf(ctx, "owner-a", "session-a", "retry-task"); err == nil || member {
		t.Fatalf("broken grant: %v %v", member, err)
	}
	if _, _, err := views.SourceOf(ctx, "", "session-a", "retry-task"); !errors.Is(err, threeview.ErrInvalid) {
		t.Fatalf("empty owner: %v", err)
	}
	if _, _, err := views.SourceOf(ctx, "owner-a", "", "retry-task"); !errors.Is(err, threeview.ErrInvalid) {
		t.Fatalf("empty session: %v", err)
	}
	if _, _, err := views.SourceOf(ctx, "owner-a", "session-a", ""); !errors.Is(err, threeview.ErrInvalid) {
		t.Fatalf("empty task: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if _, member, err := views.SourceOf(ctx, "owner-a", "session-a", "retry-task"); err == nil || member {
		t.Fatalf("database error: %v %v", member, err)
	}
}

func TestSQLiteSourceOfGrantBoundBeforeStepBinding(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := skillFixture(t)
	insertGrantTurn(t, repo, "turn-a", "owner-a", "session-a")
	store := &failingSkillBindStore{Repository: repo, fail: true}
	recipe := skillrun.Recipe{ID: "three-view", Revision: "r1", Steps: []skillrun.StepSpec{{
		Key: "front", Label: "Front", Capability: generation.ImageToImage, OfferingID: "offering-i2i", OfferingRevision: "r7",
		PromptTemplate: "front view", PromptParameter: "prompt", SourceParameter: "reference_assets", OutputRole: "image"}}}
	runs, err := skillrun.NewService(store, testRecipeResolver{recipe})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &skillRecoveryGateway{repo: repo}
	runtime, err := skillrun.NewRuntime(runs, gateway, &executiongrant.Service{Grants: repo, Prepared: repo, Policy: testApprovalPolicy(), Modes: sessionModes{repo: repo}})
	if err != nil {
		t.Fatal(err)
	}
	views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo}, Schema: catalog.ChatThreeViewSchema{RecipeID: recipe.ID, ModelSourceStepKey: "front", MaximumListed: 1}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runs.Create(ctx, skillCreateCommand())
	if err != nil {
		t.Fatal(err)
	}
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	if _, err := views.Advance(ctx, principal, "session-a", run.ID, "front", threeview.Start, 0); err == nil {
		t.Fatal("expected failed step binding")
	}
	source, member, err := views.SourceOf(ctx, "owner-a", "session-a", "paid-three-view")
	if err != nil || !member || source.Available || source.Slot != "front" {
		t.Fatalf("grant task: %+v %v %v", source, member, err)
	}
}

func TestSQLiteSourceOfAfterActualRetry(t *testing.T) {
	ctx := context.Background()
	repo, runs, _ := skillFixture(t)
	run, err := runs.Create(ctx, skillCreateCommand())
	if err != nil {
		t.Fatal(err)
	}
	front := boundSkillStep(t, repo, runs, run, "front", "SUCCEEDED")
	side := boundSkillStep(t, repo, runs, run, "side", "FAILED")
	frontID := generation.TaskID("task-" + front.OperationID[:12])
	oldID := generation.TaskID("task-" + side.OperationID[:12])
	gateway := taskReader{tasks: map[generation.TaskID]generation.Task{
		frontID: {ID: frontID, OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded,
			Assets: []generation.Asset{{ID: "front-asset", Role: "image", StorageKey: "blob"}}},
		oldID: {ID: oldID, OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskFailed,
			Failure: &generation.Failure{Retryable: true}},
	}}
	runtime, err := skillrun.NewRuntime(runs, gateway, &executiongrant.Service{Grants: repo, Prepared: repo})
	if err != nil {
		t.Fatal(err)
	}
	views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo}, Schema: catalog.ChatThreeViewSchema{RecipeID: "three-view", ModelSourceStepKey: "front", MaximumListed: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if source, member, err := views.SourceOf(ctx, "owner-a", "session-a", string(oldID)); err != nil || !member || source.TaskID != string(frontID) {
		t.Fatalf("before retry: %+v %v %v", source, member, err)
	}
	restarted, err := runtime.RestartStep(ctx, "owner-a", run.ID, "side", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, member, err := views.SourceOf(ctx, "owner-a", "session-a", string(oldID)); err != nil || member {
		t.Fatalf("old task after retry: %v %v", member, err)
	}
	newStep := boundSkillStep(t, repo, runs, restarted, "side", "SUCCEEDED")
	newID := generation.TaskID("task-" + newStep.OperationID[:12])
	gateway.tasks[newID] = generation.Task{ID: newID, OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded}
	if source, member, err := views.SourceOf(ctx, "owner-a", "session-a", string(newID)); err != nil || !member || source.TaskID != string(frontID) || source.AssetID != "front-asset" {
		t.Fatalf("new task after retry: %+v %v %v", source, member, err)
	}
	if _, member, err := views.SourceOf(ctx, "owner-a", "session-a", string(oldID)); err != nil || member {
		t.Fatalf("old bound grant after retry: %v %v", member, err)
	}
}
