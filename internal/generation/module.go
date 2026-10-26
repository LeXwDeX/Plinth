package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type Dependencies struct {
	Repository      Repository
	Catalog         Catalog
	Providers       ProviderRegistry
	Assets          AssetStore
	Inputs          InputAssetResolver
	PreparedContext PreparedContextVerifier
	// SchemaVersion is the Product Schema revision that produced a prepared input.
	SchemaVersion string
	Cache         CacheActivity
	Clock         Clock
	IDs           IDGenerator
	Random        func() float64
	Logger        *slog.Logger
	// DispatchCheckpointMaxBytes 是私有派发意图的大小上限，来自
	// application.runtime.coordinator.dispatch_checkpoint_max_bytes；
	// 0 视为未声明，按 DefaultDispatchCheckpointMaxBytes 处理。
	DispatchCheckpointMaxBytes int64
	// DefaultRequestTimeout 是单次上游请求的兜底时长，来自
	// application.runtime.coordinator.default_request_timeout_seconds；
	// 0 视为未声明，按 DefaultRequestTimeout 处理。
	// PerOwnerMaxInflightTasks 限定单用户进行中（未终态）任务数量
	// （SEC-08，Schema 声明；<=0 不限，仅测试允许）。
	PerOwnerMaxInflightTasks int
	DefaultRequestTimeout    time.Duration
	// InputFitter, when set, re-encodes input images that exceed an
	// Offering's limits before they are resolved and frozen (#276).
	InputFitter InputFitter
	// Conversions recognizes the Product Schema's declared format
	// conversions. Nil disables local delivery completion.
	Conversions DeliveryConversionRule
}

// DefaultDispatchCheckpointMaxBytes 是目录未声明时的私有派发意图上限。
const DefaultDispatchCheckpointMaxBytes = 65536

// DefaultRequestTimeout 是目录未声明时的单次上游请求兜底时长。
const DefaultRequestTimeout = 30 * time.Minute

type Module struct {
	repository                 Repository
	catalog                    Catalog
	providers                  ProviderRegistry
	assets                     AssetStore
	inputs                     InputAssetResolver
	inputFitter                InputFitter
	conversions                DeliveryConversionRule
	preparedStore              PreparedInputStore
	preparedContext            PreparedContextVerifier
	schemaVersion              string
	cache                      CacheActivity
	clock                      Clock
	ids                        IDGenerator
	logger                     *slog.Logger
	random                     func() float64
	randomMu                   sync.Mutex
	coordinatorMu              sync.Mutex
	activeSteps                int
	defaultCancellation        CancellationPolicy
	perOwnerMaxInflightTasks   int
	dispatchCheckpointMaxBytes int64
	defaultRequestTimeout      time.Duration
}

type CacheActivity interface {
	Begin(cachemanagement.Operation) func()
}

type noopCacheActivity struct{}

func (noopCacheActivity) Begin(cachemanagement.Operation) func() {
	return func() {}
}

