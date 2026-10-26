package catalog

// Offering, output, parameter and cost contract validation.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"math"
	"mime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maximumProviderShortLabelRunes bounds the vendor short name shown on compact controls.
const maximumProviderShortLabelRunes = 8

func validateOffering(
	offering schemacontract.Offering,
	protocols map[string]string,
	connectionCapabilities map[string][]schemacontract.Capability,
) error {
	if protocols[offering.AdapterKey] == "local-recovery" {
		if _, err := schemacontract.DecodeRecoveryOptions(offering); err != nil {
			return fmt.Errorf("Offering %q: %w", offering.ID, err)
		}
	} else if offering.Capability == schemacontract.AssetRecovery {
		return errors.New("asset recovery requires the local-recovery protocol")
	}
	if err := validateRemoteAssetHosts(offering); err != nil {
		return fmt.Errorf("Offering %q: %w", offering.ID, err)
	}
	if err := validateImageProtocol(offering); err != nil {
		return fmt.Errorf("Offering %q: %w", offering.ID, err)
	}
	var identityOptions struct {
		ResultIdentity *struct {
			Role    string `json:"role"`
			Variant string `json:"variant"`
		} `json:"result_identity"`
	}
	if len(offering.ProviderOptions) > 0 {
		if err := json.Unmarshal(offering.ProviderOptions, &identityOptions); err != nil {
			return err
		}
	}
	retained := 0
	for _, output := range offering.Outputs {
		if output.RetainDependencyIdentity {
			retained++
			if identityOptions.ResultIdentity == nil || identityOptions.ResultIdentity.Role != output.Role || identityOptions.ResultIdentity.Variant != output.Variant {
				return errors.New("retained dependency output requires paired result_identity")
			}
		}
		if output.RetainDependencyIdentity && (protocols[offering.AdapterKey] != "meshy" || output.Delivery != "required" || output.Minimum == nil || *output.Minimum != 1 || output.Variant == "" || output.VariantPrefix != "" || output.VariantFrom != "" || offering.PostSuccess != nil) {
			return errors.New("retain_dependency_identity requires a supported exact required single output without post-success")
		}
	}
	if identityOptions.ResultIdentity != nil && retained != 1 {
		return errors.New("result_identity requires exactly one retained dependency output")
	}
	if err := validateProviderRoutes(offering, protocols, connectionCapabilities); err != nil {
		return err
	}
	switch {
	case offering.ID == "":
		return errors.New("缺少 id")
	case offering.Revision == "":
		return fmt.Errorf("Offering %q 缺少 revision", offering.ID)
	case offering.ProviderID == "":
		return fmt.Errorf("Offering %q 缺少 provider_id", offering.ID)
	case strings.TrimSpace(offering.ProviderShortLabel) == "" || utf8.RuneCountInString(offering.ProviderShortLabel) > maximumProviderShortLabelRunes:
		return fmt.Errorf("Offering %q 的 provider_short_label 必须非空且不超过 %d 个字符", offering.ID, maximumProviderShortLabelRunes)
	case offering.ModelID == "":
		return fmt.Errorf("Offering %q 缺少 model_id", offering.ID)
	case offering.EstimatedCreditsUnitParameter != "":
		declared := false
		for _, parameter := range offering.Parameters {
			if parameter.Name == offering.EstimatedCreditsUnitParameter {
				declared = true
				break
			}
		}
		if !declared {
			return fmt.Errorf(
				"Offering %q 的 estimated_credits_unit_parameter %q 未在参数中声明",
				offering.ID,
				offering.EstimatedCreditsUnitParameter,
			)
		}
	case !offering.Capability.Valid():
		return fmt.Errorf("Offering %q 使用未知 capability %q", offering.ID, offering.Capability)
	case !offering.Domain.Valid():
		return fmt.Errorf("Offering %q 使用未知 domain %q", offering.ID, offering.Domain)
	case offering.AdapterKey == "":
		return fmt.Errorf("Offering %q 缺少 adapter_key", offering.ID)
	case offering.Timeout <= 0:
		return fmt.Errorf("Offering %q 的 timeout_seconds 必须大于 0", offering.ID)
	case offering.Retry.MaxAttempts <= 0:
		return fmt.Errorf("Offering %q 的 retry.max_attempts 必须大于 0", offering.ID)
	case offering.MaterializeAttempts <= 0:
		return fmt.Errorf("Offering %q 的 materialize_attempts 必须大于 0", offering.ID)
	case offering.Priority < 0:
		return fmt.Errorf("Offering %q 的 priority 不能为负数", offering.ID)
	case offering.DisplayOrder < 0:
		return fmt.Errorf("Offering %q 的 display_order 不能为负数", offering.ID)
	case offering.CapabilityOrder < 0:
		return fmt.Errorf("Offering %q 的 capability_order 不能为负数", offering.ID)
	case len(offering.Outputs) == 0:
		return fmt.Errorf("Offering %q 至少需要一个 output contract", offering.ID)
	}

	if err := validateReferenceSelectionPolicy(offering); err != nil {
		return err
	}

	features := make(map[string]struct{}, len(offering.Features))
	for index, feature := range offering.Features {
		if strings.TrimSpace(feature) == "" {
			return fmt.Errorf(
				"Offering %q 的 features[%d] 不能为空",
				offering.ID,
				index,
			)
		}
		if _, exists := features[feature]; exists {
			return fmt.Errorf(
				"Offering %q 的 feature %q 重复",
				offering.ID,
				feature,
			)
		}
		features[feature] = struct{}{}
	}
	names := map[string]struct{}{}
	actionParameters := 0
	for _, parameter := range offering.Parameters {
		// The input asset service checks image_input for every protocol (#276);
		// Meshy additionally checks the exact frozen bytes, which needs a single
		// asset.
		if parameter.ImageInput != nil && protocols[offering.AdapterKey] == "meshy" && parameter.Type != "asset" {
			return fmt.Errorf("Offering %q 的 Meshy image_input 只支持单个资产参数", offering.ID)
		}
		// Every image-to-image Offering projects its sources through imagewire, the one place that flattens.
		if parameter.AlphaFlatten != nil && offering.Capability != schemacontract.ImageToImage {
			return fmt.Errorf("Offering %q 的参数 %q 只有图生图能力实现 alpha_flatten", offering.ID, parameter.Name)
		}
		if parameter.Name == "" {
			return fmt.Errorf("Offering %q 存在无名称参数", offering.ID)
		}
		if _, exists := names[parameter.Name]; exists {
			return fmt.Errorf("Offering %q 的参数 %q 重复", offering.ID, parameter.Name)
		}
		names[parameter.Name] = struct{}{}
		if parameter.Semantic == schemacontract.SemanticAction {
			actionParameters++
		}
		if _, err := validateValueDefinition(parameter); err != nil {
			return fmt.Errorf("Offering %q: %w", offering.ID, err)
		}
	}
	// 动作参数由专用分组控件与提交路径按"唯一动作"消费（前端
	// actionParameter/visibleOfferingFields），两个动作语义参数会让过滤
	// 与写入口径分叉。
	if actionParameters > 1 {
		return fmt.Errorf("Offering %q 声明了 %d 个 action 语义参数", offering.ID, actionParameters)
	}
	for ruleIndex, rule := range offering.InputRules {
		if len(rule.ForbiddenWhen) > 0 {
			if rule.Message == "" {
				return fmt.Errorf("input rule 缺少说明")
			}
			for _, c := range rule.ForbiddenWhen {
				p, ok := offeringParameter(offering.Parameters, c.Parameter)
				if !ok {
					return fmt.Errorf("input rule 引用未知参数")
				}
				if err := validateParameterCondition(c, p); err != nil {
					return err
				}
			}
			continue
		}
		if rule.MaxTotalItems > 0 {
			if rule.Message == "" || len(rule.Parameters) == 0 {
				return fmt.Errorf("input rule 总量声明无效")
			}
			for _, name := range rule.Parameters {
				if _, ok := names[name]; !ok {
					return fmt.Errorf("input rule 引用未知参数")
				}
			}
			continue
		}
		if rule.Minimum < 0 ||
			rule.Maximum <= 0 ||
			rule.Minimum > rule.Maximum ||
			len(rule.Parameters) < rule.Maximum {
			return fmt.Errorf(
				"Offering %q 的 input_rules[%d] 存在无效数量约束",
				offering.ID,
				ruleIndex,
			)
		}
		parameters := map[string]struct{}{}
		for _, name := range rule.Parameters {
			if _, exists := names[name]; !exists {
				return fmt.Errorf(
					"Offering %q 的 input_rules[%d] 引用了未知参数 %q",
					offering.ID,
					ruleIndex,
					name,
				)
			}
			if _, exists := parameters[name]; exists {
				return fmt.Errorf(
					"Offering %q 的 input_rules[%d] 参数 %q 重复",
					offering.ID,
					ruleIndex,
					name,
				)
			}
			parameters[name] = struct{}{}
		}
	}
	for _, parameter := range offering.Parameters {
		for _, condition := range parameter.OmitWhen {
			target, exists := offeringParameter(offering.Parameters, condition.Parameter)
			if !exists {
				return fmt.Errorf("参数 %q omit_when 引用未知参数", parameter.Name)
			}
			if err := validateParameterCondition(condition, target); err != nil {
				return err
			}
		}
		if parameter.AlphaFlatten != nil {
			for _, condition := range parameter.AlphaFlatten.KeepWhen {
				target, exists := offeringParameter(offering.Parameters, condition.Parameter)
				if !exists {
					return fmt.Errorf("参数 %q alpha_flatten.keep_when 引用未知参数", parameter.Name)
				}
				if err := validateParameterCondition(condition, target); err != nil {
					return err
				}
			}
		}
		for retired, raw := range parameter.LegacyValues {
			var replacement any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if strings.TrimSpace(retired) == "" || optionAllowed(parameter.Options, retired) || decoder.Decode(&replacement) != nil {
				return fmt.Errorf("Offering %q 参数 %q 的 legacy_values[%q] 必须是已下线的取值并给出有效替代值", offering.ID, parameter.Name, retired)
			}
			if _, err := validateValue(parameter, replacement); err != nil {
				return fmt.Errorf("Offering %q 参数 %q 的 legacy_values[%q] 替代值无效: %w", offering.ID, parameter.Name, retired, err)
			}
		}
		for index, forced := range parameter.ForcedWhen {
			target, exists := offeringParameter(offering.Parameters, forced.When.Parameter)
			if !exists {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 forced_when[%d] 引用了未知参数 %q",
					offering.ID,
					parameter.Name,
					index,
					forced.When.Parameter,
				)
			}
			if err := validateParameterCondition(forced.When, target); err != nil {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 forced_when[%d]: %w",
					offering.ID,
					parameter.Name,
					index,
					err,
				)
			}
			var forcedValue any
			decoder := json.NewDecoder(bytes.NewReader(forced.Value))
			decoder.UseNumber()
			if len(bytes.TrimSpace(forced.Value)) == 0 ||
				decoder.Decode(&forcedValue) != nil {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 forced_when[%d] 缺少有效 value",
					offering.ID,
					parameter.Name,
					index,
				)
			}
			if _, err := validateValue(parameter, forcedValue); err != nil {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 forced_when[%d] value 无效: %w",
					offering.ID,
					parameter.Name,
					index,
					err,
				)
			}
		}
		if parameter.Fixed && (len(bytes.TrimSpace(parameter.Default)) == 0 ||
			parameter.Presentation == nil || (parameter.Presentation.Surface != schemacontract.SurfaceHidden && parameter.Presentation.Surface != schemacontract.SurfaceReadOnly)) {
			return fmt.Errorf(
				"Offering %q 参数 %q 声明 fixed 时必须有 default 且 presentation.surface 为 hidden 或 readonly",
				offering.ID,
				parameter.Name,
			)
		}
		if parameter.AdjustmentPolicy != "" && parameter.AdjustmentPolicy != "advised" {
			return fmt.Errorf("Offering %q parameter %q has unknown adjustment policy", offering.ID, parameter.Name)
		}
		if parameter.AdjustmentPolicy == "advised" && (parameter.Fixed || len(bytes.TrimSpace(parameter.Default)) == 0 || strings.TrimSpace(parameter.AdjustmentReason) == "" || parameter.Presentation == nil || parameter.Presentation.Surface != schemacontract.SurfaceReadOnly || !offering.AgentParameterAllowed(parameter)) {
			return fmt.Errorf("Offering %q parameter %q has invalid advised policy", offering.ID, parameter.Name)
		}
		if parameter.AdjustmentPolicy == "" && parameter.AdjustmentReason != "" {
			return fmt.Errorf("Offering %q parameter %q has advice without policy", offering.ID, parameter.Name)
		}
		if parameter.Presentation != nil && parameter.Presentation.Surface == schemacontract.SurfaceReadOnly && !parameter.Fixed && parameter.AdjustmentPolicy != "advised" {
			return fmt.Errorf("Offering %q 参数 %q 的 readonly 必须声明 fixed 和 default", offering.ID, parameter.Name)
		}
		if err := validatePromptDelivery(offering, parameter); err != nil {
			return err
		}
		if parameter.Presentation == nil {
			continue
		}
		for _, condition := range parameter.Presentation.DisabledWhen {
			target, exists := offeringParameter(offering.Parameters, condition.Parameter)
			if !exists {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 disabled_when 引用了未知参数 %q",
					offering.ID,
					parameter.Name,
					condition.Parameter,
				)
			}
			if condition.Parameter == parameter.Name {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 disabled_when 不能引用自身",
					offering.ID,
					parameter.Name,
				)
			}
			if err := validateParameterCondition(condition, target); err != nil {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 disabled_when: %w",
					offering.ID,
					parameter.Name,
					err,
				)
			}
		}
		for _, condition := range parameter.Presentation.VisibleWhen {
			target, exists := offeringParameter(offering.Parameters, condition.Parameter)
			if !exists {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 visible_when 引用了未知参数 %q",
					offering.ID,
					parameter.Name,
					condition.Parameter,
				)
			}
			if err := validateParameterCondition(condition, target); err != nil {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 visible_when: %w",
					offering.ID,
					parameter.Name,
					err,
				)
			}
		}
		for _, notice := range parameter.Presentation.Notices {
			target, exists := offeringParameter(
				offering.Parameters,
				notice.When.Parameter,
			)
			if !exists {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 notice 引用了未知参数 %q",
					offering.ID,
					parameter.Name,
					notice.When.Parameter,
				)
			}
			if strings.TrimSpace(notice.MessageKey) == "" {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 notice 缺少 message_key",
					offering.ID,
					parameter.Name,
				)
			}
			switch notice.Tone {
			case "info", "warning", "error":
			default:
				return fmt.Errorf(
					"Offering %q 参数 %q 的 notice tone 无效: %q",
					offering.ID,
					parameter.Name,
					notice.Tone,
				)
			}
			if err := validateParameterCondition(notice.When, target); err != nil {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 notice: %w",
					offering.ID,
					parameter.Name,
					err,
				)
			}
		}
	}
	if err := validateOfferingOutputs(offering); err != nil {
		return err
	}
	if err := validateOfferingCost(offering); err != nil {
		return err
	}
	if err := validateOfferingPostSuccess(offering); err != nil {
		return err
	}
	if err := validateOfferingCopy(offering); err != nil {
		return err
	}
	return nil
}

