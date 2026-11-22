// conversion.go 实现 Tripo 格式转换（W5 §5）：以公开 format-conversion
// Offering 承载用户新发起的显式转换任务，发送前运行私有成本规划器
// （与提交期同一共享评估器 catalog.AuthorizeExtraCredits，输入 parity），
// 超界/未知成本/未知格式/缺同意 → 零 Checkpoint、零 convert 调用；
// 授权内的真实发送走 W4 私有派发检查点（一次 ack 至多一次 HTTP）。
//
// 上游协议事实（官方公开 SDK 比对，2026-09-18 只读核验；W7 复核）：
//   - VAST-AI-Research/tripo-go-sdk postprocess.go：v3 转换端点
//     POST /v3/models/convert，请求 input（裸字符串，源任务 task_id）/
//     format + 可选 quad、face_limit、texture_size、texture_format、
//     flatten_bottom、pivot_to_center_bottom 等。SDK 参数面不含
//     scale_factor（W5 曾按次级证据声明，W7 复核官方 Go SDK 后已从
//     目录移除；未知参数在提交期被拒绝，fail-closed，不发明支持）；
//   - types.go：任务查询响应含 credits_consumed（十进制最多两位，
//     VIP 折扣可产生小数）——本实现按原始 JSON 数值原样保存证据，
//     不做整数截断；计划文档中的 consumed_credit 字段名与 v3 实测
//     不符，以 credits_consumed 为准；
//   - Python SDK format 枚举为 GLTF/USDZ/FBX/OBJ/STL/3MF（无 GLB）；
//     JS SDK 常量额外含 GLB，与 Python SDK 及批准计划不一致——GLB
//     未获交叉验证，保持未声明（fail-closed），登记为未知事实。
package tripo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 格式转换的源任务判据：唯一性按声明（semantic=source-task 的 task
// 参数，或未声明 semantic 的遗留 task 参数），零个或多个候选一律
// fail-closed——声明缺陷不按名字猜。内核 resolveTaskDependencies 同样
// 按声明参数名注入依赖，故 Adapter 无需硬编码参数名。声明缺陷与依赖
// 缺失分属不同失败类，便于运维区分"目录写错"与"用户输入不完整"。
var (
	errNoSourceTaskParameter       = errors.New("no source task parameter declared")
	errAmbiguousSourceTask         = errors.New("multiple source task parameters declared")
	errMissingSourceTaskDependency = errors.New("declared source task parameter has no resolved dependency")
)

func conversionSourceTask(
	offering generation.Offering,
	dependencies map[string]generation.TaskDependency,
) (generation.Parameter, generation.TaskDependency, error) {
	candidates := make([]int, 0, len(offering.Parameters))
	for index := range offering.Parameters {
		parameter := &offering.Parameters[index]
		if parameter.Type != "task" {
			continue
		}
		if parameter.Semantic != "" &&
			parameter.Semantic != generation.SemanticSourceTask {
			continue
		}
		candidates = append(candidates, index)
	}
	switch len(candidates) {
	case 0:
		return generation.Parameter{}, generation.TaskDependency{}, errNoSourceTaskParameter
	case 1:
	default:
		return generation.Parameter{}, generation.TaskDependency{}, errAmbiguousSourceTask
	}
	resolved := offering.Parameters[candidates[0]]
	dependency, declared := dependencies[resolved.Name]
	if !declared || dependency.UpstreamJob.RemoteID == "" {
		return generation.Parameter{}, generation.TaskDependency{}, errMissingSourceTaskDependency
	}
	return resolved, dependency, nil
}

// startConversion 处理 format-conversion 能力的公开任务：私有规划器
// 先于 Checkpoint 运行，任何拒绝都发生在零付费调用之前。
func (a *Adapter) startConversion(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	options, err := a.optionsFor(request.Offering)
	if err != nil || strings.TrimSpace(options.Endpoint) == "" {
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			"Tripo Offering 缺少 conversion endpoint",
		)
	}
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return generation.Observation{}, failure(
			generation.FailureInvalidInput,
			false,
			"规范化输入不是 JSON 对象",
		)
	}
	sourceParameter, dependency, err := conversionSourceTask(
		request.Offering,
		request.TaskDependencies,
	)
	if err != nil {
		if errors.Is(err, errMissingSourceTaskDependency) {
			return generation.Observation{}, failure(
				generation.FailureInvalidInput,
				false,
				"格式转换缺少可用的源任务依赖",
			)
		}
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			fmt.Sprintf("Tripo 格式转换声明缺陷: %v", err),
		)
	}
	// 私有发送前规划器（§5.3 第三段）：同一共享评估器、同一冻结声明、
	// 同一规范化输入。任何拒绝都不得 Checkpoint、不得发送。
	if err := planConversionDispatch(request.Offering, input); err != nil {
		return generation.Observation{}, err
	}
	body, err := conversionWireBody(
		request.Offering,
		input,
		sourceParameter,
		dependency.UpstreamJob.RemoteID,
	)
	if err != nil {
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			fmt.Sprintf("Tripo 格式转换声明缺陷: %v", err),
		)
	}
	// 非幂等付费提交前先持久化私有派发意图并取得发送许可（W4 §4.3）：
	// intent 自含协调标识（stage/submission_key/源任务/格式），不依赖
	// 进程内存；失败（含 ErrDispatchPending）必须放弃发送（0 次上游调用）。
	dispatchPermit, err := privateDispatchPermit(ctx, request.SubmissionKey, dispatchIntentStage{
		Stage: "models/convert",
		Details: map[string]any{
			"endpoint":       options.Endpoint,
			"source_task":    dependency.UpstreamJob.RemoteID,
			"source_task_id": string(dependency.TaskID),
			"format":         input[request.Offering.Cost.FormatParameter],
		},
	})
	if err != nil {
		return generation.Observation{}, err
	}
	var response envelope[struct {
		TaskID string `json:"task_id"`
	}]
	if err := a.client.DoJSON(
		ctx,
		http.MethodPost,
		options.Endpoint,
		body,
		request.SubmissionKey,
		&response,
		true,
	); err != nil {
		return generation.Observation{}, err
	}
	if err := businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return generation.Observation{}, err
	}
	if response.Data.TaskID == "" {
		return generation.Observation{}, submissionUnknown("Tripo 转换响应缺少 task_id")
	}
	// A public conversion is a pool job: count it without blocking (C3). Its
	// terminal observation releases the slot.
	a.readoptPool(request.Offering, request.AttemptID)
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: response.Data.TaskID},
		PollAfter: request.Offering.PollInterval,
		DedupeKey: privateDedupeKey(response.Data.TaskID, "accepted"),
		Dispatch:  dispatchPermit,
		Opaque: initialConversionEvidence(
			request.Offering,
			dependency.UpstreamJob.RemoteID,
			input,
		),
	}, nil
}

