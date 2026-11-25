package agenttools

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #620: reading a style Skill tells the Agent which available models can
// apply it (the Offerings that declare the Schema's Skill feature), so it
// picks one of them the first time; with none it is told so and not to try
// model after model.
func TestUseSkillStyleNamesTheModelsThatApplyIt(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills := product.Application().Chat.Skills
	directory := skills.Directory
	configuration := &chat.AgentConfigurationSnapshot{Skills: append(append([]catalog.SkillSchema{}, skills.SystemSkills...),
		catalog.SkillSchema{ID: "personal-0123456789abcdef0123456789abcdef", Name: "骑砍图标", Slug: "mb-icon-style", Prompt: "骑砍风格的物品图标。"})}
	executor := &Executor{skills: skills, styleSkillModels: func() []string { return []string{"gpt-image-2.6-sunburst", "gpt-image-2.5-sunburst"} }}
	_, content := useSkillCall(t, executor, configuration, "mb-icon-style")
	want := directory.StyleModelsText([]string{"gpt-image-2.6-sunburst", "gpt-image-2.5-sunburst"})
	if want == "" || !strings.Contains(want, "gpt-image-2.6-sunburst、gpt-image-2.5-sunburst") || !strings.Contains(content["instructions"], want) {
		t.Fatalf("style instructions = %q, want the models line %q", content["instructions"], want)
	}
	executor.styleSkillModels = func() []string { return nil }
	_, content = useSkillCall(t, executor, configuration, "mb-icon-style")
	if directory.Messages.NoStyleModel == "" || !strings.Contains(content["instructions"], directory.Messages.NoStyleModel) {
		t.Fatalf("no style model: instructions = %q", content["instructions"])
	}
	// An instruction Skill says nothing about models.
	_, content = useSkillCall(t, executor, configuration, "three-view")
	if strings.Contains(content["instructions"], directory.Messages.NoStyleModel) {
		t.Fatalf("an instruction Skill names style models: %q", content["instructions"])
	}
}
