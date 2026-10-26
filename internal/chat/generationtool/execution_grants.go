package generationtool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

func (executor *Executor) agentCommandHash(command chat.ToolCommand) (string, error) {
	if activeHarness(command) {
		command.Options.AIModel = ""
		command.Options.DomainModels = nil
		command.Options.SkillID = ""
		command.Options.Parameters = nil
	}
	encoded, err := json.Marshal(struct {
		Tool       string                     `json:"tool"`
		Arguments  json.RawMessage            `json:"arguments"`
		AIModel    string                     `json:"ai_model"`
		SkillID    string                     `json:"skill_id"`
		Parameters map[string]json.RawMessage `json:"parameters"`
	}{command.Call.Name, command.Call.Arguments, command.Options.ModelFor(toolDomain(command)),
		command.Options.SkillID, command.Options.Parameters})
	if err != nil {
		return "", err
	}
	if command.Options.Harness != nil {
		encoded, err = json.Marshal(struct {
			Command json.RawMessage `json:"command"`
			Harness harness.Binding `json:"harness"`
		}{encoded, turnHarnessBinding(command)})
		if err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (executor *Executor) checkAgentTurnActive(ctx context.Context, command chat.ToolCommand) error {
	if executor.stopRequested == nil || ctx.Err() != nil {
		return ErrOperationConflict
	}
	stopped, err := executor.stopRequested(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		return err
	}
	if stopped {
		return ErrOperationConflict
	}
	return nil
}

// agentConsentInput freezes the Offering's extra-credit consent into the input
// of an Agent step. The Agent path has one way to agree to extra credits: the
// approval card (or the session's automatic approval), both of which decide the
// exact frozen input. So the flag is not asked of the person beforehand and is
// never taken from the model, the session settings or a tool argument; the
// server sets it on every Offering that declares one, and the kernel measures
// what the input then costs extra (PreparedInput.ExtraCredits). Nothing is
// submitted without an approved grant bound to that input. An Offering without
// a consent parameter is left alone.
func agentConsentInput(offering generation.Offering, input json.RawMessage) (json.RawMessage, error) {
	if offering.ExtraConsentParameter() == "" {
		return input, nil
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(input, &values); err != nil {
		return nil, fmt.Errorf("agent input is not a JSON object: %w", err)
	}
	values[offering.ExtraConsentParameter()] = json.RawMessage(`true`)
	return json.Marshal(values)
}

func (executor *Executor) prepareAgentOperation(
	ctx context.Context, command chat.ToolCommand, capability generation.Capability,
	offering generation.Offering, input json.RawMessage, prompt, key string, references referenceUse,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	if command.OwnerID == "" || command.SessionID == "" || command.AssistantMessageID == "" ||
		command.Call.ID == "" || key == "" {
		return chat.ToolResult{NoSubmission: true}, ErrOperationConflict
	}
	if err := executor.checkAgentTurnActive(ctx, command); err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if err := executor.checkCurrentHarness(ctx, command); err != nil {
		return executor.harnessRefusal(command, err)
	}
	hash, err := executor.agentCommandHash(command)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	input, err = agentConsentInput(offering, input)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if activeHarness(command) && !forcedHarnessInputMatches(command, offering, input) {
		return executor.harnessRefusal(command, ErrOperationConflict)
	}
	reasons, err := executor.parameterReasons(command, offering, input)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	preparedGateway := executor.gateway.(preparedGateway)
	prepared, err := preparedGateway.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: generation.OwnerID(command.OwnerID), SessionID: command.SessionID,
		Capability: capability, OfferingID: offering.ID, Input: input, ParameterReasons: reasons,
	})
	if err != nil {
		// An input the Schema refuses (a count out of range, extra credits over
		// the Offering's limit) is something the person can fix. Tell the Agent
		// the reason as a tool failure so it reaches the chat, instead of an
		// empty receipt.
		if result, refused := executor.inputRefusal(command, err); refused {
			return result, nil
		}
		return chat.ToolResult{NoSubmission: true}, err
	}
	if activeHarness(command) && (prepared.OfferingID != offering.ID || prepared.OfferingRevision != offering.Revision || prepared.Capability != capability) {
		return executor.harnessRefusal(command, ErrOperationConflict)
	}
	if activeHarness(command) && !fixedHarnessInputMatches(command, prepared.NormalizedInput) {
		return executor.harnessRefusal(command, ErrOperationConflict)
	}
	// cardSettled releases the session's approval lock, if this call took it.
	cardSettled := func() {}
	extraSpend, priceErr := prepared.HasExtraSpend()
	if priceErr != nil {
		return chat.ToolResult{NoSubmission: true}, priceErr
	}
	// An earlier attempt of the same kind in this session was charged upstream
	// but its result was not stored: a repeat charges again, so it waits for a
	// person on its card whatever the approval mode says (#294).
	recharge, err := executor.unstoredChargedTasks(ctx, command, prepared.Capability)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	var rechargeTaskID string
	if len(recharge) > 0 {
		rechargeTaskID = string(recharge[0])
	}
	if rechargeTaskID != "" || executor.approvalNeedsHumanSpend(ctx, command.OwnerID, command.SessionID, capability, extraSpend) {
		// A step that gets an approval card: when the same thing is already
		// waiting on a card, point the agent at that card instead of creating a
		// second one. The lock is held until this call's own card is detached
		// (or it is known that no card results), never across a task wait.
		cardSettled = executor.lockApprovals(command.OwnerID, command.SessionID)
		defer cardSettled()
		if _, duplicate, err := executor.pendingDuplicate(ctx, command, prepared); err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		} else if duplicate {
			return chat.ToolResult{AgentContent: executor.chat.Agent.ConsentDuplicateContent, NoSubmission: true}, nil
		}
	}
	op, err := executor.operations.InsertAgentOperation(ctx, Operation{
		OwnerID: command.OwnerID, SessionID: command.SessionID,
		AssistantMessageID: command.AssistantMessageID, CallID: command.Call.ID,
		ToolName: command.Call.Name, CommandHash: hash, IdempotencyKey: key,
		PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash,
		OfferingID: prepared.OfferingID, OfferingRevision: prepared.OfferingRevision, Harness: turnHarnessBinding(command),
		RechargeTaskID: rechargeTaskID, ReferenceCount: references.Used, ReferenceTotal: references.Given,
	})
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	return executor.resumeAgentOperation(ctx, command, op, observe, cardSettled)
}

