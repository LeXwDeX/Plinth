package generation_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fixtureApproval struct {
	inputHash   string
	prepared    generation.PreparedInput
	grant       executiongrant.Grant
	operationID string
}

type submitFixtureState struct {
	sessionMu  sync.Mutex
	sessions   map[string]struct{}
	approvalMu sync.Mutex
	approvals  map[string]fixtureApproval
	keyMu      sync.Mutex
	keySeq     uint64
}

var fixtureKeySequence atomic.Uint64

// preparedAdmissionRepository adds the prepared-input and execution-grant
// persistence ports around fault-injection Repository decorators used by tests.
type preparedAdmissionRepository struct {
	generation.Repository
	base *sqlite.Repository
}

func (r preparedAdmissionRepository) CreatePreparedInput(ctx context.Context, value generation.PreparedInput) error {
	return r.base.CreatePreparedInput(ctx, value)
}
func (r preparedAdmissionRepository) CreateConversionGuarded(ctx context.Context, task generation.Task, event generation.Event, maxInflight int, check generation.ConversionConflictChecker) (generation.Task, bool, error) {
	return r.base.CreateConversionGuarded(ctx, task, event, maxInflight, check)
}
func (r preparedAdmissionRepository) AdmittedPreparedInput(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (generation.PreparedInputID, error) {
	return r.base.AdmittedPreparedInput(ctx, owner, task)
}
func (r preparedAdmissionRepository) GetPreparedInput(ctx context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	return r.base.GetPreparedInput(ctx, owner, id)
}
func (r preparedAdmissionRepository) GetExecutionPreparedSnapshot(ctx context.Context, owner, id string) (executiongrant.PreparedSnapshot, error) {
	return r.base.GetExecutionPreparedSnapshot(ctx, owner, id)
}
func (r preparedAdmissionRepository) PreparePending(ctx context.Context, identity executiongrant.Identity, source, triggerTurnID string) (executiongrant.Grant, error) {
	return r.base.PreparePending(ctx, identity, source, triggerTurnID)
}
func (r preparedAdmissionRepository) DecideExecutionGrant(ctx context.Context, owner, id, actor string, decision executiongrant.Decision) (executiongrant.Grant, error) {
	return r.base.DecideExecutionGrant(ctx, owner, id, actor, decision)
}
func (r preparedAdmissionRepository) GetExecutionGrant(ctx context.Context, owner, id string) (executiongrant.Grant, error) {
	return r.base.GetExecutionGrant(ctx, owner, id)
}
func (r preparedAdmissionRepository) OwnsSession(ctx context.Context, owner, session string) (bool, error) {
	return r.base.OwnsSession(ctx, owner, session)
}

// submitApproved preserves the old lifecycle-test intent while exercising the
// same preparation and human grant boundary required by production submits.
// Tests for bare Submit rejection call Module.Submit directly by design.
func (h *harness) submitApproved(t *testing.T, ctx context.Context, command generation.SubmitCommand) (generation.Accepted, error) {
	t.Helper()
	return h.submitFixtureState.submitApproved(t, ctx, h.module, h.repository, h.clock, command)
}

func (h *harness) submitApprovedWithModule(t *testing.T, ctx context.Context, module *generation.Module, command generation.SubmitCommand) (generation.Accepted, error) {
	t.Helper()
	return h.submitFixtureState.submitApproved(t, ctx, module, h.repository, h.clock, command)
}

func (h *checkpointHarness) submitApproved(t *testing.T, ctx context.Context, command generation.SubmitCommand) (generation.Accepted, error) {
	t.Helper()
	return h.submitFixtureState.submitApproved(t, ctx, h.module, h.repository, h.clock, command)
}

func (state *submitFixtureState) submitApproved(t *testing.T, ctx context.Context, module *generation.Module, repository *sqlite.Repository, clock *fakeClock, command generation.SubmitCommand) (generation.Accepted, error) {
	t.Helper()
	if ctx == nil {
		ctx = context.Background()
	}
	if command.SessionID == "" {
		command.SessionID = "generation-test-session:" + string(command.OwnerID)
	}
	state.ensureSession(t, ctx, repository, clock, string(command.OwnerID), command.SessionID)
	key := command.IdempotencyKey
	if key == "" {
		key = state.nextTestKey(t)
		command.IdempotencyKey = key
	}

	inputDigest := sha256.Sum256(command.Input)
	cacheKey := string(command.OwnerID) + "\x00" + command.SessionID + "\x00" + key
	state.approvalMu.Lock()
	defer state.approvalMu.Unlock()
	if state.approvals == nil {
		state.approvals = map[string]fixtureApproval{}
	}
	approval, found := state.approvals[cacheKey]
	if found && approval.inputHash != hex.EncodeToString(inputDigest[:]) {
		prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{
			OwnerID: command.OwnerID, SessionID: command.SessionID,
			Capability: command.Capability, OfferingID: command.OfferingID, Input: command.Input,
		})
		if err != nil {
			return generation.Accepted{}, err
		}
		return module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
			OwnerID: command.OwnerID, SessionID: command.SessionID, PreparedInputID: prepared.ID,
			IdempotencyKey: key, OperationID: approval.operationID, AuthorizationRef: approval.grant.RequestID,
		})
	}
	if !found {
		prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{
			OwnerID: command.OwnerID, SessionID: command.SessionID,
			Capability: command.Capability, OfferingID: command.OfferingID,
			Input: command.Input,
		})
		if err != nil {
			return generation.Accepted{}, err
		}
		grants := executiongrant.Service{Grants: repository, Prepared: repository}
		principal := executiongrant.Principal{OwnerID: string(command.OwnerID), ActorID: "generation-test-human", Source: executiongrant.HumanBrowser}
		operationDigest := sha256.Sum256(append(append([]byte(nil), command.Input...), []byte("\x00"+key)...))
		operationID := "generation-test-operation:" + hex.EncodeToString(operationDigest[:16])
		grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
			Principal: principal, SessionID: command.SessionID,
			OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
			PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
			IdempotencyKey: key, OperationID: operationID,
		})
		if err != nil {
			return generation.Accepted{}, err
		}
		if grant.Status != executiongrant.Approved {
			if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
				return generation.Accepted{}, err
			}
		}
		approval = fixtureApproval{inputHash: hex.EncodeToString(inputDigest[:]), prepared: prepared, grant: grant, operationID: operationID}
		state.approvals[cacheKey] = approval
	}
	return module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: command.OwnerID, SessionID: command.SessionID,
		PreparedInputID: approval.prepared.ID, IdempotencyKey: key,
		OperationID: approval.operationID, AuthorizationRef: approval.grant.RequestID,
	})
}