// planConversionDispatch 是发送前的成本/同意/格式规划器：
//  1. cost 声明缺失 → cost_unknown（fail-closed，不猜测金额）；
//  2. 共享评估器（与提交期同一实现）重算 estimated_extra 并执行
//     同意与上限校验——与提交期结论不一致即拒绝（冻结输入被外部
//     构造等异常路径下零发送）；
//  3. format 取值必须落在冻结 options 内（未知格式 fail-closed）。
//
// 拒绝返回非歧义 FailureRejected；任务失败但不产生任何付费调用。
func planConversionDispatch(
	offering generation.Offering,
	input map[string]any,
) error {
	if offering.Cost == nil {
		return failure(generation.FailureRejected, false, catalog.UnknownCostError(offering).Error())
	}
	normalized, err := json.Marshal(input)
	if err != nil {
		return failure(generation.FailureInternal, false, "编码转换输入失败")
	}
	if _, err := catalog.AuthorizeExtraCredits(offering, normalized); err != nil {
		return failure(generation.FailureRejected, false, err.Error())
	}
	if offering.Cost.FormatParameter != "" {
		value, _ := input[offering.Cost.FormatParameter].(string)
		if !conversionFormatDeclared(offering, value) {
			return failure(
				generation.FailureRejected,
				false,
				"目标格式不在供给项声明的可选范围内",
			)
		}
	}
	return nil
}

// conversionFormatDeclared 校验格式取值落在冻结 select options 内。
func conversionFormatDeclared(offering generation.Offering, value string) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Name != offering.Cost.FormatParameter {
			continue
		}
		for _, option := range parameter.Options {
			if option.Value == value {
				return true
			}
		}
		return false
	}
	return false
}

// conversionWireBody 按声明组装上游请求体（通用规则，无 per-format
// 分支）：task 型参数由依赖解析为 wire 值（wire_name 声明承载字段）；
// 同意参数（cost.consent_parameter）是本地同意证据，永不上行；其余
// 参数按 wire_name（缺省同名）透传。
func conversionWireBody(
	offering generation.Offering,
	input map[string]any,
	sourceParameter generation.Parameter,
	sourceRemoteID string,
) (map[string]any, error) {
	body := map[string]any{}
	for _, parameter := range offering.Parameters {
		value, exists := input[parameter.Name]
		if !exists {
			continue
		}
		if parameter.Type == "task" {
			// 源任务身份由 conversionSourceTask 唯一裁定；出现第二个
			// task 参数说明声明与解析口径分叉，宁可零发送也不把同一
			// remote ID 写进两个 wire 字段。
			if parameter.Name != sourceParameter.Name {
				return nil, fmt.Errorf(
					"unexpected task parameter %q beside source task %q",
					parameter.Name,
					sourceParameter.Name,
				)
			}
			name := parameter.Name
			if parameter.WireName != "" {
				name = parameter.WireName
			}
			body[name] = sourceRemoteID
			continue
		}
		if parameter.Name == offering.ExtraConsentParameter() {
			continue
		}
		name := parameter.Name
		if parameter.WireName != "" {
			name = parameter.WireName
		}
		body[name] = value
	}
	return body, nil
}

// conversionEvidence 是转换任务的 Provider 私有 Opaque 状态：仅保存
// 协调与计费证据（上游 remote ID、格式、credits_consumed 原始数值），
// 不进入任何公开投影（§8.1）。
type conversionEvidence struct {
	SourceRemoteID string          `json:"source_remote_id,omitempty"`
	Format         string          `json:"format,omitempty"`
	ConsumedCredit json.RawMessage `json:"consumed_credit,omitempty"`
}

// initialConversionEvidence 构造 Start 阶段的初始私有证据（源任务与
// 格式在此写入；后续观察经 mergeConversionEvidence 滚动补计费证据）。
func initialConversionEvidence(
	offering generation.Offering,
	sourceRemoteID string,
	input map[string]any,
) json.RawMessage {
	evidence := conversionEvidence{SourceRemoteID: sourceRemoteID}
	if offering.Cost != nil && offering.Cost.FormatParameter != "" {
		evidence.Format, _ = input[offering.Cost.FormatParameter].(string)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return nil
	}
	return encoded
}

// ── W6：生成期多格式同意的观察期私有转换（§5.3.1 形态） ──

// 转换子作业状态（Provider 私有，内核不解释）：
//   - dispatching：检查点已持久化但发送结果未知（崩溃/歧义）——永不
//     重发该格式，且后续任何新的付费派发都被 pending 栅栏保守阻止；
//   - submitted：已取得上游 task_id，轮询中；
//   - rate_limited：该格式的一次 convert POST 被上游以明确的 HTTP 429
//     拒绝（未受理、未计费），等待 Retry-After 或退避后以新许可重发；
//   - retry_pending：该格式的子作业以 failed 或 cancelled 结束（官方
//     Billing：“Failed and cancelled tasks are not charged.”，读取
//     2026-11-22），等待退避后以新许可重提；
//   - unknown：该格式的子作业在连接声明的时间内一直返回 unknown，结果
//     不明——不再重发，也不断言该格式失败（槽位保持 pending）；
//   - done / failed / denied / abandoned：终态。
const (
	conversionItemDispatching  = "dispatching"
	conversionItemSubmitted    = "submitted"
	conversionItemDone         = "done"
	conversionItemFailed       = "failed"
	conversionItemDenied       = "denied"
	conversionItemAbandoned    = "abandoned"
	conversionItemRateLimited  = "rate_limited"
	conversionItemRetryPending = "retry_pending"
	conversionItemUnknown      = "unknown"
)

// An explicit HTTP 429 refuses a create request without accepting it (Tripo
// Rate Limits page, read 2026-11-22: code 1007 for the request rate, code 2000
// with Retry-After for a full concurrency pool; back off from 1 s, doubling, at
// most 32 s, up to 5 retries). The budget is per format: the page bounds the
// retries of one request, not of a whole run. The same private child is sent
// again under a new permit.
const (
	maximumConversionRateLimitRetries = 5
	maximumConversionRateLimitBackoff = 32 * time.Second
	maximumConversionRateLimitWait    = 5 * time.Minute
)

// maximumConversionChildRetries is how often one format's convert child is
// resubmitted after it ended failed or cancelled. The same 1, 2, 4… second
// backoff as a refusal applies, capped at 32 s.
const maximumConversionChildRetries = 5

// 一次私有转换子作业的结局（私有检查点证据，不公开）。
const (
	conversionAttemptDone     = "done"
	conversionAttemptFailed   = "failed"
	conversionAttemptCanceled = "canceled"
	conversionAttemptUnknown  = "unknown"
)

