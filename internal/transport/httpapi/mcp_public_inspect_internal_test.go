package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

type stubModelInspector struct{}

func (stubModelInspector) InspectReviewedModel(
	context.Context, generationtool.ModelInspectRequest,
) (generationtool.ModelInspectResult, error) {
	return generationtool.ModelInspectResult{}, nil
}

// #332 step 7a: a Catalog that serves inspect_model needs the inspector and
// the chat service assembled; a deployment missing either fails to start
// instead of serving a tool that can only refuse.
func TestMCPInspectModelNeedsTheInspectorAndTheChatService(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	service := *product.Application().MCPPublic
	var declared catalog.MCPPublicTool
	for _, tool := range service.Tools {
		if tool.Name == catalog.MCPToolInspectModel {
			declared = tool
		}
	}
	if declared.Name != catalog.MCPToolInspectModel {
		t.Fatalf("the default Catalog does not declare %q", catalog.MCPToolInspectModel)
	}
	service.Tools = []catalog.MCPPublicTool{declared}
	if _, err := (&Server{}).newMCPRuntime(service, routing.Two{}, routing.One{}); err == nil ||
		!strings.Contains(err.Error(), "没有装配模型检查器") {
		t.Fatalf("without an inspector the runtime started: %v", err)
	}
	server := &Server{modelInspector: stubModelInspector{}}
	if _, err := server.newMCPRuntime(service, routing.Two{}, routing.One{}); err == nil ||
		!strings.Contains(err.Error(), "没有装配会话服务") {
		t.Fatalf("without the chat service the runtime started: %v", err)
	}
}
