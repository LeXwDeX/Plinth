package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func assetListOfferings() []generation.Offering {
	return []generation.Offering{
		{ID: "default-3d", Revision: "v1", Capability: "text-to-3d", Enabled: true, Default: true, ModelID: "default-model"},
		{ID: "locked-3d", Revision: "v1", Capability: "text-to-3d", Enabled: true, ModelID: "locked-model"},
		{ID: "image-2d", Revision: "v1", Capability: "text-to-image", Enabled: true, Default: true,
			Domain: generation.DomainImage, ModelID: "image-model"},
	}
}

func assetListService(t *testing.T, available func(generation.OfferingID) bool) (*chat.Service, *memoryChatRepository) {
	t.Helper()
	repository := newMemoryChatRepository()
	repository.sessions["session-a"] = chat.Session{ID: "session-a", OwnerID: "owner-a"}
	service := newChatService(t, repository)
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:            shippedBudgetSchema(t),
		Offerings:         assetListOfferings,
		OfferingAvailable: available,
		Store:             &recordingProductionPlans{},
	})
	return service, repository
}

func TestResolveAssetListStepFollowsTheOfferingOrder(t *testing.T) {
	t.Parallel()
	service, repository := assetListService(t, func(generation.OfferingID) bool { return true })
	ctx := context.Background()

	step, err := service.ResolveAssetListStep(ctx, "owner-a", "session-a", "text-to-3d")
	if err != nil || !step.Available || step.OfferingID != "default-3d" || step.Reason != "" {
		t.Fatalf("default step = %+v, %v", step, err)
	}

	// The person's explicit 3D model is what would run, so it is pinned.
	repository.sessions["session-a"] = chat.Session{
		ID: "session-a", OwnerID: "owner-a", ExplicitModel3D: "locked-model",
	}
	step, err = service.ResolveAssetListStep(ctx, "owner-a", "session-a", "text-to-3d")
	if err != nil || !step.Available || step.OfferingID != "locked-3d" {
		t.Fatalf("locked step = %+v, %v", step, err)
	}

	// The workspace's image model shapes an image-domain step.
	repository.turns["turn-a"] = chat.Turn{
		OwnerID: "owner-a", SessionID: "session-a",
		Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "image-model"},
	}
	step, err = service.ResolveAssetListStep(ctx, "owner-a", "session-a", "text-to-image")
	if err != nil || !step.Available || step.OfferingID != "image-2d" {
		t.Fatalf("image step = %+v, %v", step, err)
	}

	step, err = service.ResolveAssetListStep(ctx, "owner-a", "session-missing", "text-to-3d")
	if err == nil {
		t.Fatalf("missing session step = %+v, want an error", step)
	}
}

func TestResolveAssetListStepKeepsUnavailableCapabilities(t *testing.T) {
	t.Parallel()
	service, _ := assetListService(t, func(generation.OfferingID) bool { return false })
	step, err := service.ResolveAssetListStep(context.Background(), "owner-a", "session-a", "text-to-3d")
	if err != nil || step.Available || step.OfferingID != "" ||
		!strings.Contains(step.Reason, "no available offering for capability text-to-3d") {
		t.Fatalf("unavailable step = %+v, %v", step, err)
	}

	// A Catalog without the production plan policy has no runtime offering to
	// snapshot; the step is unavailable instead of silently available.
	repository := newMemoryChatRepository()
	repository.sessions["session-a"] = chat.Session{ID: "session-a", OwnerID: "owner-a"}
	bare := newChatService(t, repository)
	step, err = bare.ResolveAssetListStep(context.Background(), "owner-a", "session-a", "text-to-3d")
	if err != nil || step.Available || !strings.Contains(step.Reason, "no available offering") {
		t.Fatalf("unconfigured step = %+v, %v", step, err)
	}
}

func TestValidateAssetListBriefReusesTheSessionBriefRules(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
		config.Model3DUsage = application.Chat.Model3DUsage
		config.Image2DUsage = application.Chat.Image2DUsage
	})
	if err := service.ValidateAssetListBrief(json.RawMessage(`{"purpose":"render","style":"低多边形"}`)); err != nil {
		t.Fatalf("valid brief: %v", err)
	}
	if err := service.ValidateAssetListBrief(nil); err != nil {
		t.Fatalf("empty brief: %v", err)
	}
	if err := service.ValidateAssetListBrief(json.RawMessage(`{"purpose":"render","unknown":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := service.ValidateAssetListBrief(json.RawMessage(`{"purpose":"not-declared"}`)); err == nil {
		t.Fatal("undeclared profile accepted")
	}
	if err := service.ValidateAssetListBrief(json.RawMessage(`["not","an","object"]`)); err == nil {
		t.Fatal("non-object brief accepted")
	}
}
