package catalog

// ProcessingSourcePreference is the model variant order a processing plan
// uses when it binds a later step to the previous step's result (#334): the
// source_variant_preference the chat processing tools declare, so a plan step
// takes the same file (GLB first in the shipped Schema) that the chat tool and
// the panel pick for a source. The shipped tools declare one shared order
// (TestProcessingToolsShareOneSourcePreference); the first declared one is
// returned, nil when no tool declares any.
func ProcessingSourcePreference(tools []ChatToolSchema) []string {
	for _, tool := range tools {
		if tool.Processing != nil && len(tool.Processing.SourceVariantPreference) > 0 {
			return append([]string(nil), tool.Processing.SourceVariantPreference...)
		}
	}
	return nil
}

// ProcessingAgentStepRetries is how often the Agent's revise retry may start
// one plan step again (#665): the plan tool's
// processing.plan.revision.max_agent_retries_per_step, or 0 when no tool
// declares one (the processing service then keeps its built-in bound).
func ProcessingAgentStepRetries(tools []ChatToolSchema) int {
	for _, tool := range tools {
		if tool.Processing != nil && tool.Processing.Plan != nil &&
			tool.Processing.Plan.Revision.MaxAgentRetriesPerStep > 0 {
			return tool.Processing.Plan.Revision.MaxAgentRetriesPerStep
		}
	}
	return 0
}
