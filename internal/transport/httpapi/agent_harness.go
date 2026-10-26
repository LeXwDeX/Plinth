package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

type AgentOperationReader interface {
	PendingAgentOperation(context.Context, string, string) (generationtool.Operation, error)
}

// AGENTS 模式 harness/绑定/同意 端点。参数 store 为 harness（用户）与
// agent（set_params 工具）共写的权威请求参数；绑定把 agent 会话关联到任务
// （按任务隔离）；同意记录待批准项供前端 consent UI 读取/批准。
// 所有 store 键以 ScopeKey(owner, scope) 组合，保证按用户对象级隔离：
// 任何 principal 只能读写自己 owner 下的 scope。
// 错误 detail 一律为 error.* 词汇键（前端经 Product Schema 映射文案），
// 不在 transport 硬编码公开文案。

var (
	agentScopeRequired   = "error.agent_scope_required"
	agentInvalidBody     = "error.agent_invalid_body"
	agentSessionRequired = "error.agent_session_required"
	// agentScopeForbidden：scope 必须是当前用户自己的会话；任意
	// scope 字符串的写入会让同意/参数状态无界增长并污染 store。
	agentScopeForbidden     = "error.agent_scope_forbidden"
	agentConsentUnavailable = "error.consent_unavailable"
	agentConsentNotPending  = "error.consent_not_pending"
)

// ownsAgentScope 校验 scope 是当前 owner 名下的会话； harness 的
// 参数/同意/绑定写入全部经此守卫。
func (s *Server) ownsAgentScope(
	request *http.Request,
	owner generation.OwnerID,
	scope string,
) bool {
	if s.ownsAgentSession == nil {
		return false
	}
	return s.ownsAgentSession(request.Context(), string(owner), scope)
}

func (s *Server) agentParamsGet(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	scope := request.URL.Query().Get("scope")
	if scope == "" {
		writeProblem(writer, http.StatusBadRequest, "invalid_scope", agentScopeRequired)
		return
	}
	if !s.ownsAgentScope(request, owner, scope) {
		writeProblem(writer, http.StatusForbidden, "invalid_scope", agentScopeForbidden)
		return
	}
	key := agentparams.ScopeKey(string(owner), scope)
	writeAgentJSON(writer, s.projectAgentParameters(key, request.URL.Query().Get("offering_id")))
}

func (s *Server) agentParamsPut(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	scope := request.URL.Query().Get("scope")
	if scope == "" {
		writeProblem(writer, http.StatusBadRequest, "invalid_scope", agentScopeRequired)
		return
	}
	if !s.ownsAgentScope(request, owner, scope) {
		writeProblem(writer, http.StatusForbidden, "invalid_scope", agentScopeForbidden)
		return
	}
	var body struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	bodyReader := boundedBodyReader(writer, request, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(bodyReader).Decode(&body); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_body", agentInvalidBody)
		return
	}
	key := agentparams.ScopeKey(string(owner), scope)
	if offeringID := request.URL.Query().Get("offering_id"); offeringID != "" {
		var selected generation.Offering
		for _, o := range s.generation.Offerings("") {
			if string(o.ID) == offeringID {
				selected = o
				break
			}
		}
		if selected.ID == "" || !selected.Enabled {
			writeProblem(writer, http.StatusBadRequest, "invalid_parameters", agentInvalidBody)
			return
		}
		normalized, err := catalog.ValidateParameterPatch(selected, body.Params, false, nil)
		if err != nil {
			writeProblem(writer, http.StatusBadRequest, "invalid_parameters", agentInvalidBody)
			return
		}
		if err := s.agentParams.SetUserOffering(key, offeringID, normalized); err != nil {
			s.writeChatInternalError(writer, err)
			return
		}
	} else {
		s.agentParams.SetUser(key, body.Params)
	}
	writeAgentJSON(writer, s.projectAgentParameters(key, request.URL.Query().Get("offering_id")))
}