// internalOfferingCopyFields are Schema-only field names. They carry no
// user-facing meaning, so user-visible Offering copy must not mention them.
var internalOfferingCopyFields = []string{
	"forced_when",
	"required_when",
	"visible_when",
	"forbidden_when",
	"omit_when",
	"input_rules",
	"adjustment_policy",
	"provider_options",
	"result_assets",
	"input_paths",
}

// validateOfferingCopy rejects Schema field names and machine-only parameter
// identifiers in user-visible Offering copy. A single common word such as
// "size" or "quality" can be natural English and an API-style label, so only
// snake_case identifiers derived from this Offering's Schema are checked.
func validateOfferingCopy(offering schemacontract.Offering) error {
	copies := offeringUserFacingCopy(offering)
	identifiers := machineOnlyOfferingCopyIdentifiers(
		parameterCopyIdentifiers(offering.Parameters),
	)
	for _, copy := range copies {
		for _, field := range internalOfferingCopyFields {
			if containsOfferingCopyWord(copy.text, field) {
				return fmt.Errorf(
					"Offering %q 的用户文案 %s 含内部字段 %q；请写用户能读到的含义",
					offering.ID,
					copy.path,
					field,
				)
			}
		}
		for _, identifier := range identifiers {
			if containsOfferingCopyWord(copy.text, identifier) {
				return fmt.Errorf(
					"Offering %q 的用户文案 %s 含内部参数名 %q；请使用参数标签",
					offering.ID,
					copy.path,
					identifier,
				)
			}
		}
	}
	return nil
}