// 资源身份（role/variant）不在 Provider 内硬编码：由冻结快照的 on-demand
// 输出声明解析（conversionRequirement + generation.SelectionVariant），
// 避免与目录形成第二真源（回顾性整改 P2）。

// conversionAttempt 是一次私有 convert 提交的计费与身份证据：上游任务 id、
// 该任务报告的 credits_consumed 与它的结局。检查点保留每一次提交，进程重启
// 后仍能说明每个格式发过几次、每次的远端身份是什么。
type conversionAttempt struct {
	RemoteID        string          `json:"remote_id,omitempty"`
	CreditsConsumed json.RawMessage `json:"credits_consumed,omitempty"`
	Outcome         string          `json:"outcome,omitempty"`
}

// conversionItem 是单个选中格式的私有转换状态。
type conversionItem struct {
	Status          string          `json:"status"`
	RemoteID        string          `json:"remote_id,omitempty"`
	CreditsConsumed json.RawMessage `json:"credits_consumed,omitempty"`
	// Locator 是子作业完成后上游返回的产物定位符（可能是有时效的签名
	// URL）：本地下载失败或进程重启后凭此重发产物声明，**绝不**重发付费
	// 转换（回顾性整改 P1）。
	Locator string `json:"locator,omitempty"`
	// DeliveryAttempts 是已就该格式重发产物声明的次数：已重发过而槽位仍
	// 未兑现时，下一轮先按协议刷新定位符（GET 查询，非付费）再重发。
	DeliveryAttempts int `json:"delivery_attempts,omitempty"`
	// RateLimitRetries counts explicit 429 refusals of this private child
	// (never accepted upstream, so never charged). The budget is this
	// format's own.
	RateLimitRetries int `json:"rate_limit_retries,omitempty"`
	// RetryAttempts counts the resubmissions this format already made after a
	// child ended failed or cancelled.
	RetryAttempts int `json:"retry_attempts,omitempty"`
	// RetryWaitSeconds is the backoff the format is waiting out before its
	// next resubmission (retry_pending only).
	RetryWaitSeconds int `json:"retry_wait_seconds,omitempty"`
	// Attempts is the private submission history of this format.
	Attempts []conversionAttempt `json:"attempts,omitempty"`
}

// formatConversionOptions declares how an Offering's private format
// conversions run. Tripo's format-convert pool is shared with texture and
// refine and holds five jobs (rate-limits page, read 2026-11-22): an Offering
// that converts several formats declares that number, and the Adapter keeps at
// most that many private children in flight for one Task. Absent means one at a
// time, the behavior every Task frozen before the declaration had. The number
// bounds one Task's in-flight children; the account-wide pool is the upstream's
// to answer, which it does with code 2000 and Retry-After.
type formatConversionOptions struct {
	ConcurrencyLimit int `json:"concurrency_limit,omitempty"`
}

// conversionConcurrency is how many private convert children one Task keeps in
// flight at once.
func conversionConcurrency(options providerOptions) int {
	if options.FormatConversion != nil && options.FormatConversion.ConcurrencyLimit > 0 {
		return options.FormatConversion.ConcurrencyLimit
	}
	return 1
}

// conversionConcurrency is the Adapter's effective per-Task child concurrency.
// When an upstream pool is registered, one attempt holds one slot, so the Task
// keeps at most one child in flight: the account-wide limit then bounds the sum
// across Tasks instead of being multiplied by every Task's declared fan-out.
func (a *Adapter) conversionConcurrency(options providerOptions) int {
	if a.poolEnabled() {
		return 1
	}
	return conversionConcurrency(options)
}

// conversionRetryBackoff is the wait before the next attempt of a private
// child: 1, 2, 4… seconds, capped at 32 s (the official rate-limits page).
// attempt is the 1-based number of the attempt being scheduled.
func conversionRetryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return min(time.Second<<(attempt-1), maximumConversionRateLimitBackoff)
}

// conversionOutcome names how a private convert child ended.
func conversionOutcome(phase generation.UpstreamPhase) string {
	switch phase {
	case generation.UpstreamCompleted:
		return conversionAttemptDone
	case generation.UpstreamFailed:
		return conversionAttemptFailed
	case generation.UpstreamCanceled:
		return conversionAttemptCanceled
	default:
		return conversionAttemptUnknown
	}
}

// applyConversionChild folds one poll of a private convert child into the
// format's state: a child that is still queued or running changes nothing, and
// a child that reached an outcome is recorded as one more attempt with its
// remote id, credits and end state. A completed child becomes done; a failed or
// cancelled one becomes retry_pending with the documented backoff while its own
// budget lasts, and failed once it does not (the official billing page: neither
// is charged, so the same format is worth sending again). A child whose bounded
// unknown window ran out never reaches here: the caller ends the format as
// unknown, which is never resent and never asserted as a failure. It returns the
// updated item and, for a retry, the wait before the resubmission.
func applyConversionChild(
	item conversionItem,
	phase generation.UpstreamPhase,
	locator string,
	credits json.RawMessage,
) (conversionItem, time.Duration) {
	// A running child already reports the price frozen when it was created, and
	// the private ledger keeps it as evidence for the stage it belongs to.
	if len(credits) > 0 {
		item.CreditsConsumed = bytes.Clone(credits)
	}
	switch phase {
	case generation.UpstreamCompleted, generation.UpstreamFailed, generation.UpstreamCanceled:
	default:
		// Still queued or running: the child stays in flight.
		return item, 0
	}
	item.Attempts = append(item.Attempts, conversionAttempt{
		RemoteID:        item.RemoteID,
		CreditsConsumed: bytes.Clone(credits),
		Outcome:         conversionOutcome(phase),
	})
	item.RetryWaitSeconds = 0
	switch phase {
	case generation.UpstreamCompleted:
		item.Locator = locator
		item.Status = conversionItemDone
		if locator == "" {
			// 上游报完成却没有产物：与失败一样只终结该格式。
			item.Status = conversionItemFailed
			item.RemoteID = ""
		}
	default:
		item.Locator = ""
		item.RemoteID = ""
		if item.RetryAttempts >= maximumConversionChildRetries {
			item.Status = conversionItemFailed
			return item, 0
		}
		item.Status = conversionItemRetryPending
		item.RetryWaitSeconds = int(conversionRetryBackoff(item.RetryAttempts + 1).Seconds())
		return item, time.Duration(item.RetryWaitSeconds) * time.Second
	}
	return item, 0
}

// conversionRetryWait is the longest wait the pending retries ask for. The
// observation carries it as PollAfter, so every pending format has waited at
// least its own backoff when the next round runs.
func conversionRetryWait(conversions map[string]conversionItem) time.Duration {
	wait := time.Duration(0)
	for _, item := range conversions {
		if item.Status != conversionItemRetryPending {
			continue
		}
		if item.RetryWaitSeconds <= 0 {
			continue
		}
		if candidate := time.Duration(item.RetryWaitSeconds) * time.Second; candidate > wait {
			wait = candidate
		}
	}
	return wait
}

