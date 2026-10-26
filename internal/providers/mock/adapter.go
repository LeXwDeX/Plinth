package mock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 提示词里的开关。只有 PROVIDER_MODE=mock 才会构造本适配器，所以这些记号
// 不可能出现在 live 配置里：
//   - MarkerInsufficientBalance：Start 直接返回 Provider 的“余额不足”分类，
//     让内核真实的错误映射、失败卡片和“换个模型”路径跑一遍；
//   - MarkerSlow：任务在 SlowLatency（默认 20 秒）内逐步推进度，用来验证
//     进行中的任务不阻塞对话；
//   - MarkerLargeModel：GLB 产物用零字节填充到 LargeModelBytes（约 80 MiB），
//     超过容器 /tmp 的 64 MB tmpfs 和 Meshy 的 70 MiB 输入上限（#329），用来在本机
//     复现大模型的加工路径（#321）；以这种作业为上游来源的作业（如 Bang
//     分件）同样产出大模型；
//   - MarkerDuplicateRemoteID：所有带该记号的提交都返回同一个上游任务 ID
//     （DuplicateRemoteID），模拟上游复用已用过的任务 ID（#298）。第一个
//     提交正常推进，后来的提交由内核按 submission_unknown 结束；
//   - MarkerSourceExpired：Start 按服务商已不保留来源原任务的方式拒绝
//     （404 Task not found → source_expired），用来在本机走一遍来源过期后的
//     失败卡、来源失效记录和换用已保存文件的路径（#326）；
//   - MarkerTexturePadding：结果 GLB/USDZ 里的贴图在 PNG 结束块后多 1 个对齐空格
//     （dev-test 钥匙扣 GLB 的形态，#330）；以它为来源的转换（按原任务或按已保存
//     的 GLB）把带填充的贴图带进自己的文件，和 Meshy Convert 一样；
//   - MarkerConvertRejected：结果本身正常，但以它为来源的转换得到的贴图在结束块
//     后带真正的多余数据，交付检查拒收：转换在上游完成（计费）后交付失败，用来在
//     本机走一遍“已扣费但交付失败”的免费取回与再次计费重试（#330）；
//   - MarkerConvertRejectedOnce：同上，但原任务的交付重试（每次重试重读一次已完成
//     的作业，共 materialize_attempts−1 次）用完之后，再次观察（免费取回）时去掉
//     多余数据，取回拿到干净文件并成功，用来在本机走一遍取回成功后的状态（#330 第二轮）。
const (
	MarkerInsufficientBalance = "[mock:insufficient_balance]"
	MarkerSlow                = "[mock:slow]"
	MarkerDuplicateRemoteID   = "[mock:duplicate_remote_id]"
	MarkerLargeModel          = "[mock:large_model]"
	MarkerSourceExpired       = "[mock:source_expired]"
	// MarkerTexturePadding gives the result's embedded texture one alignment
	// space after its PNG IEND chunk, as the dev-test keychain GLB had (#330);
	// conversions of the result (by its upstream job or its saved GLB) copy
	// the padded texture into their files, as Meshy Convert does.
	MarkerTexturePadding = "[mock:texture_padding]"
	// MarkerConvertRejected keeps the result's own files clean but gives
	// every conversion of it a texture with a real trailer after IEND, which
	// the delivery check refuses: the conversion finishes upstream (charged)
	// and then fails delivery, as #330 B did on dev-test.
	MarkerConvertRejected = "[mock:convert_rejected]"
	// MarkerConvertRejectedOnce is MarkerConvertRejected until the refused
	// files were served: a free refetch (which reads the finished job again
	// through Observe) gets clean files and succeeds.
	MarkerConvertRejectedOnce = "[mock:convert_rejected_once]"
	// LargeModelBytes is the GLB size of a MarkerLargeModel job.
	LargeModelBytes   = 80 << 20
	DuplicateRemoteID = "mock-job-duplicate"
)