type offeringCopyField struct {
	path string
	text string
}

// offeringUserFacingCopy lists the Offering strings a person reads. The
// terminology guard and the internal-identifier guard share this scope.
func offeringUserFacingCopy(offering schemacontract.Offering) []offeringCopyField {
	copies := []offeringCopyField{{"model_name", offering.ModelName}}
	for parameterIndex, parameter := range offering.Parameters {
		prefix := fmt.Sprintf("parameters[%d]", parameterIndex)
		copies = append(copies,
			offeringCopyField{prefix + ".label", parameter.Label},
			offeringCopyField{prefix + ".description", parameter.Description},
			offeringCopyField{prefix + ".adjustment_reason", parameter.AdjustmentReason},
		)
		for optionIndex, option := range parameter.Options {
			copies = append(copies, offeringCopyField{fmt.Sprintf("%s.options[%d].label", prefix, optionIndex), option.Label})
		}
	}
	for ruleIndex, rule := range offering.InputRules {
		copies = append(copies, offeringCopyField{fmt.Sprintf("input_rules[%d].message", ruleIndex), rule.Message})
	}
	if offering.Cost != nil {
		copies = append(copies, offeringCopyField{"cost.extra_label", offering.Cost.ExtraLabel})
	}
	if offering.CostPolicy != nil {
		messages := offering.CostPolicy.Messages
		copies = append(copies,
			offeringCopyField{"cost_policy.messages.consent_required", messages.ConsentRequired},
			offeringCopyField{"cost_policy.messages.over_limit", messages.OverLimit},
			offeringCopyField{"cost_policy.messages.unknown_cost", messages.UnknownCost},
		)
	}
	var options struct {
		Messages map[string]string `json:"messages"`
	}
	if len(offering.ProviderOptions) > 0 && json.Unmarshal(offering.ProviderOptions, &options) == nil {
		keys := make([]string, 0, len(options.Messages))
		for key := range options.Messages {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			copies = append(copies, offeringCopyField{
				fmt.Sprintf("provider_options.messages[%q]", key),
				options.Messages[key],
			})
		}
	}
	execution := offering.ExecutionMessages
	copies = append(copies,
		offeringCopyField{"execution_messages.inline_store_failed", execution.InlineStoreFailed},
		offeringCopyField{"execution_messages.stored_asset_invalid", execution.StoredAssetInvalid},
		offeringCopyField{"execution_messages.submission_unknown", execution.SubmissionUnknown},
		offeringCopyField{"execution_messages.refetch_source_unavailable", execution.RefetchSourceUnavailable},
		offeringCopyField{"execution_messages.refetch_result_unavailable", execution.RefetchResultUnavailable},
		offeringCopyField{"execution_messages.upstream_job_conflict", execution.UpstreamJobConflict},
		offeringCopyField{"execution_messages.retired_model", execution.RetiredModel},
		offeringCopyField{"execution_messages.reference_limit", execution.ReferenceLimit},
		offeringCopyField{"execution_messages.reference_limit_one", execution.ReferenceLimitOne},
		offeringCopyField{"execution_messages.reference_limit_exceeded", execution.ReferenceLimitExceeded},
		offeringCopyField{"execution_messages.extra_credits_consent_required", execution.ExtraCreditsConsentRequired},
		offeringCopyField{"execution_messages.extra_credits_over_limit", execution.ExtraCreditsOverLimit},
	)
	cancellation := offering.Cancellation
	copies = append(copies,
		offeringCopyField{"cancellation.local_only", cancellation.LocalOnly},
		offeringCopyField{"cancellation.requesting", cancellation.Requesting},
		offeringCopyField{"cancellation.confirmed", cancellation.Confirmed},
		offeringCopyField{"cancellation.failed", cancellation.Failed},
		offeringCopyField{"cancellation.unsupported", cancellation.Unsupported},
		offeringCopyField{"cancellation.unknown", cancellation.Unknown},
	)
	return copies
}

// parameterCopyIdentifiers returns the Schema names and wire names of the
// parameters. Callers filter them to machine-only identifiers.
func parameterCopyIdentifiers(parameters []schemacontract.Parameter) []string {
	identifiers := make([]string, 0, len(parameters)*2)
	for _, parameter := range parameters {
		identifiers = append(identifiers, parameter.Name, parameter.WireName)
	}
	return identifiers
}

// machineOnlyOfferingCopyIdentifiers keeps snake_case identifiers only.
// A single common word can be natural copy ("Choose image quality") and must
// not be treated as a leaked wire name.
func machineOnlyOfferingCopyIdentifiers(candidates []string) []string {
	identifiers := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] || !isMachineOnlyOfferingCopyIdentifier(candidate) {
			continue
		}
		seen[candidate] = true
		identifiers = append(identifiers, candidate)
	}
	return identifiers
}

func isMachineOnlyOfferingCopyIdentifier(identifier string) bool {
	if !strings.Contains(identifier, "_") {
		return false
	}
	for index := 0; index < len(identifier); index++ {
		character := identifier[index]
		if character == '_' ||
			character >= '0' && character <= '9' ||
			character >= 'a' && character <= 'z' {
			continue
		}
		return false
	}
	return true
}