func authorizePrepared(t *testing.T, ctx context.Context, repository *sqlite.Repository, prepared generation.PreparedInput, key string) generation.SubmitPreparedCommand {
	t.Helper()
	grants := executiongrant.Service{Grants: repository, Prepared: repository}
	principal := executiongrant.Principal{OwnerID: string(prepared.OwnerID), ActorID: "generation-test-human", Source: executiongrant.HumanBrowser}
	digest := sha256.Sum256([]byte(prepared.InputHash + "\x00" + key))
	operationID := "generation-test-operation:" + hex.EncodeToString(digest[:16])
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: prepared.SessionID,
		OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: key, OperationID: operationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != executiongrant.Approved {
		grant, err = grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve)
		if err != nil {
			t.Fatal(err)
		}
	}
	return generation.SubmitPreparedCommand{OwnerID: prepared.OwnerID, SessionID: prepared.SessionID,
		PreparedInputID: prepared.ID, IdempotencyKey: key, OperationID: operationID, AuthorizationRef: grant.RequestID}
}

func (state *submitFixtureState) ensureSession(t *testing.T, ctx context.Context, repository *sqlite.Repository, clock *fakeClock, ownerID, sessionID string) {
	t.Helper()
	if ownerID == "" || sessionID == "" {
		t.Fatal("approved submit requires an owner and session")
	}
	state.sessionMu.Lock()
	defer state.sessionMu.Unlock()
	if state.sessions == nil {
		state.sessions = map[string]struct{}{}
	}
	key := ownerID + "\x00" + sessionID
	if _, ok := state.sessions[key]; ok {
		return
	}
	owned, err := repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		t.Fatalf("check fixture session ownership: %v", err)
	}
	if !owned {
		now := clock.Now()
		if _, err := repository.CreateSession(ctx, chat.Session{
			ID: sessionID, OwnerID: ownerID, OwnerName: "Generation test human",
			Title: "Generation test session", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create fixture session: %v", err)
		}
	}
	state.sessions[key] = struct{}{}
}

func (state *submitFixtureState) nextTestKey(t *testing.T) string {
	t.Helper()
	state.keyMu.Lock()
	defer state.keyMu.Unlock()
	state.keySeq++
	return fmt.Sprintf("generation-test:%s:%d", t.Name(), fixtureKeySequence.Add(1))
}
