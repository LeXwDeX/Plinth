package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// ChatHarnessSchema declares semantic operations independently of the model
// selected for new generation. Tool wiring and model instructions stay private.
type ChatHarnessSchema struct {
	SourceKinds                []HarnessSourceKind      `json:"source_kinds,omitempty"`
	TaskSourceModes            []HarnessSourceKind      `json:"task_source_modes,omitempty"`
	DefaultTaskSourceMode      string                   `json:"default_task_source_mode,omitempty"`
	SourceClarificationMessage string                   `json:"source_clarification_message,omitempty"`
	Groups                     []HarnessGroupSchema     `json:"groups"`
	Operations                 []HarnessOperationSchema `json:"operations"`
	DefaultEnabledOperations   []string                 `json:"default_enabled_operations"`
	DefaultAllowPrerequisites  bool                     `json:"default_allow_prerequisites"`
	PrerequisiteTool           string                   `json:"prerequisite_tool"`
	BaseTools                  []string                 `json:"base_tools"`
	ContextTemplate            string                   `json:"context_template"`
	RejectionMessage           string                   `json:"rejection_message"`
	ChangedMessage             string                   `json:"changed_message"`
	MissingInputMessage        string                   `json:"missing_input_message"`
	UnavailableMessage         string                   `json:"unavailable_message"`
	ProposalMessage            string                   `json:"proposal_message"`
	// ConversationInputDescription is appended to the tool description of a
	// reference image the Agent takes from the conversation (#187 D).
	ConversationInputDescription string `json:"conversation_input_description"`
	// MissingConversationInputMessage and MissingParametersMessage refuse a
	// call that lacks a required input and name it at {parameters}: a picture
	// the person must attach or name in the chat, or a parameter the person
	// must set in the operation settings.
	MissingConversationInputMessage string `json:"missing_conversation_input_message"`
	MissingParametersMessage        string `json:"missing_parameters_message"`
}

type HarnessSourceKind struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type HarnessGroupSchema struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	EmptyMessage string `json:"empty_message"`
}

type PublicHarnessOperation struct {
	ID          string                    `json:"id"`
	Label       string                    `json:"label"`
	Description string                    `json:"description"`
	Group       string                    `json:"group"`
	Capability  schemacontract.Capability `json:"capability"`
	ModelIDs    []string                  `json:"model_ids,omitempty"`
	// Prerequisites lists legal paths that precede this exact goal. The goal
	// is not repeated in a path. Visibility or a proposal grants no execution.
	Prerequisites                   [][]string `json:"prerequisites,omitempty"`
	UnavailableReason               string     `json:"unavailable_reason,omitempty"`
	UploadedSourceUnavailableReason string     `json:"uploaded_source_unavailable_reason,omitempty"`
}

type HarnessOperationSchema struct {
	PublicHarnessOperation
	ToolName      string                     `json:"tool_name"`
	ToolArguments map[string]json.RawMessage `json:"tool_arguments,omitempty"`
}

type PublicHarnessSchema struct {
	SourceKinds                []HarnessSourceKind      `json:"source_kinds,omitempty"`
	TaskSourceModes            []HarnessSourceKind      `json:"task_source_modes,omitempty"`
	DefaultTaskSourceMode      string                   `json:"default_task_source_mode,omitempty"`
	SourceClarificationMessage string                   `json:"source_clarification_message,omitempty"`
	Groups                     []HarnessGroupSchema     `json:"groups"`
	Operations                 []PublicHarnessOperation `json:"operations"`
	DefaultEnabledOperations   []string                 `json:"default_enabled_operations"`
	DefaultAllowPrerequisites  bool                     `json:"default_allow_prerequisites"`
}