// Options 控制模拟延迟。零值是历史行为：作业在第二次观察时完成。
type Options struct {
	// ImageLatency、ModelLatency 为非零时，对应领域的作业按时间推进度。
	ImageLatency time.Duration
	ModelLatency time.Duration
	// SlowLatency 是带 MarkerSlow 的作业总时长；零表示 20 秒。
	SlowLatency time.Duration
	// Now 供测试注入时钟；零值用 time.Now。
	Now func() time.Time
	// Inputs 让图改图读到参考图和蒙版，局部重绘因此只改蒙版区域；
	// nil 时（单元测试）所有请求都走程序化出图。
	Inputs generation.InputAssetResolver
}

// DevelopmentOptions 是本地验收用的延迟：图片不到 1 秒，3D 约 8 秒，让进度环、
// 进度卡和“已完成”提示都有机会出现。
func DevelopmentOptions() Options {
	return Options{ImageLatency: 700 * time.Millisecond, ModelLatency: 8 * time.Second}
}

type Adapter struct {
	mu          sync.Mutex
	jobs        map[string]*job
	submissions map[string]string
	options     Options
	hueMu       sync.Mutex
	hues        map[string]float64
	// glbs maps the SHA-256 of a GLB this adapter delivered to its job, so a
	// conversion of the saved file (model_url route) inherits the job's
	// texture behavior like one by its upstream job does.
	glbs map[string]*job
}

type job struct {
	remoteID  string
	request   generation.StartRequest
	input     map[string]any
	startedAt time.Time
	slow      bool
	large     bool
	polls     int
	completed bool
	plans     map[string]claimPlan
	model     *builtModel
	edit      *editSource
	// textureTail follows the IEND chunk of the textures this job embeds;
	// derivedTail is what jobs made from this one embed (#330 markers).
	textureTail []byte
	derivedTail []byte
	// derivedOnce marks jobs made from this one as tailOnce. A tailOnce job
	// drops its textureTail on the first observation that the failing
	// delivery cannot have made: after the tail was served, the kernel
	// re-observes the finished job once per materialization retry (the
	// Offering's materialize_attempts minus one, under the source Task's
	// IDs); the next observation is a refetch.
	derivedOnce bool
	tailOnce    bool
	tailServed  bool
	reobserved  int
	// W6 成功后观察的 mock 状态（进程内模拟，无 HTTP 子提交）。
	postSuccessDelivered bool
	postSuccessOpaque    json.RawMessage
}

func New() *Adapter { return NewWithOptions(Options{}) }