// nextConversionDispatch picks the format to send next: a format whose child
// ended failed or cancelled and whose backoff has passed first (it is already
// paid for and planned), then a format that was never started. Both follow the
// frozen selection order, so the choice never depends on map iteration.
func nextConversionDispatch(selections []string, conversions map[string]conversionItem) (string, bool) {
	for _, format := range selections {
		if item, exists := conversions[format]; exists && item.Status == conversionItemRetryPending {
			return format, true
		}
	}
	remaining := unstartedFormats(selections, conversions)
	if len(remaining) == 0 {
		return "", false
	}
	return remaining[0], true
}

// postSuccessState 是成功后观察期 attempt.Opaque 的载体：完整转换表
// （含每个格式的状态/子作业 remote id/credits_consumed 证据）。内核对
// 内容不解释、不公开（§8.1）。
type postSuccessState struct {
	Stage         string                    `json:"stage,omitempty"`
	SubmissionKey string                    `json:"submission_key,omitempty"`
	Format        string                    `json:"format,omitempty"`
	Conversions   map[string]conversionItem `json:"conversions"`
}

// observePostSuccessConversion 是成功后观察的 Tripo 私有编排：
// 每轮至多一次动作——轮询在途转换（完成即交付产物）、派发下一个未开始
// 的已同意格式、重发未兑现的已完成产物、或发出完成断言。
//
// 回顾性整改 P0/P1 的状态机契约（新批准行为，取代旧验收口径）：
//   - **完整冻结选中集治理完成**：首个/中间/末位格式失败都不得静默取消
//     其余独立已授权格式；只要还有选中格式未开始，就不断言任何终态，
//     下一轮继续派发；
//   - **未开始派发优先于未兑现重发（A1）**：某格式的本地下载永久失败
//     时，其重发不得占据派发轮次、饿死其余尚未开始的独立已授权格式；
//     重发只在全部选中格式都已开始后执行（歧义遗留与在途轮询两个守卫
//     的相对顺序不变）；
//   - **上游完成 ≠ 本地兑现**：完成的子作业只产出产物声明，兑现与否由
//     内核按本地资产证据判定；内核回报槽位仍未决时按定位符重发（必要时
//     刷新签名定位符），重试永不触发 convert；多个格式同时未决时按
//     DeliveryAttempts 最少者轮换重发（平局取字典序，P1 公平性，第三波
//     经父任务裁决落地）——字典序靠前格式的永久下载失败不得独占重发轮次；
//   - **歧义（dispatching 遗留）永不重发**：该格式与全部未发送格式
//     abandoned（pending 栅栏在恢复期阻止一切新的付费派发，§4.3）。
//
// 规划器与提交期共用 catalog.AuthorizeExtraCredits（单源、输入 parity）。
func (a *Adapter) observePostSuccessConversion(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	options, err := a.optionsFor(request.Offering)
	if err != nil || strings.Count(options.ObserveEndpoint, "{task_id}") != 1 {
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			"Tripo Offering 缺少 observe_endpoint",
		)
	}
	a.touchPool(request.Offering, request.AttemptID)
	state := parsePostSuccessState(request.Opaque)
	selections := frozenConversionSelections(request.Offering, request.Input)
	if len(submittedFormats(selections, state.Conversions)) > 0 {
		a.readoptPool(request.Offering, request.AttemptID)
	}
	base := generation.Observation{
		Job:       &request.Job,
		PollAfter: request.Offering.PollInterval,
	}
	if len(selections) == 0 {
		// 未选择任何格式：零转换工作、零付费调用（历史快照/未选形态）。
		return a.postSuccessCompletion(base, request, state, selections), nil
	}
	// 承载选中项的公开输出声明缺失 → 无法标识资源身份：fail-closed，
	// 在任何付费派发之前拒绝（不发明 role/variant）。
	requirement, declared := conversionRequirement(request.Offering)
	if !declared {
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			"Tripo Offering 缺少承载附加导出格式的 on-demand 输出声明",
		)
	}

	// 1) 歧义遗留：dispatching 无回执 → 该格式与全部未发送格式
	//    abandoned（pending 栅栏使后续 Checkpoint 永远失败，诚实反映
	//    不可重发；已成功格式不受影响）。
	if hasDispatchingRemnant(state.Conversions) {
		for _, format := range unstartedFormats(selections, state.Conversions) {
			state.Conversions[format] = conversionItem{Status: conversionItemAbandoned}
		}
		for format, item := range state.Conversions {
			if item.Status == conversionItemDispatching {
				item.Status = conversionItemAbandoned
				state.Conversions[format] = item
			}
		}
		return a.postSuccessCompletion(base, request, state, selections), nil
	}

	// 2) 轮询全部在途子作业（有界并发下通常有多个，顺序稳定）。完成的
	//    子作业本轮只登记状态，产物从下一轮起交付（每轮至多一个产物
	//    声明）；失败/取消的子作业进入退避等待，预算用尽才终结自身。
	backoff := false
	for _, format := range submittedFormats(selections, state.Conversions) {
		item := state.Conversions[format]
		value, err := a.observeTask(ctx, options, item.RemoteID, string(request.AttemptID)+":convert:"+format)
		if err != nil {
			return generation.Observation{}, err
		}
		status := conversionStatusWord(value.Status)
		rule, known := declaredStatusRule(a.statusMapping, status)
		if !known {
			return generation.Observation{}, failure(generation.FailureProtocol, false, "Tripo 转换返回未知任务状态")
		}
		phase, _ := conversionChildPhase(a.statusMapping, value.Status)
		if a.unknownStatusExpired(item.RemoteID, status, rule) {
			// 结果不明：不重发、不断言失败，槽位保持 pending 由 deadline
			// 收尾（父任务已成功，语义不回退）。
			item.Attempts = append(item.Attempts, conversionAttempt{
				RemoteID:        item.RemoteID,
				CreditsConsumed: bytes.Clone(value.CreditsConsumed),
				Outcome:         conversionAttemptUnknown,
			})
			item.Status = conversionItemUnknown
			item.Locator = ""
			state.Conversions[format] = item
			continue
		}
		locator := ""
		if phase == generation.UpstreamCompleted {
			locator = firstHTTPOutput(value.Output, "model_url", "model")
		}
		updated, wait := applyConversionChild(item, phase, locator, value.CreditsConsumed)
		if wait > 0 {
			backoff = true
		}
		if updated.Status == conversionItemDone {
			// 上游完成 ≠ 本地兑现：本轮交付这一个产物声明，兑现由内核按
			// 本地资产证据判定（P1）。其余在途子作业下一轮继续。
			updated.DeliveryAttempts++
			state.Conversions[format] = updated
			return a.deliveryObservation(base, request, state, format, updated.Locator, updated.DeliveryAttempts), nil
		}
		state.Conversions[format] = updated
	}

	// 3) 刚进入退避的格式本轮不再派发：先让退避走完（官方 1、2、4… 秒）。
	if backoff {
		base.Phase = generation.UpstreamRunning
		base.Progress = 50
		base.PollAfter = conversionRetryWait(state.Conversions)
		base.DedupeKey = privateDedupeKey("convert", "retry-backoff", strconv.Itoa(len(state.Conversions)))
		base.Opaque = encodePostSuccessState(state)
		return base, nil
	}
	// 派发下一个已同意格式（规划器 → 检查点 → 一次 HTTP）：优先待重提
	// 格式，其次未开始的格式；同时在途的子作业不超过声明并发。未开始
	//    的独立已授权转换优先于未兑现重发（A1 整改，resources-review
	//    REJECT）：某个格式的本地下载永久失败不得占据每一轮观察、饿死其余
	//    尚未派发的选中格式；重发只在全部选中格式都已开始之后进行。
	if inflight := len(submittedFormats(selections, state.Conversions)); inflight < a.conversionConcurrency(options) {
		if format, ok := nextConversionDispatch(selections, state.Conversions); ok {
			if err := planPostSuccessConversion(request.Offering, request.Input); err != nil {
				// 未知/超预算/缺同意：零 Checkpoint、零发送；剩余格式 denied。
				for _, remaining := range unstartedFormats(selections, state.Conversions) {
					state.Conversions[remaining] = conversionItem{Status: conversionItemDenied}
				}
				return a.postSuccessCompletion(base, request, state, selections), nil
			}
			if options.ConvertEndpoint == "" {
				for _, remaining := range unstartedFormats(selections, state.Conversions) {
					state.Conversions[remaining] = conversionItem{Status: conversionItemFailed}
				}
				return a.postSuccessCompletion(base, request, state, selections), nil
			}
			previous, existed := state.Conversions[format]
			extras, err := conversionExtras(request.Offering, options, request.Input, format)
			if err != nil {
				return generation.Observation{}, err
			}
			// Fixed order (C1): take the upstream slot, then change the entry
			// state, then take the dispatch permit, then send. A full pool
			// changes nothing and returns a running observation with the
			// upstream queue clock.
			slot, acquired, wait := a.acquirePool(ctx, request.Offering, request.AttemptID)
			if !acquired {
				queued := base
				queued.Phase = generation.UpstreamRunning
				queued.Progress = 50
				queued.DedupeKey = privateDedupeKey("convert", format, "pool-queued", strconv.Itoa(len(state.Conversions)))
				queued.Opaque = encodePostSuccessState(state)
				return a.poolQueueObservation(queued, request.Offering, wait), nil
			}
			// The retry budget is spent when the resubmission is accepted, not
			// when it is planned: a 429 refusal never reached the upstream.
			attempts := previous.RetryAttempts
			submissionKey := string(request.AttemptID) + ":convert:" + format
			if previous.Status == conversionItemRetryPending || previous.Status == conversionItemRateLimited {
				attempts++
			}
			if attempts > 0 {
				submissionKey += ":retry:" + strconv.Itoa(attempts)
			}
			state.Conversions[format] = conversionItem{
				Status:           conversionItemDispatching,
				RateLimitRetries: previous.RateLimitRetries,
				RetryAttempts:    attempts,
				Attempts:         previous.Attempts,
			}
			permit, err := privateDispatchPermit(ctx, submissionKey, dispatchIntentStage{
				Stage: "models/convert",
				Details: map[string]any{
					"format":      format,
					"source_task": request.Job.RemoteID,
					// intent 自含完整转换表：检查点事务会用 intent 覆盖
					// opaque_json，部分状态会丢失协调事实（§4.5.2）。
					"conversions": state.Conversions,
				},
			})
			if err != nil {
				// ErrDispatchPending 等失败：零发送；返回错误由内核有界重排
				//（下一轮按 1) 保守 abandoned）。
				slot.Release()
				return generation.Observation{}, err
			}
			remoteID, err := a.convertModel(ctx, options.ConvertEndpoint, request.Job.RemoteID, format, submissionKey, extras)
			if err != nil {
				// The account pool is full (code 2000) and this path is gated by
				// the pool: restore the entry, release the slot and queue again
				// without spending the retry budget (C4). Without a pool the
				// historical per-format budget below applies.
				if a.poolEnabled() {
					if retryAfter, full := poolFullRetryAfter(err); full && permit != nil {
						permit.Result = generation.DispatchResultNotDispatched
						restoreConversionItem(state.Conversions, format, previous, existed)
						slot.Release()
						queued := base
						queued.Phase = generation.UpstreamRunning
						queued.Progress = 50
						queued.DedupeKey = privateDedupeKey("convert", format, "pool-refused", strconv.Itoa(len(state.Conversions)))
						queued.Opaque = encodePostSuccessState(state)
						queued.Dispatch = permit
						return a.poolQueueObservation(queued, request.Offering, retryAfter), nil
					}
				}
				// 明确的 HTTP 429 是上游拒绝（未受理）：以 not_dispatched 结算
				// 许可，按 Retry-After 或退避稍后以新许可重发同一格式；预算用尽
				// 时该格式以确定失败结束。
				if retryAfter, refused := explicitRateLimit(err); refused && permit != nil {
					permit.Result = generation.DispatchResultNotDispatched
					wait, exhausted := recordConversionRefusal(state.Conversions, format, retryAfter)
					if exhausted {
						observation := a.postSuccessCompletion(base, request, state, selections)
						observation.Dispatch = permit
						return observation, nil
					}
					base.Phase = generation.UpstreamRunning
					base.Progress = 50
					base.PollAfter = wait
					base.DedupeKey = privateDedupeKey("convert", format, "rate_limited", strconv.Itoa(state.Conversions[format].RateLimitRetries))
					base.Opaque = encodePostSuccessState(state)
					base.Dispatch = permit
					return base, nil
				}
				// 发送失败/歧义：无回执 → 检查点停留 dispatching，下一轮按 1)
				// 保守 abandoned；本轮返回错误（内核有界重排，不重发）。
				return generation.Observation{}, err
			}
			// The slot now belongs to this attempt's children (C2).
			a.handoffPool(request.Offering, request.AttemptID)
			state.Conversions[format] = conversionItem{
				Status:           conversionItemSubmitted,
				RemoteID:         remoteID,
				RateLimitRetries: state.Conversions[format].RateLimitRetries,
				RetryAttempts:    attempts,
				Attempts:         previous.Attempts,
			}
			state.Stage = "models/convert"
			state.SubmissionKey = submissionKey
			state.Format = format
			base.Phase = generation.UpstreamRunning
			base.Progress = 50
			base.DedupeKey = privateDedupeKey("convert", format, remoteID, "submitted")
			base.Opaque = encodePostSuccessState(state)
			base.Dispatch = permit
			return base, nil
		}
	}

	// 4) 无未开始格式、无待重提格式。上游已完成但内核回报槽位仍未兑现
	//    （下载失败/重启/CAS 失败）：重发同一产物声明；多个格式同时未兑现
	//    时选 DeliveryAttempts 最少者（平局取字典序，P1 公平性轮换，进度随
	//    私有状态跨重启持久化）；已重发过则先刷新可能过期的签名定位符
	//    （observe_endpoint 的 GET 查询，非付费，绝不重发 convert）。
	if format, item, found := undeliveredConversion(state.Conversions, request.ResourceSlots, requirement); found {
		locator := item.Locator
		if item.DeliveryAttempts > 0 {
			value, err := a.observeTask(
				ctx,
				options,
				item.RemoteID,
				string(request.AttemptID)+":convert:"+format+":refresh",
			)
			if err != nil {
				return generation.Observation{}, err
			}
			if refreshed := firstHTTPOutput(value.Output, "model_url", "model"); refreshed != "" {
				locator = refreshed
			}
		}
		if locator == "" {
			item.Status = conversionItemFailed
			item.Locator = ""
			state.Conversions[format] = item
			return a.postSuccessCompletion(base, request, state, selections), nil
		}
		item.Locator = locator
		item.DeliveryAttempts++
		state.Conversions[format] = item
		return a.deliveryObservation(base, request, state, format, locator, item.DeliveryAttempts), nil
	}

	// 5) 无在途、无未开始、无待兑现：完成断言。仍有待重提格式时不
	//    断言终态（下一轮继续），按退避等待。
	if wait := conversionRetryWait(state.Conversions); wait > 0 && hasPendingRetry(selections, state.Conversions) {
		base.Phase = generation.UpstreamRunning
		base.Progress = 50
		base.PollAfter = wait
		base.DedupeKey = privateDedupeKey("convert", "retry-pending", strconv.Itoa(len(state.Conversions)))
		base.Opaque = encodePostSuccessState(state)
		return base, nil
	}
	return a.postSuccessCompletion(base, request, state, selections), nil
}