func New(deps Dependencies) (*Module, error) {
	switch {
	case deps.Repository == nil:
		return nil, errors.New("generation: 缺少 Repository")
	case deps.Catalog == nil:
		return nil, errors.New("generation: 缺少 Catalog")
	case deps.Providers == nil:
		return nil, errors.New("generation: 缺少 ProviderRegistry")
	case deps.Assets == nil:
		return nil, errors.New("generation: 缺少 AssetStore")
	}
	if deps.Clock == nil {
		deps.Clock = systemClock{}
	}
	if deps.IDs == nil {
		deps.IDs = randomIDs{}
	}
	if deps.Inputs == nil {
		deps.Inputs = unavailableInputAssets{}
	}
	if deps.Cache == nil {
		deps.Cache = noopCacheActivity{}
	}
	if deps.Random == nil {
		deps.Random = rand.Float64
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	dispatchCheckpointMaxBytes := deps.DispatchCheckpointMaxBytes
	if dispatchCheckpointMaxBytes <= 0 {
		dispatchCheckpointMaxBytes = DefaultDispatchCheckpointMaxBytes
	}
	perOwnerMaxInflight := deps.PerOwnerMaxInflightTasks
	defaultRequestTimeout := deps.DefaultRequestTimeout
	if defaultRequestTimeout <= 0 {
		defaultRequestTimeout = DefaultRequestTimeout
	}
	var defaultCancellation CancellationPolicy
	if source, ok := deps.Catalog.(interface{ DefaultCancellationPolicy() CancellationPolicy }); ok {
		defaultCancellation = source.DefaultCancellationPolicy()
	}
	return &Module{
		repository:                 deps.Repository,
		catalog:                    deps.Catalog,
		providers:                  deps.Providers,
		assets:                     deps.Assets,
		inputs:                     deps.Inputs,
		inputFitter:                deps.InputFitter,
		conversions:                deps.Conversions,
		preparedStore:              preparedStoreOf(deps.Repository),
		preparedContext:            deps.PreparedContext,
		schemaVersion:              deps.SchemaVersion,
		cache:                      deps.Cache,
		clock:                      deps.Clock,
		ids:                        deps.IDs,
		logger:                     deps.Logger,
		random:                     deps.Random,
		defaultCancellation:        defaultCancellation,
		dispatchCheckpointMaxBytes: dispatchCheckpointMaxBytes,
		defaultRequestTimeout:      defaultRequestTimeout,
		perOwnerMaxInflightTasks:   perOwnerMaxInflight,
	}, nil
}

func (m *Module) Submit(ctx context.Context, cmd SubmitCommand) (Accepted, error) {
	if cmd.OwnerID == "" {
		return Accepted{}, &Error{Code: ErrInvalidInput, Operation: "submit", Detail: "缺少任务所有者"}
	}
	if !cmd.Capability.Valid() {
		return Accepted{}, &Error{
			Code:      ErrInvalidCapability,
			Operation: "submit",
			Detail:    fmt.Sprintf("未知能力 %q", cmd.Capability),
		}
	}
	if len(cmd.IdempotencyKey) > 200 {
		return Accepted{}, &Error{
			Code:      ErrInvalidInput,
			Operation: "submit",
			Detail:    "幂等键最多允许 200 个字符",
		}
	}
	if len(cmd.SessionID) > 200 {
		return Accepted{}, &Error{Code: ErrInvalidInput, Operation: "submit", Detail: "会话标识最多允许 200 个字符"}
	}

	offeringID, err := m.availableOfferingID(cmd.Capability, cmd.OfferingID)
	if err != nil {
		return Accepted{}, err
	}
	offering, normalized, err := m.catalog.Normalize(cmd.Capability, offeringID, cmd.Input)
	if err != nil {
		return Accepted{}, err
	}
	offering, normalized, err = m.fitInputs(ctx, cmd.OwnerID, cmd.Capability, offering, normalized)
	if err != nil {
		return Accepted{}, err
	}
	quote, err := offering.EvaluateNativeQuote(normalized)
	if err != nil {
		return Accepted{}, &Error{Code: ErrInvalidInput, Operation: "submit", Cause: err}
	}
	if quote != nil && cmd.Admission == nil {
		return Accepted{}, &Error{Code: ErrForbidden, Operation: "submit"}
	}
	if err := validateRecoverySubmission(offering, normalized, cmd.IdempotencyKey, cmd.Admission); err != nil {
		return Accepted{}, err
	}
	resolved, err := m.inputs.Resolve(ctx, cmd.OwnerID, offering, normalized)
	if err != nil {
		return Accepted{}, err
	}
	dependencies, err := m.resolveTaskDependencies(
		ctx,
		cmd.OwnerID,
		offering,
		normalized,
		cmd.SessionID,
	)
	if err != nil {
		return Accepted{}, err
	}
	if _, ok := m.providers.Provider(offering); !ok {
		return Accepted{}, &Error{
			Code:      ErrUnavailable,
			Operation: "submit",
			Detail:    fmt.Sprintf("供给项 %q 的适配器 %q 未注册", offering.ID, offering.AdapterKey),
		}
	}
	if err := m.validateProviderInput(ctx, cmd.OwnerID, cmd.SessionID, offering, normalized, resolved, dependencies); err != nil {
		return Accepted{}, err
	}
	frozenOffering, err := resolveConditionalOutputs(offering, normalized)
	if err != nil {
		return Accepted{}, &Error{Code: ErrInvalidInput, Operation: "submit", Cause: err}
	}

	now := m.clock.Now()
	task := Task{
		NativeQuote:    quote,
		ID:             m.ids.TaskID(),
		OwnerID:        cmd.OwnerID,
		SessionID:      cmd.SessionID,
		Capability:     cmd.Capability,
		Offering:       frozenOffering,
		Input:          normalized,
		RequestHash:    hashRequest(offering, normalized),
		IdempotencyKey: cmd.IdempotencyKey,
		Admission:      cmd.Admission,
		State:          TaskQueued,
		Progress:       0,
		NextActionAt:   now,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
		Assets:         []Asset{},
	}
	if intent, ok := m.declaredConversion(frozenOffering, normalized); ok {
		task.Conversion = &intent
	}
	event := Event{
		TaskID:    task.ID,
		Type:      "task.submitted",
		Data:      eventData(map[string]any{"offering_id": offering.ID, "capability": task.Capability}),
		CreatedAt: now,
	}
	created, replayed, err := m.createTask(ctx, task, event)
	if err != nil {
		return Accepted{}, mapRepositoryError("submit", err)
	}
	return Accepted{TaskID: created.ID, Replayed: replayed}, nil
}

// createTask is the one Task admission. A paid format conversion must go
// through the repository's transactional guard: it reads every active Task of
// the same owner and session and refuses a second run of the same exact
// source with an overlapping target format. A repository without the guard
// cannot admit the conversion, because an unprotected first check would race
// another submission. Ordinary Tasks keep the existing Create path.
func (m *Module) createTask(ctx context.Context, task Task, event Event) (Task, bool, error) {
	if task.Conversion == nil {
		return m.repository.Create(ctx, task, event, m.perOwnerMaxInflightTasks)
	}
	guarded, ok := m.repository.(ConversionGuardedRepository)
	if !ok {
		return Task{}, false, &Error{Code: ErrUnavailable, Operation: "submit", Detail: "转换任务的提交保护未配置"}
	}
	return guarded.CreateConversionGuarded(ctx, task, event, m.perOwnerMaxInflightTasks, m.declaredConversionOfTask)
}

// declaredConversion names the conversion a Task runs from its frozen
// Offering and input. The same rule serves a new submission and every active
// stored Task the repository guard reads, so an old Task is recognized without
// a persisted conversion marker.
func (m *Module) declaredConversion(offering Offering, input json.RawMessage) (ConversionIntent, bool) {
	if m.conversions == nil {
		return ConversionIntent{}, false
	}
	return m.conversions.ConversionIntent(offering, input)
}

func (m *Module) declaredConversionOfTask(task Task) (ConversionIntent, bool, error) {
	if task.Conversion != nil {
		return *task.Conversion, true, nil
	}
	intent, ok := m.declaredConversion(task.Offering, task.Input)
	return intent, ok, nil
}

// DefaultOfferingID 返回该能力的默认供给项：优先显式声明 Default 的
// 可用 Offering，其次按目录顺序的第一个可用项。兼容入口与原生入口
// 共用此单一实现，避免两套默认选路规则漂移。
func (m *Module) DefaultOfferingID(capability Capability) (OfferingID, error) {
	var firstAvailable OfferingID
	for _, offering := range m.catalog.List(capability) {
		if !m.OfferingAvailable(offering.ID) {
			continue
		}
		if firstAvailable == "" {
			firstAvailable = offering.ID
		}
		if offering.Default {
			return offering.ID, nil
		}
	}
	if firstAvailable != "" {
		return firstAvailable, nil
	}
	return "", &Error{
		Code:      ErrUnavailable,
		Operation: "select_default_offering",
		Detail:    fmt.Sprintf("能力 %q 当前没有可用的供给项", capability),
	}
}

func (m *Module) availableOfferingID(
	capability Capability,
	requested OfferingID,
) (OfferingID, error) {
	if requested != "" {
		return requested, nil
	}
	return m.DefaultOfferingID(capability)
}

func (m *Module) resolveTaskDependencies(
	ctx context.Context,
	owner OwnerID,
	offering Offering,
	input json.RawMessage,
	session string,
) (map[string]TaskDependency, error) {
	var values map[string]any
	if err := json.Unmarshal(input, &values); err != nil {
		return nil, &Error{
			Code:      ErrInvalidInput,
			Operation: "resolve_task_dependencies",
			Detail:    "任务依赖输入无效",
			Cause:     err,
		}
	}
	dependencies := map[string]TaskDependency{}
	for _, parameter := range offering.Parameters {
		if parameter.Type != "task" {
			continue
		}
		if parameter.TaskSourceBinding != "" && (parameter.TaskSourceBinding != "provider" || parameter.TaskSourceKind == schema.TaskSourceAssets || !parameter.Required || !parameter.SameProvider || len(parameter.TaskOfferings) != 1) {
			return nil, invalidTaskDependency(offering, parameter)
		}
		if parameter.TaskSourceKind != "" && parameter.TaskSourceKind != schema.TaskSourceUpstreamJob && parameter.TaskSourceKind != schema.TaskSourceAssets {
			return nil, invalidTaskDependency(offering, parameter)
		}
		raw, exists := values[parameter.Name]
		if !exists {
			continue
		}
		text, ok := raw.(string)
		if !ok || text == "" {
			return nil, invalidTaskDependency(offering, parameter)
		}
		aggregate, err := m.repository.Load(ctx, TaskID(text))
		if err != nil {
			if errors.Is(err, RepositoryNotFound) {
				return nil, invalidTaskDependency(offering, parameter)
			}
			return nil, mapRepositoryError("resolve_task_dependencies", err)
		}
		attemptStateAllowed := aggregate.CurrentAttempt != nil &&
			(aggregate.CurrentAttempt.State == "" || aggregate.CurrentAttempt.State == AttemptUpstreamSucceeded ||
				(parameter.TaskSourceKind == schema.TaskSourceAssets && aggregate.CurrentAttempt.State == AttemptDeliveryCompleted))
		if aggregate.Task.ID != TaskID(text) || aggregate.Task.OwnerID != owner ||
			(parameter.SameSession && (session == "" || aggregate.Task.SessionID != session)) ||
			aggregate.Task.State != TaskSucceeded ||
			aggregate.CurrentAttempt == nil || !attemptStateAllowed ||
			!taskCapabilityAllowed(parameter.TaskCapabilities, aggregate.Task.Capability) ||
			!taskOfferingAllowed(parameter.TaskOfferings, aggregate.Task.Offering.ID) ||
			(parameter.SameProvider &&
				aggregate.Task.Offering.ProviderID != offering.ProviderID) {
			return nil, invalidTaskDependency(offering, parameter)
		}
		facts := []Task{aggregate.Task}
		if err := m.withUpstreamJobFacts(ctx, facts); err != nil {
			return nil, err
		}
		if UpstreamJobExpired(parameter, facts[0], m.dependencyNow()) {
			return nil, expiredTaskDependency(offering, parameter)
		}
		boundSession := ""
		if parameter.SameSession {
			boundSession = aggregate.Task.SessionID
		}
		if parameter.TaskSourceKind == schema.TaskSourceAssets {
			if aggregate.Task.ID != TaskID(text) ||
				(aggregate.CurrentAttempt.State != AttemptUpstreamSucceeded && aggregate.CurrentAttempt.State != AttemptDeliveryCompleted) {
				return nil, invalidTaskDependency(offering, parameter)
			}
			asset, err := SelectTaskSourceAsset(parameter, aggregate.Task)
			if err != nil {
				return nil, invalidTaskDependency(offering, parameter)
			}
			if err := m.verifyTaskDependencyAsset(ctx, asset); err != nil {
				return nil, err
			}
			dependencies[parameter.Name] = TaskDependency{
				TaskID: aggregate.Task.ID, Capability: aggregate.Task.Capability,
				OfferingID: aggregate.Task.Offering.ID, ProviderID: aggregate.Task.Offering.ProviderID,
				ModelID: aggregate.Task.Offering.ModelID, SessionID: boundSession, Assets: []Asset{asset},
			}
			continue
		}
		// A refetched result (#295) was delivered by its failed source's
		// accepted job; the refetch Attempt itself holds none.
		call, callErr := m.providerCallFor(ctx, aggregate.Task, aggregate.CurrentAttempt)
		if callErr != nil && !errors.Is(callErr, errRefetchSourceChanged) && !errors.Is(callErr, RepositoryNotFound) {
			return nil, callErr
		}
		if callErr != nil || call.Job == nil {
			return nil, invalidTaskDependency(offering, parameter)
		}
		port, ok := m.providers.Provider(aggregate.Task.Offering)
		if !ok {
			return nil, &Error{Code: ErrUnavailable, Operation: "resolve_task_dependencies", Detail: "任务来源适配器不可用"}
		}
		job := *call.Job
		if resolver, ok := port.(DependencyJobResolver); ok {
			job, err = resolver.ResolveDependencyJob(DependencyJobRequest{
				Offering: call.Offering, Job: job,
				Opaque: append(json.RawMessage(nil), call.Opaque...),
			})
			if err != nil {
				return nil, invalidTaskDependency(offering, parameter)
			}
		}
		if strings.TrimSpace(job.RemoteID) == "" || job.ProviderID != aggregate.Task.Offering.ProviderID ||
			job.ProviderID != call.Job.ProviderID {
			return nil, invalidTaskDependency(offering, parameter)
		}
		var binding json.RawMessage
		if parameter.TaskSourceBinding != "" {
			if parameter.TaskSourceBinding != "provider" || aggregate.CurrentAttempt.ID == "" || aggregate.CurrentAttempt.State != AttemptUpstreamSucceeded {
				return nil, invalidTaskDependency(offering, parameter)
			}
			resolver, ok := port.(DependencyBindingResolver)
			if !ok {
				return nil, invalidTaskDependency(offering, parameter)
			}
			for _, asset := range aggregate.Task.Assets {
				if err := m.verifyTaskDependencyAsset(ctx, asset); err != nil {
					return nil, err
				}
			}
			binding, err = resolver.ResolveDependencyBinding(DependencyBindingRequest{Offering: aggregate.Task.Offering, TaskID: aggregate.Task.ID, AttemptID: aggregate.CurrentAttempt.ID, Job: job, Claims: aggregate.Claims, Assets: aggregate.Task.Assets})
			if err == nil {
				binding, err = CanonicalDependencyBinding(binding)
			}
			if err != nil {
				return nil, invalidTaskDependency(offering, parameter)
			}
		}
		dependencies[parameter.Name] = TaskDependency{
			SessionID: boundSession, ProviderBinding: binding,
			TaskID:      aggregate.Task.ID,
			Capability:  aggregate.Task.Capability,
			OfferingID:  aggregate.Task.Offering.ID,
			ProviderID:  aggregate.Task.Offering.ProviderID,
			ModelID:     aggregate.Task.Offering.ModelID,
			UpstreamJob: job,
			Assets:      append([]Asset(nil), aggregate.Task.Assets...),
		}
	}
	return dependencies, nil
}

func (m *Module) verifyTaskDependencyAsset(ctx context.Context, asset Asset) error {
	if m.assets == nil {
		return &Error{Code: ErrUnavailable, Operation: "resolve_task_dependencies", Detail: "来源资产存储未配置"}
	}
	body, err := m.assets.Open(ctx, asset)
	if err != nil {
		return &Error{Code: ErrUnavailable, Operation: "resolve_task_dependencies", Detail: "来源资产暂不可读取", Cause: err}
	}
	if body == nil {
		return &Error{Code: ErrUnavailable, Operation: "resolve_task_dependencies", Detail: "来源资产暂不可读取"}
	}
	hash := sha256.New()
	size, readErr := io.Copy(hash, io.LimitReader(body, asset.Size+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || size != asset.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), asset.SHA256) {
		return &Error{Code: ErrConflict, Operation: "resolve_task_dependencies", Detail: "来源资产内容或摘要已变化", Cause: errors.Join(readErr, closeErr)}
	}
	if err := ctx.Err(); err != nil {
		return &Error{Code: ErrUnavailable, Operation: "resolve_task_dependencies", Detail: "来源资产校验已取消", Cause: err}
	}
	return nil
}

// expiredTaskDependency refuses a task reference whose Provider job is past
// the Schema's declared age (#325). It is an input refusal like any other
// dependency mismatch, and it wraps ErrSourceExpired so callers can name it.
func expiredTaskDependency(offering Offering, parameter Parameter) error {
	if refusal := taskDependencyRefusal(offering, parameter, schema.InputRefusalSourceExpired, ErrSourceExpired); refusal != nil {
		return refusal
	}
	return &Error{
		Code:      ErrInvalidInput,
		Operation: "resolve_task_dependencies",
		Detail:    fmt.Sprintf("任务依赖参数 %q 引用的服务商原任务已超过保留期", parameter.Name),
		Cause:     ErrSourceExpired,
	}
}

func (m *Module) dependencyNow() time.Time {
	if m.clock == nil {
		return time.Now()
	}
	return m.clock.Now()
}

func invalidTaskDependency(offering Offering, parameter Parameter) error {
	if refusal := taskDependencyRefusal(offering, parameter, schema.InputRefusalSourceUnsupported, nil); refusal != nil {
		return refusal
	}
	return &Error{
		Code:      ErrInvalidInput,
		Operation: "resolve_task_dependencies",
		Detail:    fmt.Sprintf("任务依赖参数 %q 不存在、不属于当前用户、尚未成功、能力或供给方不匹配", parameter.Name),
	}
}

// taskDependencyRefusal returns the Offering's UserFacing reason for a refused
// Task source. The public text uses only the parameter label; an Offering that
// declares no copy keeps its internal diagnostic and the tool layer hides it.
func taskDependencyRefusal(offering Offering, parameter Parameter, reason schema.InputRefusalReason, cause error) *Error {
	values := schema.InputRefusalValues{}
	if strings.TrimSpace(parameter.Label) != "" {
		values.Label = parameter.Label
	} else {
		reason = schema.InputRefusalParameters
	}
	refusal := offering.ExecutionMessages.InputRefusal(reason, values)
	if refusal == nil {
		return nil
	}
	refusal.Cause = cause
	return refusal
}

func taskCapabilityAllowed(allowed []Capability, actual Capability) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, capability := range allowed {
		if capability == actual {
			return true
		}
	}
	return false
}