func containsOfferingCopyWord(text, word string) bool {
	if word == "" {
		return false
	}
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], word)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(word)
		if (index == 0 || !isOfferingCopyWordByte(text[index-1])) &&
			(end == len(text) || !isOfferingCopyWordByte(text[end])) {
			return true
		}
		offset = end
	}
	return false
}

func isOfferingCopyWordByte(character byte) bool {
	return character == '_' ||
		character >= '0' && character <= '9' ||
		character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z'
}

// validateOfferingPostSuccess 落实 §6.1 声明校验：observe=true 要求
// interval_seconds>0、deadline_seconds>0 且 deadline≥interval；observe
// 缺省（false）时调度字段不得声明（避免半截声明歧义）。
func validateOfferingPostSuccess(offering schemacontract.Offering) error {
	policy := offering.PostSuccess
	if policy == nil {
		return nil
	}
	if !policy.Observe {
		if policy.IntervalSeconds != 0 || policy.DeadlineSeconds != 0 {
			return fmt.Errorf(
				"Offering %q 的 post_success 未声明 observe 却携带 interval/deadline",
				offering.ID,
			)
		}
		return nil
	}
	if policy.IntervalSeconds <= 0 {
		return fmt.Errorf(
			"Offering %q 的 post_success.interval_seconds 必须大于 0",
			offering.ID,
		)
	}
	if policy.DeadlineSeconds <= 0 {
		return fmt.Errorf(
			"Offering %q 的 post_success.deadline_seconds 必须大于 0",
			offering.ID,
		)
	}
	if policy.DeadlineSeconds < policy.IntervalSeconds {
		return fmt.Errorf(
			"Offering %q 的 post_success.deadline_seconds 不得小于 interval_seconds",
			offering.ID,
		)
	}
	return nil
}

// validateOfferingCost 落实 §5.2 成本声明的加载期校验：有限原语、
// 条件引用既有参数、consent/format 参数形态、cost_policy 必须搭配
// cost 且上限非负。声明缺失即由规划器 fail-closed，不在此伪造默认。
// W6 加固（W5 咨询项）：可能产生额外积分的形态（extra_when 或
// selection_parameter）必须同时声明 consent_parameter 与 cost_policy，
// 否则加载期拒绝（无同意/无上限的付费形态即无效声明）；
// extra_when 与 selection_parameter 互斥（避免双重计费语义）。
func validateOfferingCost(offering schemacontract.Offering) error {
	if offering.NativePricing != nil {
		return validateNativePricing(offering)
	}
	if offering.Cost == nil {
		if offering.CostPolicy != nil {
			return fmt.Errorf(
				"Offering %q 声明 cost_policy 却缺少 cost 声明",
				offering.ID,
			)
		}
		return nil
	}
	cost := offering.Cost
	if cost.BaseCredits < 0 {
		return fmt.Errorf(
			"Offering %q 的 cost.base_credits 不能为负数",
			offering.ID,
		)
	}
	if len(cost.ExtraWhen) > 0 && cost.ExtraCredits <= 0 {
		return fmt.Errorf(
			"Offering %q 声明 extra_when 时 cost.extra_credits 必须大于 0",
			offering.ID,
		)
	}
	if cost.SelectionParameter != "" {
		if cost.ExtraCredits <= 0 {
			return fmt.Errorf(
				"Offering %q 声明 selection_parameter 时 cost.extra_credits 必须大于 0",
				offering.ID,
			)
		}
		if len(cost.ExtraWhen) > 0 {
			return fmt.Errorf(
				"Offering %q 的 cost.extra_when 与 selection_parameter 不能同时声明",
				offering.ID,
			)
		}
		target, exists := offeringParameter(offering.Parameters, cost.SelectionParameter)
		if !exists {
			return fmt.Errorf(
				"Offering %q 的 cost.selection_parameter 引用了未知参数 %q",
				offering.ID,
				cost.SelectionParameter,
			)
		}
		if target.Type != "string-array" || len(target.Options) == 0 {
			return fmt.Errorf(
				"Offering %q 的 cost.selection_parameter %q 必须是带 options 的 string-array 参数",
				offering.ID,
				cost.SelectionParameter,
			)
		}
	}
	for index, condition := range cost.ExtraWhen {
		target, exists := offeringParameter(offering.Parameters, condition.Parameter)
		if !exists {
			return fmt.Errorf(
				"Offering %q 的 cost.extra_when[%d] 引用了未知参数 %q",
				offering.ID,
				index,
				condition.Parameter,
			)
		}
		if err := validateParameterCondition(condition, target); err != nil {
			return fmt.Errorf(
				"Offering %q 的 cost.extra_when[%d]: %w",
				offering.ID,
				index,
				err,
			)
		}
	}
	if cost.ConsentParameter != "" {
		target, exists := offeringParameter(offering.Parameters, cost.ConsentParameter)
		if !exists {
			return fmt.Errorf(
				"Offering %q 的 cost.consent_parameter 引用了未知参数 %q",
				offering.ID,
				cost.ConsentParameter,
			)
		}
		if target.Type != "boolean" {
			return fmt.Errorf(
				"Offering %q 的 cost.consent_parameter %q 必须是 boolean 参数",
				offering.ID,
				cost.ConsentParameter,
			)
		}
	}
	if cost.FormatParameter != "" {
		target, exists := offeringParameter(offering.Parameters, cost.FormatParameter)
		if !exists {
			return fmt.Errorf(
				"Offering %q 的 cost.format_parameter 引用了未知参数 %q",
				offering.ID,
				cost.FormatParameter,
			)
		}
		if target.Type != "select" || len(target.Options) == 0 {
			return fmt.Errorf(
				"Offering %q 的 cost.format_parameter %q 必须是带 options 的 select 参数",
				offering.ID,
				cost.FormatParameter,
			)
		}
	}
	extraPossible := len(cost.ExtraWhen) > 0 || cost.SelectionParameter != ""
	if extraPossible {
		// W6 加固：可产生额外积分的声明必须同时具备同意参数与上限。
		if cost.ConsentParameter == "" {
			return fmt.Errorf(
				"Offering %q 的 cost 声明可产生额外积分却缺少 consent_parameter",
				offering.ID,
			)
		}
		if offering.CostPolicy == nil {
			return fmt.Errorf(
				"Offering %q 的 cost 声明可产生额外积分却缺少 cost_policy",
				offering.ID,
			)
		}
		// The approval card words the extra-credit line with this name.
		if strings.TrimSpace(cost.ExtraLabel) == "" {
			return fmt.Errorf(
				"Offering %q 的 cost 声明可产生额外积分却缺少 extra_label",
				offering.ID,
			)
		}
	}
	if offering.CostPolicy != nil {
		if offering.CostPolicy.MaxAuthorizedExtraCredits < 0 {
			return fmt.Errorf(
				"Offering %q 的 cost_policy.max_authorized_extra_credits 不能为负数",
				offering.ID,
			)
		}
	}
	return nil
}

// validateOutputContractIdentifier 校验资源契约标识符：禁止空白、
// 空段（首点/相邻双点）；尾点对前缀声明合法（如 "basic_animations."）。
func validateOutputContractIdentifier(field, value string) bool {
	if value == "" {
		return true
	}
	if strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) {
			return false
		}
	}
	if strings.HasPrefix(value, ".") || strings.Contains(value, "..") {
		return false
	}
	return true
}