// deliveryObservation 构造"交付一个已完成转换产物"的观察：只携带产物
// 声明（兑现由内核按本地资产证据判定），不断言 ready——上游完成 ≠ 本地
// READY（P1）。dedupe key 含重发次数，便于事件与日志区分首次交付与重发。
func (a *Adapter) deliveryObservation(
	base generation.Observation,
	request generation.ObserveRequest,
	state postSuccessState,
	format string,
	locator string,
	attempt int,
) generation.Observation {
	requirement, _ := conversionRequirement(request.Offering)
	item := state.Conversions[format]
	base.Phase = generation.UpstreamCompleted
	base.Progress = 100
	base.DedupeKey = privateDedupeKey("convert", format, item.RemoteID, "deliver", strconv.Itoa(attempt))
	base.Artifacts = []generation.ArtifactClaim{{
		ID:        "convert:" + format,
		Role:      requirement.Role,
		Variant:   conversionVariant(requirement, format),
		Locator:   locator,
		MediaType: modelMediaType(locator),
	}}
	base.Opaque = encodePostSuccessState(state)
	return base
}

// postSuccessCompletion 构造"本轮无产物交付"的观察：携带滚动状态与资源
// 断言。断言形状由冻结快照的声明决定（不做静默重解释）：
//   - 逐项形状（声明 variant_from）：逐个终态失败项断言其自身槽位，
//     混合结果由内核按槽位公开；未开始的选中格式存在时不断言任何终态；
//   - 聚合形状（历史快照，仅一个前缀槽位）：只有**全部**选中格式到达
//     终态才断言，且仅在全部成功时断言 ready（绝不 anyDone → 整槽 ready）。
func (a *Adapter) postSuccessCompletion(
	base generation.Observation,
	request generation.ObserveRequest,
	state postSuccessState,
	selections []string,
) generation.Observation {
	// The post-success run is over: free the attempt's upstream slot.
	a.releasePool(request.Offering, request.AttemptID)
	base.Phase = generation.UpstreamCompleted
	base.Progress = 100
	base.DedupeKey = "post-success:" + string(request.AttemptID) + ":" + strconv.Itoa(len(state.Conversions))
	base.Opaque = encodePostSuccessState(state)
	base.ResourceUpdates = append(base.ResourceUpdates, conversionAssertions(request, state, selections)...)
	return base
}