type unavailableInputAssets struct{}

func (unavailableInputAssets) Resolve(
	_ context.Context,
	_ OwnerID,
	offering Offering,
	_ json.RawMessage,
) (map[string][]InputAsset, error) {
	for _, parameter := range offering.Parameters {
		if parameter.Type == "asset" || parameter.Type == "asset-array" {
			return nil, &Error{
				Code:      ErrUnavailable,
				Operation: "resolve_input_assets",
				Detail:    "输入资产服务未配置",
			}
		}
	}
	return map[string][]InputAsset{}, nil
}

func (unavailableInputAssets) Open(context.Context, InputAsset) (io.ReadCloser, error) {
	return nil, errors.New("输入资产服务未配置")
}

func (m *Module) Get(ctx context.Context, owner OwnerID, id TaskID) (Task, error) {
	return m.GetScoped(ctx, OwnTasks(owner), id)
}

// Exists 报告任务是否已被任意所有者持久化，含保留的 tombstone。
// 存储错误返回未知错误，不把未知任务归属当成不存在。
func (m *Module) Exists(ctx context.Context, id TaskID) (bool, error) {
	existed, err := m.TaskExisted(ctx, id)
	if err != nil {
		return existed, mapRepositoryError("exists", err)
	}
	return existed, nil
}