// resumeAgentOperation drives a prepared operation. cardSettled, when not nil,
// is called once it is known whether this call ends on an approval card (the
// detach landed, or the grant was decided and the turn goes on): from then on
// nothing needs the session's approval lock, and a task wait must not hold it.
func (executor *Executor) resumeAgentOperation(
	ctx context.Context, command chat.ToolCommand, op Operation,
	observe func(chat.ToolObservation) error, cardSettled func(),
) (chat.ToolResult, error) {
	if cardSettled == nil {
		cardSettled = func() {}
	}
	if err := executor.checkAgentTurnActive(ctx, command); err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if op.Harness != turnHarnessBinding(command) || op.OwnerID != command.OwnerID || op.SessionID != command.SessionID ||
		op.AssistantMessageID != command.AssistantMessageID || op.CallID != command.Call.ID ||
		op.ToolName != command.Call.Name ||
		op.IdempotencyKey != command.IdempotencyKey() {
		return chat.ToolResult{NoSubmission: true}, ErrOperationConflict
	}
	// A bound grant proves this exact operation already accepted a paid Task.
	// Recover it before consulting mutable harness options or current history.
	// No second submission is possible on this branch.
	var grant executiongrant.Grant
	var err error
	if op.GrantRequestID != "" {
		grant, err = executor.grants.Grants.GetExecutionGrant(ctx, op.OwnerID, op.GrantRequestID)
		if err != nil {
			return chat.ToolResult{}, err
		}
		if !agentGrantMatchesOperation(grant, op) {
			return chat.ToolResult{NoSubmission: true}, ErrOperationConflict
		}
		if op.Detached {
			// The turn stopped waiting for this grant. Its decision and result
			// belong to the pending-approval service now; the turn must not
			// submit, observe or report the task a second time.
			return chat.ToolResult{AgentContent: executor.awaitingApproval(ctx, command, op), NoSubmission: true}, nil
		}
		if grant.Status == executiongrant.Bound {
			if grant.TaskID == "" {
				return chat.ToolResult{NoSubmission: true}, ErrOperationConflict
			}
			command.BoundTaskID = grant.TaskID
			return executor.observeBoundTask(ctx, command, observe)
		}
	}
	if err := executor.checkCurrentHarness(ctx, command); err != nil {
		return executor.harnessRefusal(command, err)
	}
	hash, err := executor.agentCommandHash(command)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if op.CommandHash != hash {
		return chat.ToolResult{NoSubmission: true}, ErrOperationConflict
	}
	preparedGateway := executor.gateway.(preparedGateway)
	prepared, err := preparedGateway.GetPreparedInput(ctx, generation.OwnerID(op.OwnerID), op.PreparedInputID)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if prepared.ID != op.PreparedInputID || string(prepared.OwnerID) != op.OwnerID ||
		prepared.SessionID != op.SessionID || prepared.InputHash != op.PreparedInputHash ||
		prepared.OfferingID != op.OfferingID || prepared.OfferingRevision != op.OfferingRevision {
		return chat.ToolResult{NoSubmission: true}, ErrOperationConflict
	}
	command, err = executor.verifyAgentHarnessPreparedSource(ctx, command, prepared)
	if err != nil {
		return executor.harnessRefusal(command, err)
	}
	if op.GrantRequestID == "" {
		principal := executiongrant.Principal{OwnerID: op.OwnerID,
			ActorID: "agent:" + op.AssistantMessageID, Source: executiongrant.AgentPending}
		grant, err = executor.grants.Prepare(ctx, executiongrant.PrepareCommand{
			Principal: principal, SessionID: op.SessionID,
			OfferingID: string(op.OfferingID), OfferingRevision: op.OfferingRevision,
			PreparedInputID: string(op.PreparedInputID), PreparedInputHash: op.PreparedInputHash,
			IdempotencyKey: op.IdempotencyKey, OperationID: op.OperationID,
			TriggerTurnID: op.AssistantMessageID, Harness: op.Harness,
		})
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
		op, err = executor.operations.AttachAgentGrantRequest(ctx, op, grant.RequestID)
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
	} else {
		// The persisted grant was verified before the frozen-input checks above.
	}
	if grant.Status == executiongrant.Pending && !op.PolicyEvaluated {
		// The session approval mode may already approve this step. A restart
		// between Prepare and approval converges here too. A step that needs a
		// human stays pending, and the policy never looks at it again: a later
		// switch to the automatic mode must not approve a request the user was
		// already shown. A step that repeats a charged-but-unstored attempt is
		// never the policy's to approve (#294).
		if op.RechargeTaskID == "" {
			grant, err = executor.grants.ApplyPolicy(ctx, grant)
			if err != nil {
				return chat.ToolResult{NoSubmission: true}, err
			}
		}
		if grant.Status == executiongrant.Pending {
			if err := executor.operations.MarkAgentPolicyEvaluated(ctx, op); err != nil {
				return chat.ToolResult{NoSubmission: true}, err
			}
		}
	}
	if grant.Status == executiongrant.Pending {
		// A step that needs a human ends the turn as soon as its approval card
		// exists: the person keeps chatting while it waits. Leave the grant
		// pending and detach it; the approval stays on its card until the user
		// decides or it expires, and the pending-approval service submits it
		// and posts the outcome. The detach only lands while the grant is still
		// pending, so a decision that committed first is honoured below.
		_, detached, err := executor.operations.DetachAgentOperation(ctx, op)
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
		if detached {
			return chat.ToolResult{AgentContent: executor.awaitingApproval(ctx, command, op), NoSubmission: true}, nil
		}
		// The detach is refused after a Stop of this turn (the card must not be
		// handed to a person while the turn that owns it is being cancelled; the
		// sweep cancels the pending grant visibly) or after a decision committed
		// first, in which case the turn continues below.
		if err := executor.checkAgentTurnActive(ctx, command); err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
		grant, err = executor.grants.Grants.GetExecutionGrant(ctx, op.OwnerID, op.GrantRequestID)
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
		if !agentGrantMatchesOperation(grant, op) {
			return chat.ToolResult{NoSubmission: true}, ErrOperationConflict
		}
	}
	// No card of this call is still being created: a decision or a Stop
	// committed before the detach, or the policy approved the step.
	cardSettled()
	switch grant.Status {
	case executiongrant.Pending:
		// Keep the durable turn waiting for a human decision, Stop, or the end
		// of the Schema's wait.
		return chat.ToolResult{Suspend: true, NoSubmission: true}, nil
	case executiongrant.Denied:
		return chat.ToolResult{AgentContent: executor.chat.Agent.ConsentDeniedContent, NoSubmission: true}, nil
	case executiongrant.Claimed:
		return chat.ToolResult{Suspend: true}, nil
	case executiongrant.Approved, executiongrant.Bound:
		// The decision is already persisted. SubmitPrepared verifies it once more
		// in the Task creation transaction and handles exact replay.
	default:
		return chat.ToolResult{NoSubmission: true}, fmt.Errorf("unexpected execution grant state %q", grant.Status)
	}
	if err := executor.checkAgentTurnActive(ctx, command); err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	command, err = executor.verifyAgentHarnessPreparedSource(ctx, command, prepared)
	if err != nil {
		return executor.harnessRefusal(command, err)
	}
	accepted, err := preparedGateway.SubmitPrepared(context.WithoutCancel(ctx), generation.SubmitPreparedCommand{
		OwnerID: generation.OwnerID(op.OwnerID), SessionID: op.SessionID,
		PreparedInputID: op.PreparedInputID, IdempotencyKey: op.IdempotencyKey,
		OperationID: op.OperationID, AuthorizationRef: op.GrantRequestID, Harness: op.Harness,
	})
	if err != nil {
		if errors.Is(err, harness.ErrStale) {
			return executor.harnessRefusal(command, err)
		}
		return chat.ToolResult{NoSubmission: knownNotSubmitted(err)}, err
	}
	task, err := executor.gateway.Get(ctx, generation.OwnerID(op.OwnerID), accepted.TaskID)
	if err != nil {
		return executor.awaitTask(command, accepted.TaskID,
			executor.taskWaitDeadline(command, generation.Offering{}), err)
	}
	if task.OwnerID != generation.OwnerID(op.OwnerID) || task.SessionID != op.SessionID ||
		task.ID != accepted.TaskID {
		return chat.ToolResult{TaskID: string(accepted.TaskID)},
			errors.New("agent operation accepted task identity mismatch")
	}
	if observe != nil {
		if err := observe(chat.ToolObservation{Registered: true, TaskID: string(task.ID),
			Capability: string(task.Capability), AIModel: task.Offering.ModelID,
			NativeQuote: task.NativeQuote, EstimatedCredits: taskCredits(task), ApprovalSource: approvalSource(grant)}); err != nil {
			return executor.awaitTask(command, task.ID, executor.taskWaitDeadline(command, task.Offering), err)
		}
	}
	return executor.wait(ctx, command, task.Offering, task.ID, observe,
		taskStep(task, approvalSource(grant)))
}

// AgentGrantMatchesOperation reports whether a grant is exactly the one the
// agent operation froze: same owner, session, offering, prepared input, key and
// trigger turn.
func AgentGrantMatchesOperation(grant executiongrant.Grant, op Operation) bool {
	return agentGrantMatchesOperation(grant, op)
}

func agentGrantMatchesOperation(grant executiongrant.Grant, op Operation) bool {
	return grant.Harness == op.Harness && grant.RequestID == op.GrantRequestID && grant.OwnerID == op.OwnerID &&
		grant.SessionID == op.SessionID && grant.OperationID == op.OperationID &&
		grant.IdempotencyKey == op.IdempotencyKey &&
		grant.PreparedInputID == string(op.PreparedInputID) &&
		grant.PreparedInputHash == op.PreparedInputHash &&
		grant.OfferingID == string(op.OfferingID) &&
		grant.OfferingRevision == op.OfferingRevision &&
		grant.PlanStepID == "" && grant.Source == executiongrant.AgentPending &&
		grant.TriggerTurnID == op.AssistantMessageID
}
