package catalog

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func creativeLabTool(t *testing.T, c *Catalog, name string) (int, ChatToolSchema) {
	t.Helper()
	for index, tool := range c.application.Chat.Tools {
		if tool.Name == name {
			return index, tool
		}
	}
	t.Fatalf("missing tool %s", name)
	return -1, ChatToolSchema{}
}

// #189: the Creative Lab Agent tools pick their Offering from a template
// argument or from the paired prototype Task. Every template names exactly one
// declared Offering of the tool's capabilities and every prototype pairs with
// exactly one build.
func TestCreativeLabAgentToolsDeclareTemplatesAndPairing(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, design := creativeLabTool(t, c, "create_creative_lab_design")
	if design.OfferingArgument != "template" || !design.IgnoreUserModel || design.RequireUniqueOffering || design.Execution.Consent != "human" || design.Execution.AgentScope != "3d" {
		t.Fatalf("design tool contract = %+v", design)
	}
	var parameters struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(design.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	templates := parameters.Properties["template"].Enum
	if len(templates) != 9 {
		t.Fatalf("templates = %v", templates)
	}
	for _, template := range templates {
		found := false
		for _, offering := range c.byID {
			if offering.ModelID == template && slices.Contains(design.Execution.Capabilities, offering.Capability) {
				found = true
				if offering.Capability == "creative-lab-build" && offering.ID != "meshy/creative-lab/fidget-collapsible/generate" {
					t.Fatalf("template %s is a build stage", template)
				}
			}
		}
		if !found {
			t.Fatalf("template %s has no Offering", template)
		}
	}
	_, build := creativeLabTool(t, c, "build_creative_lab_item")
	if build.OfferingFromSource != "source_task" || build.ArgumentMappings["source_task"] != sourceTaskFromImageHandle ||
		build.OverrideMappings[sourceTaskFromImageHandle] != "source_task" || build.AcceptReferenceImages == nil || *build.AcceptReferenceImages {
		t.Fatalf("build tool contract = %+v", build)
	}
	for _, product := range []string{"keychain", "fridge-magnet", "figure", "vinyl-figure", "brick-figure", "lamp", "keycap", "fidget-pixel"} {
		buildOffering := c.byID[schemacontract.OfferingID("meshy/creative-lab/"+product+"/build")]
		paired := false
		for _, parameter := range buildOffering.Parameters {
			if parameter.Name == "source_task" && slices.Equal(parameter.TaskOfferings, []schemacontract.OfferingID{schemacontract.OfferingID("meshy/creative-lab/" + product + "/prototype")}) {
				paired = true
			}
		}
		if !paired {
			t.Fatalf("%s build is not paired with its prototype", product)
		}
	}
}

func TestChatToolOfferingSelectionRejectsAmbiguousOrUnboundDeclarations(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	designIndex, design := creativeLabTool(t, c, "create_creative_lab_design")
	buildIndex, build := creativeLabTool(t, c, "build_creative_lab_item")
	check := func(t *testing.T, mutate func(tools []ChatToolSchema, offerings generationMap), want string) {
		t.Helper()
		tools := slices.Clone(c.application.Chat.Tools)
		tools[designIndex], tools[buildIndex] = design, build
		tools[designIndex].ArgumentMappings = maps.Clone(design.ArgumentMappings)
		tools[buildIndex].ArgumentMappings = maps.Clone(build.ArgumentMappings)
		offerings := maps.Clone(c.byID)
		mutate(tools, offerings)
		err := validateChatToolOfferingSelection(tools, offerings)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
	t.Run("unknown template", func(t *testing.T) {
		check(t, func(tools []ChatToolSchema, _ generationMap) {
			tools[designIndex].Parameters = json.RawMessage(strings.Replace(string(design.Parameters), "creative-lab-figure-prototype", "creative-lab-unknown", 1))
		}, "creative-lab-unknown")
	})
	t.Run("template bound to the workspace model", func(t *testing.T) {
		check(t, func(tools []ChatToolSchema, _ generationMap) { tools[designIndex].IgnoreUserModel = false }, "ignore_user_model")
	})
	t.Run("template mapped as an Offering parameter", func(t *testing.T) {
		check(t, func(tools []ChatToolSchema, _ generationMap) {
			tools[designIndex].ArgumentMappings["template"] = "template"
		}, "不能映射")
	})
	t.Run("both selectors", func(t *testing.T) {
		check(t, func(tools []ChatToolSchema, _ generationMap) { tools[designIndex].OfferingFromSource = "source_task" }, "不能同时声明")
	})
	t.Run("source without task mapping", func(t *testing.T) {
		check(t, func(tools []ChatToolSchema, _ generationMap) {
			tools[buildIndex].ArgumentMappings["source_task"] = "source-task"
		}, "source-task-from-image-handle")
	})
	t.Run("one prototype pairs with two builds", func(t *testing.T) {
		check(t, func(_ []ChatToolSchema, offerings generationMap) {
			duplicate := offerings["meshy/creative-lab/figure/build"]
			duplicate.ID = "meshy/creative-lab/figure/build-copy"
			duplicate.ModelID = "creative-lab-figure-build-copy"
			offerings[duplicate.ID] = duplicate
		}, "无法唯一选择")
	})
}
