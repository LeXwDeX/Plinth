package assetlistrunner_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/assetlistrunner"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// restartTurnStates are every turn state the real chat service stores.
var restartTurnStates = []string{"queued", "running", "waiting_task", "done", "error"}

// restartProjects answers the item session's project reads; the restart test
// only needs the project to exist as a scope.
type restartProjects struct{}

func (restartProjects) Exists(_ context.Context, _, projectID string) (bool, error) {
	return projectID != "", nil
}

func (restartProjects) Context(_ context.Context, _, projectID string) (chat.ProjectContext, error) {
	return chat.ProjectContext{ID: projectID, Name: "Restart"}, nil
}

// restartStack is one application instance over the test database: the real
// SQLite stores, the real chat and asset-list services, and a counting
// scheduler in place of the orchestrator. Closing it is the process crash.
type restartStack struct {
	repository *sqlite.Repository
	lists      *assetlist.Service
	chat       *chat.Service
	scheduler  *fakeScheduler
	runner     *assetlistrunner.Runner
}

func newRestartStack(
	t *testing.T,
	databasePath string,
	application catalog.ApplicationSchema,
	schema catalog.AssetListSchema,
) *restartStack {
	t.Helper()
	repository, err := sqlitetest.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	lists, err := assetlist.New(assetlist.Config{
		Repository:         repository.AssetListRepository(),
		Runs:               repository.AssetListRunRepository(),
		Presence:           repository.AssetListPresenceRepository(),
		Schema:             schema,
		ProjectMaxItems:    application.Project.Limits.MaximumListItems,
		MaxListsPerProject: schema.Limits.MaxListsPerProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatService, err := chat.NewService(chat.ServiceConfig{
		Repository:     repository,
		Projects:       restartProjects{},
		DefaultTitle:   "Session",
		UserRole:       "user",
		AssistantRole:  "assistant",
		PendingContent: "pending",
		EventFields:    chat.EventFieldPolicy{Type: "type", MessageID: "message_id"},
		EventTypes:     chat.EventTypePolicy{MessageCreated: "message.created"},
		StreamActive:   "active", StreamDone: "done", StreamError: "error",
		TurnQueued: "queued", TurnRunning: "running", TurnWaitingTask: "waiting_task",
		TurnDone: "done", TurnError: "error",
		PlaceholderHealing: chat.PlaceholderHealingPolicy{
			StaleAfter: time.Minute, PromptArgument: "prompt", PromptPreviewCharacters: 20,
			InterruptedMessage: "interrupted", InterruptedPromptTemplate: "interrupted {prompt}",
		},
		StreamCleanupTTL: time.Minute, SubscriberBufferSize: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatService.SetAssetLists(chat.AssetListPolicy{
		Service: lists, Schema: schema, StepsEnabled: false,
	})
	scheduler := &fakeScheduler{}
	runner := assetlistrunner.New(assetlistrunner.Config{
		Runs:      repository.AssetListRunRepository(),
		Lists:     lists,
		Chat:      chatService,
		Scheduler: scheduler,
		Schema:    schema,
		Clock:     time.Now,
		Logger:    slog.New(slog.DiscardHandler),
	})
	return &restartStack{
		repository: repository,
		lists:      lists,
		chat:       chatService,
		scheduler:  scheduler,
		runner:     runner,
	}
}

// TestRestartDoesNotOpenASecondTurnOrReschedule runs one tick, drops the whole
// assembly (as a crash would), rebuilds over the same database and advances
// again: the turn opened by the first instance is still the only one and the
// scheduler is not called twice for it.
func TestRestartDoesNotOpenASecondTurnOrReschedule(t *testing.T) {
	ctx := context.Background()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	if application.AssetList == nil || application.AssetList.Execution == nil {
		t.Fatal("the default catalog must declare application.asset_list.execution")
	}
	schema := *application.AssetList
	execution := *schema.Execution
	execution.Enabled = true
	schema.Execution = &execution

	databasePath := filepath.Join(t.TempDir(), "restart.db")

	first := newRestartStack(t, databasePath, application, schema)
	projects, err := project.New(first.repository.ProjectRepository(), project.Config{
		Schema: *application.Project,
	})
	if err != nil {
		t.Fatal(err)
	}
	projectRow, err := projects.Create(ctx, testOwner, "Restart")
	if err != nil {
		t.Fatal(err)
	}
	proposalSession, err := first.chat.CreateSessionInProject(ctx, testOwner, "owner", "Proposal", projectRow.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := first.lists.Propose(ctx, assetlist.ProposeInput{
		Owner: testOwner, ProjectID: projectRow.ID, SessionID: proposalSession.ID,
		RequestKey: "restart-list", Title: "Restart list",
		Items: []assetlist.ItemInput{{
			Kind: "character", Name: "Hero", Description: "a hero", RouteID: "text-to-3d",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := first.lists.Decide(ctx, assetlist.DecideInput{
		Owner: testOwner, ProjectID: projectRow.ID, ListID: draft.ID,
		Decision: assetlist.DecisionConfirm,
	})
	if err != nil || confirmed.Status != assetlist.StatusConfirmed {
		t.Fatalf("confirmed list = %+v, %v", confirmed, err)
	}
	if run, err := first.lists.RunByList(ctx, testOwner, projectRow.ID, confirmed.ID); err != nil ||
		run.Status != assetlist.RunRunning {
		t.Fatalf("run = %+v, %v; want running", run, err)
	}

	if err := first.runner.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	claimed, err := first.lists.ItemByID(ctx, testOwner, confirmed.ID, confirmed.Items[0].ID)
	if err != nil || claimed.SessionID == "" || claimed.LastTurnMessageID == "" {
		t.Fatalf("item after the first tick = %+v, %v; want a bound session and a recorded turn",
			claimed, err)
	}
	turns, err := first.repository.ListSessionTurns(ctx, testOwner, claimed.SessionID, restartTurnStates)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns after the first tick = %d, %v; want 1", len(turns), err)
	}
	if turns[0].AssistantMessageID != claimed.LastTurnMessageID || len(first.scheduler.calls) != 1 {
		t.Fatalf("turn = %q, recorded = %q, schedules = %d; want the one scheduled turn",
			turns[0].AssistantMessageID, claimed.LastTurnMessageID, len(first.scheduler.calls))
	}
	if err := first.repository.Close(); err != nil {
		t.Fatal(err)
	}

	second := newRestartStack(t, databasePath, application, schema)
	defer second.repository.Close()
	if err := second.runner.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	turns, err = second.repository.ListSessionTurns(ctx, testOwner, claimed.SessionID, restartTurnStates)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns after the restart = %d, %v; want the first instance's only turn",
			len(turns), err)
	}
	if len(second.scheduler.calls) != 0 {
		t.Fatalf("schedules after the restart = %d; want none for a recorded active turn",
			len(second.scheduler.calls))
	}
	reopened, err := second.lists.ItemByID(ctx, testOwner, confirmed.ID, confirmed.Items[0].ID)
	if err != nil || reopened.LastTurnMessageID != claimed.LastTurnMessageID {
		t.Fatalf("item after the restart = %+v, %v; want the recorded turn kept",
			reopened, err)
	}
	if run, err := second.lists.RunByList(ctx, testOwner, projectRow.ID, confirmed.ID); err != nil ||
		run.Status != assetlist.RunRunning {
		t.Fatalf("run after the restart = %+v, %v; want still running", run, err)
	}
}