// validateOutputVariantDerivation 校验逐项展开声明（回顾性整改 P2）：
// variant_from 只允许出现在带 variant_prefix 的 on-demand 声明上，必须指向
// 带 options 的 string-array 参数；Offering 同时声明 cost.selection_parameter
// 时两者必须一致（避免选中项身份出现第二真源）；variant_case 限定枚举。
func validateOutputVariantDerivation(
	offering schemacontract.Offering,
	output schemacontract.AssetRequirement,
	at string,
) error {
	switch output.VariantCase {
	case schemacontract.VariantCaseVerbatim,
		schemacontract.VariantCaseLower,
		schemacontract.VariantCaseUpper:
	default:
		return fmt.Errorf("%s的 variant_case %q 无效", at, output.VariantCase)
	}
	if output.VariantFrom == "" {
		return fmt.Errorf("%s声明了 variant_case 却缺少 variant_from", at)
	}
	if output.Delivery != schemacontract.AssetDeliveryOnDemand {
		return fmt.Errorf(
			"%s的 variant_from 只允许用于 delivery %q 的声明",
			at,
			schemacontract.AssetDeliveryOnDemand,
		)
	}
	if output.VariantPrefix == "" {
		return fmt.Errorf("%s的 variant_from 要求同时声明 variant_prefix", at)
	}
	target, exists := offeringParameter(offering.Parameters, output.VariantFrom)
	if !exists {
		return fmt.Errorf(
			"%s的 variant_from 引用了未知参数 %q",
			at,
			output.VariantFrom,
		)
	}
	if target.Type != "string-array" || len(target.Options) == 0 {
		return fmt.Errorf(
			"%s的 variant_from %q 必须是带 options 的 string-array 参数",
			at,
			output.VariantFrom,
		)
	}
	if offering.Cost != nil &&
		offering.Cost.SelectionParameter != "" &&
		offering.Cost.SelectionParameter != output.VariantFrom {
		return fmt.Errorf(
			"%s的 variant_from %q 与 cost.selection_parameter %q 不一致",
			at,
			output.VariantFrom,
			offering.Cost.SelectionParameter,
		)
	}
	return nil
}

// validateOfferingOutputs 落实资源契约（§3.2 规则 2/5/6）的加载期校验：
// delivery 枚举、标识符、显式 delivery 必须显式变体范围与 minimum、
// 同 role 歧义拒绝（旧语义共存/重复/前缀重叠/精确落入前缀）、
// group 内不得混用 required 与 background/on-demand。
func validateOfferingOutputs(offering schemacontract.Offering) error {
	type roleContract struct {
		legacy   int
		variants map[string]int
		prefixes []string
	}
	groupClass := map[string]string{}
	contracts := map[string]*roleContract{}
	for index, output := range offering.Outputs {
		at := fmt.Sprintf(
			"Offering %q 的 outputs[%d]（role %q",
			offering.ID,
			index,
			output.Role,
		)
		if output.Variant != "" || output.VariantPrefix != "" {
			at += fmt.Sprintf(
				" variant %q prefix %q",
				output.Variant,
				output.VariantPrefix,
			)
		}
		at += "）"
		if output.Role == "" {
			return fmt.Errorf("%s缺少 role", at)
		}
		if output.Role == schemacontract.GaussianSplatRole || output.Variant == schemacontract.GaussianSplatVariant || slices.Contains(output.MediaTypes, schemacontract.GaussianSplatMediaType) || output.DeliveryProfile == schemacontract.DeliveryProfileGaussianSplat32V1 {
			if output.Role != schemacontract.GaussianSplatRole || output.DeliveryProfile != schemacontract.DeliveryProfileGaussianSplat32V1 || output.Minimum == nil || *output.Minimum != 1 || output.Delivery != schemacontract.AssetDeliveryRequired || output.MaxBytes > schemacontract.GaussianSplatMaxBytes {
				return fmt.Errorf("%s必须声明完整且有界的 Gaussian Splat 必需产物身份", at)
			}
		}
		if output.DeliveryProfile != "" {
			variant, media, known := schemacontract.DeliveryProfileFormat(output.DeliveryProfile)
			if !known || output.Variant != variant || output.VariantPrefix != "" || output.VariantFrom != "" || len(output.MediaTypes) != 1 || output.MediaTypes[0] != media || output.MaxBytes <= 0 {
				return fmt.Errorf("%s的 delivery_profile 必须绑定支持的精确格式、MIME 与正大小上限", at)
			}
		}
		if output.Fidelity != "" {
			if output.Fidelity != "skeleton" && output.Fidelity != "animation" {
				return fmt.Errorf("%s的 fidelity %q 无效", at, output.Fidelity)
			}
			// A closed media-type set prevents a mixed-format prefix from making
			// unchecked assets satisfy the fidelity declaration. FBX requires the
			// exact binary profile; the inspector does not accept ASCII FBX.
			if len(output.MediaTypes) == 0 {
				return fmt.Errorf("%s的 fidelity 必须限定有检查器的 media_types", at)
			}
			for _, mediaType := range output.MediaTypes {
				binaryFBX := mediaType == "model/fbx" && len(output.MediaTypes) == 1 && output.DeliveryProfile == "binary-fbx-v1"
				if mediaType != "model/gltf-binary" && mediaType != "model/gltf+json" && !binaryFBX {
					return fmt.Errorf("%s的 fidelity 不支持媒体类型 %q", at, mediaType)
				}
			}
		}
		switch output.Delivery {
		case "", schemacontract.AssetDeliveryRequired,
			schemacontract.AssetDeliveryBackground, schemacontract.AssetDeliveryOnDemand:
		default:
			return fmt.Errorf(
				"%s的 delivery %q 无效",
				at,
				output.Delivery,
			)
		}
		for field, value := range map[string]string{
			"variant":        output.Variant,
			"variant_prefix": output.VariantPrefix,
			"group":          output.Group,
		} {
			if !validateOutputContractIdentifier(field, value) {
				return fmt.Errorf("%s的 %s %q 不是合法标识符", at, field, value)
			}
		}
		if output.Variant != "" && output.VariantPrefix != "" {
			return fmt.Errorf(
				"%s的 variant 与 variant_prefix 不能同时声明",
				at,
			)
		}
		if output.VariantFrom != "" || output.VariantCase != "" {
			if err := validateOutputVariantDerivation(offering, output, at); err != nil {
				return err
			}
		}
		if output.RequiredWhen != nil {
			condition := *output.RequiredWhen
			parameter, exists := offeringParameter(offering.Parameters, condition.Parameter)
			if output.Delivery != schemacontract.AssetDeliveryRequired || output.Variant == "" || output.VariantPrefix != "" || output.VariantFrom != "" || output.Minimum == nil || *output.Minimum != 1 || !exists || !validRequiredWhenCondition(parameter, condition) {
				return fmt.Errorf("%s的 required_when 必须引用 text 参数（present）或 select 参数的声明选项（equals），并限定精确 required 槽位", at)
			}
		}
		// minimum 语义（§3.2 规则 5）：旧语义缺省等效 1，显式值保持
		// 既有严格性；显式 delivery 必须显式且满足各自下界。
		if output.Delivery == "" {
			if output.Minimum != nil && *output.Minimum < 1 {
				return fmt.Errorf("%s的 minimum 不能小于 1", at)
			}
		} else {
			if output.Variant == "" && output.VariantPrefix == "" {
				return fmt.Errorf(
					"%s声明了显式 delivery %q 却缺少 variant/variant_prefix 变体范围",
					at,
					output.Delivery,
				)
			}
			if output.Minimum == nil {
				return fmt.Errorf(
					"%s声明了显式 delivery %q 却缺少显式 minimum",
					at,
					output.Delivery,
				)
			}
			if *output.Minimum < 0 {
				return fmt.Errorf(
					"%s的 delivery %q 要求 minimum ≥ 0",
					at,
					output.Delivery,
				)
			}
		}
		contract := contracts[output.Role]
		if contract == nil {
			contract = &roleContract{variants: map[string]int{}}
			contracts[output.Role] = contract
		}
		switch {
		case output.Variant != "":
			if contract.legacy > 0 {
				return fmt.Errorf(
					"%s与同 role 旧语义声明共存，形成歧义",
					at,
				)
			}
			if _, duplicate := contract.variants[output.Variant]; duplicate {
				return fmt.Errorf("%s重复声明 variant", at)
			}
			for _, prefix := range contract.prefixes {
				if strings.HasPrefix(output.Variant, prefix) {
					return fmt.Errorf(
						"%s落入前缀 %q 的匹配范围，声明重叠",
						at,
						prefix,
					)
				}
			}
			contract.variants[output.Variant] = index
		case output.VariantPrefix != "":
			if contract.legacy > 0 {
				return fmt.Errorf(
					"%s与同 role 旧语义声明共存，形成歧义",
					at,
				)
			}
			for _, prefix := range contract.prefixes {
				if strings.HasPrefix(output.VariantPrefix, prefix) ||
					strings.HasPrefix(prefix, output.VariantPrefix) {
					return fmt.Errorf(
						"%s与前缀 %q 重叠",
						at,
						prefix,
					)
				}
			}
			for variant := range contract.variants {
				if strings.HasPrefix(variant, output.VariantPrefix) {
					return fmt.Errorf(
						"%s覆盖精确变体 %q，声明重叠",
						at,
						variant,
					)
				}
			}
			contract.prefixes = append(contract.prefixes, output.VariantPrefix)
		default:
			if contract.legacy > 0 || len(contract.variants) > 0 ||
				len(contract.prefixes) > 0 {
				return fmt.Errorf(
					"%s的旧语义声明不得与同 role 其他声明共存",
					at,
				)
			}
			contract.legacy++
		}
		if output.Group == "" {
			continue
		}
		class := "required"
		switch output.Delivery {
		case schemacontract.AssetDeliveryBackground, schemacontract.AssetDeliveryOnDemand:
			class = "optional"
		}
		if existing, mixed := groupClass[output.Group]; mixed && existing != class {
			return fmt.Errorf(
				"%s的 group %q 混用了 required 与 background/on-demand 交付",
				at,
				output.Group,
			)
		}
		groupClass[output.Group] = class
	}
	return nil
}

