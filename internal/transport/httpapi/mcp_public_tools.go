package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/mcphttp"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/mcppublic"
	"github.com/LeXwDeX/Plinth/internal/routing"
	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

// The tools of the external MCP service (#332). Which of them are served, how
// they are described, which arguments they take and which scope they need is
// the Product Schema's (application.mcp_public.tools); this file is what the
// declared names run. Every tool here only reads: it counts no step, charges
// nothing and creates no Task. A read goes through the same kernel, projection
// and asset modules the web API uses, so a Task is shown the way the web API
// shows it, and a Task of another owner is not found.

type mcpTool struct {
	declared catalog.MCPPublicTool
	input    *toolschema.Schema
	output   json.RawMessage
	run      func(*mcpCall) (mcpOutcome, error)
}

// mcpCall is one tool call as the tool sees it: the person is the key's owner,
// the arguments were checked against the declared schema.
type mcpCall struct {
	ctx       context.Context
	locale    string
	owner     generation.OwnerID
	arguments map[string]any
	// base is "scheme://host" of the request; empty when the request had no
	// Host, which leaves the download URLs relative.
	base string
	// tool is the declaration of the tool being called; keyID the access key
	// that called and ownerName the person's name (the claims').
	tool      *mcpTool
	keyID     string
	ownerName string
}

// mcpProblem is a business failure: a tool result with isError, written for
// the calling Agent. RetryAfterSeconds is set when waiting is the remedy.
type mcpProblem struct {
	Code, Message     string
	RetryAfterSeconds *int
}

// mcpOutcome is what a tool produced. Subject says what the status block
// should be about.
type mcpOutcome struct {
	data    map[string]any
	problem *mcpProblem
	subject mcppublic.Subject
}

type mcpRuntime struct {
	server        *Server
	service       catalog.MCPPublicSchema
	tools         map[string]*mcpTool
	order         []string
	downloadRoute routing.Two
	signedRoute   routing.One
	signedTTL     time.Duration
}

func (s *Server) newMCPRuntime(service catalog.MCPPublicSchema, route routing.Two, signedRoute routing.One) (*mcpRuntime, error) {
	ttl := time.Duration(service.SignedDownloadTTLSeconds) * time.Second
	handlers := map[string]func(*mcpCall) (mcpOutcome, error){
		catalog.MCPToolListServices: s.mcpListServices,
		catalog.MCPToolGetStatus:    s.mcpGetStatus,
		catalog.MCPToolGetTask:      s.mcpGetTask,
		catalog.MCPToolListTasks:    s.mcpListTasks,
		catalog.MCPToolGetTaskAssets: func(call *mcpCall) (mcpOutcome, error) {
			return s.mcpGetTaskAssets(call, route, signedRoute, ttl)
		},
		catalog.MCPToolInspectModel:     s.mcpInspectModel,
		catalog.MCPToolUploadImage:      s.mcpUploadImage,
		catalog.MCPToolGenerateImage:    s.mcpStartTask,
		catalog.MCPToolRemoveBackground: s.mcpStartTask,
		catalog.MCPToolCreateModel:      s.mcpStartTask,
		catalog.MCPToolProcessModel:     s.mcpStartTask,
	}
	runtime := &mcpRuntime{server: s, service: service, tools: map[string]*mcpTool{}, downloadRoute: route, signedRoute: signedRoute, signedTTL: ttl}
	for _, declared := range service.Tools {
		run, implemented := handlers[declared.Name]
		if !implemented {
			return nil, fmt.Errorf("httpapi: mcp_public 声明了未实现的工具 %q", declared.Name)
		}
		if declared.Scope == "generate" && (s.mcpAssets == nil || s.executionGrants == nil || s.inputAssets == nil || s.chat == nil) {
			return nil, fmt.Errorf("httpapi: mcp_public 声明了发起生成的工具 %q，但没有装配资产映射、授权服务、输入资产或会话服务", declared.Name)
		}
		// inspect_model is read-only, but it reads the Task session's
		// production brief: without the chat service there is nothing to read.
		if declared.Name == catalog.MCPToolInspectModel && s.modelInspector == nil {
			return nil, fmt.Errorf("httpapi: mcp_public 声明了 inspect_model，但没有装配模型检查器")
		}
		if declared.Name == catalog.MCPToolInspectModel && s.chat == nil {
			return nil, fmt.Errorf("httpapi: mcp_public 声明了 inspect_model，但没有装配会话服务（读取制作简报）")
		}
		input, _, err := service.ParsedInputSchema(declared.Name)
		if err != nil {
			return nil, fmt.Errorf("httpapi: mcp_public 工具 %s 的参数 Schema: %w", declared.Name, err)
		}
		output, _, err := service.ToolOutputSchema(declared.Name)
		if err != nil {
			return nil, fmt.Errorf("httpapi: mcp_public 工具 %s 的输出 Schema: %w", declared.Name, err)
		}
		runtime.tools[declared.Name] = &mcpTool{declared: declared, input: input, output: output, run: run}
		runtime.order = append(runtime.order, declared.Name)
	}
	return runtime, nil
}

