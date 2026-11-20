package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #373: the turn directive of a structured multiview request says what the
// server has already bound (the model and the views), tells the Agent to call
// the tool at once without a catalog lookup first, and that the session's lock
// has no say in this step. The incident turn ran six minutes: the Agent listed
// the Offerings, then called the tool under a lock the request outranks.
func TestMultiviewDirectiveBindsTheModelAndViewsAndSkipsTheLookup(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{schema: schema, registry: registry, agentScope: agentscope.FromSchema(schema.AgentScope)}
	tool := schema.ThreeView.Multiview.Tool
	views := []MultiviewView{{View: "front", AssetID: "a1"}, {View: "left", AssetID: "a2"}, {View: "back", AssetID: "a3"}}
	for name, request := range map[string]*MultiviewRequest{
		"labelled uploads":    {Model: "tripo-p2-multiview", Views: views},
		"a three-view button": {Group: "run-7", Model: "tripo-p2-multiview", Views: views},
	} {
		t.Run(name, func(t *testing.T) {
			directive := o.multiviewDirective(registry, TurnOptions{AgentScope: "auto", Multiview: request})
			for _, want := range []string{
				tool, "model 填 tripo-p2-multiview", "front / left / back",
				"已经按用户", "直接调用工具 " + tool, "不要先调用 list_offerings", "不受会话里锁定的 3D 模型影响",
			} {
				if !strings.Contains(directive, want) {
					t.Errorf("directive lacks %q: %s", want, directive)
				}
			}
			if request.Group != "" && !strings.Contains(directive, "three_view_group 填 run-7，") {
				t.Errorf("a button directive must name its group: %s", directive)
			}
			// The mock Agent reads the model up to the next sentence mark, so the
			// model must be followed by one.
			if !strings.Contains(directive, "model 填 tripo-p2-multiview。") && !strings.Contains(directive, "model 填 tripo-p2-multiview，") {
				t.Errorf("the model must end at a sentence mark: %s", directive)
			}
		})
	}
	if directive := o.multiviewDirective(registry, TurnOptions{AgentScope: "auto"}); directive != "" {
		t.Fatalf("a turn without a request carries a directive: %s", directive)
	}
}
