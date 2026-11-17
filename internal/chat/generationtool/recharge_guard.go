package generationtool

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// SessionTasks is the read side of the task kernel the recharge guard uses:
// the session's recent Tasks, a full Task, and the source of a recovery Task.
type SessionTasks interface {
	List(context.Context, generation.TaskListScope, generation.ListQuery) (generation.TaskPage, error)
	Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error)
	TaskRecoveryProvenance(context.Context, generation.Task) (*generation.RecoveryProvenance, error)
	// SessionRefetchSources maps each upstream refetch Task (#295) of the
	// session to the failed Task whose accepted result it reads again.
	SessionRefetchSources(context.Context, generation.OwnerID, string) (map[generation.TaskID]generation.TaskID, error)
}

// rechargeScanLimit bounds how many of the session's newest Tasks the guard
// reads. A charged-but-unstored failure older than that many Tasks of the
// session is no longer the "previous attempt" a resubmission repeats.
const rechargeScanLimit = 100

// unstoredChargedTasks returns, newest first, the session's Tasks that failed
// after the upstream finished and charged and that nothing has answered since
// (see UnstoredChargedTasks). Kinds are the capabilities of this tool (its
// Schema execution capabilities) plus the capability being prepared: the Agent
// may move between a tool's capabilities (text-to-image and image-to-image,
// text-to-3d and image-to-3d) or models when it "tries again", so neither the
// Offering nor the exact input identifies a repeat.
func (executor *Executor) unstoredChargedTasks(ctx context.Context, command chat.ToolCommand,
	capability generation.Capability) ([]generation.TaskID, error) {
	return UnstoredChargedTasks(ctx, executor.tasks, executor.chat, generation.OwnerID(command.OwnerID), command.SessionID,
		append([]generation.Capability{capability}, command.Tool.Execution.Capabilities...))
}

// UnstoredChargedTasks returns, newest first, the session's Tasks that failed
// after the upstream finished and charged (the Schema's
// generation_failed_metered_by_class declares the class for an Offering that
// bills in credits, the same rule as MeteredFailureMessage) and that nothing has
// answered since: no Task able to produce the same kind of result was created
// in the session after the failure, and no asset recovery of it is under way or
// done. kinds are the capabilities whose Tasks count as "the same kind". A
// repeat of such a step charges again, so every entry that submits steps on
// someone's behalf (the Agent, the external MCP service) sends a step that
// would repeat one to a person instead of approving it (#294).
//
// A later Task of the same kind proves a decision was made after the failure:
// a person ran it from the workspace, retried it from the task list, or
// approved it on a card this guard forced. That is what clears the guard.
func UnstoredChargedTasks(ctx context.Context, tasks SessionTasks, schema catalog.ChatSchema,
	owner generation.OwnerID, session string, kindList []generation.Capability) ([]generation.TaskID, error) {
	classes := schema.Presentation.Messages.GenerationFailedMeteredByClass
	if tasks == nil || len(classes) == 0 || owner == "" || session == "" {
		return nil, nil
	}
	kinds := map[generation.Capability]bool{}
	for _, kind := range kindList {
		kinds[kind] = true
	}
	page, err := tasks.List(ctx, generation.OwnTasks(owner),
		generation.ListQuery{SessionID: session, Limit: rechargeScanLimit})
	if err != nil {
		return nil, err
	}
	refetches, err := tasks.SessionRefetchSources(ctx, owner, session)
	if err != nil {
		return nil, err
	}
	recovered := map[generation.TaskID]bool{}
	var latest time.Time
	for _, task := range page.Tasks {
		// A refetch reads the charged result again without a new charge (#295).
		// Only a succeeded refetch means the result was recovered; while it is
		// queued, running or materializing, and after it failed or was canceled,
		// the earlier result is still not in hand. It is free, so it is never
		// the later same-kind decision that clears the guard either.
		if source, refetch := refetches[task.ID]; refetch {
			if task.State == generation.TaskSucceeded {
				recovered[source] = true
			}
			continue
		}
		if kinds[task.Capability] && task.CreatedAt.After(latest) {
			latest = task.CreatedAt
		}
		// An asset recovery answers the failure only after it actually
		// succeeded; a queued, running, materializing, failed or canceled one
		// leaves the result out of hand.
		if task.Capability != schemacontract.AssetRecovery || task.State != generation.TaskSucceeded {
			continue
		}
		full, err := tasks.Get(ctx, owner, task.ID)
		if err != nil {
			return nil, err
		}
		provenance, err := tasks.TaskRecoveryProvenance(ctx, full)
		if err != nil {
			return nil, err
		}
		if provenance != nil {
			recovered[provenance.SourceTaskID] = true
		}
	}
	var unstored []generation.TaskID
	for _, task := range page.Tasks {
		if task.State != generation.TaskFailed || task.Failure == nil || !kinds[task.Capability] ||
			recovered[task.ID] || classes[string(task.Failure.Class)] == "" {
			continue
		}
		failedAt := task.UpdatedAt
		if task.FinishedAt != nil {
			failedAt = *task.FinishedAt
		}
		if latest.After(failedAt) {
			continue // a same-kind Task was started after this failure was known
		}
		// The list projection omits the Offering's billing flag; read the Task.
		full, err := tasks.Get(ctx, owner, task.ID)
		if err != nil {
			return nil, err
		}
		if MeteredFailureMessage(schema, full.Offering.CreditsMetered, full.Failure) != "" {
			unstored = append(unstored, task.ID)
		}
	}
	return unstored, nil
}