func NewWithOptions(options Options) *Adapter {
	if options.SlowLatency <= 0 {
		options.SlowLatency = 20 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Adapter{
		jobs:        make(map[string]*job),
		submissions: make(map[string]string),
		glbs:        make(map[string]*job),
		options:     options,
	}
}

// W4 私有派发检查点盘点：mock 无任何 HTTP/付费私有子提交（作业在进程
// 内存中模拟），因此不消费 DispatchCheckpointer；不为此发明子流程。
// 内核对未消费检查点的 Provider 不改变任何行为（Observation 无回执即
// 走既有路径）。

func (a *Adapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	if err := ctx.Err(); err != nil {
		return generation.Observation{}, err
	}
	if !json.Valid(request.Input) {
		return generation.Observation{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureInvalidInput,
				SafeDetail: "mock adapter 收到无效 JSON",
			},
		}
	}
	if bytes.Contains(request.Input, []byte(MarkerInsufficientBalance)) {
		return generation.Observation{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:          generation.FailureInsufficientBalance,
				Retryable:      false,
				SafeDetail:     "mock 上游账户余额不足",
				UpstreamStatus: 402,
				UpstreamCode:   "insufficient_balance",
			},
		}
	}

	if bytes.Contains(request.Input, []byte(MarkerSourceExpired)) {
		return generation.Observation{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:          generation.FailureSourceExpired,
				Retryable:      false,
				SafeDetail:     "mock 上游已不保留来源模型的原任务（404 Task not found）",
				UpstreamStatus: 404,
			},
		}
	}

	edit := a.loadEdit(ctx, request)
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing, ok := a.submissions[request.SubmissionKey]; ok {
		stored := a.jobs[existing]
		return accepted(stored), nil
	}
	identity := sha256.Sum256([]byte(request.SubmissionKey))
	remoteID := fmt.Sprintf("mock-job-%x", identity[:16])
	if bytes.Contains(request.Input, []byte(MarkerDuplicateRemoteID)) {
		remoteID = DuplicateRemoteID
		// The first job keeps answering for the ID; a later submission only
		// receives the same ID back, as a misbehaving upstream would send.
		if stored, ok := a.jobs[remoteID]; ok {
			a.submissions[request.SubmissionKey] = remoteID
			return accepted(stored), nil
		}
	}
	input := map[string]any{}
	_ = json.Unmarshal(request.Input, &input)
	stored := &job{
		remoteID:  remoteID,
		request:   request,
		input:     input,
		startedAt: a.options.Now(),
		slow:      bytes.Contains(request.Input, []byte(MarkerSlow)),
		large:     bytes.Contains(request.Input, []byte(MarkerLargeModel)),
		edit:      edit,
	}
	switch {
	case bytes.Contains(request.Input, []byte(MarkerTexturePadding)):
		stored.textureTail, stored.derivedTail = []byte(" "), []byte(" ")
	case bytes.Contains(request.Input, []byte(MarkerConvertRejectedOnce)):
		stored.derivedTail, stored.derivedOnce = []byte("mock-trailer"), true
	case bytes.Contains(request.Input, []byte(MarkerConvertRejected)):
		stored.derivedTail = []byte("mock-trailer")
	}
	inherit := func(source *job) {
		if len(source.derivedTail) > 0 && len(stored.textureTail) == 0 {
			stored.textureTail, stored.derivedTail = source.derivedTail, source.derivedTail
			stored.tailOnce, stored.derivedOnce = source.derivedOnce, source.derivedOnce
		}
	}
	for _, dependency := range request.TaskDependencies {
		// A result made from a large mock result is large too (#321).
		if source, ok := a.jobs[dependency.UpstreamJob.RemoteID]; ok {
			stored.large = stored.large || source.large
			inherit(source)
		}
	}
	for _, assets := range request.InputAssets {
		for _, asset := range assets {
			if source, ok := a.glbs[asset.SHA256]; ok {
				inherit(source)
			}
		}
	}
	a.jobs[remoteID] = stored
	a.submissions[request.SubmissionKey] = remoteID
	return accepted(stored), nil
}

func accepted(stored *job) generation.Observation {
	return generation.Observation{
		Phase: generation.UpstreamAccepted,
		Job: &generation.UpstreamJob{
			ProviderID: stored.request.Offering.ProviderID,
			RemoteID:   stored.remoteID,
		},
		Progress:  1,
		PollAfter: 10 * time.Millisecond,
		DedupeKey: stored.remoteID + ":accepted",
	}
}

