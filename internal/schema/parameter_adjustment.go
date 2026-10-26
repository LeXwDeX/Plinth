package schema

// UserParameterAllowed and AgentParameterAllowed are the shared settings
// authority predicates. Source inputs and server consent use their own ports.
func (o Offering) UserParameterAllowed(p Parameter) bool {
	return o.AgentParameterAllowed(p) && p.AdjustmentPolicy != "advised"
}
func (o Offering) AgentParameterAllowed(p Parameter) bool {
	if p.Fixed || (p.AdjustmentPolicy != "" && p.AdjustmentPolicy != "advised") || p.Name == o.ExtraConsentParameter() {
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