// GetScoped 与 List/Delete 共用 TaskListScope 语义：持有 tasks.read.any
// 的调用方使用 AllTasks() 跨 owner 读取，普通调用方使用 OwnTasks(owner)。
func (m *Module) GetScoped(ctx context.Context, scope TaskListScope, id TaskID) (Task, error) {
	if id == "" ||
		(!scope.AllOwners() && scope.OwnerID() == "") ||
		(scope.AllOwners() && scope.OwnerID() != "") {
		return Task{}, &Error{Code: ErrNotFound, Operation: "get", Detail: "任务不存在"}
	}
	if !scope.AllOwners() {
		task, err := m.repository.Get(ctx, scope.OwnerID(), id)
		if err != nil {
			return Task{}, mapRepositoryError("get", err)
		}
		tasks := []Task{task}
		if err := m.withUpstreamJobFacts(ctx, tasks); err != nil {
			return Task{}, err
		}
		return tasks[0], nil
	}
	aggregate, err := m.repository.Load(ctx, id)
	if err != nil {
		return Task{}, mapRepositoryError("get", err)
	}
	task := aggregate.Task
	if aggregate.CurrentAttempt != nil {
		task.ProviderData = append(json.RawMessage(nil), aggregate.CurrentAttempt.Opaque...)
	}
	tasks := []Task{task}
	if err := m.withUpstreamJobFacts(ctx, tasks); err != nil {
		return Task{}, err
	}
	return tasks[0], nil
}

