package schema

// UserParameterAllowed and AgentParameterAllowed are the shared settings
// authority predicates. Source inputs and server consent use their own ports.
// A "user" policy keeps a panel setting with the person: only the person may
// set it, the Agent cannot (#360).
func (o Offering) UserParameterAllowed(p Parameter) bool {
	return p.AdjustmentPolicy != "advised" && o.parameterAdjustable(p)
}
func (o Offering) AgentParameterAllowed(p Parameter) bool {
	return p.AdjustmentPolicy != "user" && o.parameterAdjustable(p)
}

// parameterAdjustable applies the rules both authorities share, after the
// policy that separates them.
func (o Offering) parameterAdjustable(p Parameter) bool {
	if p.Fixed || (p.AdjustmentPolicy != "" && p.AdjustmentPolicy != "advised" && p.AdjustmentPolicy != "user") || p.Name == o.ExtraConsentParameter() {
		return false
	}
	if p.Presentation != nil && (p.Presentation.Surface == SurfaceHidden || (p.Presentation.Surface == SurfaceReadOnly && p.AdjustmentPolicy != "advised")) {
		return false
	}
	if _, source := SourceParameterRole(p); source {
		return false
	}
	if p.Semantic == SemanticReferenceImage || p.Semantic == SemanticMask {
		return false
	}
	switch p.Type {
	case "task", "asset", "asset-array":
		return false
	}
	return true
}

// OverrideParameterAllowed reports whether a Schema-level override may fix the
// parameter's value (a concept sketch's cheap per-Offering values, #242): the
// value must be one the person or the Agent can legitimately set, and a fixed
// backend value is never overridden.
func (o Offering) OverrideParameterAllowed(p Parameter) bool {
	return o.UserParameterAllowed(p) || o.AgentParameterAllowed(p)
}

// AdvisedParameter reports whether the Offering declares name as a parameter
// whose non-default value is frozen as an adjustment that needs a reason.
func (o Offering) AdvisedParameter(name string) bool {
	for _, parameter := range o.Parameters {
		if parameter.Name == name {
			return parameter.AdjustmentPolicy == "advised"
		}
	}
	return false
}
