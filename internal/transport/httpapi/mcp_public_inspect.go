package httpapi

import (
	"errors"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/mcppublic"
)

// mcpInspectModel is inspect_model of the external MCP service (#332): the same
// read-only inspection the platform's Agent tool runs, over the Task's saved
// model file. It never writes self-check evidence, counts a step or creates a
// Task. The purpose judgement reads the brief stored in the Task's session;
// purpose names one chat.model3d_usage profile for this call only (#332 7c-1),
// and a session without a brief falls back to the Schema's default profile.
func (s *Server) mcpInspectModel(call *mcpCall) (mcpOutcome, error) {
	task, missing, err := s.mcpOwnedTask(call, mcpStringArgument(call, "task_id"))
	if err != nil {
		return mcpOutcome{}, err
	}
	if missing != nil {
		return *missing, nil
	}
	purpose := mcpStringArgument(call, "purpose")
	if purpose != "" && !s.mcpModelPurposeKnown(purpose) {
		return s.mcpInvalidArguments(call.locale, s.mcpInvalidDetail(call.locale, "$.purpose",
			s.application.MCPPublic.Messages.InvalidDetails.PurposeUnknown, "{purposes}", s.mcpModelPurposeIDs())), nil
	}
	assetID := mcpStringArgument(call, "asset_id")
	if s.modelInspector == nil {
		return mcpOutcome{problem: &mcpProblem{
			Code:    "model_unavailable",
			Message: s.mcpText(call.locale, s.application.MCPPublic.Messages.ModelUnavailable),
		}}, nil
	}
	brief, err := s.chat.SessionBrief(call.ctx, string(call.owner), task.SessionID)
	if err != nil {
		brief = chat.ProductionBrief{}
	}
	result, err := s.modelInspector.InspectReviewedModel(call.ctx, generationtool.ModelInspectRequest{
		Owner: call.owner, TaskID: task.ID, AssetID: generation.AssetID(assetID),
		SourceVariantPreference: s.application.MCPPublic.Generation.SourceVariantPreference,
		Brief:                   brief, Purpose: purpose,
		AllowedFixes: s.mcpInspectModelFixCapabilities(), Locale: call.locale,
	})
	if err != nil {
		var refusal *generationtool.ModelInspectRefusal
		if !errors.As(err, &refusal) {
			return mcpOutcome{}, err
		}
		code, known := mcpInspectProblemCodes[refusal.Kind]
		if !known {
			return mcpOutcome{}, err
		}
		// The message is the platform inspect_model's own localized text.
		return mcpOutcome{problem: &mcpProblem{Code: code, Message: refusal.Message}}, nil
	}
	subject := mcppublic.Subject{}
	marked, err := s.mcpAssetSessions(call, task.SessionID)
	if err != nil {
		return mcpOutcome{}, err
	}
	if marked[task.SessionID] {
		subject.AssetID, subject.Costs = task.SessionID, s.mcpStepCosts(call.ctx, call.owner, task.SessionID)
	}
	return mcpOutcome{
		data: map[string]any{
			"task_id": string(result.TaskID), "asset_id": string(result.AssetID),
			"report": result.Report, "usage_check": result.UsageCheck,
			"parts_total": result.PartsTotal, "parts_truncated": result.PartsTruncated,
		},
		subject: subject,
	}, nil
}

// mcpModelPurposeKnown reports whether the Schema declares the 3D purpose.
func (s *Server) mcpModelPurposeKnown(id string) bool {
	for _, profile := range s.application.Chat.Model3DUsage.Profiles {
		if profile.ID == id {
			return true
		}
	}
	return false
}

// mcpModelPurposeIDs lists the purposes a caller may pass, for the refusal that
// names them.
func (s *Server) mcpModelPurposeIDs() string {
	ids := make([]string, 0, len(s.application.Chat.Model3DUsage.Profiles))
	for _, profile := range s.application.Chat.Model3DUsage.Profiles {
		ids = append(ids, profile.ID)
	}
	return strings.Join(ids, "、")
}

// mcpInspectModelFixCapabilities are the capabilities the declared
// process_model tool can execute: inspect_model may name only these in a fix
// hint (#332 7c-1). A Catalog without the tool gets an empty set, not nil,
// because nil means "no filter" to the inspection core.
func (s *Server) mcpInspectModelFixCapabilities() []generation.Capability {
	for _, tool := range s.application.MCPPublic.Tools {
		if tool.Name == catalog.MCPToolProcessModel {
			return append([]generation.Capability{}, tool.Capabilities...)
		}
	}
	return []generation.Capability{}
}

// mcpInspectProblemCodes maps the refusal kinds of InspectReviewedModel to the
// business error codes of the tool result.
var mcpInspectProblemCodes = map[string]string{
	generationtool.ModelInspectNotFound:       "asset_not_found",
	generationtool.ModelInspectNotReady:       "model_not_ready",
	generationtool.ModelInspectNotGLB:         "model_not_glb",
	generationtool.ModelInspectTooLarge:       "model_too_large",
	generationtool.ModelInspectUnreadable:     "model_unreadable",
	generationtool.ModelInspectUnknownPurpose: "invalid_arguments",
	generationtool.ModelInspectUnavailable:    "model_unavailable",
}
