package generation

import "github.com/LeXwDeX/Plinth/internal/schema"

// B5 契约解耦：执行词表迁入 internal/schema，此处别名再导出。
type (
	ForcedValue         = schema.ForcedValue
	ErrorClassRule      = schema.ErrorClassRule
	HTTPStatusClass     = schema.HTTPStatusClass
	ErrorMessageMatcher = schema.ErrorMessageMatcher
	ErrorFieldPaths     = schema.ErrorFieldPaths
	ErrorMapping        = schema.ErrorMapping
	StatusRule          = schema.StatusRule
	StatusMapping       = schema.StatusMapping
	ResponseAlias       = schema.ResponseAlias
)