func (schema ChatHarnessSchema) Public() PublicHarnessSchema {
	out := PublicHarnessSchema{TaskSourceModes: append([]HarnessSourceKind(nil), schema.TaskSourceModes...), DefaultTaskSourceMode: schema.DefaultTaskSourceMode, SourceClarificationMessage: schema.SourceClarificationMessage, Groups: schema.Groups, SourceKinds: append([]HarnessSourceKind(nil), schema.SourceKinds...),
		DefaultEnabledOperations:  schema.DefaultEnabledOperations,
		DefaultAllowPrerequisites: schema.DefaultAllowPrerequisites,
		Operations:                make([]PublicHarnessOperation, 0, len(schema.Operations))}
	for _, operation := range schema.Operations {
		out.Operations = append(out.Operations, operation.PublicHarnessOperation)
	}
	return out
}

func validateHarness(schema ChatSchema, capabilityPresentations []CapabilityPresentation) error {
	h := schema.Harness
	if len(h.SourceKinds) > 0 {
		if len(h.SourceKinds) != 2 || h.SourceKinds[0].ID != "task_asset" || h.SourceKinds[1].ID != "input_asset" {
			return fmt.Errorf("harness.source_kinds must declare task_asset followed by input_asset")
		}
		for _, kind := range h.SourceKinds {
			if strings.TrimSpace(kind.Label) == "" || strings.TrimSpace(kind.Description) == "" {
				return fmt.Errorf("harness source kinds require presentation")
			}
		}
	}
	if len(h.TaskSourceModes) > 0 || h.DefaultTaskSourceMode != "" || h.SourceClarificationMessage != "" {
		if len(h.TaskSourceModes) != 2 || h.TaskSourceModes[0].ID != "agent" || h.TaskSourceModes[1].ID != "fixed" || (h.DefaultTaskSourceMode != "agent" && h.DefaultTaskSourceMode != "fixed") || strings.TrimSpace(h.SourceClarificationMessage) == "" {
			return fmt.Errorf("harness task source modes require agent/fixed, a declared default and clarification message")
		}
		for _, mode := range h.TaskSourceModes {
			if strings.TrimSpace(mode.Label) == "" || strings.TrimSpace(mode.Description) == "" {
				return fmt.Errorf("harness task source mode requires presentation")
			}
		}
	}
	capabilities := map[schemacontract.Capability]bool{}
	for _, capability := range capabilityPresentations {
		capabilities[capability.ID] = true
	}
	if len(h.Groups) != 2 || h.Groups[0].ID != "common" || h.Groups[1].ID != "model" {
		return fmt.Errorf("harness.groups must declare common followed by model")
	}
	for _, group := range h.Groups {
		if strings.TrimSpace(group.Label) == "" || strings.TrimSpace(group.EmptyMessage) == "" {
			return fmt.Errorf("harness group %q requires presentation", group.ID)
		}
	}
	if len(h.Operations) == 0 || len(h.BaseTools) == 0 {
		return fmt.Errorf("harness operations and base_tools are required")
	}
	for field, value := range map[string]string{"context_template": h.ContextTemplate, "rejection_message": h.RejectionMessage, "changed_message": h.ChangedMessage, "missing_input_message": h.MissingInputMessage, "unavailable_message": h.UnavailableMessage, "proposal_message": h.ProposalMessage,
		"conversation_input_description": h.ConversationInputDescription, "missing_conversation_input_message": h.MissingConversationInputMessage, "missing_parameters_message": h.MissingParametersMessage} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("harness.%s is required", field)
		}
	}
	for field, value := range map[string]string{"missing_conversation_input_message": h.MissingConversationInputMessage, "missing_parameters_message": h.MissingParametersMessage} {
		if !strings.Contains(value, "{parameters}") {
			return fmt.Errorf("harness.%s requires {parameters}", field)
		}
	}
	if !strings.Contains(h.ContextTemplate, "{snapshot}") {
		return fmt.Errorf("harness.context_template requires {snapshot}")
	}
	tools := map[string]ChatToolSchema{}
	for _, tool := range schema.Tools {
		tools[tool.Name] = tool
	}
	names := map[string]bool{}
	for _, tool := range schema.AgentTools {
		names[tool.Name] = !slices.Contains(tool.Execution.Effects, "PAID_TASK")
	}
	seenBase := map[string]bool{}
	for _, name := range h.BaseTools {
		if !names[name] || seenBase[name] {
			return fmt.Errorf("harness base tool %q is unknown, paid or duplicate", name)
		}
		seenBase[name] = true
	}
	plan, ok := tools[h.PrerequisiteTool]
	if !ok || plan.Processing == nil || plan.Processing.Plan == nil {
		return fmt.Errorf("harness.prerequisite_tool must use the processing plan implementation")
	}
	ops := map[string]HarnessOperationSchema{}
	for _, op := range h.Operations {
		if op.ID == "" || op.Label == "" || op.Description == "" || (op.Group != "common" && op.Group != "model") {
			return fmt.Errorf("harness operation %q has incomplete identity", op.ID)
		}
		if _, duplicate := ops[op.ID]; duplicate {
			return fmt.Errorf("harness operation %q is duplicated", op.ID)
		}
		if _, known := capabilities[op.Capability]; !known {
			return fmt.Errorf("harness operation %q has unknown capability", op.ID)
		}
		if (op.Group == "common" && len(op.ModelIDs) > 0) || (op.Group == "model" && len(op.ModelIDs) == 0) {
			return fmt.Errorf("harness operation %q has inconsistent model grouping", op.ID)
		}
		if op.ToolName == "" {
			if op.UnavailableReason == "" || len(op.ToolArguments) > 0 {
				return fmt.Errorf("harness operation %q without tool requires an unavailable reason", op.ID)
			}
		} else {
			tool, exists := tools[op.ToolName]
			if !exists || tool.Processing == nil || tool.Processing.Plan != nil || !slices.Contains(tool.Execution.Capabilities, op.Capability) {
				return fmt.Errorf("harness operation %q has incompatible tool wiring", op.ID)
			}
			var parameters struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if json.Unmarshal(tool.Parameters, &parameters) != nil {
				return fmt.Errorf("harness tool %q parameters invalid", op.ToolName)
			}
			for name, value := range op.ToolArguments {
				if _, ok := parameters.Properties[name]; !ok || !json.Valid(value) {
					return fmt.Errorf("harness operation %q has unknown tool argument %q", op.ID, name)
				}
			}
			if tool.Processing.StepArgument != "" {
				var step string
				if json.Unmarshal(op.ToolArguments[tool.Processing.StepArgument], &step) != nil || tool.Processing.Steps[step] != op.Capability {
					return fmt.Errorf("harness operation %q has incompatible step", op.ID)
				}
			} else if tool.Capability != op.Capability {
				return fmt.Errorf("harness operation %q capability differs from its tool", op.ID)
			}
		}
		ops[op.ID] = op
	}
	enabled := map[string]bool{}
	for _, id := range h.DefaultEnabledOperations {
		if _, ok := ops[id]; !ok || enabled[id] {
			return fmt.Errorf("harness default operation %q is unknown or duplicate", id)
		}
		enabled[id] = true
	}
	for _, op := range h.Operations {
		for _, path := range op.Prerequisites {
			if len(path) == 0 {
				return fmt.Errorf("harness prerequisite path for %q is empty", op.ID)
			}
			seen := map[string]bool{op.ID: true}
			for _, id := range path {
				candidate, ok := ops[id]
				if !ok || seen[id] || candidate.ToolName == "" {
					return fmt.Errorf("harness prerequisite path for %q contains unknown, repeated or unwired %q", op.ID, id)
				}
				if !slices.Contains(plan.Execution.Capabilities, candidate.Capability) {
					return fmt.Errorf("harness prerequisite %q is not supported by plan tool", id)
				}
				seen[id] = true
			}
			if !slices.Contains(plan.Execution.Capabilities, op.Capability) {
				return fmt.Errorf("harness prerequisite goal %q is not supported by plan tool", op.ID)
			}
		}
	}
	return nil
}

// ValidateHarnessParameter reuses Offering value validation without inventing
// missing required inputs, applying defaults, granting extra cost, or writing a
// PreparedInput. Full cross-field validation still happens in Generation.
func ValidateHarnessParameter(parameter schemacontract.Parameter, raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("parameter must contain one JSON value")
	}
	normalized, err := validateValue(parameter, value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}
