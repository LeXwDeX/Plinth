package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/mcpagent"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

type failingSkillBindStore struct {
	*Repository
	fail bool
}

func (s *failingSkillBindStore) BindSkillTask(ctx context.Context, command skillrun.TaskCommand) (skillrun.Run, error) {
	if s.fail {
		return skillrun.Run{}, errors.New("skill binding unavailable")
	}
	return s.Repository.BindSkillTask(ctx, command)
}

// This gateway faults after the real SQLite grant/task transaction commits.
// It keeps Provider work out of a recovery test while retaining cost admission.
type skillRecoveryGateway struct {
	repo              *Repository
	prepares, submits int
	loseResponse      bool
}

func (g *skillRecoveryGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	g.prepares++
	digest := sha256.Sum256(cmd.Input)
	prepared := generation.PreparedInput{ID: "prepared-agent", OwnerID: cmd.OwnerID, SessionID: cmd.SessionID,
		Capability: cmd.Capability, OfferingID: cmd.OfferingID, OfferingRevision: "r7",
		NormalizedInput: cmd.Input, InputHash: hex.EncodeToString(digest[:]),
		Assets: []generation.PreparedInputAsset{{ID: "source-a"}}, CreatedAt: time.Now().UTC()}
	return prepared, g.repo.CreatePreparedInput(ctx, prepared)
}

func (g *skillRecoveryGateway) GetPreparedInput(ctx context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	return g.repo.GetPreparedInput(ctx, owner, id)
}

func (g *skillRecoveryGateway) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
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
		taskID = "paid-three-view"
		offering, _ := json.Marshal(generation.Offering{ID: prepared.OfferingID, Revision: prepared.OfferingRevision,
			ModelID: "original-model", Capability: prepared.Capability, Timeout: time.Hour})
		digest := sha256.Sum256([]byte(string(prepared.OfferingID) + "\x00" + string(prepared.NormalizedInput)))
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = conn.ExecContext(ctx, `INSERT INTO generation_tasks
			(id,owner_id,session_id,capability,offering_json,input_json,request_hash,idempotency_key,state,version,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,'QUEUED',1,?,?)`, taskID, cmd.OwnerID, cmd.SessionID,
			prepared.Capability, string(offering), string(prepared.NormalizedInput), hex.EncodeToString(digest[:]), cmd.IdempotencyKey, now, now)
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
	if g.loseResponse {
		return generation.Accepted{}, errors.New("response lost after paid Task commit")
	}
	return generation.Accepted{TaskID: generation.TaskID(taskID)}, nil
}