func validateOfferingContent(
	offering schemacontract.Offering,
	content ContentSchema,
) error {
	for _, parameter := range offering.Parameters {
		if parameter.Presentation == nil {
			continue
		}
		for _, notice := range parameter.Presentation.Notices {
			if strings.TrimSpace(content.Messages[notice.MessageKey]) == "" {
				return fmt.Errorf(
					"Offering %q 参数 %q 的 notice 引用了不存在的 message %q",
					offering.ID,
					parameter.Name,
					notice.MessageKey,
				)
			}
		}
	}
	return nil
}

func offeringParameter(
	parameters []schemacontract.Parameter,
	name string,
) (schemacontract.Parameter, bool) {
	for _, parameter := range parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return schemacontract.Parameter{}, false
}

// validRequiredWhenCondition admits the two condition shapes whose truth is
// fully decided by the frozen input: a text parameter being present, or a
// single-value select equal to one of its declared string options (an
// output format that decides which exact slot the upstream returns).
func validRequiredWhenCondition(parameter schemacontract.Parameter, condition schemacontract.ParameterCondition) bool {
	switch {
	case parameter.Type == "text":
		return condition.Operator == "present"
	case parameter.Type == "select" && condition.Operator == "equals":
		var value string
		if err := json.Unmarshal(condition.Value, &value); err != nil || value == "" {
			return false
		}
		for _, option := range parameter.Options {
			if option.Value == value {
				return true
			}
		}
	}
	return false
}

func validateParameterCondition(
	condition schemacontract.ParameterCondition,
	target schemacontract.Parameter,
) error {
	switch condition.Operator {
	case "equals", "not-equals":
		if len(bytes.TrimSpace(condition.Value)) == 0 {
			return fmt.Errorf("operator %q 必须声明 value", condition.Operator)
		}
		decoder := json.NewDecoder(bytes.NewReader(condition.Value))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("value 不是有效 JSON: %w", err)
		}
		if _, err := validateValue(target, value); err != nil {
			return fmt.Errorf("value 不符合参数 %q: %w", target.Name, err)
		}
	case "truthy", "falsy", "present", "absent":
		if len(bytes.TrimSpace(condition.Value)) != 0 {
			return fmt.Errorf("operator %q 不应声明 value", condition.Operator)
		}
	default:
		return fmt.Errorf("未知 operator %q", condition.Operator)
	}
	return nil
}

