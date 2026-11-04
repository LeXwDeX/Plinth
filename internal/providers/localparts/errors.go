package localparts

import (
	"errors"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const operation = "local_model_parts"

// invalidModel reports a source that passed the container checks but violates
// the static rewrite subset. The detail stays generic; the reason is internal.
func invalidModel(reason string) error {
	return &generation.Error{Code: generation.ErrInvalidModelInput, Operation: operation, Detail: "模型内容不符合本地拆分或合并约束", Cause: errors.New(reason)}
}

// unsupportedModel carries a user-visible Chinese reason. It is reserved for
// content the first version deliberately refuses (skeletons, animation).
func unsupportedModel(reason string) error {
	return &generation.Error{Code: generation.ErrUnsupportedModelInput, Operation: operation, Detail: reason}
}

// limitError reports a bound declared by the frozen Offering, not a malformed
// source.
func limitError(reason string) error {
	return &generation.Error{Code: generation.ErrModelInputLimit, Operation: operation, Detail: "模型超出本地拆分或合并的资源上限", Cause: errors.New(reason)}
}

func sourceError() error {
	return &generation.Error{Code: generation.ErrInvalidInput, Operation: operation, Detail: "模型来源与冻结输入不一致"}
}

func unavailableError(detail string, cause error) error {
	return &generation.Error{Code: generation.ErrUnavailable, Operation: operation, Detail: detail, Cause: cause}
}