// ListTools lists the tools the key's scopes allow.
func (r *mcpRuntime) ListTools(ctx context.Context) ([]mcphttp.PublicTool, error) {
	principal, ok := mcpPrincipalFrom(ctx)
	if !ok {
		return nil, errors.New("mcp: request has no authenticated key")
	}
	locale := catalog.LocaleFrom(ctx)
	listed := make([]mcphttp.PublicTool, 0, len(r.order))
	for _, name := range r.order {
		tool := r.tools[name]
		if !principal.Credential().HasScope(tool.declared.Scope) {
			continue
		}
		listed = append(listed, mcphttp.PublicTool{
			Name:         tool.declared.Name,
			Description:  r.server.localizedText(locale, tool.declared.Description),
			InputSchema:  tool.declared.InputSchema,
			OutputSchema: tool.output,
		})
	}
	return listed, nil
}

// CallTool runs one tool. A refusal of the arguments or a Task that is not the
// caller's is a result with isError; only a failure the caller cannot act on
// is an error.
func (r *mcpRuntime) CallTool(ctx context.Context, name string, arguments json.RawMessage) (mcphttp.PublicResult, error) {
	tool, found := r.tools[name]
	if !found {
		return mcphttp.PublicResult{}, mcphttp.ErrUnknownTool
	}
	principal, ok := mcpPrincipalFrom(ctx)
	if !ok {
		return mcphttp.PublicResult{}, errors.New("mcp: request has no authenticated key")
	}
	if !principal.Credential().HasScope(tool.declared.Scope) {
		return mcphttp.PublicResult{}, &mcphttp.ScopeError{Scope: tool.declared.Scope}
	}
	locale := catalog.LocaleFrom(ctx)
	base, _ := ctx.Value(mcpBaseKey{}).(string)
	call := &mcpCall{
		ctx: ctx, locale: locale, owner: generation.OwnerID(principal.ID()), base: base,
		tool: tool, keyID: principal.Credential().KeyID, ownerName: principal.Claims.Name,
	}
	var outcome mcpOutcome
	if err := tool.input.Validate(arguments); err != nil {
		outcome = r.server.mcpInvalidArguments(locale, err.Error())
	} else {
		if err := json.Unmarshal(arguments, &call.arguments); err != nil {
			return mcphttp.PublicResult{}, fmt.Errorf("decode validated arguments: %w", err)
		}
		var err error
		if outcome, err = tool.run(call); err != nil {
			return mcphttp.PublicResult{}, err
		}
	}
	subject := outcome.subject
	subject.OwnerID = principal.ID()
	block := r.server.mcpStatus.Build(ctx, locale, subject)
	structured := make(map[string]any, len(outcome.data)+2)
	for key, value := range outcome.data {
		structured[key] = value
	}
	if outcome.problem != nil {
		failure := map[string]any{"code": outcome.problem.Code, "message": outcome.problem.Message}
		if outcome.problem.RetryAfterSeconds != nil {
			failure["retry_after_seconds"] = *outcome.problem.RetryAfterSeconds
		}
		structured["error"] = failure
	}
	structured[r.server.mcpStatus.Key()] = block
	encoded, err := json.Marshal(structured)
	if err != nil {
		return mcphttp.PublicResult{}, fmt.Errorf("encode tool result: %w", err)
	}
	text := string(encoded)
	if outcome.problem != nil {
		text = outcome.problem.Message
	}
	return mcphttp.PublicResult{
		Structured: encoded,
		Text:       []string{text, r.server.mcpStatus.Summary(locale, block)},
		IsError:    outcome.problem != nil,
	}, nil
}

