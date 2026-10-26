package generation

import "github.com/LeXwDeX/Plinth/internal/schema"

// B5 契约解耦：条件评估辅助迁入 internal/schema，此处再导出。
var (
	ParameterOmitted  = schema.ParameterOmitted
	ConditionMatches  = schema.ConditionMatches
	ApplyForcedValues = schema.ApplyForcedValues
)
