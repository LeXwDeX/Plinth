// Package schema 承载 catalog 与 generation 共享的契约词表与叶子类型
// （B5 解耦：catalog 不得依赖 generation，业务层单向依赖本包）。
package schema

import "encoding/json"

type Capability string

const (
	TextToImage      Capability = "text-to-image"
	ImageToImage     Capability = "image-to-image"
	TextTo3D         Capability = "text-to-3d"
	ImageTo3D        Capability = "image-to-3d"
	ImageToMultiview Capability = "image-to-multiview"
	EditMultiview    Capability = "edit-multiview"
	Remesh           Capability = "remesh"
	Retexture        Capability = "retexture"
	Rigging          Capability = "rigging"
	RigCheck         Capability = "rig-check"
	Animation        Capability = "animation"
	BackgroundRemove Capability = "background-removal"
	FormatConversion Capability = "format-conversion"
	AssetRecovery    Capability = "asset-recovery"
)

func (c Capability) Valid() bool {
	return validSchemaIdentifier(string(c))
}

func validSchemaIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 64 ||
		value[0] < 'a' || value[0] > 'z' ||
		value[len(value)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, character := range []byte(value[1:]) {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return true
}

type CapabilityDomain string

const (
	DomainImage      CapabilityDomain = "image"
	DomainModel3D    CapabilityDomain = "model-3d"
	DomainProcessing CapabilityDomain = "processing"
)

func (domain CapabilityDomain) Valid() bool {
	return validSchemaIdentifier(string(domain))
}

type FailureClass string

// FailureInsufficientBalance means the upstream refused because the account
// behind the key has no balance left (arrears, exhausted credits). It is a
// definite refusal that we never retry, and the user hears it as "the provider
// account is out of balance" rather than as a generic rejection.
const (
	FailureInvalidInput        FailureClass = "invalid_input"
	FailureAuthentication      FailureClass = "authentication"
	FailureRateLimited         FailureClass = "rate_limited"
	FailureTransport           FailureClass = "transport"
	FailureTimedOut            FailureClass = "timed_out"
	FailureRejected            FailureClass = "rejected"
	FailureInsufficientBalance FailureClass = "insufficient_balance"
	// FailureCredentialQuota: the account has balance, but the API key itself
	// reached the credit limit its owner set in the provider console (#93).
	// Another key or a larger key limit fixes it; topping up the account does
	// not. A definite refusal that is never retried.
	FailureCredentialQuota     FailureClass = "credential_quota"
	FailureUpstreamUnavailable FailureClass = "upstream_unavailable"
	FailureSubmissionUnknown   FailureClass = "submission_unknown"
	FailureIntegrity           FailureClass = "integrity_failure"
	FailureProtocol            FailureClass = "protocol"
	FailureInternal            FailureClass = "internal"
	// FailureSourceExpired: the Provider no longer keeps the job a task
	// dependency refers to (#325: an expired Meshy task answers 404). The same
	// input fails again, so it is never retried; a new request can use the
	// saved model file instead.
	FailureSourceExpired FailureClass = "source_expired"
)

// Valid 报告声明的失败分类词是否属于封闭枚举。
func (class FailureClass) Valid() bool {
	switch class {
	case FailureInvalidInput,
		FailureAuthentication,
		FailureRateLimited,
		FailureTransport,
		FailureTimedOut,
		FailureRejected,
		FailureInsufficientBalance,
		FailureCredentialQuota,
		FailureUpstreamUnavailable,
		FailureSubmissionUnknown,
		FailureIntegrity,
		FailureProtocol,
		FailureInternal,
		FailureSourceExpired:
		return true
	default:
		return false
	}
}

type ParameterCondition struct {
	Parameter string          `json:"parameter"`
	Operator  string          `json:"operator"`
	Value     json.RawMessage `json:"value,omitempty"`
}

type ParameterNotice struct {
	When       ParameterCondition `json:"when"`
	MessageKey string             `json:"message_key"`
	Tone       string             `json:"tone"`
}

type ParameterSemantic string

const (
	SemanticPrompt         ParameterSemantic = "prompt"
	SemanticMask           ParameterSemantic = "mask"
	SemanticNegativePrompt ParameterSemantic = "negative-prompt"
	SemanticReferenceImage ParameterSemantic = "reference-image"
	SemanticPolycount      ParameterSemantic = "polycount"
	SemanticOutputSize     ParameterSemantic = "output-size"
	SemanticQuality        ParameterSemantic = "quality"

	// 来源/动作族标记处理链参数的稳定角色：跨 Provider 的参数名可由
	// Schema 自由声明，消费方按语义定位（同名回退见 SourceParameterRole）。
	SemanticSourceTask  ParameterSemantic = "source-task"
	SemanticSourceAsset ParameterSemantic = "source-asset"
	SemanticSourceURL   ParameterSemantic = "source-url"
	SemanticAction      ParameterSemantic = "action"
)

func (semantic ParameterSemantic) Valid() bool {
	switch semantic {
	case SemanticPrompt,
		SemanticMask,
		SemanticNegativePrompt,
		SemanticReferenceImage,
		SemanticPolycount,
		SemanticOutputSize,
		SemanticQuality,
		SemanticSourceTask,
		SemanticSourceAsset,
		SemanticSourceURL,
		SemanticAction:
		return true
	default:
		return false
	}
}

// ParameterSemantic 的三个闭包各自归属明确、不得互相推定：
//   - 词表闭包：Valid()，Schema 可声明的全部语义；
//   - 资源别名闭包：catalog 的 asset_aliases 校验
//     （knownParameterSemantic），只收服务端按语义定位资源的别名；
//   - skill 覆盖闭包：SkillSettable，skill 只能覆盖"用户可填"的生成
//     参数。来源与动作族不在此闭包：来源由处理规划器按计划注入，动作由
//     用户在处理面板显式选择（Schema 是 UI 唯一配置来源，AGENTS 工作区
//     交互契约），skill 无权代填。
//
// SkillSettable 是**显式白名单**而非"词表减来源动作族"：新增语义必须在此
// 表态后才能被 skill 覆盖，否则一个系统注入型语义会静默落入闭包并覆盖
// 规划器/用户已定的值（executor.buildInput 的 override 应用循环）。
func (semantic ParameterSemantic) SkillSettable() bool {
	switch semantic {
	case SemanticPrompt,
		SemanticMask,
		SemanticNegativePrompt,
		SemanticReferenceImage,
		SemanticPolycount,
		SemanticOutputSize,
		SemanticQuality:
		return true
	default:
		return false
	}
}

// SourceParameterRole 判定参数是否承担处理链来源角色，返回其角色语义：
// 已声明 semantic 时以其为准（非来源语义即非来源参数）；未声明时按遗留
// 名+类型回退（与 B7 mask 判定同款方法论）。回退分支只在 Schema 未标注
// semantic 的存量 Offering 上生效。
func SourceParameterRole(parameter Parameter) (ParameterSemantic, bool) {
	if parameter.Semantic != "" {
		switch parameter.Semantic {
		case SemanticSourceTask, SemanticSourceAsset, SemanticSourceURL:
			return parameter.Semantic, true
		}
		return "", false
	}
	switch {
	case parameter.Name == "source_task" && parameter.Type == "task":
		return SemanticSourceTask, true
	case parameter.Name == "source_asset" && parameter.Type == "asset":
		return SemanticSourceAsset, true
	case parameter.Name == "source_url" && parameter.Type == "text":
		return SemanticSourceURL, true
	case parameter.Name == "source_assets":
		return SemanticSourceAsset, true
	}
	return "", false
}

// StepInputParameter reports whether parameter is an ordinary input the person
// or the Agent supplies for a step: not fixed by the Schema, not one of the
// step's source parameters and not hidden from the person. It is the one
// filter every projection of "what this step takes" starts from (A2, #121).
func StepInputParameter(parameter Parameter) bool {
	if _, source := SourceParameterRole(parameter); source || parameter.Fixed {
		return false
	}
	return parameter.Presentation == nil || parameter.Presentation.Surface != SurfaceHidden
}

// ConversationReferenceImage reports whether parameter takes its value from the
// conversation: a reference image the person attaches to a message or names by
// its handle (#187 D, #92). Such a picture belongs to one request, so the Agent
// passes the handle and the executor resolves it to an owned input asset; the
// saved settings never fix or fill it. A caller that also keeps the step's
// extra-credit consent parameter out excludes Offering.ExtraConsentParameter
// itself.
func ConversationReferenceImage(parameter Parameter) bool {
	return StepInputParameter(parameter) && parameter.Semantic == SemanticReferenceImage &&
		(parameter.Type == "asset" || parameter.Type == "asset-array")
}