// conversionAssertions 按冻结快照声明推导本轮资源断言（通用规则，无
// per-format 分支）。
func conversionAssertions(
	request generation.ObserveRequest,
	state postSuccessState,
	selections []string,
) []generation.ResourceUpdate {
	if len(state.Conversions) == 0 {
		return nil
	}
	requirement, declared := conversionRequirement(request.Offering)
	if !declared {
		return nil
	}
	if requirement.VariantFrom == "" {
		// 聚合形状（历史快照兼容）：全部选中格式终态才断言。
		if update := aggregateCompletionUpdate(state.Conversions, selections, requirement); update != nil {
			return []generation.ResourceUpdate{*update}
		}
		return nil
	}
	// 逐项形状：每个终态失败项断言自己的槽位（内核只应用仍 pending 的
	// 槽位，重复断言幂等）；done 项不断言——READY 只来自本地资产证据。
	updates := make([]generation.ResourceUpdate, 0, len(state.Conversions))
	for _, format := range sortedFormats(state.Conversions) {
		item := state.Conversions[format]
		stateCode, reason, terminalFailure := conversionFailureAssertion(item.Status)
		if !terminalFailure {
			continue
		}
		if !slotStillPending(request.ResourceSlots, requirement.Role, conversionVariant(requirement, format)) {
			continue
		}
		updates = append(updates, generation.ResourceUpdate{
			Role:    requirement.Role,
			Variant: conversionVariant(requirement, format),
			State:   stateCode,
			Reason:  reason,
		})
	}
	return updates
}

// conversionFailureAssertion 把私有终态映射到内核有限枚举（失败族）；
// done/dispatching/submitted 不产生断言。
func conversionFailureAssertion(
	status string,
) (generation.ResourceStateCode, string, bool) {
	switch status {
	case conversionItemAbandoned:
		return generation.ResourceAbandoned, "post_success.conversion_abandoned", true
	case conversionItemDenied:
		return generation.ResourceDenied, "post_success.conversion_denied", true
	case conversionItemFailed:
		return generation.ResourceFailed, "post_success.conversion_failed", true
	default:
		return "", "", false
	}
}