// mcpText is a message of the Schema, in the locale.
func (s *Server) mcpText(locale, text string) string { return s.localizedText(locale, text) }

func (s *Server) mcpInvalidArguments(locale, detail string) mcpOutcome {
	message := strings.ReplaceAll(s.mcpText(locale, s.application.MCPPublic.Messages.InvalidArguments), "{detail}", detail)
	return mcpOutcome{problem: &mcpProblem{Code: "invalid_arguments", Message: message}}
}

func (s *Server) mcpTaskNotFound(locale string) mcpOutcome {
	return mcpOutcome{problem: &mcpProblem{Code: "task_not_found", Message: s.mcpText(locale, s.application.MCPPublic.Messages.TaskNotFound)}}
}

func (s *Server) mcpAssetNotFound(locale string) mcpOutcome {
	return mcpOutcome{problem: &mcpProblem{Code: "asset_not_found", Message: s.mcpText(locale, s.application.MCPPublic.Messages.AssetNotFound)}}
}

// mcpStringArgument reads an optional string argument.
func mcpStringArgument(call *mcpCall, name string) string {
	value, _ := call.arguments[name].(string)
	return value
}

// ---- the task views --------------------------------------------------------

type mcpFailureView struct {
	Category             string `json:"category"`
	NextAction           string `json:"next_action"`
	Retryable            bool   `json:"retryable"`
	UserDecisionRequired bool   `json:"user_decision_required"`
	Advice               string `json:"advice"`
	Detail               string `json:"detail"`
}