func (s *Server) agentBindPut(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var body struct {
		Session string `json:"session"`
		Task    string `json:"task"`
	}
	bodyReader := boundedBodyReader(writer, request, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(bodyReader).Decode(&body); err != nil || body.Session == "" {
		writeProblem(writer, http.StatusBadRequest, "invalid_body", agentSessionRequired)
		return
	}
	if !s.ownsAgentScope(request, owner, body.Session) {
		writeProblem(writer, http.StatusForbidden, "invalid_scope", agentScopeForbidden)
		return
	}
	key := agentparams.ScopeKey(string(owner), body.Session)
	taskJSON, _ := json.Marshal(body.Task)
	s.agentState.Set(key, map[string]json.RawMessage{
		"task_id": taskJSON,
	})
	// One session is one creative task. A new generation task ID represents a
	// version in that session, so the user's harness overrides remain in scope.
	writeAgentJSON(writer, map[string]any{"session": body.Session, "task": body.Task})
}

func (s *Server) agentConsentGet(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	scope := request.URL.Query().Get("scope")
	if scope == "" {
		writeProblem(writer, http.StatusBadRequest, "invalid_scope", agentScopeRequired)
		return
	}
	if !s.ownsAgentScope(request, owner, scope) {
		writeProblem(writer, http.StatusForbidden, "invalid_scope", agentScopeForbidden)
		return
	}
	if operation, grant, found, err := s.currentAgentExecutionGrant(request, owner, scope); err != nil {
		s.writeChatInternalError(writer, err)
		return
	} else if found {
		if operation.Detached {
			// The card left the turn: the pending-approvals list shows it. The
			// turn may still be streaming its closing text; projecting the card
			// here too would put the same request on the page twice.
			writeAgentJSON(writer, map[string]any{"consent": map[string]any{}})
			return
		}
		active, err := s.agentGrantTurnActive(request, owner, scope, operation.AssistantMessageID)
		if err != nil {
			s.writeChatInternalError(writer, err)
			return
		}
		if !active {
			writeAgentJSON(writer, map[string]any{"consent": map[string]any{}})
			return
		}
		prepared, err := s.generation.GetPreparedInput(request.Context(), owner, operation.PreparedInputID)
		if err != nil || prepared.InputHash != operation.PreparedInputHash {
			s.writeChatInternalError(writer, err)
			return
		}
		// Project the exact frozen operation through a small allowlist. Never
		// echo Provider wire, upstream IDs, opaque checkpoints, or credentials.
		pending := map[string]any{
			"tool":              operation.ToolName,
			"capability":        prepared.Capability,
			"offering_id":       operation.OfferingID,
			"offering_revision": operation.OfferingRevision,
		}
		if len(prepared.ParameterAdjustments) > 0 {
			pending["parameter_adjustments"] = s.parameterAdjustmentsIn(requestLocale(request), prepared.ParameterAdjustments)
		}
		var input struct {
			Prompt         string `json:"prompt"`
			NegativePrompt string `json:"negative_prompt"`
		}
		if json.Unmarshal(prepared.NormalizedInput, &input) == nil {
			if input.Prompt != "" {
				pending["prompt"] = input.Prompt
			}
			if input.NegativePrompt != "" {
				pending["negative_prompt"] = input.NegativePrompt
			}
		}
		if len(prepared.Assets) > 0 {
			assets := make([]map[string]any, 0, len(prepared.Assets))
			for _, asset := range prepared.Assets {
				assets = append(assets, map[string]any{
					"parameter": asset.Parameter, "asset_id": asset.ID,
					"role": asset.Role,
				})
			}
			pending["reference_assets"] = assets
			pending["reference_asset_count"] = len(assets)
		}
		if offering, ok := s.generation.Offering(operation.OfferingID); ok &&
			offering.Revision == operation.OfferingRevision {
			pending["model"] = s.textIn(requestLocale(request), offering.ModelName)
			pending["provider"] = s.textIn(requestLocale(request), offering.ProviderName)
			if prepared.NativeQuote != nil {
				pending["native_quote"] = s.quoteIn(requestLocale(request), offering, prepared.NativeQuote)
			} else {
				pending["credits"] = offering.EstimateTotalCredits(prepared.NormalizedInput)
			}
			if extra := offering.ExtraCreditDetail(prepared.NormalizedInput); prepared.NativeQuote == nil && extra.Credits > 0 {
				// The card says what the extra credits are for, and the base price
				// without them.
				locale := requestLocale(request)
				pending["extra_credits"] = extra.Credits
				pending["extra_label"] = s.textIn(locale, extra.Label)
				var items []string
				if extra.Items != nil {
					items = make([]string, len(extra.Items))
					for index, item := range extra.Items {
						items[index] = s.textIn(locale, item)
					}
				}
				pending["extra_items"] = items
				pending["base_credits"] = offering.EstimateTotalCredits(prepared.NormalizedInput) - extra.Credits
			}
			// A processing step names the platform task it continues so the
			// human sees which model they are approving a change to.
			var values map[string]json.RawMessage
			if json.Unmarshal(prepared.NormalizedInput, &values) == nil {
				for _, parameter := range offering.Parameters {
					if role, isSource := schemacontract.SourceParameterRole(parameter); isSource &&
						role == schemacontract.SemanticSourceTask {
						var taskID string
						if json.Unmarshal(values[parameter.Name], &taskID) == nil && taskID != "" {
							pending["source_task_id"] = taskID
						}
					}
				}
			}
		}
		decision := string(grant.Status)
		if grant.Status == executiongrant.Claimed || grant.Status == executiongrant.Bound {
			decision = "approved"
		}
		writeAgentJSON(writer, map[string]any{"consent": map[string]any{
			"consent_pending":    pending,
			"consent_request_id": operation.GrantRequestID,
			"consent_decision":   decision,
			"consent_approved": grant.Status == executiongrant.Approved ||
				grant.Status == executiongrant.Claimed || grant.Status == executiongrant.Bound,
		}})
		return
	}
	if pending, found, err := s.currentSkillExecutionGrant(request, owner, scope); err != nil {
		s.writeChatInternalError(writer, err)
		return
	} else if found {
		prepared, err := s.generation.GetPreparedInput(request.Context(), owner, pending.PreparedInputID)
		if err != nil || prepared.InputHash != pending.PreparedInputHash ||
			prepared.SessionID != scope || prepared.OfferingID != pending.OfferingID ||
			prepared.OfferingRevision != pending.OfferingRevision {
			s.writeChatInternalError(writer, err)
			return
		}
		projection := map[string]any{
			"tool": "create_three_view", "recipe_id": "image-three-view",
			"run_id": pending.RunID, "step_key": pending.StepKey,
			"capability":        prepared.Capability,
			"offering_id":       pending.OfferingID,
			"offering_revision": pending.OfferingRevision,
		}
		var input struct {
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal(prepared.NormalizedInput, &input) == nil && input.Prompt != "" {
			projection["prompt"] = input.Prompt
		}
		if len(prepared.Assets) > 0 {
			assets := make([]map[string]any, 0, len(prepared.Assets))
			for _, asset := range prepared.Assets {
				assets = append(assets, map[string]any{
					"parameter": asset.Parameter, "asset_id": asset.ID, "role": asset.Role,
				})
			}
			projection["reference_assets"] = assets
			projection["reference_asset_count"] = len(assets)
		}
		if offering, ok := s.generation.Offering(pending.OfferingID); ok &&
			offering.Revision == pending.OfferingRevision {
			projection["model"] = s.textIn(requestLocale(request), offering.ModelName)
			projection["provider"] = s.textIn(requestLocale(request), offering.ProviderName)
			if prepared.NativeQuote != nil {
				projection["native_quote"] = s.quoteIn(requestLocale(request), offering, prepared.NativeQuote)
			} else {
				projection["credits"] = offering.EstimateTotalCredits(prepared.NormalizedInput)
			}
		}
		writeAgentJSON(writer, map[string]any{"consent": map[string]any{
			"consent_pending": projection, "consent_request_id": pending.GrantRequestID,
			"consent_decision": "pending", "consent_approved": false,
		}})
		return
	}
	if s.agentOperations != nil && s.executionGrants != nil {
		writeAgentJSON(writer, map[string]any{"consent": map[string]any{}})
		return
	}
	key := agentparams.ScopeKey(string(owner), scope)
	state := s.agentState.Get(key)
	delete(state, agentparams.ConsentClaimKey)
	writeAgentJSON(writer, map[string]any{"consent": state})
}

func (s *Server) agentConsentPut(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	scope := request.URL.Query().Get("scope")
	if scope == "" {
		writeProblem(writer, http.StatusBadRequest, "invalid_scope", agentScopeRequired)
		return
	}
	if !s.ownsAgentScope(request, owner, scope) {
		writeProblem(writer, http.StatusForbidden, "invalid_scope", agentScopeForbidden)
		return
	}
	var body struct {
		Approved  *bool  `json:"approved"`
		RequestID string `json:"request_id"`
	}
	bodyReader := boundedBodyReader(writer, request, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(bodyReader).Decode(&body); err != nil || body.RequestID == "" || body.Approved == nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_body", agentInvalidBody)
		return
	}
	if s.pendingApprovals != nil {
		// A request whose turn already ended is decided by the pending-approval
		// service, whichever operation is the session's latest.
		session, detached, err := s.pendingApprovals.DetachedSession(request.Context(), string(owner), body.RequestID)
		if err != nil {
			s.writeChatInternalError(writer, err)
			return
		}
		if detached {
			if session != scope {
				writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
				return
			}
			principal, human := s.taskHumanPrincipal(request, owner)
			if !human {
				s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
				return
			}
			decision := executiongrant.Deny
			if *body.Approved {
				decision = executiongrant.Approve
			}
			outcome, handled, err := s.pendingApprovals.Decide(request.Context(), principal, body.RequestID, decision)
			if err != nil {
				s.writeTaskGrantError(writer, err)
				return
			}
			if !handled {
				writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
				return
			}
			response := pendingOutcomeWire(outcome)
			response["scope"], response["approved"] = scope, *body.Approved
			writeAgentJSON(writer, response)
			return
		}
	}
	if operation, grant, found, err := s.currentAgentExecutionGrant(request, owner, scope); err != nil {
		s.writeChatInternalError(writer, err)
		return
	} else if found {
		active, err := s.agentGrantTurnActive(request, owner, scope, operation.AssistantMessageID)
		if err != nil {
			s.writeChatInternalError(writer, err)
			return
		}
		if !active {
			writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
			return
		}
		if body.RequestID != operation.GrantRequestID || grant.Status != executiongrant.Pending {
			writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
			return
		}
		if offering, available := s.generation.Offering(operation.OfferingID); !available ||
			offering.Revision != operation.OfferingRevision {
			writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
			return
		}
		principal, human := s.taskHumanPrincipal(request, owner)
		if !human {
			s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
			return
		}
		decision := executiongrant.Deny
		if *body.Approved {
			decision = executiongrant.Approve
		}
		if _, err := s.executionGrants.Decide(request.Context(), principal, operation.GrantRequestID, decision); err != nil {
			// The decision itself refuses an approval whose turn was stopped
			// after the check above (one statement, so no Stop can slip in
			// between): answer it as the not-pending conflict it is.
			if errors.Is(err, executiongrant.ErrState) && s.agentTurnStopped(request, owner, scope, operation.AssistantMessageID) {
				writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
				return
			}
			s.writeTaskGrantError(writer, err)
			return
		}
		s.chatScheduler.ResumeConsentWait(string(owner), scope)
		writeAgentJSON(writer, map[string]any{"scope": scope, "approved": *body.Approved})
		return
	}
	if pending, found, err := s.currentSkillExecutionGrant(request, owner, scope); err != nil {
		s.writeChatInternalError(writer, err)
		return
	} else if found {
		if body.RequestID != pending.GrantRequestID {
			writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
			return
		}
		if *body.Approved {
			if offering, available := s.generation.Offering(pending.OfferingID); !available ||
				offering.Revision != pending.OfferingRevision {
				writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
				return
			}
		}
		principal, human := s.taskHumanPrincipal(request, owner)
		if !human {
			s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
			return
		}
		decision := executiongrant.Deny
		if *body.Approved {
			decision = executiongrant.Approve
		}
		if _, err := s.executionGrants.Decide(request.Context(), principal, pending.GrantRequestID, decision); err != nil {
			if errors.Is(err, executiongrant.ErrState) && s.skillGrantTurnStopped(request, owner, scope, pending.GrantRequestID) {
				writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
				return
			}
			s.writeTaskGrantError(writer, err)
			return
		}
		s.chatScheduler.ResumeConsentWait(string(owner), scope)
		writeAgentJSON(writer, map[string]any{"scope": scope, "approved": *body.Approved})
		return
	}
	if s.agentOperations != nil && s.executionGrants != nil {
		writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
		return
	}
	key := agentparams.ScopeKey(string(owner), scope)
	decided, err := s.agentState.DecideConsent(key, body.RequestID, *body.Approved)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "consent_unavailable", agentConsentUnavailable, err)
		return
	}
	if !decided {
		writeProblem(writer, http.StatusConflict, "consent_not_pending", agentConsentNotPending)
		return
	}
	// AGT-12：决定写入即唤醒因等待同意而挂起的轮次（重新入队续跑，
	// 不占 worker 等待）。
	s.chatScheduler.ResumeConsentWait(string(owner), scope)
	writeAgentJSON(writer, map[string]any{"scope": scope, "approved": *body.Approved})
}