func (a *Adapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	if err := ctx.Err(); err != nil {
		return generation.Observation{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, ok := a.jobs[request.Job.RemoteID]
	if ok && stored.tailOnce && stored.completed && stored.tailServed && len(stored.textureTail) > 0 {
		stored.reobserved++
		attempts := request.Offering.MaterializeAttempts
		if attempts <= 0 {
			attempts = 3 // the kernel's default
		}
		if stored.reobserved >= attempts {
			stored.textureTail = nil
		}
	}
	if !ok {
		return generation.Observation{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureRejected,
				SafeDetail: "mock 上游作业不存在",
			},
		}
	}
	if request.PostSuccess {
		return a.observePostSuccess(stored, request)
	}
	stored.polls++
	if progress, running := a.runningProgress(stored); running {
		return generation.Observation{
			Phase:     generation.UpstreamRunning,
			Job:       &request.Job,
			Progress:  progress,
			PollAfter: a.pollInterval(stored),
			DedupeKey: stored.remoteID + ":running:" + strconv.Itoa(progress),
		}, nil
	}
	stored.completed = true
	plans := planClaims(stored.request.Offering, stored.input)
	stored.plans = make(map[string]claimPlan, len(plans))
	claims := make([]generation.ArtifactClaim, 0, len(plans))
	var first claimPlan
	for index, plan := range plans {
		if index == 0 {
			first = plan
		}
		stored.plans[plan.key] = plan
		claims = append(claims, generation.ArtifactClaim{
			ID:        fmt.Sprintf("%s:%02d:%s", stored.remoteID, index, plan.key),
			Role:      plan.role,
			Variant:   plan.variant,
			Locator:   "mock://" + stored.remoteID + "/" + plan.key,
			MediaType: plan.mediaType,
		})
	}
	opaque, err := json.Marshal([]map[string]any{{
		"identifier":         stored.remoteID,
		"status":             string(generation.TaskSucceeded),
		"downloadable":       true,
		"source_name":        first.variant,
		"successful_ordinal": 1,
	}})
	if err != nil {
		return generation.Observation{}, err
	}
	return generation.Observation{
		Phase:     generation.UpstreamCompleted,
		Job:       &request.Job,
		Progress:  100,
		Artifacts: claims,
		DedupeKey: stored.remoteID + ":completed",
		Opaque:    opaque,
	}, nil
}

// runningProgress 报告作业是否仍在进行以及当前进度。零延迟的作业沿用历史
// 行为：第一次观察返回 50%，第二次完成。
func (a *Adapter) runningProgress(stored *job) (int, bool) {
	latency := a.latency(stored)
	if latency <= 0 {
		if stored.polls < 2 {
			return 50, true
		}
		return 100, false
	}
	elapsed := a.options.Now().Sub(stored.startedAt)
	if elapsed >= latency {
		return 100, false
	}
	return 5 + int(90*float64(elapsed)/float64(latency)), true
}

func (a *Adapter) latency(stored *job) time.Duration {
	switch {
	case stored.slow:
		return a.options.SlowLatency
	case stored.request.Offering.Domain == generation.DomainImage:
		return a.options.ImageLatency
	default:
		return a.options.ModelLatency
	}
}

func (a *Adapter) pollInterval(stored *job) time.Duration {
	latency := a.latency(stored)
	if latency <= 0 {
		return 10 * time.Millisecond
	}
	return max(100*time.Millisecond, min(time.Second, latency/20))
}

// observePostSuccess 按内核只读回报的未决槽位逐个交付产物：逐项槽位用其自身
// variant，聚合前缀槽位用 前缀+"mock"（历史快照形状）。READY 由内核按本地
// 资产证据判定（回顾性整改 P1），mock 只对内核无法自行判定的聚合前缀槽位
// 保留完成断言；无未决槽位 → idle（幂等、零新增产物）。
func (a *Adapter) observePostSuccess(
	stored *job,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	pending := pendingMockSlots(request)
	if len(pending) == 0 {
		return generation.Observation{
			Phase:     generation.UpstreamCompleted,
			Job:       &request.Job,
			Progress:  100,
			DedupeKey: stored.remoteID + ":post-success:idle",
			Opaque:    stored.postSuccessOpaque,
		}, nil
	}
	stored.postSuccessDelivered = true
	artifacts := make([]generation.ArtifactClaim, 0, len(pending))
	updates := make([]generation.ResourceUpdate, 0)
	for _, slot := range pending {
		variant := slot.Variant
		if variant == "" {
			variant = slot.VariantPrefix + "mock"
		}
		if variant == "" {
			continue
		}
		plan := newClaimPlan(slot.Role, variant, 0, 1, stored.input)
		content, err := a.renderPlan(stored, plan)
		if err != nil {
			return generation.Observation{}, err
		}
		artifacts = append(artifacts, generation.ArtifactClaim{
			ID:        stored.remoteID + ":post-success:" + variant,
			Role:      slot.Role,
			Variant:   variant,
			Inline:    content,
			MediaType: plan.mediaType,
		})
		if slot.VariantPrefix != "" {
			updates = append(updates, generation.ResourceUpdate{
				Role:          slot.Role,
				VariantPrefix: slot.VariantPrefix,
				State:         generation.ResourceReady,
			})
		}
	}
	opaque, _ := json.Marshal(map[string]any{
		"identifier":   stored.remoteID,
		"post_success": true,
	})
	stored.postSuccessOpaque = opaque
	return generation.Observation{
		Phase:           generation.UpstreamCompleted,
		Job:             &request.Job,
		Progress:        100,
		DedupeKey:       stored.remoteID + ":post-success:delivered:" + strconv.Itoa(len(artifacts)),
		Artifacts:       artifacts,
		ResourceUpdates: updates,
		Opaque:          opaque,
	}, nil
}

func (a *Adapter) OpenAsset(
	ctx context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, generation.AssetMetadata{}, err
	}
	if len(request.Claim.Inline) > 0 {
		return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
			MediaType: request.Claim.MediaType,
			Size:      int64(len(request.Claim.Inline)),
		}, nil
	}
	if !strings.HasPrefix(request.Claim.Locator, "mock://") {
		return nil, generation.AssetMetadata{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureProtocol,
				SafeDetail: "mock adapter 无法读取该产物位置",
			},
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	remoteID, key, _ := strings.Cut(strings.TrimPrefix(request.Claim.Locator, "mock://"), "/")
	stored, ok := a.jobs[remoteID]
	if !ok || !stored.completed {
		return nil, generation.AssetMetadata{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureUpstreamUnavailable,
				Retryable:  true,
				SafeDetail: "mock 产物尚不可用",
			},
		}
	}
	plan, ok := stored.plans[key]
	if !ok {
		return nil, generation.AssetMetadata{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureProtocol,
				SafeDetail: "mock 产物位置未登记",
			},
		}
	}
	if stored.tailOnce && len(stored.textureTail) > 0 {
		stored.tailServed = true
	}
	content, err := a.renderPlan(stored, plan)
	if err != nil {
		return nil, generation.AssetMetadata{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureInternal,
				SafeDetail: "mock 产物编码失败",
			},
			Cause: err,
		}
	}
	return io.NopCloser(bytes.NewReader(content)), generation.AssetMetadata{
		MediaType: plan.mediaType,
		Size:      int64(len(content)),
	}, nil
}