// withUpstreamJobFacts fills each succeeded Task's upstream-job facts
// (#326), owner by owner, when the repository records them.
func (m *Module) withUpstreamJobFacts(ctx context.Context, tasks []Task) error {
	reader, ok := m.repository.(UpstreamJobFactsReader)
	if !ok {
		return nil
	}
	byOwner := map[OwnerID][]TaskID{}
	for _, task := range tasks {
		if task.State == TaskSucceeded {
			byOwner[task.OwnerID] = append(byOwner[task.OwnerID], task.ID)
		}
	}
	for owner, ids := range byOwner {
		facts, err := reader.UpstreamJobFacts(ctx, owner, ids)
		if err != nil {
			return mapRepositoryError("upstream_job_facts", err)
		}
		for index := range tasks {
			if fact, found := facts[tasks[index].ID]; found && tasks[index].OwnerID == owner {
				tasks[index].UpstreamJobCreatedAt, tasks[index].UpstreamJobGone = fact.CreatedAt, fact.Gone
			}
		}
	}
	return nil
}

// LookupByIdempotency returns an already accepted Task for reconciliation.
// It deliberately does not re-normalize current Catalog input or call Submit:
// a changed Offering must not cause a second paid operation while a prior
// acceptance is being recovered.
func (m *Module) LookupByIdempotency(ctx context.Context, owner OwnerID, key string) (Task, error) {
	if owner == "" || key == "" || len(key) > 200 {
		return Task{}, &Error{Code: ErrInvalidInput, Operation: "lookup_idempotency", Detail: "缺少任务身份"}
	}
	task, err := m.repository.GetByIdempotency(ctx, owner, key)
	if err != nil {
		return Task{}, mapRepositoryError("lookup_idempotency", err)
	}
	return task, nil
}

// TaskExisted 报告任务是否存在（含已 tombstone 删除的任务，GEN-02）。
// 数据库错误按失败返回，由调用方 fail-closed。
func (m *Module) TaskExisted(ctx context.Context, id TaskID) (bool, error) {
	if id == "" {
		return false, nil
	}
	_, err := m.repository.Load(ctx, id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, RepositoryNotFound):
		tombstoned, tombstoneErr := m.repository.TaskTombstoned(ctx, id)
		if tombstoneErr != nil {
			return true, tombstoneErr
		}
		return tombstoned, nil
	default:
		return true, err
	}
}