func (s *Server) currentSkillExecutionGrant(request *http.Request, owner generation.OwnerID,
	sessionID string) (skillrun.PendingGrant, bool, error) {
	if s.skillRuns == nil || s.skillRuns.Runs == nil || s.executionGrants == nil ||
		s.executionGrants.Grants == nil {
		return skillrun.PendingGrant{}, false, nil
	}
	pending, err := s.skillRuns.Runs.PendingGrant(request.Context(), owner, sessionID)
	if errors.Is(err, skillrun.ErrMissing) {
		return skillrun.PendingGrant{}, false, nil
	}
	if err != nil {
		return skillrun.PendingGrant{}, false, err
	}
	grant, err := s.executionGrants.Grants.GetExecutionGrant(request.Context(), string(owner), pending.GrantRequestID)
	if err != nil {
		return skillrun.PendingGrant{}, false, err
	}
	if grant.Status != executiongrant.Pending || grant.OwnerID != string(owner) ||
		grant.SessionID != sessionID || grant.PreparedInputID != string(pending.PreparedInputID) ||
		grant.PreparedInputHash != pending.PreparedInputHash || grant.OfferingID != string(pending.OfferingID) ||
		grant.OfferingRevision != pending.OfferingRevision || grant.OperationID != pending.OperationID ||
		grant.IdempotencyKey != pending.IdempotencyKey {
		return skillrun.PendingGrant{}, false, executiongrant.ErrConflict
	}
	if grant.Source == executiongrant.AgentPending {
		if grant.TriggerTurnID == "" {
			return skillrun.PendingGrant{}, false, executiongrant.ErrConflict
		}
		active, err := s.agentGrantTurnActive(request, owner, sessionID, grant.TriggerTurnID)
		if err != nil {
			return skillrun.PendingGrant{}, false, err
		}
		if !active {
			return skillrun.PendingGrant{}, false, nil
		}
	}
	return pending, true, nil
}