func validateValueDefinition(schema schemacontract.Parameter) (schemacontract.Parameter, error) {
	if err := validateTaskSourceParameter(schema); err != nil {
		return schema, err
	}
	switch schema.Type {
	case "text", "select", "boolean", "number", "string-array", "asset", "asset-array", "task":
	default:
		return schema, fmt.Errorf("参数 %q 使用未知类型 %q", schema.Name, schema.Type)
	}
	if schema.ImageSize != nil {
		c := schema.ImageSize
		if c.MinPixels <= 0 || c.MaxPixels < c.MinPixels || c.MaxRatio < 1 || c.Multiple < 0 || c.MaxEdge < 0 || (schema.Type != "text" && schema.Type != "select") {
			return schema, fmt.Errorf("参数 %q 图片尺寸约束无效", schema.Name)
		}
	}
	if schema.ImageInput != nil {
		c := schema.ImageInput
		// A mask's alpha is what it means: it is never flattened.
		if schema.Semantic == "mask" {
			return schema, fmt.Errorf("参数 %q 是蒙版，不能声明 alpha_flatten", schema.Name)
		}
		if (schema.Type != "asset" && schema.Type != "asset-array") || schema.MaxBytes <= 0 || len(schema.MediaTypes) == 0 || c.MinWidth <= 0 || c.MinHeight <= 0 || c.MaxPixels <= 0 || c.MaxPixels/c.MinWidth < c.MinHeight ||
			c.MaxAspectRatio < 0 || (c.MaxAspectRatio > 0 && c.MaxAspectRatio < 1) {
			return schema, fmt.Errorf("参数 %q 来源图片尺寸约束无效", schema.Name)
		}
		// Meshy's adapter narrows this to PNG/JPEG/WebP for its own Offerings.
		for _, mediaType := range schema.MediaTypes {
			if !strings.HasPrefix(mediaType, "image/") {
				return schema, fmt.Errorf("参数 %q 来源图片类型不受支持", schema.Name)
			}
		}
	}
	if schema.AlphaFlatten != nil {
		policy := schema.AlphaFlatten
		// A mask's alpha is what it means: it is never flattened.
		if schema.Semantic == "mask" {
			return schema, fmt.Errorf("参数 %q 是蒙版，不能声明 alpha_flatten", schema.Name)
		}
		if (schema.Type != "asset" && schema.Type != "asset-array") || schema.MaxBytes <= 0 || len(schema.MediaTypes) == 0 || !isHexColor(policy.BackgroundColor) || policy.MaxPixels <= 0 {
			return schema, fmt.Errorf("参数 %q 的 alpha_flatten 无效", schema.Name)
		}
		for _, mediaType := range schema.MediaTypes {
			if !strings.HasPrefix(mediaType, "image/") {
				return schema, fmt.Errorf("参数 %q 的 alpha_flatten 只适用于图片", schema.Name)
			}
		}
	}
	if schema.Type == "select" && len(schema.Options) == 0 {
		return schema, fmt.Errorf("select 参数 %q 缺少 options", schema.Name)
	}
	// 带 options 的 number 是封闭枚举：每个选项必须是取值范围内、互不相同的有限数字。
	if schema.Type == "number" && len(schema.Options) > 0 {
		seen := make(map[float64]bool, len(schema.Options))
		for _, option := range schema.Options {
			value, err := strconv.ParseFloat(option.Value, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || seen[value] ||
				(schema.Integer && math.Trunc(value) != value) ||
				(schema.Minimum != nil && value < *schema.Minimum) ||
				(schema.Maximum != nil && value > *schema.Maximum) {
				return schema, fmt.Errorf("number 参数 %q 的选项 %q 无效或重复", schema.Name, option.Value)
			}
			seen[value] = true
		}
	}
	// 带 options 的 boolean 是“二选一”呈现：恰为 false/true 两项，前端据此渲染为两个标签。
	if schema.Type == "boolean" && len(schema.Options) > 0 {
		if len(schema.Options) != 2 ||
			schema.Options[0].Value == schema.Options[1].Value ||
			!isBooleanOptionValue(schema.Options[0].Value) ||
			!isBooleanOptionValue(schema.Options[1].Value) {
			return schema, fmt.Errorf("boolean 参数 %q 的 options 必须恰为 \"false\" 与 \"true\" 两项", schema.Name)
		}
		for _, option := range schema.Options {
			if strings.TrimSpace(option.Label) == "" {
				return schema, fmt.Errorf("boolean 参数 %q 的选项 %q 缺少 label", schema.Name, option.Value)
			}
		}
	}
	if schema.Minimum != nil && schema.Maximum != nil && *schema.Minimum > *schema.Maximum {
		return schema, fmt.Errorf("参数 %q 的 minimum 大于 maximum", schema.Name)
	}
	if schema.MinItems < 0 || schema.MaxItems < 0 ||
		(schema.MaxItems > 0 && schema.MinItems > schema.MaxItems) {
		return schema, fmt.Errorf("参数 %q 的数组数量约束无效", schema.Name)
	}
	if schema.MaxBytes < 0 {
		return schema, fmt.Errorf("参数 %q 的 max_bytes 不能为负数", schema.Name)
	}
	// A total encoded bound covers several images together; a single image
	// over it could never be sent, so max_bytes must fit inside it.
	if schema.MaxTotalEncodedBytes < 0 ||
		(schema.MaxTotalEncodedBytes > 0 && (schema.Type != "asset-array" ||
			schema.MaxBytes <= 0 || schemacontract.Base64EncodedBytes(schema.MaxBytes) > schema.MaxTotalEncodedBytes)) {
		return schema, fmt.Errorf("参数 %q 的 max_total_encoded_bytes 无效", schema.Name)
	}
	if schema.Semantic != "" && !schema.Semantic.Valid() {
		return schema, fmt.Errorf(
			"参数 %q 使用未知 semantic %q",
			schema.Name,
			schema.Semantic,
		)
	}
	for _, extension := range schema.Extensions {
		if strings.TrimSpace(extension) == "" {
			return schema, fmt.Errorf("参数 %q 存在空 extensions 值", schema.Name)
		}
	}
	if len(schema.Extensions) > 0 &&
		schema.Type != "asset" &&
		schema.Type != "asset-array" &&
		!(schema.Type == "task" && schema.TaskSourceKind == schemacontract.TaskSourceAssets) {
		return schema, fmt.Errorf(
			"非 asset 参数 %q 不能声明 extensions",
			schema.Name,
		)
	}
	for _, capability := range schema.TaskCapabilities {
		if !capability.Valid() {
			return schema, fmt.Errorf(
				"参数 %q 使用未知 task capability %q",
				schema.Name,
				capability,
			)
		}
	}
	if schema.Type != "task" && len(schema.TaskCapabilities) > 0 {
		return schema, fmt.Errorf(
			"非 task 参数 %q 不能声明 task_capabilities",
			schema.Name,
		)
	}
	if schema.Type != "task" && schema.SameProvider {
		return schema, fmt.Errorf(
			"非 task 参数 %q 不能声明 same_provider",
			schema.Name,
		)
	}
	if schema.Presentation != nil {
		if strings.TrimSpace(schema.Label) == "" {
			return schema, fmt.Errorf("可显示参数 %q 缺少 label", schema.Name)
		}
		if !schema.Presentation.Surface.Valid() {
			return schema, fmt.Errorf(
				"参数 %q 使用未知 presentation.surface %q",
				schema.Name,
				schema.Presentation.Surface,
			)
		}
		if !schema.Presentation.Control.Valid() {
			return schema, fmt.Errorf(
				"参数 %q 使用未知 presentation.control %q",
				schema.Name,
				schema.Presentation.Control,
			)
		}
		if schema.Presentation.Control == schemacontract.ControlSlider &&
			(schema.Type != "number" ||
				schema.Minimum == nil ||
				schema.Maximum == nil) {
			return schema, fmt.Errorf(
				"参数 %q 的 slider 控件要求 number 类型且显式 minimum/maximum",
				schema.Name,
			)
		}
		// multi-select 是受限多选控件（W6 生成期格式多选）：要求
		// string-array 类型且声明非空 options（提交期逐项校验取值）。
		if schema.Presentation.Control == schemacontract.ControlMultiSelect &&
			(schema.Type != "string-array" || len(schema.Options) == 0) {
			return schema, fmt.Errorf(
				"参数 %q 的 multi-select 控件要求 string-array 类型且声明 options",
				schema.Name,
			)
		}
		if schema.Presentation.Step != nil && *schema.Presentation.Step <= 0 {
			return schema, fmt.Errorf("参数 %q 的 presentation.step 必须大于 0", schema.Name)
		}
		for _, option := range schema.Options {
			if strings.TrimSpace(option.Label) == "" {
				return schema, fmt.Errorf("可显示参数 %q 存在无 label 选项", schema.Name)
			}
		}
	}
	return schema, nil
}

func validateTaskSourceParameter(parameter schemacontract.Parameter) error {
	if parameter.SameSession && parameter.Type != "task" {
		return fmt.Errorf("same_session requires task source")
	}
	if parameter.TaskSourceBinding != "" && (parameter.TaskSourceBinding != "provider" || parameter.Type != "task" || parameter.TaskSourceKind == schemacontract.TaskSourceAssets || !parameter.Required || !parameter.SameProvider || len(parameter.TaskOfferings) != 1) {
		return fmt.Errorf("task_source_binding requires a required exact same-provider upstream task source")
	}

	if parameter.Type != "task" && (parameter.TaskSourceKind != "" || len(parameter.TaskOfferings) > 0) {
		return fmt.Errorf("非 task 参数 %q 不能声明 task_source_kind 或 task_offerings", parameter.Name)
	}
	if parameter.TaskSourceKind != "" && parameter.TaskSourceKind != schemacontract.TaskSourceUpstreamJob && parameter.TaskSourceKind != schemacontract.TaskSourceAssets {
		return fmt.Errorf("参数 %q 使用未知 task_source_kind %q", parameter.Name, parameter.TaskSourceKind)
	}
	if parameter.UpstreamJobMaxAgeSeconds < 0 || (parameter.UpstreamJobMaxAgeSeconds > 0 &&
		(parameter.Type != "task" || parameter.TaskSourceKind == schemacontract.TaskSourceAssets)) {
		return fmt.Errorf("参数 %q 的 upstream_job_max_age_seconds 只能是 upstream_job task 参数上的正秒数", parameter.Name)
	}
	seen := map[schemacontract.OfferingID]bool{}
	for _, id := range parameter.TaskOfferings {
		if strings.TrimSpace(string(id)) == "" || strings.TrimSpace(string(id)) != string(id) || seen[id] {
			return fmt.Errorf("参数 %q 的 task_offerings 含空值、空白或重复项", parameter.Name)
		}
		seen[id] = true
	}
	if parameter.TaskSourceKind != schemacontract.TaskSourceAssets {
		return nil
	}
	if parameter.MaxBytes <= 0 || len(parameter.MediaTypes) == 0 || len(parameter.Extensions) == 0 {
		return fmt.Errorf("参数 %q 的 assets 来源必须声明 media_types、extensions 和正 max_bytes", parameter.Name)
	}
	mediaSeen := map[string]bool{}
	for _, media := range parameter.MediaTypes {
		parsed, params, err := mime.ParseMediaType(media)
		if err != nil || len(params) != 0 || !strings.Contains(media, "/") || strings.Contains(media, "*") || parsed != media || mediaSeen[media] {
			return fmt.Errorf("参数 %q 的 assets 来源 media_types 必须精确且不能重复", parameter.Name)
		}
		mediaSeen[media] = true
	}
	extensionSeen := map[string]bool{}
	for _, extension := range parameter.Extensions {
		if len(extension) < 2 || extension[0] != '.' || strings.ToLower(extension) != extension || strings.ContainsAny(extension[1:], ".\\/ \t\r\n") || extensionSeen[extension] {
			return fmt.Errorf("参数 %q 的 assets 来源 extensions 必须为不重复的小写扩展名", parameter.Name)
		}
		extensionSeen[extension] = true
	}
	return nil
}

func validateTaskOfferingReferences(offerings generationMap) error {
	for _, offering := range offerings {
		for _, parameter := range offering.Parameters {
			for _, id := range parameter.TaskOfferings {
				source, exists := offerings[id]
				if !exists {
					return fmt.Errorf("Offering %q 参数 %q 的 task_offerings 引用未知 Offering %q", offering.ID, parameter.Name, id)
				}
				if parameter.SameProvider && source.ProviderID != offering.ProviderID {
					return fmt.Errorf("Offering %q 参数 %q 的 task_offerings 与 same_provider 冲突", offering.ID, parameter.Name)
				}
				if len(parameter.TaskCapabilities) > 0 {
					allowed := false
					for _, capability := range parameter.TaskCapabilities {
						allowed = allowed || capability == source.Capability
					}
					if !allowed {
						return fmt.Errorf("Offering %q 参数 %q 的 task_offerings 与 task_capabilities 冲突", offering.ID, parameter.Name)
					}
				}
			}
		}
	}
	return nil
}

// validatePromptDelivery 校验 prompt_delivery 声明：模板必须含 {value}，
// 被传递参数自身不能是提示词或资产/任务引用，且 Offering 必须恰有一个文本提示词参数。
func validatePromptDelivery(offering schemacontract.Offering, parameter schemacontract.Parameter) error {
	if parameter.PromptDelivery == nil {
		return nil
	}
	if !strings.Contains(parameter.PromptDelivery.Template, "{value}") {
		return fmt.Errorf(
			"Offering %q 参数 %q 的 prompt_delivery.template 必须非空且包含 {value}",
			offering.ID,
			parameter.Name,
		)
	}
	if parameter.Semantic == schemacontract.SemanticPrompt {
		return fmt.Errorf(
			"Offering %q 参数 %q 自身是提示词参数，不能声明 prompt_delivery",
			offering.ID,
			parameter.Name,
		)
	}
	switch parameter.Type {
	case "asset", "asset-array", "task":
		return fmt.Errorf(
			"Offering %q 参数 %q 的类型 %q 不能声明 prompt_delivery",
			offering.ID,
			parameter.Name,
			parameter.Type,
		)
	}
	delivered := 0
	for _, candidate := range offering.Parameters {
		if candidate.PromptDelivery != nil {
			delivered++
		}
	}
	// 只允许一个：规范化靠“提示词以该行结尾”判断已追加，多个时无法保持幂等。
	if delivered > 1 {
		return fmt.Errorf(
			"Offering %q 最多只能有一个参数声明 prompt_delivery，当前 %d 个",
			offering.ID,
			delivered,
		)
	}
	prompts := 0
	for _, candidate := range offering.Parameters {
		if candidate.Semantic != schemacontract.SemanticPrompt {
			continue
		}
		prompts++
		if candidate.Type != "text" {
			return fmt.Errorf(
				"Offering %q 的提示词参数 %q 必须是 text 类型才能接收 prompt_delivery",
				offering.ID,
				candidate.Name,
			)
		}
	}
	if prompts != 1 {
		return fmt.Errorf(
			"Offering %q 参数 %q 声明了 prompt_delivery，要求 Offering 恰有一个提示词参数，当前 %d 个",
			offering.ID,
			parameter.Name,
			prompts,
		)
	}
	return nil
}

func isBooleanOptionValue(value string) bool {
	return value == "true" || value == "false"
}

// A preservation declaration is only meaningful for a bounded list of model reference images.
func validateReferenceSelectionPolicy(offering schemacontract.Offering) error {
	switch offering.ReferenceSelectionPolicy {
	case "":
		return nil
	case "preserve":
		if offering.Capability != schemacontract.ImageTo3D {
			return fmt.Errorf("Offering %q reference_selection_policy preserve requires image-to-3d", offering.ID)
		}
		count := 0
		for _, parameter := range offering.Parameters {
			if parameter.Semantic != schemacontract.SemanticReferenceImage {
				continue
			}
			count++
			if parameter.Type != "asset-array" || parameter.MinItems < 1 || parameter.MaxItems < parameter.MinItems {
				return fmt.Errorf("Offering %q preserve requires bounded reference images", offering.ID)
			}
		}
		if count != 1 {
			return fmt.Errorf("Offering %q preserve requires one reference-image parameter", offering.ID)
		}
		return nil
	default:
		return fmt.Errorf("Offering %q has unknown reference_selection_policy %q", offering.ID, offering.ReferenceSelectionPolicy)
	}
}