// slotStillPending 判定内核是否仍把某个逐项槽位视为未决：内核未提供槽位
// 视图时（例如直接调用适配器的单元测试）保守返回 true，由内核侧的
// "仅 pending 可转移 + 终态不可逆"规则兜住幂等。
func slotStillPending(
	slots []generation.ResourceSlotState,
	role string,
	variant string,
) bool {
	if len(slots) == 0 {
		return true
	}
	for _, slot := range slots {
		if slot.Role == role && slot.Variant == variant {
			return slot.State == generation.ResourcePending
		}
	}
	return false
}

// aggregateCompletionUpdate 是历史聚合槽位（单个前缀槽位）的完成断言：
// 只有全部选中格式到达终态才断言；全部 done → ready，否则取最严重的
// 失败态（abandoned > denied > failed）。混合结果**绝不**断言 ready——
// 单个前缀槽位无法表达混合事实，宁可保持 pending 交由 deadline finalizer
// 按资产事实收尾（显式登记的兼容行为，不做静默重解释）。
func aggregateCompletionUpdate(
	conversions map[string]conversionItem,
	selections []string,
	requirement generation.AssetRequirement,
) *generation.ResourceUpdate {
	if len(selections) == 0 {
		return nil
	}
	anyAbandoned, anyDenied, anyFailed := false, false, false
	done := 0
	for _, format := range selections {
		item, exists := conversions[format]
		if !exists {
			// 仍有选中格式未开始：不得终结聚合槽位（P0）。
			return nil
		}
		switch item.Status {
		case conversionItemDone:
			done++
		case conversionItemAbandoned:
			anyAbandoned = true
		case conversionItemDenied:
			anyDenied = true
		case conversionItemFailed:
			anyFailed = true
		case conversionItemDispatching, conversionItemSubmitted, conversionItemRateLimited:
			return nil
		}
	}
	if done == len(selections) {
		return &generation.ResourceUpdate{
			Role:          requirement.Role,
			VariantPrefix: requirement.VariantPrefix,
			State:         generation.ResourceReady,
		}
	}
	switch {
	case anyAbandoned:
		return &generation.ResourceUpdate{
			Role:          requirement.Role,
			VariantPrefix: requirement.VariantPrefix,
			State:         generation.ResourceAbandoned,
			Reason:        "post_success.conversion_abandoned",
		}
	case anyDenied:
		return &generation.ResourceUpdate{
			Role:          requirement.Role,
			VariantPrefix: requirement.VariantPrefix,
			State:         generation.ResourceDenied,
			Reason:        "post_success.conversion_denied",
		}
	case anyFailed:
		return &generation.ResourceUpdate{
			Role:          requirement.Role,
			VariantPrefix: requirement.VariantPrefix,
			State:         generation.ResourceFailed,
			Reason:        "post_success.conversion_failed",
		}
	}
	return nil
}

// conversionRequirement 从冻结快照解析承载选中项的 on-demand 输出声明
// （通用规则，无 per-format 分支）：优先逐项声明（variant_from），否则
// 回退到 selection 驱动的聚合前缀声明（历史快照形状）。
func conversionRequirement(
	offering generation.Offering,
) (generation.AssetRequirement, bool) {
	if offering.PriceSelectionParameter() == "" {
		return generation.AssetRequirement{}, false
	}
	aggregate := generation.AssetRequirement{}
	found := false
	for _, requirement := range offering.Outputs {
		if requirement.Delivery != generation.AssetDeliveryOnDemand {
			continue
		}
		if requirement.VariantFrom != "" {
			return requirement, true
		}
		if requirement.VariantPrefix != "" && !found {
			aggregate = requirement
			found = true
		}
	}
	return aggregate, found
}

// conversionVariant 派生单个选中格式的公开变体身份：逐项声明走内核同一
// 实现（generation.SelectionVariant，无第二真源）；历史快照（声明前形状）
// 保持既有的小写派生，确保在途任务的资产身份逐字节不变。
func conversionVariant(requirement generation.AssetRequirement, format string) string {
	if requirement.VariantFrom != "" {
		return generation.SelectionVariant(requirement, format)
	}
	return requirement.VariantPrefix + strings.ToLower(format)
}

// undeliveredConversion 返回"上游已完成但内核槽位仍未兑现"且
// **DeliveryAttempts 最少**的格式（P1 公平性，第三波经父任务裁决落地）：
// 多个格式同时未兑现时，重发轮次在候选间轮换，平局取字典序
// （sortedFormats 稳定序 + 严格小于比较）——字典序靠前格式的永久下载
// 失败不得独占全部重发轮次，下载已恢复的靠后格式必须在 deadline 前
// 获得重发机会。DeliveryAttempts 随私有状态持久化，轮换进度跨重启
// 不丢；历史快照（无 delivery_attempts 字段 → 全零平局）退化为既有
// 字典序行为，语义逐字节不变。重发的唯一界限仍是 Schema
// post_success.deadline_seconds，本选择器不引入任何尝试次数上限。
//
// 内核在成功后观察请求中只读地回报槽位状态，本地下载失败、进程重启
// 或提交 CAS 失败都会让槽位保持 pending，此时凭私有 locator 重发产物
// 声明即可，绝不重发付费转换。
func undeliveredConversion(
	conversions map[string]conversionItem,
	slots []generation.ResourceSlotState,
	requirement generation.AssetRequirement,
) (string, conversionItem, bool) {
	if requirement.VariantFrom == "" || len(slots) == 0 {
		// 聚合形状没有逐项身份可对照（历史快照）：保持既有行为，不重发。
		return "", conversionItem{}, false
	}
	bestFormat := ""
	var bestItem conversionItem
	found := false
	for _, format := range sortedFormats(conversions) {
		item := conversions[format]
		if item.Status != conversionItemDone {
			continue
		}
		if !slotStillPending(slots, requirement.Role, conversionVariant(requirement, format)) {
			continue
		}
		if !found || item.DeliveryAttempts < bestItem.DeliveryAttempts {
			bestFormat, bestItem, found = format, item, true
		}
	}
	return bestFormat, bestItem, found
}

// sortedFormats 返回状态表中的格式名（稳定序）。
func sortedFormats(conversions map[string]conversionItem) []string {
	formats := make([]string, 0, len(conversions))
	for format := range conversions {
		formats = append(formats, format)
	}
	sort.Strings(formats)
	return formats
}