func (s *Server) agentGrantTurnActive(request *http.Request, owner generation.OwnerID, sessionID, assistantMessageID string) (bool, error) {
	if s.chat == nil {
		return false, executiongrant.ErrConflict
	}
	turn, err := s.chat.GetTurn(request.Context(), string(owner), sessionID, assistantMessageID)
	if err != nil {
		return false, err
	}
	if turn.StopRequested {
		return false, nil
	}
	return turn.State == s.chat.TurnQueuedState() ||
		turn.State == s.chat.TurnRunningState() ||
		turn.State == s.chat.TurnWaitingTaskState(), nil
}

// agentTurnStopped reports whether the turn no longer takes a decision because
// a Stop was requested. A lookup failure reads as not stopped, so the caller
// keeps its original error.
func (s *Server) agentTurnStopped(request *http.Request, owner generation.OwnerID, sessionID, assistantMessageID string) bool {
	if s.chat == nil {
		return false
	}
	turn, err := s.chat.GetTurn(request.Context(), string(owner), sessionID, assistantMessageID)
	return err == nil && turn.StopRequested
}

// skillGrantTurnStopped is agentTurnStopped for the turn that triggered a Skill
// run's grant.
func (s *Server) skillGrantTurnStopped(request *http.Request, owner generation.OwnerID, sessionID, requestID string) bool {
	grant, err := s.executionGrants.Grants.GetExecutionGrant(request.Context(), string(owner), requestID)
	return err == nil && grant.TriggerTurnID != "" &&
		s.agentTurnStopped(request, owner, sessionID, grant.TriggerTurnID)
}