// rechargeContent is the tool result of a step the guard sent to an approval
// card: the Schema's consent_recharge_content naming the earlier Task(s), or
// the plain waiting text when the Schema declares none.
// awaitingApproval is what the Agent reads about a step that now waits on its
// approval card: the Schema's waiting text, the service, and the frozen price
// as the card shows it, split into base and extra with what the extra pays
// for (#321 I), so a reply quotes it instead of estimating. When the step uses
// fewer of the reference pictures than the turn gave, the tool's reference
// note says so (#329 E), as the card and the finished result do.
func (executor *Executor) awaitingApproval(ctx context.Context, command chat.ToolCommand, op Operation, decision executiongrant.PolicyResult) string {
	tool := command.Tool
	content := map[string]any{"status": "awaiting_approval", "message": executor.rechargeContent(command, op)}
	if status := executor.chat.Agent.ConsentWaitingStatus; status != "" {
		content["status_text"] = executor.localized(command, status)
	}
	// A budget-mode step that the plan could not cover says why it needs a
	// person, and what the plan still holds (#103), so the Agent does not
	// repeat the same request or misreport the budget. Both keys are declared
	// by the tool's result_fields (#103 review B4).
	if decision.Reason != "" {
		if key := tool.ResultFields.Reason; key != "" {
			content[key] = decision.Reason
		}
	}
	if decision.Budget != nil {
		if key := tool.ResultFields.Budget; key != "" {
			content[key] = budgetResult(*decision.Budget)
		}
	}
	if gateway, ok := executor.gateway.(preparedGateway); ok {
		if prepared, err := gateway.GetPreparedInput(ctx, generation.OwnerID(op.OwnerID), op.PreparedInputID); err == nil {
			for _, offering := range executor.gateway.Offerings(prepared.Capability) {
				if offering.ID != prepared.OfferingID {
					continue
				}
				content["provider"] = executor.localized(command, offering.ProviderName)
				content["model"] = executor.localized(command, offering.ModelName)
				if key := tool.ResultFields.ReferenceNote; key != "" {
					if note := executor.operationReferenceNote(command, op, prepared, offering); note != "" {
						content[key] = note
					}
				}
				// #129: the card's frozen values over the purpose's budget.
				if note := executor.purposeBudgetNote(command, offering, prepared.NormalizedInput); note != "" {
					content[tool.ResultFields.PurposeBudgetNote] = note
				}
				switch quote := quoteOf(offering, prepared.NormalizedInput, prepared.NativeQuote, executor.localizeText(command)).(type) {
				case creditQuote:
					content["quote"] = quote
				case map[string]string:
					content["native_quote"] = quote
				}
			}
		}
	}
	encoded, _ := json.Marshal(content)
	return string(encoded)
}

func (executor *Executor) rechargeContent(command chat.ToolCommand, op Operation) string {
	template := executor.localized(command, executor.chat.Agent.ConsentRechargeContent)
	if op.RechargeTaskID == "" || template == "" {
		return executor.localized(command, executor.chat.Agent.ConsentWaitingContent)
	}
	return strings.ReplaceAll(template, "{task_id}", op.RechargeTaskID)
}