// blocksSuccess 与内核 requirementBlocksSuccess 同语义（mock 不 import
// 内核内部函数，按公开常量等价实现：background/on-demand 不阻断）。
func blocksSuccess(delivery string) bool {
	return delivery != generation.AssetDeliveryBackground &&
		delivery != generation.AssetDeliveryOnDemand
}

// pendingMockSlots 从内核只读回报的槽位视图中取出仍未决的槽位（mock 交付
// 目标）。内核未提供视图时（直接调用适配器的路径）回退到冻结声明的全部
// 非必需槽位，保持既有"单轮交付"行为。
func pendingMockSlots(request generation.ObserveRequest) []generation.ResourceSlotState {
	if len(request.ResourceSlots) == 0 {
		slots := make([]generation.ResourceSlotState, 0, len(request.Offering.Outputs))
		for _, output := range request.Offering.Outputs {
			if blocksSuccess(output.Delivery) {
				continue
			}
			if output.Variant == "" && output.VariantPrefix == "" {
				continue
			}
			slots = append(slots, generation.ResourceSlotState{
				Role:          output.Role,
				Variant:       output.Variant,
				VariantPrefix: output.VariantPrefix,
				Group:         output.Group,
				State:         generation.ResourcePending,
			})
		}
		return slots
	}
	pending := make([]generation.ResourceSlotState, 0, len(request.ResourceSlots))
	for _, slot := range request.ResourceSlots {
		if slot.State != generation.ResourcePending {
			continue
		}
		pending = append(pending, slot)
	}
	return pending
}