func (s *Server) currentAgentExecutionGrant(request *http.Request, owner generation.OwnerID, sessionID string) (generationtool.Operation, executiongrant.Grant, bool, error) {
	if s.agentOperations == nil {
		return generationtool.Operation{}, executiongrant.Grant{}, false, nil
	}
	operation, err := s.agentOperations.PendingAgentOperation(request.Context(), string(owner), sessionID)
	if errors.Is(err, generationtool.ErrOperationNotFound) || errors.Is(err, sql.ErrNoRows) {
		return generationtool.Operation{}, executiongrant.Grant{}, false, nil
	}
	if err != nil {
		return generationtool.Operation{}, executiongrant.Grant{}, false, err
	}
	if operation.OwnerID != string(owner) || operation.SessionID != sessionID ||
		operation.GrantRequestID == "" || s.executionGrants == nil || s.executionGrants.Grants == nil {
		return generationtool.Operation{}, executiongrant.Grant{}, false, executiongrant.ErrConflict
	}
	grant, err := s.executionGrants.Grants.GetExecutionGrant(request.Context(), string(owner), operation.GrantRequestID)
	if err != nil {
		return generationtool.Operation{}, executiongrant.Grant{}, false, err
	}
	if grant.OwnerID != string(owner) || grant.SessionID != sessionID ||
		grant.PreparedInputID != string(operation.PreparedInputID) ||
		grant.PreparedInputHash != operation.PreparedInputHash ||
		grant.OfferingID != string(operation.OfferingID) ||
		grant.OfferingRevision != operation.OfferingRevision ||
		grant.OperationID != operation.OperationID || grant.IdempotencyKey != operation.IdempotencyKey {
		return generationtool.Operation{}, executiongrant.Grant{}, false, executiongrant.ErrConflict
	}
	return operation, grant, true, nil
}