type mcpTaskView struct {
	ID               string          `json:"id"`
	AssetID          *string         `json:"asset_id"`
	Capability       string          `json:"capability"`
	OfferingID       string          `json:"offering_id"`
	Provider         string          `json:"provider"`
	Model            string          `json:"model"`
	State            string          `json:"state"`
	StateLabel       string          `json:"state_label"`
	Terminal         bool            `json:"terminal"`
	Outcome          string          `json:"outcome"`
	Progress         *int            `json:"progress"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
	FinishedAt       *string         `json:"finished_at"`
	AssetCount       int             `json:"asset_count"`
	Failure          *mcpFailureView `json:"failure"`
	PollAfterSeconds *int            `json:"poll_after_seconds"`
}

// mcpTaskView projects a Task the way the web API does: the public state and
// its label come from the Schema's task states, the failure is the platform's
// own attribution (generation.ClassifyFailure) with the Schema's advice, and no
// internal detail, upstream text or storage key is shown.
func (s *Server) mcpTaskView(locale string, task generation.Task) (mcpTaskView, *mcppublic.Failure) {
	presentation := catalog.TaskStatePresentation{
		InternalState: string(task.State), PublicState: string(task.State), Label: string(task.State), Outcome: "running",
	}
	for _, configured := range s.application.TaskStates {
		if configured.InternalState == string(task.State) {
			presentation = configured
			break
		}
	}
	view := mcpTaskView{
		ID: string(task.ID), Capability: string(task.Capability), OfferingID: string(task.Offering.ID),
		Provider: task.Offering.ProviderID, Model: task.Offering.ModelID,
		State: presentation.PublicState, StateLabel: s.mcpText(locale, presentation.Label),
		Terminal: presentation.Terminal, Outcome: presentation.Outcome,
		CreatedAt: task.CreatedAt.UTC().Format(timeLayout), UpdatedAt: task.UpdatedAt.UTC().Format(timeLayout),
		AssetCount: len(task.Assets),
	}
	if task.SessionID != "" {
		session := task.SessionID
		view.AssetID = &session
	}
	if task.FinishedAt != nil {
		finished := task.FinishedAt.UTC().Format(timeLayout)
		view.FinishedAt = &finished
	}
	switch presentation.Progress.Mode {
	case "reported":
		progress := task.Progress
		view.Progress = &progress
	case "fixed":
		progress := presentation.Progress.FixedValue
		view.Progress = &progress
	}
	if !presentation.Terminal {
		poll := s.application.MCPPublic.PollIntervalSeconds
		view.PollAfterSeconds = &poll
	}
	var attribution *mcppublic.Failure
	if task.Failure != nil {
		report := generation.ClassifyFailure(task.Failure.Class, task.Failure.Retryable, task.Failure.Ambiguous)
		advice := s.application.Chat.Processing.Failures.Advice[string(report.Category)]
		if advice == "" {
			advice = s.application.Chat.Processing.Failures.Actions[string(report.Action)]
		}
		advice = s.mcpText(locale, advice)
		detail := ""
		if public := s.publicFailure(locale, task.Failure); public != nil {
			detail = public.Detail
		}
		view.Failure = &mcpFailureView{
			Category: string(report.Category), NextAction: string(report.Action), Retryable: report.Retryable,
			UserDecisionRequired: report.UserDecisionRequired, Advice: advice, Detail: detail,
		}
		attribution = &mcppublic.Failure{
			TaskID: string(task.ID), Category: string(report.Category), NextAction: string(report.Action),
			Retryable: report.Retryable, UserDecisionRequired: report.UserDecisionRequired, Advice: advice,
		}
	}
	return view, attribution
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

// mcpStepCosts names the services an asset's latest Tasks used and roughly what
// one step of each costs in the provider's own unit. A service without a native
// price is left out: its cost is not cheaply derivable.
func (s *Server) mcpStepCosts(ctx context.Context, owner generation.OwnerID, sessionID string) []mcppublic.StepCost {
	policy := s.application.StepPolicy
	if sessionID == "" || policy == nil {
		return nil
	}
	counted := make(map[generation.Capability]bool, len(policy.CountedCapabilities))
	for _, capability := range policy.CountedCapabilities {
		counted[capability] = true
	}
	page, err := s.generation.List(ctx, generation.OwnTasks(owner), generation.ListQuery{
		SessionID: sessionID, Limit: s.application.MCPPublic.StatusBlock.CostTaskWindow,
	})
	if err != nil {
		s.logger.Warn("MCP 状态块：资产任务读取失败", "owner_id", string(owner), "asset_id", sessionID, "error", err)
		return nil
	}
	var costs []mcppublic.StepCost
	seen := map[generation.OfferingID]bool{}
	for _, task := range page.Tasks {
		if !counted[task.Capability] || seen[task.Offering.ID] {
			continue
		}
		seen[task.Offering.ID] = true
		offering := task.Offering
		if current, exists := s.generation.Offering(task.Offering.ID); exists {
			offering = current
		}
		quote := defaultNativePrice(offering)
		if quote == nil {
			continue
		}
		costs = append(costs, mcppublic.StepCost{
			OfferingID: string(offering.ID), Provider: offering.ProviderID, Capability: string(task.Capability),
			Amount: quote.TotalAmount, Unit: quote.Unit, UnitLabel: quote.UnitLabel,
		})
	}
	return costs
}

// ---- get_task, list_tasks, get_task_assets -----------------------------------

func (s *Server) mcpOwnedTask(call *mcpCall, taskID string) (generation.Task, *mcpOutcome, error) {
	task, err := s.generation.GetSummaryScoped(call.ctx, generation.OwnTasks(call.owner), generation.TaskID(taskID))
	if generation.IsCode(err, generation.ErrNotFound) {
		missing := s.mcpTaskNotFound(call.locale)
		return generation.Task{}, &missing, nil
	}
	return task, nil, err
}

// mcpAssetSessions says which of the sessions are assets of the caller (#332):
// a session the service made for a name. A task of another session, say one
// started in the browser, is the caller's task but belongs to no asset.
func (s *Server) mcpAssetSessions(call *mcpCall, sessionIDs ...string) (map[string]bool, error) {
	if s.mcpAssets == nil {
		return map[string]bool{}, nil
	}
	return s.mcpAssets.Marked(call.ctx, string(call.owner), sessionIDs)
}

func (s *Server) mcpGetTask(call *mcpCall) (mcpOutcome, error) {
	task, missing, err := s.mcpOwnedTask(call, mcpStringArgument(call, "task_id"))
	if err != nil {
		return mcpOutcome{}, err
	}
	if missing != nil {
		return *missing, nil
	}
	view, attribution := s.mcpTaskView(call.locale, task)
	subject := mcppublic.Subject{Failure: attribution}
	marked, err := s.mcpAssetSessions(call, task.SessionID)
	if err != nil {
		return mcpOutcome{}, err
	}
	if marked[task.SessionID] {
		subject.AssetID, subject.Costs = task.SessionID, s.mcpStepCosts(call.ctx, call.owner, task.SessionID)
	} else {
		view.AssetID = nil
	}
	return mcpOutcome{data: map[string]any{"task": view}, subject: subject}, nil
}

func (s *Server) mcpListTasks(call *mcpCall) (mcpOutcome, error) {
	service := s.application.MCPPublic
	query := generation.ListQuery{Limit: service.ListPageSize, Cursor: mcpStringArgument(call, "cursor")}
	if limit, ok := call.arguments["limit"].(float64); ok {
		query.Limit = min(int(limit), service.ListMaxPageSize)
	}
	asset := mcpStringArgument(call, "asset_id")
	if asset != "" {
		marked, err := s.mcpAssetSessions(call, asset)
		if err != nil {
			return mcpOutcome{}, err
		}
		if !marked[asset] {
			return s.mcpAssetNotFound(call.locale), nil
		}
		query.SessionID = asset
	}
	page, err := s.generation.List(call.ctx, generation.OwnTasks(call.owner), query)
	if mcpChainHasCode(err, generation.ErrInvalidInput) {
		return s.mcpInvalidArguments(call.locale, "$.cursor: is not a cursor of this list"), nil
	}
	if err != nil {
		return mcpOutcome{}, err
	}
	sessions := make([]string, 0, len(page.Tasks))
	for _, task := range page.Tasks {
		sessions = append(sessions, task.SessionID)
	}
	marked, err := s.mcpAssetSessions(call, sessions...)
	if err != nil {
		return mcpOutcome{}, err
	}
	views := make([]mcpTaskView, 0, len(page.Tasks))
	for _, task := range page.Tasks {
		view, _ := s.mcpTaskView(call.locale, task)
		if !marked[task.SessionID] {
			view.AssetID = nil
		}
		views = append(views, view)
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	outcome := mcpOutcome{data: map[string]any{"tasks": views, "next_cursor": next}}
	if asset != "" {
		outcome.subject = mcppublic.Subject{AssetID: asset, Costs: s.mcpStepCosts(call.ctx, call.owner, asset)}
	}
	return outcome, nil
}

type mcpAssetView struct {
	ID          string  `json:"id"`
	Role        string  `json:"role"`
	Variant     *string `json:"variant"`
	Format      *string `json:"format"`
	MediaType   string  `json:"media_type"`
	Size        int64   `json:"size"`
	SHA256      string  `json:"sha256"`
	Required    bool    `json:"required"`
	Delivery    *string `json:"delivery"`
	DownloadURL string  `json:"download_url"`
}

func (s *Server) mcpGetTaskAssets(call *mcpCall, route routing.Two, signedRoute routing.One, ttl time.Duration) (mcpOutcome, error) {
	signed, _ := call.arguments["signed"].(bool)
	if signed && s.taskAssetLinks == nil {
		return mcpOutcome{problem: &mcpProblem{
			Code:    "signed_download_unavailable",
			Message: s.mcpText(call.locale, s.application.MCPPublic.Messages.SignedDownloadUnavailable),
		}}, nil
	}
	task, missing, err := s.mcpOwnedTask(call, mcpStringArgument(call, "task_id"))
	if err != nil {
		return mcpOutcome{}, err
	}
	if missing != nil {
		return *missing, nil
	}
	view, attribution := s.mcpTaskView(call.locale, task)
	subject := mcppublic.Subject{Failure: attribution}
	marked, err := s.mcpAssetSessions(call, task.SessionID)
	if err != nil {
		return mcpOutcome{}, err
	}
	if marked[task.SessionID] {
		subject.AssetID, subject.Costs = task.SessionID, s.mcpStepCosts(call.ctx, call.owner, task.SessionID)
	}
	projected := s.newTaskResponse(call.locale, task)
	assets := make([]mcpAssetView, 0, len(projected.Assets))
	// downloadExpires is the first link's expiry: every link of this answer was
	// signed at once and lasts the same.
	var downloadExpires any
	for _, asset := range projected.Assets {
		entry := mcpAssetView{
			ID: string(asset.ID), Role: asset.Role, MediaType: asset.MediaType, Size: asset.Size,
			SHA256: asset.SHA256, Required: asset.Required,
		}
		if signed {
			token, expires, err := s.taskAssetLinks.SignTaskAsset(task.ID, asset.ID, call.owner, ttl)
			if err != nil {
				return mcpOutcome{}, fmt.Errorf("签署任务产物下载链接: %w", err)
			}
			entry.DownloadURL = call.base + signedRoute.Render(token)
			if downloadExpires == nil {
				downloadExpires = expires.UTC().Format(time.RFC3339)
			}
		} else {
			entry.DownloadURL = call.base + route.Render(string(task.ID), string(asset.ID))
		}
		if asset.Variant != "" {
			variant := asset.Variant
			entry.Variant = &variant
		}
		if asset.Format != "" {
			format := asset.Format
			entry.Format = &format
		}
		if asset.Delivery != "" {
			delivery := asset.Delivery
			entry.Delivery = &delivery
		}
		assets = append(assets, entry)
	}
	return mcpOutcome{
		data: map[string]any{
			"task_id": string(task.ID), "state": view.State,
			"complete": task.State == generation.TaskSucceeded, "assets": assets,
			"download_expires_at": downloadExpires,
		},
		subject: subject,
	}, nil
}

// mcpChainHasCode reports whether any domain error in the chain has the code.
// The kernel wraps a repository's refusal of a cursor in an unavailable error,
// so the code of the cause is the one that says the caller was wrong.
func mcpChainHasCode(err error, code generation.ErrorCode) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if domain, ok := err.(*generation.Error); ok && domain.Code == code {
			return true
		}
	}
	return false
}

// ---- get_status ----------------------------------------------------------------

type mcpAssetStatus struct {
	AssetID        string  `json:"asset_id"`
	Name           *string `json:"name"`
	Title          *string `json:"title"`
	UsedSteps      *int    `json:"used_steps"`
	LastActivityAt *string `json:"last_activity_at"`
}

func (s *Server) mcpGetStatus(call *mcpCall) (mcpOutcome, error) {
	assets, more, err := s.mcpStatus.Assets(call.ctx, string(call.owner))
	if err != nil {
		// The listing is best effort like the block itself: say what could not
		// be read instead of failing the call.
		s.logger.Warn("MCP get_status：资产列表读取失败", "owner_id", string(call.owner), "error", err)
		assets, more = nil, false
	}
	listed := make([]mcpAssetStatus, 0, len(assets))
	for _, asset := range assets {
		used := asset.Used
		entry := mcpAssetStatus{AssetID: asset.SessionID, UsedSteps: &used}
		if asset.Name != "" {
			name := asset.Name
			entry.Name = &name
		}
		if asset.Title != "" {
			title := asset.Title
			entry.Title = &title
		}
		if !asset.LastActivityAt.IsZero() {
			last := asset.LastActivityAt.UTC().Format(timeLayout)
			entry.LastActivityAt = &last
		}
		listed = append(listed, entry)
	}
	return mcpOutcome{data: map[string]any{"assets": listed, "assets_truncated": more}}, nil
}

// ---- list_services ---------------------------------------------------------------

type mcpCostView struct {
	Amount    string `json:"amount"`
	Unit      string `json:"unit"`
	UnitLabel string `json:"unit_label"`
}

type mcpInputView struct {
	MaxLength        int      `json:"max_length,omitempty"`
	Name             string   `json:"name"`
	Label            string   `json:"label"`
	Kind             string   `json:"kind"`
	Required         bool     `json:"required"`
	MinItems         int      `json:"min_items,omitempty"`
	MaxItems         int      `json:"max_items,omitempty"`
	MediaTypes       []string `json:"media_types,omitempty"`
	MaxBytes         int64    `json:"max_bytes,omitempty"`
	TaskCapabilities []string `json:"task_capabilities,omitempty"`
}

type mcpOptionView struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type mcpParameterView struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	// Advised marks a parameter the service advises against changing: a caller
	// that changes it says why in parameter_reasons. Advice is the service's
	// own text about it.
	Advised   bool            `json:"advised,omitempty"`
	Advice    string          `json:"advice,omitempty"`
	Default   json.RawMessage `json:"default,omitempty"`
	Options   []mcpOptionView `json:"options,omitempty"`
	Minimum   *float64        `json:"minimum,omitempty"`
	Maximum   *float64        `json:"maximum,omitempty"`
	MaxLength int             `json:"max_length,omitempty"`
}

type mcpServiceView struct {
	ID           string             `json:"id"`
	Provider     string             `json:"provider"`
	ProviderName string             `json:"provider_name"`
	Model        string             `json:"model"`
	ModelName    string             `json:"model_name"`
	Available    bool               `json:"available"`
	Recommended  bool               `json:"recommended"`
	StepCost     *mcpCostView       `json:"step_cost"`
	Inputs       []mcpInputView     `json:"inputs"`
	Parameters   []mcpParameterView `json:"parameters"`
}

type mcpCapabilityView struct {
	Capability string           `json:"capability"`
	Services   []mcpServiceView `json:"services"`
}

type mcpGroupView struct {
	ID           string              `json:"id"`
	Label        string              `json:"label"`
	Capabilities []mcpCapabilityView `json:"capabilities"`
}

// mcpListServices lists the offerings of the Schema's service groups from the
// same Catalog the web API's offering list reads. Whether a service can run now
// is the runtime's answer; one that cannot is listed as unavailable and is never
// replaced by another.
func (s *Server) mcpListServices(call *mcpCall) (mcpOutcome, error) {
	service := s.application.MCPPublic
	filter := mcpStringArgument(call, "capability")
	availableOnly, _ := call.arguments["available_only"].(bool)
	if filter != "" {
		served := false
		for _, group := range service.ServiceGroups {
			for _, capability := range group.Capabilities {
				served = served || string(capability) == filter
			}
		}
		if !served {
			return s.mcpInvalidArguments(call.locale, "$.capability: is not served through MCP"), nil
		}
	}
	groups := make([]mcpGroupView, 0, len(service.ServiceGroups))
	for _, group := range service.ServiceGroups {
		view := mcpGroupView{ID: group.ID, Label: group.Label, Capabilities: []mcpCapabilityView{}}
		for _, capability := range group.Capabilities {
			if filter != "" && string(capability) != filter {
				continue
			}
			entry := mcpCapabilityView{Capability: string(capability), Services: []mcpServiceView{}}
			for _, offering := range s.generation.Offerings(capability) {
				if !offering.Enabled {
					continue
				}
				available := s.generation.OfferingAvailable(offering.ID)
				if availableOnly && !available {
					continue
				}
				entry.Services = append(entry.Services, mcpServiceOf(offering, available))
			}
			if len(entry.Services) > 0 || filter != "" {
				view.Capabilities = append(view.Capabilities, entry)
			}
		}
		if len(view.Capabilities) > 0 {
			groups = append(groups, view)
		}
	}
	// The listing is made entirely of Schema data, so its copy follows the
	// request's language like the web API's offering list does.
	return mcpOutcome{data: map[string]any{"groups": s.localizeSchemaValue(call.locale, groups)}}, nil
}

func mcpServiceOf(offering generation.Offering, available bool) mcpServiceView {
	advice := func(parameter generation.Parameter) string {
		if parameter.AdjustmentPolicy == "advised" {
			return parameter.AdjustmentReason
		}
		return ""
	}
	view := mcpServiceView{
		ID: string(offering.ID), Provider: offering.ProviderID, ProviderName: offering.ProviderName,
		Model: offering.ModelID, ModelName: offering.ModelName, Available: available, Recommended: offering.Recommended,
		Inputs: []mcpInputView{}, Parameters: []mcpParameterView{},
	}
	if quote := defaultNativePrice(offering); quote != nil {
		view.StepCost = &mcpCostView{Amount: quote.TotalAmount, Unit: quote.Unit, UnitLabel: quote.UnitLabel}
	}
	for _, parameter := range offering.Parameters {
		// Only what a caller may set: the generation tools refuse a fixed,
		// hidden or read-only parameter named in parameters, so the listing does
		// not offer one. The pictures and the source are inputs, not parameters.
		if parameter.Fixed {
			continue
		}
		switch parameter.Type {
		case "asset", "asset-array", "task":
		default:
			if !offering.AgentParameterAllowed(parameter) {
				continue
			}
		}
		if parameter.Semantic == generation.SemanticPrompt {
			// The prompt is an input of its own (the prompt argument), not a parameter.
			view.Inputs = append(view.Inputs, mcpInputView{
				Name: parameter.Name, Label: parameter.Label, Kind: "prompt", Required: parameter.Required, MaxLength: parameter.MaxLength,
			})
			continue
		}
		switch parameter.Type {
		case "asset", "asset-array", "task":
			input := mcpInputView{
				Name: parameter.Name, Label: parameter.Label, Kind: parameter.Type, Required: parameter.Required,
				MinItems: parameter.MinItems, MaxItems: parameter.MaxItems, MediaTypes: parameter.MediaTypes, MaxBytes: parameter.MaxBytes,
			}
			for _, capability := range parameter.TaskCapabilities {
				input.TaskCapabilities = append(input.TaskCapabilities, string(capability))
			}
			view.Inputs = append(view.Inputs, input)
		default:
			entry := mcpParameterView{
				Name: parameter.Name, Label: parameter.Label, Type: parameter.Type, Required: parameter.Required,
				Advised: parameter.AdjustmentPolicy == "advised", Advice: advice(parameter),
				Default: parameter.Default, Minimum: parameter.Minimum, Maximum: parameter.Maximum, MaxLength: parameter.MaxLength,
			}
			for _, option := range parameter.Options {
				entry.Options = append(entry.Options, mcpOptionView{Value: option.Value, Label: option.Label})
			}
			view.Parameters = append(view.Parameters, entry)
		}
	}
	return view
}