// convertModel 发送一次格式转换（一次 ack 至多一次 HTTP，§4.3）。extras 是
// Offering 声明的其余请求字段（贴图格式、贴图尺寸、原点、FBX 预设，见
// conversion_parameters.go）；没有声明的 Offering 与旧冻结任务仍只发送
// input 和 format。
func (a *Adapter) convertModel(
	ctx context.Context,
	endpoint string,
	sourceRemoteID string,
	format string,
	submissionKey string,
	extras map[string]any,
) (string, error) {
	var response envelope[struct {
		TaskID string `json:"task_id"`
	}]
	body := map[string]any{"input": sourceRemoteID, "format": format}
	for name, value := range extras {
		body[name] = value
	}
	if err := a.client.DoJSON(
		ctx,
		http.MethodPost,
		endpoint,
		body,
		submissionKey,
		&response,
		true,
	); err != nil {
		return "", err
	}
	if err := businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return "", err
	}
	if response.Data.TaskID == "" {
		return "", submissionUnknown("Tripo 转换响应缺少 task_id")
	}
	return response.Data.TaskID, nil
}

// planPostSuccessConversion 是生成期转换的发送前规划器：与提交期同一
// 共享评估器（catalog.AuthorizeExtraCredits）、同一冻结声明与冻结输入。
// 任何拒绝都零 Checkpoint、零发送。
func planPostSuccessConversion(
	offering generation.Offering,
	input json.RawMessage,
) error {
	if offering.PriceSelectionParameter() == "" {
		return failure(generation.FailureRejected, false, catalog.UnknownCostError(offering).Error())
	}
	if _, err := catalog.AuthorizeExtraCredits(offering, input); err != nil {
		return failure(generation.FailureRejected, false, err.Error())
	}
	return nil
}

// frozenConversionSelections 读取冻结输入的选中格式（仅接受冻结 options
// 内的取值；未声明/空选择返回 nil——无转换工作）。
func frozenConversionSelections(
	offering generation.Offering,
	input json.RawMessage,
) []string {
	if offering.PriceSelectionParameter() == "" {
		return nil
	}
	var values map[string]any
	if len(input) > 0 {
		decoder := json.NewDecoder(strings.NewReader(string(input)))
		if decoder.Decode(&values) != nil {
			return nil
		}
	}
	declared := map[string]bool{}
	for _, parameter := range offering.Parameters {
		if parameter.Name != offering.PriceSelectionParameter() {
			continue
		}
		for _, option := range parameter.Options {
			declared[option.Value] = true
		}
		break
	}
	if len(declared) == 0 {
		return nil
	}
	items, _ := values[offering.PriceSelectionParameter()].([]any)
	selections := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		text, ok := item.(string)
		if !ok || !declared[text] || seen[text] {
			continue
		}
		seen[text] = true
		selections = append(selections, text)
	}
	return selections
}

// parsePostSuccessState 解析上一轮私有状态（空/nil → 空表）。
func parsePostSuccessState(opaque json.RawMessage) postSuccessState {
	state := postSuccessState{Conversions: map[string]conversionItem{}}
	if len(opaque) > 0 {
		_ = json.Unmarshal(opaque, &state)
	}
	if state.Conversions == nil {
		state.Conversions = map[string]conversionItem{}
	}
	return state
}

// encodePostSuccessState 序列化私有状态（编码失败保持空表——协调事实
// 丢失时下一轮按 dispatching 遗留/重新规划保守处理）。
func encodePostSuccessState(state postSuccessState) json.RawMessage {
	if state.Conversions == nil {
		state.Conversions = map[string]conversionItem{}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	return encoded
}

// recordConversionRefusal records an explicit 429 refusal of a private
// conversion child. Each format has its own budget (the official page bounds
// the retries of one request, not of a run); within it the format waits as
// rate_limited (the returned wait is Retry-After, or the backoff), after it the
// format fails. Retry-After wins whenever the upstream sent one.
func recordConversionRefusal(conversions map[string]conversionItem, format string, retryAfter time.Duration) (time.Duration, bool) {
	item := conversions[format]
	if item.RateLimitRetries >= maximumConversionRateLimitRetries {
		item.Status = conversionItemFailed
		conversions[format] = item
		return 0, true
	}
	item.Status = conversionItemRateLimited
	item.RateLimitRetries++
	conversions[format] = item
	return conversionRateLimitWait(retryAfter, item.RateLimitRetries), false
}

// conversionRateLimitFailure is the definite, non-retryable failure of a run
// whose explicit 429 refusals used up their budget; no new Attempt repeats the
// paid base generation.
func conversionRateLimitFailure(detail string) *generation.Failure {
	return &generation.Failure{Class: generation.FailureRateLimited, Retryable: false, SafeDetail: detail}
}

func hasDispatchingRemnant(conversions map[string]conversionItem) bool {
	for _, item := range conversions {
		if item.Status == conversionItemDispatching {
			return true
		}
	}
	return false
}

// submittedFormats 返回在途（submitted）的选中格式，按冻结选择顺序——
// 有界并发下同一轮要轮询全部在途子作业，顺序与 map 迭代无关。
func submittedFormats(selections []string, conversions map[string]conversionItem) []string {
	formats := make([]string, 0, len(conversions))
	for _, format := range selections {
		if item, exists := conversions[format]; exists && item.Status == conversionItemSubmitted {
			formats = append(formats, format)
		}
	}
	return formats
}

// hasPendingRetry 报告是否仍有格式在等待重提（退避中）。
func hasPendingRetry(selections []string, conversions map[string]conversionItem) bool {
	for _, format := range selections {
		if item, exists := conversions[format]; exists && item.Status == conversionItemRetryPending {
			return true
		}
	}
	return false
}

// allFormatsDone 报告选中格式是否都已完成。
func allFormatsDone(selections []string, conversions map[string]conversionItem) bool {
	for _, format := range selections {
		if item, exists := conversions[format]; !exists || item.Status != conversionItemDone {
			return false
		}
	}
	return true
}

// conversionStatusWord 归一化上游状态词（小写、去空白）。
func conversionStatusWord(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

// unstartedFormats 返回尚未出现在状态表中、或上次被明确 429 拒绝（未受理）
// 的选中格式（稳定序）。
func unstartedFormats(selections []string, conversions map[string]conversionItem) []string {
	remaining := make([]string, 0, len(selections))
	for _, format := range selections {
		if item, exists := conversions[format]; !exists || item.Status == conversionItemRateLimited {
			remaining = append(remaining, format)
		}
	}
	return remaining
}

// conversionChildPhase 按连接状态词表解析转换子作业状态。
func conversionChildPhase(
	mapping *generation.StatusMapping,
	status string,
) (generation.UpstreamPhase, generation.StatusRule) {
	rule, declared := declaredStatusRule(mapping, strings.ToLower(strings.TrimSpace(status)))
	if !declared {
		return generation.UpstreamRunning, rule
	}
	switch rule.Phase {
	case "completed":
		return generation.UpstreamCompleted, rule
	case "failed":
		return generation.UpstreamFailed, rule
	case "canceled":
		return generation.UpstreamCanceled, rule
	default:
		return generation.UpstreamRunning, rule
	}
}