// boundedBodyReader 限制请求体大小；未声明上限（0）时不包装，
// 便于最小装配的单元测试。
func boundedBodyReader(
	writer http.ResponseWriter,
	request *http.Request,
	maxBytes int64,
) io.Reader {
	if maxBytes <= 0 {
		return request.Body
	}
	return http.MaxBytesReader(writer, request.Body, maxBytes)
}

func writeAgentJSON(writer http.ResponseWriter, payload map[string]any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(payload)
}

func agentParamsOrNew(store *agentparams.Store) *agentparams.Store {
	if store == nil {
		return agentparams.NewStore()
	}
	return store
}

func (s *Server) projectAgentParameters(key, offeringID string) map[string]any {
	stored := s.agentParams.GetOffering(key, offeringID)
	if offeringID == "" {
		return map[string]any{"params": stored}
	}
	values := map[string]json.RawMessage{}
	result := map[string]any{"params": values}
	states := s.agentParams.OfferingStates(key, offeringID)
	visible := map[string]agentparams.ParameterState{}
	for _, o := range s.generation.Offerings("") {
		if string(o.ID) != offeringID {
			continue
		}
		for _, p := range o.Parameters {
			if !o.AgentParameterAllowed(p) {
				delete(values, p.Name)
				continue
			}
			state, ok := states[p.Name]
			if !ok || state.Source == "default" || (state.Source == "user" && !o.UserParameterAllowed(p)) {
				state = agentparams.ParameterState{Source: "default", Value: append(json.RawMessage(nil), p.Default...)}
			} else if raw, exists := stored[p.Name]; exists {
				values[p.Name] = raw
			}
			visible[p.Name] = state
		}
	}
	result["parameter_states"] = visible
	return result
}