func (m *Module) List(
	ctx context.Context,
	scope TaskListScope,
	query ListQuery,
) (TaskPage, error) {
	if !scope.AllOwners() && scope.OwnerID() == "" {
		return TaskPage{}, &Error{Code: ErrNotFound, Operation: "list", Detail: "任务不存在"}
	}
	if scope.AllOwners() && scope.OwnerID() != "" {
		return TaskPage{}, &Error{
			Code:      ErrInvalidInput,
			Operation: "list",
			Detail:    "任务查询范围无效",
		}
	}
	if query.Capability != "" && !query.Capability.Valid() {
		return TaskPage{}, &Error{
			Code:      ErrInvalidCapability,
			Operation: "list",
			Detail:    "能力名称无效",
		}
	}
	for _, state := range query.States {
		if !state.Valid() {
			return TaskPage{}, &Error{
				Code:      ErrInvalidInput,
				Operation: "list",
				Detail:    fmt.Sprintf("任务状态 %q 无效", state),
			}
		}
	}
	for _, state := range query.ExcludedStates {
		if !state.Valid() {
			return TaskPage{}, &Error{
				Code:      ErrInvalidInput,
				Operation: "list",
				Detail:    fmt.Sprintf("排除任务状态 %q 无效", state),
			}
		}
	}
	if query.Offset < 0 || (query.Cursor != "" && query.Offset != 0) {
		return TaskPage{}, &Error{
			Code:      ErrInvalidInput,
			Operation: "list",
			Detail:    "任务分页参数无效",
		}
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.Limit > 100 {
		query.Limit = 100
	}
	if len(query.SessionID) > 200 {
		return TaskPage{}, &Error{Code: ErrInvalidInput, Operation: "list", Detail: "会话标识最多允许 200 个字符"}
	}
	page, err := m.repository.List(ctx, scope, query)
	if err != nil {
		return TaskPage{}, mapRepositoryError("list", err)
	}
	if page.Tasks == nil {
		page.Tasks = []Task{}
	}
	if err := m.withUpstreamJobFacts(ctx, page.Tasks); err != nil {
		return TaskPage{}, err
	}
	return page, nil
}

func (m *Module) Delete(ctx context.Context, command DeleteCommand) error {
	if err := validateTaskDeletion(command); err != nil {
		return err
	}
	aggregate, err := m.repository.Load(ctx, command.TaskID)
	if err != nil {
		return mapRepositoryError("delete", err)
	}
	if (!command.Scope.AllOwners() &&
		aggregate.Task.OwnerID != command.Scope.OwnerID()) ||
		aggregate.Task.Capability != command.ExpectedCapability {
		return &Error{Code: ErrNotFound, Operation: "delete"}
	}

	if aggregate.CurrentAttempt != nil &&
		aggregate.CurrentAttempt.UpstreamJob != nil {
		provider, available := m.providers.Provider(aggregate.Task.Offering)
		if available {
			if deleter, supported := provider.(ProviderTaskDeleter); supported {
				request := DeleteProviderRequest{
					TaskID:    aggregate.Task.ID,
					AttemptID: aggregate.CurrentAttempt.ID,
					Offering:  aggregate.Task.Offering,
					Job:       *aggregate.CurrentAttempt.UpstreamJob,
				}
				if err := deleter.DeleteProviderTask(ctx, request); err != nil {
					return &Error{
						Code:      ErrUnavailable,
						Operation: "delete_provider_task",
						Cause:     err,
					}
				}
			}
		}
	}

	now := m.clock.Now()
	err = m.repository.Tombstone(ctx, Tombstone{
		TaskID:          aggregate.Task.ID,
		ActorID:         command.ActorID,
		ExpectedVersion: aggregate.Task.Version,
		DeletedAt:       now,
		Event: Event{
			TaskID: aggregate.Task.ID,
			Type:   command.Policy.AuditEventType,
			Data: eventData(map[string]any{
				"actor_id": command.ActorID,
				"policy":   command.Policy.LocalRecordPolicy,
			}),
			CreatedAt: now,
		},
	})
	if err != nil {
		return mapRepositoryError("delete", err)
	}
	return nil
}

func validateTaskDeletion(command DeleteCommand) error {
	if command.ActorID == "" ||
		command.TaskID == "" ||
		(!command.Scope.AllOwners() && command.Scope.OwnerID() == "") ||
		(command.Scope.AllOwners() && command.Scope.OwnerID() != "") ||
		!command.ExpectedCapability.Valid() {
		return &Error{Code: ErrInvalidInput, Operation: "delete"}
	}
	policy := command.Policy
	if policy.LocalRecordPolicy != "tombstone" ||
		policy.UpstreamPolicy != "when-supported" ||
		policy.UpstreamFailurePolicy != "abort" ||
		policy.GeneratedAssetPolicy != "retain" ||
		policy.AuditEventType == "" {
		return &Error{Code: ErrInvalidInput, Operation: "delete_policy"}
	}
	return nil
}

func (m *Module) Events(
	ctx context.Context,
	owner OwnerID,
	id TaskID,
	after int64,
	limit int,
) ([]Event, error) {
	return m.EventsScoped(ctx, OwnTasks(owner), id, after, limit)
}

func (m *Module) EventsScoped(
	ctx context.Context,
	scope TaskListScope,
	id TaskID,
	after int64,
	limit int,
) ([]Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	owner := scope.OwnerID()
	if scope.AllOwners() {
		// 跨 owner 读取先按 scope 解析任务归属，事件仓储仍按真实 owner 过滤。
		task, err := m.GetScoped(ctx, scope, id)
		if err != nil {
			return nil, err
		}
		owner = task.OwnerID
	}
	events, err := m.repository.Events(ctx, owner, id, after, limit)
	if err != nil {
		return nil, mapRepositoryError("events", err)
	}
	if events == nil {
		events = []Event{}
	}
	return events, nil
}

type ProviderWebhookCommand struct {
	ProviderID string
	RemoteID   string
	Payload    []byte
	EventType  string
}

type ProviderWebhookResult struct {
	TaskID  TaskID
	Applied bool
}

func (m *Module) IngestProviderWebhook(
	ctx context.Context,
	command ProviderWebhookCommand,
) (ProviderWebhookResult, error) {
	providerID := strings.TrimSpace(command.ProviderID)
	remoteID := strings.TrimSpace(command.RemoteID)
	eventType := strings.TrimSpace(command.EventType)
	if providerID == "" || remoteID == "" ||
		len(command.Payload) == 0 || eventType == "" {
		return ProviderWebhookResult{}, &Error{
			Code:      ErrInvalidInput,
			Operation: "ingest_provider_webhook",
		}
	}
	aggregate, err := m.repository.LoadByUpstreamJob(
		ctx,
		providerID,
		remoteID,
	)
	if err != nil {
		return ProviderWebhookResult{}, mapRepositoryError(
			"ingest_provider_webhook",
			err,
		)
	}
	result := ProviderWebhookResult{TaskID: aggregate.Task.ID}
	if aggregate.Task.State == TaskSucceeded && postSuccessObservationActive(aggregate.Task) {
		// §6.2 触点 5：声明 observe 且 SUCCEEDED 的父任务放行进成功后
		// 观察的受约束应用（仅 COMPLETED + 新可选产物；回执无检查点
		// 上下文一律保守丢弃）。删除竞态由 tombstone 排除裁决（上方
		// LoadByUpstreamJob 已排除，迟到 webhook 不可复活已删除任务）。
		attempt := aggregate.CurrentAttempt
		if attempt == nil || attempt.UpstreamJob == nil {
			return result, nil
		}
		observation, decodeErr := m.decodeWebhookObservation(
			ctx,
			aggregate,
			attempt,
			providerID,
			remoteID,
			command.Payload,
		)
		if decodeErr != nil {
			return ProviderWebhookResult{}, decodeErr
		}
		if observation == nil {
			return result, nil
		}
		if observation.Dispatch != nil {
			// webhook 路径无检查点栅栏：携带回执的成功后观察整体保守
			// 丢弃（不结算、不改资源态、不重发）。
			return result, nil
		}
		if err := m.applyPostSuccessObservation(
			ctx,
			Lease{Aggregate: aggregate},
			attempt,
			*observation,
			nil,
		); err != nil {
			return ProviderWebhookResult{}, err
		}
		result.Applied = true
		return result, nil
	}
	if aggregate.Task.State.Terminal() ||
		aggregate.Task.State == TaskMaterializing {
		return result, nil
	}
	attempt := aggregate.CurrentAttempt
	if attempt == nil || attempt.UpstreamJob == nil ||
		attempt.UpstreamJob.ProviderID != providerID ||
		attempt.UpstreamJob.RemoteID != remoteID {
		return ProviderWebhookResult{}, &Error{
			Code:      ErrNotFound,
			Operation: "ingest_provider_webhook",
		}
	}
	observation, decodeErr := m.decodeWebhookObservation(
		ctx,
		aggregate,
		attempt,
		providerID,
		remoteID,
		command.Payload,
	)
	if decodeErr != nil {
		return ProviderWebhookResult{}, decodeErr
	}
	if observation == nil {
		return result, nil
	}
	if staleObservation(aggregate.Task, attempt, *observation) {
		return result, nil
	}
	err = m.applyObservation(
		ctx,
		Lease{Aggregate: aggregate},
		attempt,
		*observation,
		eventType,
		nil,
	)
	if err != nil {
		return ProviderWebhookResult{}, err
	}
	result.Applied = true
	return result, nil
}

// decodeWebhookObservation 解码并校验 webhook 载荷；解码器不可用、载荷
// 非法或作业身份不匹配返回错误；陈旧 observation 由调用方各自判定
// （成功前走 staleObservation，成功后走 applyPostSuccessObservation）。
func (m *Module) decodeWebhookObservation(
	ctx context.Context,
	aggregate Aggregate,
	attempt *Attempt,
	providerID string,
	remoteID string,
	payload []byte,
) (*Observation, error) {
	provider, available := m.providers.Provider(aggregate.Task.Offering)
	if !available {
		return nil, &Error{
			Code:      ErrUnavailable,
			Operation: "ingest_provider_webhook",
		}
	}
	decoder, supported := provider.(ProviderWebhookDecoder)
	if !supported {
		return nil, &Error{
			Code:      ErrUnavailable,
			Operation: "ingest_provider_webhook",
		}
	}
	observation, err := decoder.DecodeWebhook(ctx, WebhookDecodeRequest{
		TaskID:    aggregate.Task.ID,
		AttemptID: attempt.ID,
		Offering:  aggregate.Task.Offering,
		Job:       *attempt.UpstreamJob,
		Payload:   payload,
	})
	if errors.Is(err, ErrWebhookNotApplicable) {
		return nil, nil
	}
	if err != nil {
		return nil, &Error{
			Code:      ErrInvalidInput,
			Operation: "decode_provider_webhook",
			Cause:     err,
		}
	}
	if err := validateObservation(observation); err != nil {
		return nil, &Error{
			Code:      ErrInvalidInput,
			Operation: "decode_provider_webhook",
			Cause:     err,
		}
	}
	if observation.Job == nil ||
		observation.Job.ProviderID != providerID ||
		observation.Job.RemoteID != remoteID {
		return nil, &Error{
			Code:      ErrInvalidInput,
			Operation: "decode_provider_webhook",
		}
	}
	return &observation, nil
}

func (m *Module) Offerings(capability Capability) []Offering {
	offerings := m.catalog.List(capability)
	if offerings == nil {
		return []Offering{}
	}
	return offerings
}

func (m *Module) Offering(id OfferingID) (Offering, bool) {
	return m.catalog.Get(id)
}

func (m *Module) OfferingAvailable(id OfferingID) bool {
	offering, exists := m.catalog.Get(id)
	if !exists || !offering.Enabled {
		return false
	}
	_, available := m.providers.Provider(offering)
	return available
}

func (m *Module) OpenAsset(
	ctx context.Context,
	owner OwnerID,
	taskID TaskID,
	assetID AssetID,
) (AssetContent, error) {
	return m.OpenAssetScoped(ctx, OwnTasks(owner), taskID, assetID)
}

func (m *Module) OpenAssetScoped(
	ctx context.Context,
	scope TaskListScope,
	taskID TaskID,
	assetID AssetID,
) (AssetContent, error) {
	task, err := m.GetScoped(ctx, scope, taskID)
	if err != nil {
		return AssetContent{}, err
	}
	for _, asset := range task.Assets {
		if asset.ID != assetID {
			continue
		}
		body, err := m.assets.Open(ctx, asset)
		if err != nil {
			return AssetContent{}, &Error{
				Code:      ErrUnavailable,
				Operation: "open_asset",
				Detail:    "资产暂不可读取",
				Cause:     err,
			}
		}
		return AssetContent{
			Body:      body,
			MediaType: asset.MediaType,
			Size:      asset.Size,
			SHA256:    asset.SHA256,
			Role:      asset.Role,
			Variant:   asset.Variant,
		}, nil
	}
	return AssetContent{}, &Error{Code: ErrNotFound, Operation: "open_asset", Detail: "资产不存在"}
}

func (m *Module) Close() error {
	err := m.repository.Close()
	if closer, ok := m.assets.(interface{ Close() error }); ok {
		err = errors.Join(err, closer.Close())
	}
	return err
}

// hashRequest 计算幂等请求哈希（B17：剔除 Offering revision——
// catalog 无害修订会换哈希，同幂等键重放被误判冲突导致重复扣费；
// 参数变化仍会换哈希，冲突检测语义保留）。
func hashRequest(offering Offering, input json.RawMessage) string {
	sum := sha256.Sum256([]byte(string(offering.ID) + "\x00" + string(input)))
	return hex.EncodeToString(sum[:])
}

// ExpectedRequestHash exposes the existing Task idempotency identity to the
// admission store. PreparedInput has a richer digest and must not replace this
// legacy hash when replaying tasks accepted before input freezing existed.
func ExpectedRequestHash(offeringID OfferingID, normalizedInput json.RawMessage) string {
	return hashRequest(Offering{ID: offeringID}, normalizedInput)
}

var (
	RepositoryNotFound              = errors.New("repository: not found")
	RepositorySessionNotFound       = errors.New("repository: session not found")
	RepositoryConflict              = errors.New("repository: conflict")
	RepositoryIdempotencyConflict   = errors.New("repository: idempotency conflict")
	RepositoryAdmissionFull         = errors.New("repository: owner inflight admission full")
	RepositoryAuthorizationRequired = errors.New("repository: execution authorization required")
	// RepositoryConversionConflict: another active Task of the same owner and
	// session already runs the same exact model source into an overlapping
	// target format. Nothing was written.
	RepositoryConversionConflict = errors.New("repository: active conversion already runs for this source and format")
	// RepositoryUpstreamJobConflict: the upstream job an Attempt tried to
	// record is already recorded for another Attempt (one job belongs to one
	// Attempt). The coordinator ends that Task; it is never a global fault.
	RepositoryUpstreamJobConflict = errors.New("repository: upstream job already recorded for another attempt")
)

// ErrConversionInProgress marks a refusal caused by an overlapping active
// format conversion. The caller did not create or charge a second Task; it may
// retry after that Task reaches a terminal state.
var ErrConversionInProgress = errors.New("active format conversion overlaps")

func mapRepositoryError(operation string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, RepositorySessionNotFound):
		return &Error{Code: ErrNotFound, Operation: operation, Detail: "会话不存在", Cause: err}
	case errors.Is(err, RepositoryNotFound):
		return &Error{Code: ErrNotFound, Operation: operation, Detail: "任务不存在", Cause: err}
	case errors.Is(err, RepositoryConversionConflict):
		return &Error{
			Code:      ErrConflict,
			Operation: operation,
			Detail:    "同一来源的格式转换正在进行，请等待完成后再试",
			Cause:     errors.Join(ErrConversionInProgress, err),
		}
	case errors.Is(err, RepositoryIdempotencyConflict):
		return &Error{
			Code:      ErrIdempotencyConflict,
			Operation: operation,
			Detail:    "幂等键已用于不同请求",
			Cause:     err,
		}
	case errors.Is(err, RepositoryAdmissionFull):
		return &Error{
			Code:       ErrTooManyRequests,
			Operation:  operation,
			Detail:     "进行中的任务数量已达上限，请等待任务完成后再提交",
			RetryAfter: 30 * time.Second,
			Cause:      err,
		}
	case errors.Is(err, harness.ErrStale):
		return &Error{Code: ErrConflict, Operation: operation, Detail: "error.harness_selection_changed", Cause: err}
	case errors.Is(err, RepositoryAuthorizationRequired):
		return &Error{Code: ErrForbidden, Operation: operation, Detail: "当前操作需要单独确认", Cause: err}
	case errors.Is(err, RepositoryConflict):
		return &Error{
			Code:      ErrConflict,
			Operation: operation,
			Detail:    "任务已被并发更新，请重试",
			Cause:     err,
		}
	case errors.Is(err, RepositoryUpstreamJobConflict):
		return &Error{Code: ErrInternal, Operation: operation, Detail: "上游作业已记录在其他任务上", Cause: err}
	default:
		return &Error{Code: ErrUnavailable, Operation: operation, Detail: "本地持久化暂不可用", Cause: err}
	}
}
