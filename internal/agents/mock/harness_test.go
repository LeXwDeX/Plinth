package mock_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// harnessTool is the shape the server gives a selected operation's tool: a
// fixed offering and a closed parameters object (#187 D).
func harnessTool(name, offering string) agents.Tool {
	return agents.Tool{Name: name, Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{
		"offering_id":{"type":"string","const":"` + offering + `"},
		"parameter_reasons":{"type":"object"},
		"parameters":{"type":"object","additionalProperties":false,"required":["reference_image"],"properties":{
			"reference_image":{"type":"string","pattern":"^@\\S+$","description":"handle"},
			"prompt":{"type":"string"}}}}}`)}
}

func TestMockCallsTheSelectedOperationWithTheAttachedPicture(t *testing.T) {
	tools := []agents.Tool{harnessTool("retexture_model", "hyper3d/texture/local-model"), {Name: "list_offerings"}}
	user := agents.Message{Role: "user", Content: "请按功能设置给模型换贴图，提示词 warm carved wood\n句柄 @img1：用户上传"}
	name, arguments := plan(t, []agents.Message{selectionPrompt("@model1"), user}, tools)
	if name != "retexture_model" || arguments["offering_id"] != "hyper3d/texture/local-model" {
		t.Fatalf("call %s %v", name, arguments)
	}
	parameters, _ := arguments["parameters"].(map[string]any)
	if parameters["reference_image"] != "@img1" || parameters["prompt"] == nil || len(arguments) != 2 {
		t.Fatalf("harness arguments %v", arguments)
	}
	// Bang through process_model gets the same treatment, with no free-mode keys.
	tools = []agents.Tool{harnessTool("process_model", "hyper3d/bang/local-model")}
	user = agents.Message{Role: "user", Content: "按功能设置把模型分件，参考图用 @img2"}
	name, arguments = plan(t, []agents.Message{user}, tools)
	parameters, _ = arguments["parameters"].(map[string]any)
	if name != "process_model" || parameters["reference_image"] != "@img2" || arguments["step"] != nil || arguments["source_task_id"] != nil {
		t.Fatalf("segmentation call %s %v", name, arguments)
	}
}