func (g *skillRecoveryGateway) VerifyPreparedTaskReplay(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Task, error) {
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

func (g *skillRecoveryGateway) Get(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (generation.Task, error) {
	return g.repo.Get(ctx, owner, task)
}

type skillRecoveryReferences struct {
	calls int
	fail  bool
}

func (r *skillRecoveryReferences) Resolve(context.Context, generation.OwnerID, []string) ([]generation.AssetID, error) {
	r.calls++
	if r.fail {
		return nil, errors.New("references changed after acceptance")
	}
	return []generation.AssetID{"source-a"}, nil
}

type skillRecoveryOfferings struct {
	calls    int
	disabled bool
}

func (o *skillRecoveryOfferings) Offerings(generation.Capability) []generation.Offering {
	o.calls++
	return []generation.Offering{{ID: "offering-i2i", ModelID: "original-model", Enabled: !o.disabled}}
}
func (o *skillRecoveryOfferings) OfferingAvailable(generation.OfferingID) bool { return !o.disabled }

type unusedSkillGenerator struct{}

func (unusedSkillGenerator) Execute(context.Context, chat.ToolCommand, func(chat.ToolObservation) error) (chat.ToolResult, error) {
	return chat.ToolResult{}, errors.New("unexpected generation execution")
}

func TestThreeViewRecoversAcceptedPaidStepWithoutNewPreparation(t *testing.T) {
	for _, fault := range []string{"lost submit response", "failed Skill binding", "failed observation"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			repo, _, path := skillFixture(t)
			insertGrantTurn(t, repo, "turn-a", "owner-a", "session-a")
			store := &failingSkillBindStore{Repository: repo}
			gateway := &skillRecoveryGateway{repo: repo}
			references, offerings := &skillRecoveryReferences{}, &skillRecoveryOfferings{}
			recipe := skillrun.Recipe{ID: "image-three-view", Revision: "r1", Steps: []skillrun.StepSpec{{
				Key: "front", Label: "Front", Capability: generation.ImageToImage, OfferingID: "offering-i2i", OfferingRevision: "r7",
				PromptTemplate: "front view", PromptParameter: "prompt", SourceParameter: "reference_assets", OutputRole: "image"}}}
			makeExecutor := func() *mcpagent.Executor {
				t.Helper()
				runs, err := skillrun.NewService(store, testRecipeResolver{recipe})
				if err != nil {
					t.Fatal(err)
				}
				runtime, err := skillrun.NewRuntime(runs, gateway, &executiongrant.Service{Grants: store.Repository, Prepared: store.Repository})
				if err != nil {
					t.Fatal(err)
				}
				executor, err := agenttools.New(agenttools.Config{Params: agentparams.NewStore(), Generate: unusedSkillGenerator{},
					TaskObservationTimeout: time.Minute, ConsentReconcileContent: "Task observation unavailable"})
				if err != nil {
					t.Fatal(err)
				}
				if err := executor.SetSkillRuntime(runtime, references, offerings,
					[]catalog.ChatSkillRecipeSchema{{ID: recipe.ID, Revision: recipe.Revision}}, store.Repository.TurnStopRequested); err != nil {
					t.Fatal(err)
				}
				return mcpagent.Wrap(executor, nil)
			}
			command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
				Call:    agents.ToolCall{ID: "stable-call", Name: "create_three_view", Arguments: []byte(`{}`)},
				History: []chat.Message{{Role: "user", ImageURLs: []string{"/owned/source-a"}}}, Options: chat.TurnOptions{AIModel: "original-model"}}
			executor := makeExecutor()
			result, err := executor.Execute(ctx, command, nil)
			if err != nil || !result.Suspend || gateway.submits != 0 {
				t.Fatalf("paid call did not wait for human decision: %+v err=%v submits=%d", result, err, gateway.submits)
			}
			pending, err := store.Repository.PendingGrant(ctx, "owner-a", "session-a")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.DecideExecutionGrant(ctx, "owner-a", pending.GrantRequestID, "human-a", executiongrant.Approve); err != nil {
				t.Fatal(err)
			}
			gateway.loseResponse = fault == "lost submit response"
			store.fail = fault == "failed Skill binding"
			result, err = executor.Execute(ctx, command, func(chat.ToolObservation) error {
				if fault == "failed observation" {
					return errors.New("observation write unavailable")
				}
				return nil
			})
			if err == nil || gateway.submits != 1 || result.NoSubmission {
				t.Fatalf("fault did not preserve uncertain acceptance: %+v err=%v submits=%d", result, err, gateway.submits)
			}
			if fault != "lost submit response" && (!result.AwaitTask || result.WaitUntil.IsZero() || result.TaskID != "paid-three-view") {
				t.Fatalf("known acceptance lost wait identity: %+v", result)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			store.Repository, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Repository.Close()
			gateway.repo, gateway.loseResponse, store.fail = store.Repository, false, false
			// Changed current configuration must not affect an accepted step.
			recipe.Revision = "changed-recipe"
			references.fail, offerings.disabled = true, true
			command.Options.AIModel, command.History, command.Call.Arguments = "removed-model", nil, []byte(`invalid`)
			command.TaskWaitDeadline = result.WaitUntil
			command.BoundTaskID = result.TaskID
			executor = makeExecutor()
			identity, err := executor.TaskIdentityForCall(ctx, command)
			if err != nil || identity.TaskID != "paid-three-view" || identity.IdempotencyKey != pending.IdempotencyKey {
				t.Fatalf("MCP wrapper lost original Skill identity: %+v err=%v", identity, err)
			}
			bound, err := executor.BoundTaskForCall(ctx, command)
			if err != nil || bound != "paid-three-view" {
				t.Fatalf("historical binding lookup: %q %v", bound, err)
			}
			observed := 0
			result, err = executor.Execute(ctx, command, func(o chat.ToolObservation) error {
				if !o.Registered || o.TaskID != "paid-three-view" || o.AIModel != "original-model" {
					t.Errorf("observation changed original Task: %+v", o)
				}
				observed++
				return nil
			})
			if err != nil || result.TaskID != "paid-three-view" || result.Suspend || result.AwaitTask || observed != 1 ||
				gateway.submits != 1 || gateway.prepares != 1 || references.calls != 1 || offerings.calls != 1 {
				t.Fatalf("recovery executed mutable selection or paid work: %+v err=%v prepares=%d submits=%d references=%d offerings=%d", result, err, gateway.prepares, gateway.submits, references.calls, offerings.calls)
			}
			recovered, err := store.Repository.GetSkillRunByIdempotency(ctx, "owner-a", "chat-skill:session-a:stable-call")
			if err != nil || recovered.Steps[0].TaskID != "paid-three-view" || recovered.RecipeRevision != "r1" {
				t.Fatalf("original Skill binding not repaired: %+v %v", recovered, err)
			}
			for _, mode := range []string{"foreign owner", "foreign turn", "foreign step key", "foreign task"} {
				invalid := command
				switch mode {
				case "foreign owner":
					invalid.OwnerID = "owner-b"
				case "foreign turn":
					invalid.AssistantMessageID = "turn-other"
				case "foreign step key":
					invalid.FrozenIdempotencyKey = "other-paid-step"
				case "foreign task":
					invalid.BoundTaskID = "task-other"
				}
				identity, err := executor.TaskIdentityForCall(ctx, invalid)
				if mode == "foreign owner" {
					if err != nil || identity.TaskID != "" {
						t.Fatalf("owner-scoped absence exposed Task: %+v %v", identity, err)
					}
				} else if !errors.Is(err, chat.ErrTurnCheckpointConflict) {
					t.Fatalf("%s accepted: %+v %v", mode, identity, err)
				}
			}
		})
	}
}
