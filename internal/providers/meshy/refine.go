package meshy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Meshy Text to 3D needs two upstream tasks for a textured model: a preview
// (mesh only) and a refine that textures that preview. An Offering that
// declares provider_options.refine_after_preview ({"set": {...},
// "source_parameter": "..."}) runs both inside one public task: Start
// submits the preview, Observe submits the refine once the preview succeeds
// and then reports the refine task. The refine is a private sub-job;
// its remote ID lives only in the attempt's opaque checkpoint.

const refineDispatchStage = "text-to-3d.refine"

// ResolveDependencyJob projects the delivered result from the frozen source.
// It never observes or submits a remote task and keeps the preview identity intact.
func (a *Adapter) ResolveDependencyJob(request generation.DependencyJobRequest) (generation.UpstreamJob, error) {
	var options wireOptions
	if len(request.Offering.ProviderOptions) != 0 {
		if err := json.Unmarshal(request.Offering.ProviderOptions, &options); err != nil {
			return generation.UpstreamJob{}, fmt.Errorf("invalid source declaration")
		}
	}
	if options.RefineAfterPreview == nil {
		return request.Job, nil
	}
	// Historical false is a disabled single-stage declaration.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(request.Offering.ProviderOptions, &raw)
	if string(raw["refine_after_preview"]) == "false" {
		return request.Job, nil
	}
	if !refineAfterPreview(request.Offering) {
		return generation.UpstreamJob{}, fmt.Errorf("invalid source refine declaration")
	}
	var checkpoint struct {
		RefineTaskID string `json:"refine_task_id"`
		Stage        string `json:"stage"`
	}
	if err := json.Unmarshal(request.Opaque, &checkpoint); err != nil ||
		checkpoint.Stage != "" || strings.TrimSpace(checkpoint.RefineTaskID) == "" {
		return generation.UpstreamJob{}, fmt.Errorf("source refine result unavailable")
	}
	job := request.Job
	job.RemoteID = strings.TrimSpace(checkpoint.RefineTaskID)
	return job, nil
}

// refineState is the private opaque checkpoint of a two-stage task.
type refineState struct {
	RefineTaskID string `json:"refine_task_id,omitempty"`
}

// refineDeclaration is provider_options.refine_after_preview. Set holds the
// input values that turn the frozen preview input into a refine request
// (Meshy: mode=refine); SourceParameter names the parameter that carries the
// preview task's remote ID (Meshy: preview_task_id). Both come from the
// Product Schema, so the adapter holds no Meshy parameter names.
type refineDeclaration struct {
	Set             map[string]any `json:"set"`
	SourceParameter string         `json:"source_parameter"`
}

// UnmarshalJSON also accepts the boolean form frozen into task snapshots
// created before the declaration carried its values.
func (d *refineDeclaration) UnmarshalJSON(raw []byte) error {
	var enabled bool
	if json.Unmarshal(raw, &enabled) == nil {
		if enabled {
			*d = refineDeclaration{Set: map[string]any{"mode": "refine"}, SourceParameter: "preview_task_id"}
		}
		return nil
	}
	type plain refineDeclaration
	return json.Unmarshal(raw, (*plain)(d))
}

func refineAfterPreview(offering generation.Offering) bool {
	_, ok := refineDeclarationOf(offering)
	return ok
}

func refineDeclarationOf(offering generation.Offering) (refineDeclaration, bool) {
	var options wireOptions
	if json.Unmarshal(offering.ProviderOptions, &options) != nil || options.RefineAfterPreview == nil {
		return refineDeclaration{}, false
	}
	declaration := *options.RefineAfterPreview
	declaration.SourceParameter = strings.TrimSpace(declaration.SourceParameter)
	if len(declaration.Set) == 0 || declaration.SourceParameter == "" {
		return refineDeclaration{}, false
	}
	return declaration, true
}

// parseRefineState reads the checkpoint. pending is true when the stored bytes
// are a refine dispatch intent without a recorded remote ID: the paid POST may
// or may not have reached Meshy, so it must never be sent again.
func parseRefineState(opaque json.RawMessage) (state refineState, pending bool) {
	if len(opaque) == 0 {
		return refineState{}, false
	}
	var intent struct {
		Stage string `json:"stage"`
	}
	if json.Unmarshal(opaque, &intent) == nil && intent.Stage == refineDispatchStage {
		return refineState{}, true
	}
	_ = json.Unmarshal(opaque, &state)
	state.RefineTaskID = strings.TrimSpace(state.RefineTaskID)
	return state, false
}

