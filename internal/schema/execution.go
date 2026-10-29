package schema

// ErrorClassRule 声明一条上游错误词汇到失败分类的映射结果。
type ErrorClassRule struct {
	Class     string `json:"class"`
	Retryable bool   `json:"retryable,omitempty"`
}

// HTTPStatusClass 声明一段 HTTP 状态码区间的通用分类。
type HTTPStatusClass struct {
	Min       int    `json:"min"`
	Max       int    `json:"max"`
	Class     string `json:"class"`
	Retryable bool   `json:"retryable,omitempty"`
}

// ErrorMessageMatcher 按上游错误消息文本匹配分类。
type ErrorMessageMatcher struct {
	Equals      string   `json:"equals,omitempty"`
	ContainsAll []string `json:"contains_all,omitempty"`
	ContainsAny []string `json:"contains_any,omitempty"`
	Class       string   `json:"class"`
	Retryable   bool     `json:"retryable,omitempty"`
	// BeforeStatusRanges makes a hit outrank http_status_ranges, for a refusal
	// whose message is more specific than its status (a Meshy 402 that names
	// the key's credit limit, not the account balance, #93).
	BeforeStatusRanges bool `json:"before_status_ranges,omitempty"`
	// HTTPStatus limits the matcher to responses whose HTTP status lies in the
	// range. Absent, the matcher applies to every status. A message that is
	// only meaningful under one status (Meshy's "credit limit" under 402)
	// declares it here instead of classifying the same words under any status.
	HTTPStatus *StatusRange `json:"http_status,omitempty"`
}

// StatusRange is an inclusive range of HTTP status codes.
type StatusRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Contains reports whether status lies in the range.
func (r StatusRange) Contains(status int) bool { return status >= r.Min && status <= r.Max }

// ErrorFieldPaths 声明错误证据在响应 JSON 中的取值路径（点分段）。
type ErrorFieldPaths struct {
	TypePath    string `json:"type_path,omitempty"`
	CodePath    string `json:"code_path,omitempty"`
	MessagePath string `json:"message_path,omitempty"`
	StatusPath  string `json:"status_path,omitempty"`
}

// ErrorMapping 是连接级错误词汇表：HTTP 状态区间、上游状态/错误码映射、
// 消息匹配器与证据字段路径。未命中任何规则时由通用安全兜底分类。
type ErrorMapping struct {
	HTTPStatusRanges  []HTTPStatusClass         `json:"http_status_ranges,omitempty"`
	UpstreamStatusMap map[string]ErrorClassRule `json:"upstream_status_map,omitempty"`
	UpstreamCodeMap   map[string]ErrorClassRule `json:"upstream_code_map,omitempty"`
	MessageMatchers   []ErrorMessageMatcher     `json:"message_matchers,omitempty"`
	ErrorFields       ErrorFieldPaths           `json:"error_fields,omitempty"`
	RetryAfterHeader  string                    `json:"retry_after_header,omitempty"`
}

// StatusRule 声明一个上游任务状态词到观察相位（及失败分类）的映射。
type StatusRule struct {
	Phase     string `json:"phase"`
	Class     string `json:"class,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

// StatusMapping 是连接级任务状态词表：键为上游状态词（小写），
// 值为内核观察相位。未声明的状态词按未知状态协议错误处理。
type StatusMapping struct {
	UpstreamStatus map[string]StatusRule `json:"upstream_status,omitempty"`
}

// ResponseAlias 声明一类产物在上游响应 output 中的候选字段名：
// 按声明顺序取第一个 HTTP URL 值；Key 参与 claim 幂等标识，
// ClaimID 缺省为 Key（兼容既有 claim ID 模板可显式覆盖）；
// Variant 声明产出的逻辑变体（如转换任务的 "converted"），
// 用于对齐 outputs 的 (role, variant) 资源契约。
type ResponseAlias struct {
	Key     string   `json:"key"`
	ClaimID string   `json:"claim_id,omitempty"`
	Role    string   `json:"role"`
	Variant string   `json:"variant,omitempty"`
	Fields  []string `json:"fields"`
}
