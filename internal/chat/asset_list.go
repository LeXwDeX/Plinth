package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Asset list availability (#327): a production list snapshots the runtime
// Offering of every route step, exactly as a production plan would quote it.
// The list service owns the workflow; these two adapters let it reuse the
// plan's offering selection and the session brief's validation instead of
// duplicating either.

// ResolveAssetListStep resolves one route step for a list owned by the
// session. It reuses stepOffering's order: the person's explicit 3D model,
// the workspace's image model for image-domain steps, the Agent's pinned
// model (none here) and the capability's default available Offering. An
// unavailable capability is reported as available=false with the reason; it
// never blocks the proposal.
func (service *Service) ResolveAssetListStep(
	ctx context.Context,
	ownerID string,
	sessionID string,
	capability generation.Capability,
) (assetlist.RouteStep, error) {
	ownerID = strings.TrimSpace(ownerID)
	sessionID = strings.TrimSpace(sessionID)
	step := assetlist.RouteStep{Capability: string(capability)}
	if ownerID == "" || sessionID == "" {
		return step, fmt.Errorf("chat: asset list step needs an owner and a session")
	}
	explicitModel, err := service.SessionExplicitModel3D(ctx, ownerID, sessionID)
	if err != nil {
		return step, err
	}
	// The workspace image model is the one the person last sent with in the
	// image domain (#318); it is what create_three_view would select.
	models, _, err := service.SessionModels(ctx, ownerID, sessionID)
	if err != nil {
		return step, err
	}
	command := ToolCommand{
		OwnerID:   ownerID,
		SessionID: sessionID,
		Options: TurnOptions{
			ExplicitModel3D: explicitModel,
			CreativeDomain:  string(generation.DomainModel3D),
			DomainModels:    map[string]string{"image": models["image"]},
		},
	}
	policy := service.productionPlans
	if policy == nil {
		step.Reason = "no available offering for capability " + string(capability)
		return step, nil
	}
	offering, _, refusal := policy.stepOffering(
		string(capability), "",
		strings.TrimSpace(command.Options.ExplicitModel3D),
		strings.TrimSpace(command.Options.ModelFor("image")),
	)
	if refusal != "" {
		step.Reason = refusal
		return step, nil
	}
	step.OfferingID = string(offering.ID)
	step.Available = true
	return step, nil
}

// ValidateAssetListBrief checks one list's shared brief against the Schema's
// model3d_usage.brief fields. It reuses the session brief's rules (declared
// fields, profile ids, select options, bounds) and returns the same refusal.
func (service *Service) ValidateAssetListBrief(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("brief must be a JSON object: %w", err)
	}
	if fields == nil {
		return fmt.Errorf("brief must be a JSON object")
	}
	_, err := ValidateProductionBrief(
		service.model3DUsage.Brief,
		service.model3DUsage.Profiles,
		service.image2DUsage.Profiles,
		fields,
	)
	return err
}