func (a *Adapter) observeWithRefine(
	ctx context.Context,
	request generation.ObserveRequest,
	endpoint string,
	version string,
) (generation.Observation, error) {
	state, pending := parseRefineState(request.Opaque)
	if pending {
		return generation.Observation{
			Job:       &request.Job,
			Phase:     generation.UpstreamFailed,
			Opaque:    request.Opaque,
			DedupeKey: "refine-dispatch-unknown",
			Failure: &generation.Failure{
				Class:      generation.FailureRejected,
				SafeDetail: "Meshy 精细化提交结果不明，已停止再次付费",
			},
		}, nil
	}
	if state.RefineTaskID == "" {
		return a.observePreviewStage(ctx, request, endpoint, version)
	}
	var response taskResponse
	if err := a.client.DoJSON(
		ctx,
		http.MethodGet,
		fmt.Sprintf("openapi/%s/%s/%s", version, endpoint, url.PathEscape(state.RefineTaskID)),
		nil,
		string(request.AttemptID)+":refine",
		&response,
		false,
	); err != nil {
		return generation.Observation{}, err
	}
	refineRequest := request
	refineRequest.Job.RemoteID = state.RefineTaskID
	observation, err := a.observation(refineRequest, response)
	if err != nil {
		return generation.Observation{}, err
	}
	// The public attempt keeps the preview job identity; the refine stays private.
	observation.Job = &request.Job
	observation.Opaque = request.Opaque
	observation.DedupeKey = "refine:" + observation.DedupeKey
	switch observation.Phase {
	case generation.UpstreamAccepted, generation.UpstreamRunning:
		observation.Phase = generation.UpstreamRunning
		observation.Progress = 50 + clampProgress(response.Progress)/2
	}
	return observation, nil
}

func (a *Adapter) observePreviewStage(
	ctx context.Context,
	request generation.ObserveRequest,
	endpoint string,
	version string,
) (generation.Observation, error) {
	var response taskResponse
	if err := a.client.DoJSON(
		ctx,
		http.MethodGet,
		fmt.Sprintf("openapi/%s/%s/%s", version, endpoint, url.PathEscape(request.Job.RemoteID)),
		nil,
		string(request.AttemptID),
		&response,
		false,
	); err != nil {
		return generation.Observation{}, err
	}
	observation, err := a.observation(request, response)
	if err != nil {
		return generation.Observation{}, err
	}
	observation.Opaque = request.Opaque
	switch observation.Phase {
	case generation.UpstreamAccepted, generation.UpstreamRunning:
		observation.Progress = clampProgress(response.Progress) / 2
		return observation, nil
	case generation.UpstreamCompleted:
		if request.PostSuccess {
			// 成功后观察期父任务已终态，绝不补交付费精细化。
			return observation, nil
		}
	default:
		return observation, nil
	}
	refineID, permit, err := a.submitRefine(ctx, request)
	if err != nil {
		return generation.Observation{}, err
	}
	opaque, err := json.Marshal(refineState{RefineTaskID: refineID})
	if err != nil {
		return generation.Observation{}, providerFailure(generation.FailureInternal, false, "Meshy 精细化检查点无法编码")
	}
	return generation.Observation{
		Job:        &request.Job,
		Phase:      generation.UpstreamRunning,
		Progress:   50,
		ObservedAt: time.Now().UTC(),
		PollAfter:  request.Offering.PollInterval,
		DedupeKey:  "refine:" + refineID + ":submitted",
		Opaque:     opaque,
		Dispatch:   permit,
	}, nil
}

// submitRefine builds the refine body from the frozen input through the same
// Schema projection as the preview (omit_when switches the stage-specific
// parameters), then sends the paid create behind a private dispatch permit.
func (a *Adapter) submitRefine(
	ctx context.Context,
	request generation.ObserveRequest,
) (string, *generation.DispatchReceipt, error) {
	endpoint, version, err := wireRoute(request.Offering)
	if err != nil {
		return "", nil, err
	}
	declaration, ok := refineDeclarationOf(request.Offering)
	if !ok {
		return "", nil, providerFailure(generation.FailureInvalidInput, false, "Meshy 精细化声明缺少 set 或 source_parameter")
	}
	var body map[string]any
	if err := json.Unmarshal(request.Input, &body); err != nil || body == nil {
		return "", nil, providerFailure(generation.FailureInvalidInput, false, "规范化输入不是 JSON 对象")
	}
	for name, value := range declaration.Set {
		body[name] = value
	}
	delete(body, declaration.SourceParameter)
	if err := a.projectInput(ctx, generation.StartRequest{
		TaskID:    request.TaskID,
		AttemptID: request.AttemptID,
		Offering:  request.Offering,
		Input:     request.Input,
	}, body); err != nil {
		return "", nil, err
	}
	sourceWireName := declaration.SourceParameter
	for _, parameter := range request.Offering.Parameters {
		if parameter.Name == declaration.SourceParameter && strings.TrimSpace(parameter.WireName) != "" {
			sourceWireName = strings.TrimSpace(parameter.WireName)
		}
	}
	body[sourceWireName] = request.Job.RemoteID
	submissionKey := string(request.AttemptID) + ":refine"
	permit, err := privateDispatchPermit(ctx, submissionKey, dispatchIntentStage{
		Stage:   refineDispatchStage,
		Details: map[string]any{"endpoint": endpoint, "version": version},
	})
	if err != nil {
		return "", nil, err
	}
	var response struct {
		Result string `json:"result"`
	}
	if err := a.client.DoJSON(
		ctx,
		http.MethodPost,
		fmt.Sprintf("openapi/%s/%s", version, endpoint),
		body,
		submissionKey,
		&response,
		true,
	); err != nil {
		return "", nil, err
	}
	refineID := strings.TrimSpace(response.Result)
	if refineID == "" {
		return "", nil, submissionUnknown("Meshy 精细化创建响应缺少 result")
	}
	return refineID, permit, nil
}

func clampProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}
