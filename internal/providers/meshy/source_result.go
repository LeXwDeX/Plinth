package meshy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A unique result value is provider-private continuation metadata, not a public
// arbitrary string input. Each binding reads a previously delivered task using
// this adapter's authenticated connection, before any paid dispatch permit.
type sourceResultBinding struct {
	SourceParameter string `json:"source_parameter"`
	Endpoint        string `json:"endpoint"`
	APIVersion      string `json:"api_version"`
	TaskType        string `json:"task_type"`
	ValueField      string `json:"value_field"`
	PairedField     string `json:"paired_field"`
	WireName        string `json:"wire_name"`
}

func validateSourceResults(offering generation.Offering, options wireOptions) error {
	if len(options.SourceResults) > 8 {
		return fmt.Errorf("source_result_bindings 超过数量上限")
	}
	seen := map[string]bool{}
	for _, binding := range options.SourceResults {
		bindValue := binding.ValueField != "" || binding.WireName != ""
		if !safeEndpointPath(binding.Endpoint) || !safePathSegment(binding.APIVersion) || !safePathSegment(binding.TaskType) ||
			!inputPathSegment.MatchString(binding.PairedField) ||
			(bindValue && (!inputPathSegment.MatchString(binding.ValueField) || binding.ValueField == binding.PairedField ||
				!inputPathSegment.MatchString(binding.WireName) || seen[binding.WireName])) {
			return fmt.Errorf("source_result_bindings 声明无效或目标重复")
		}
		if bindValue {
			seen[binding.WireName] = true
		}
		found := false
		for _, parameter := range offering.Parameters {
			if parameter.Name == binding.SourceParameter {
				found = (!bindValue || parameter.TaskSourceBinding == "provider") && parameter.Type == "task" && parameter.Required && parameter.SameProvider && len(parameter.TaskOfferings) == 1 &&
					(parameter.TaskSourceKind == "" || parameter.TaskSourceKind == "upstream_job")
			}
			wireName := strings.TrimSpace(parameter.WireName)
			if wireName == "" {
				wireName = parameter.Name
			}
			if bindValue && (binding.WireName == parameter.Name || binding.WireName == wireName) {
				return fmt.Errorf("source_result_bindings 不能覆盖公开参数")
			}
		}
		for _, path := range options.InputPaths {
			if len(path) > 0 && binding.WireName == path[0] {
				return fmt.Errorf("source_result_bindings 与嵌套输入冲突")
			}
		}
		if !found || (bindValue && binding.WireName == options.ModelParameter) {
			return fmt.Errorf("source_result_bindings 需要必需的同 Provider、精确单 Offering 上游任务来源")
		}
	}
	return nil
}

func (a *Adapter) bindSourceResults(ctx context.Context, request generation.StartRequest, options wireOptions, body map[string]any) error {
	for _, binding := range options.SourceResults {
		dependency, exists := request.TaskDependencies[binding.SourceParameter]
		validSource := false
		for _, parameter := range request.Offering.Parameters {
			if parameter.Name == binding.SourceParameter {
				validSource = slices.Contains(parameter.TaskOfferings, dependency.OfferingID) && (!parameter.SameSession || (request.SessionID != "" && dependency.SessionID == request.SessionID))
			}
		}
		if !exists || !validSource || dependency.ProviderID != request.Offering.ProviderID ||
			dependency.UpstreamJob.ProviderID != request.Offering.ProviderID || !safePathSegment(dependency.UpstreamJob.RemoteID) {
			return providerFailure(generation.FailureInvalidInput, false, "已解析的原型任务来源与声明不符")
		}
		if _, supplied := body[binding.WireName]; binding.WireName != "" && supplied {
			return providerFailure(generation.FailureInvalidInput, false, "候选值只能从已交付原型读取")
		}
		if binding.ValueField != "" {
			candidate, err := frozenSourceCandidate(dependency, binding)
			if err != nil {
				return providerFailure(generation.FailureInvalidInput, false, "原型候选或预览未冻结")
			}
			body[binding.WireName] = candidate
			continue
		}
		var response taskResponse
		if err := a.client.DoJSON(ctx, http.MethodGet,
			fmt.Sprintf("openapi/%s/%s/%s", binding.APIVersion, binding.Endpoint, url.PathEscape(dependency.UpstreamJob.RemoteID)),
			nil, string(request.AttemptID), &response, false); err != nil {
			return err
		}
		rule, declared := declaredStatusRule(a.statusMapping, strings.ToLower(strings.TrimSpace(response.Status)))
		var taskType string
		if json.Unmarshal(response.Raw["type"], &taskType) != nil || taskType != binding.TaskType || response.ID != dependency.UpstreamJob.RemoteID || !declared || rule.Phase != "completed" {
			return providerFailure(generation.FailureProtocol, false, "原型返回的身份或成功状态不符")
		}
		var paired []string
		if json.Unmarshal(response.Raw[binding.PairedField], &paired) != nil || len(paired) != 1 || strings.TrimSpace(paired[0]) == "" || len(paired[0]) > 16384 {
			return providerFailure(generation.FailureProtocol, false, "原型必须返回唯一预览")
		}
	}
	return nil
}

// This is an opaque JSON value, never a URL path. Preserve punctuation exactly.
func validSourceResultValue(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 4096 && strings.IndexFunc(value, unicode.IsControl) < 0
}

// Preparation validates only durable local binding evidence. Validation-only
// source GETs remain Start-time checks and are never performed by this port.
func validateFrozenSourceResults(request generation.InputValidationRequest) error {
	var options wireOptions
	if json.Unmarshal(request.Offering.ProviderOptions, &options) != nil {
		// Historical malformed options retain the existing model/input validator error path.
		return nil
	}
	if err := validateSourceResults(request.Offering, options); err != nil {
		return providerFailure(generation.FailureInvalidInput, false, "原型来源声明无效")
	}
	for _, binding := range options.SourceResults {
		if binding.ValueField == "" {
			continue
		}
		dependency, exists := request.TaskDependencies[binding.SourceParameter]
		if !exists || dependency.ProviderID != request.Offering.ProviderID {
			return providerFailure(generation.FailureInvalidInput, false, "原型候选或预览未冻结")
		}
		if _, err := frozenSourceCandidate(dependency, binding); err != nil {
			return providerFailure(generation.FailureInvalidInput, false, "原型候选或预览未冻结")
		}
	}
	return nil
}
