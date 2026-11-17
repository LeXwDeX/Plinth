package httpapi

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/imagereview"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

type stubImageInspector struct{}

func (stubImageInspector) InspectOwned(
	context.Context, imagereview.InspectRequest,
) (imagereview.Inspection, error) {
	return imagereview.Inspection{}, nil
}

type stubInputAssets struct{}

func (stubInputAssets) UploadAsset(
	context.Context, generation.OwnerID, generation.InputAssetUpload,
) (generation.InputAsset, error) {
	return generation.InputAsset{}, nil
}

func (stubInputAssets) OpenAuthorized(
	context.Context, generation.OwnerID, generation.AssetID, bool,
) (generation.InputAsset, io.ReadCloser, error) {
	return generation.InputAsset{}, nil, nil
}

// #332 step 7b: a Catalog that serves inspect_image needs the picture
// inspector and the chat service assembled; a deployment missing either fails
// to start instead of serving a tool that can only refuse.
func TestMCPInspectImageNeedsTheInspectorAndTheChatService(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	service := *product.Application().MCPPublic
	var declared catalog.MCPPublicTool
	for _, tool := range service.Tools {
		if tool.Name == catalog.MCPToolInspectImage {
			declared = tool
		}
	}
	if declared.Name != catalog.MCPToolInspectImage {
		t.Fatalf("the default Catalog does not declare %q", catalog.MCPToolInspectImage)
	}
	service.Tools = []catalog.MCPPublicTool{declared}
	if _, err := (&Server{}).newMCPRuntime(service, routing.Two{}, routing.One{}); err == nil ||
		!strings.Contains(err.Error(), "没有装配图片检查器") {
		t.Fatalf("without a picture inspector the runtime started: %v", err)
	}
	server := &Server{imageInspector: stubImageInspector{}}
	if _, err := server.newMCPRuntime(service, routing.Two{}, routing.One{}); err == nil ||
		!strings.Contains(err.Error(), "没有装配会话服务") {
		t.Fatalf("without the chat service the runtime started: %v", err)
	}
}

// #332 step 7b: every picture slot taken is image_busy with a short wait, and
// the picture is never opened.
func TestMCPInspectImageRefusesWhenEverySlotIsTaken(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		application:    product.Application(),
		imageInspector: stubImageInspector{},
		inputAssets:    stubInputAssets{},
		mcpImageSlots:  make(chan struct{}, 1),
	}
	server.mcpImageSlots <- struct{}{}
	outcome, err := server.mcpInspectImage(&mcpCall{
		ctx: context.Background(), locale: "zh-CN",
		arguments: map[string]any{"image": "asset_x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.problem == nil || outcome.problem.Code != "image_busy" {
		t.Fatalf("a full pool = %+v", outcome)
	}
	if outcome.problem.RetryAfterSeconds == nil || *outcome.problem.RetryAfterSeconds != mcpImageBusyWaitSeconds {
		t.Errorf("retry after = %v", outcome.problem.RetryAfterSeconds)
	}
	if !strings.Contains(outcome.problem.Message, "2") || !strings.Contains(outcome.problem.Message, "秒") {
		t.Errorf("message = %q", outcome.problem.Message)
	}
}
