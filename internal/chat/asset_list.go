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
// session. It asks the same question a turn would — which model would this
// step run — and answers it with stepOffering, i.e. chat.ResolveModel: the
// person's explicit model for the capability's domain, the purpose's
// recommendation, the workspace value and the capability's default available
// Offering, in that order (#380 step 1). The list's own route names no model,
// so the Agent's argument and the plan's pin take no part. An unavailable
// capability is reported as available=false with the reason; it never blocks
// the proposal.
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
	explicitImage, err := service.SessionExplicitModelImage(ctx, ownerID, sessionID)
	if err != nil {
		return step, err
	}
	// The workspace image model is the one the person last sent with in the
	// image domain (#318); it is what create_three_view would select.
	models, _, err := service.SessionModels(ctx, ownerID, sessionID)
	if err != nil {
		return step, err
	}
	// The step asks what a turn of this session would run, so a retired lock
	// moves to its successor first, exactly as startTurn freezes it (#380 S1:
	// a lock left on a retired model would otherwise read as unavailable).
	command := ToolCommand{
		OwnerID:   ownerID,
		SessionID: sessionID,
		Options: TurnOptions{
			ExplicitModel3D:    service.currentModel(explicitModel),
			ExplicitModelImage: service.currentModel(explicitImage),
			CreativeDomain:     string(generation.DomainModel3D),
			DomainModels:       map[string]string{"image": models["image"]},
		},
	}
	policy := service.productionPlans
	if policy == nil {
		step.Reason = "no available offering for capability " + string(capability)
		return step, nil
	}
	offering, _, refusal := policy.stepOffering(command, string(capability), "")
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
