package generationtool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const (
	watercolorID = "personal-0123456789abcdef0123456789abcdef"
	inkID        = "personal-fedcba9876543210fedcba9876543210"
	howtoID      = "personal-00000000000000000000000000000001"
)

// skillChoiceRig is a generation executor over the shipped Schema and a turn
// whose frozen configuration holds the system Skills, two personal style
// Skills and one personal instruction Skill.
func skillChoiceRig(t *testing.T) (*Executor, catalog.ChatToolSchema, *chat.AgentConfigurationSnapshot) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	var refine catalog.ChatToolSchema
	for _, tool := range app.Chat.Tools {
		if tool.Name == "refine_image" {
			refine = tool
		}
	}
	if refine.Name == "" {
		t.Fatal("refine_image is not declared")
	}
	skills := append([]catalog.SkillSchema{}, app.Chat.Skills.SystemSkills...)
	skills = append(skills,
		catalog.SkillSchema{ID: watercolorID, Name: "水彩", Prompt: "柔和的水彩笔触。"},
		catalog.SkillSchema{ID: inkID, Name: "水墨", Prompt: "水墨晕染。"},
		catalog.SkillSchema{ID: howtoID, Name: "做法", Slug: "my-howto", Apply: catalog.SkillApplyInstruction, Prompt: "先列清单。"},
	)
	return &Executor{chat: app.Chat}, refine, &chat.AgentConfigurationSnapshot{Skills: skills}
}

func skillCommand(tool catalog.ChatToolSchema, configuration *chat.AgentConfigurationSnapshot, picked string, arguments map[string]any) chat.ToolCommand {
	encoded, _ := json.Marshal(arguments)
	return chat.ToolCommand{
		OwnerID: "owner", SessionID: "session", AssistantMessageID: "assistant",
		Call:          agents.ToolCall{ID: "call-1", Name: tool.Name, Arguments: encoded},
		Tool:          tool,
		Options:       chat.TurnOptions{SkillID: picked},
		Configuration: configuration,
	}
}

func slugOf(t *testing.T, configuration *chat.AgentConfigurationSnapshot, id string) string {
	t.Helper()
	for _, skill := range configuration.Skills {
		if skill.ID == id {
			return catalog.SkillSlug(skill)
		}
	}
	t.Fatalf("no Skill %q", id)
	return ""
}

// #548: the Agent applies a personal style Skill to one call by its standard
// name; the resolved Skill ID joins the call's fingerprint.
func TestAgentStyleSkillResolvesToItsIDForThisCallOnly(t *testing.T) {
	executor, refine, configuration := skillChoiceRig(t)
	command, skill := executor.resolveCallSkill(skillCommand(refine, configuration, "",
		map[string]any{"prompt": "a cat", "skill": slugOf(t, configuration, watercolorID)}))
	if skill.Refusal != "" || skill.Note != "" {
		t.Fatalf("skill = %+v", skill)
	}
	if command.Options.SkillID != watercolorID {
		t.Fatalf("resolved Skill = %q, want %q", command.Options.SkillID, watercolorID)
	}
	if skill.Used == nil || skill.Used.Source != "agent" || !skill.Used.Applied || skill.Used.DisplayName != "水彩" {
		t.Fatalf("used = %+v", skill.Used)
	}
	evidence := skillChoiceEvidence(skill)
	if evidence == nil || evidence.ID != watercolorID || evidence.Name != "水彩" || evidence.Source != "agent" {
		t.Fatalf("approval evidence = %+v", evidence)
	}
	// The same call without a skill applies none.
	plain, none := executor.resolveCallSkill(skillCommand(refine, configuration, "", map[string]any{"prompt": "a cat"}))
	if plain.Options.SkillID != "" || none.Used != nil {
		t.Fatalf("a call without skill resolved %q / %+v", plain.Options.SkillID, none.Used)
	}
}

// #548: a call with a skill and the same call without one are different
// approvals, and the command a resume rebuilds from the frozen turn
// fingerprints exactly like the first preparation.
func TestSkillArgumentChangesTheFingerprintAndResumeRecomputesTheSame(t *testing.T) {
	executor, refine, configuration := skillChoiceRig(t)
	with := skillCommand(refine, configuration, "", map[string]any{"prompt": "a cat", "skill": slugOf(t, configuration, watercolorID)})
	without := skillCommand(refine, configuration, "", map[string]any{"prompt": "a cat"})
	hash := func(command chat.ToolCommand) string {
		t.Helper()
		resolved, _ := executor.resolveCallSkill(command)
		value, err := executor.agentCommandHash(resolved)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	prepared := hash(with)
	if prepared == hash(without) {
		t.Fatal("a call with a skill fingerprints like the same call without one")
	}
	// A resume rebuilds the command from the frozen turn and resolves again.
	if resumed := hash(with); resumed != prepared {
		t.Fatalf("resume fingerprint %s, want the prepared %s", resumed, prepared)
	}
	// A different style Skill is a different approval.
	other := skillCommand(refine, configuration, "", map[string]any{"prompt": "a cat", "skill": slugOf(t, configuration, inkID)})
	if hash(other) == prepared {
		t.Fatal("two different style Skills fingerprint the same")
	}
	// A picked tool-binding Skill styles nothing: it is cleared before the
	// first preparation and again on resume, so both fingerprint alike.
	picked := skillCommand(refine, configuration, "system-three-view", map[string]any{"prompt": "a cat"})
	resolved, _ := executor.resolveCallSkill(picked)
	if resolved.Options.SkillID != "" {
		t.Fatalf("a picked tool Skill stayed on the generation call: %q", resolved.Options.SkillID)
	}
	if hash(picked) != hash(without) {
		t.Fatal("a picked tool Skill changed the generation call's fingerprint")
	}
}

// #548: the person's own style Skill always wins. A different Skill the Agent
// named is ignored and the result says so; the same one is no conflict.
func TestPersonPickedStyleSkillWinsOverTheAgents(t *testing.T) {
	executor, refine, configuration := skillChoiceRig(t)
	inkSlug := slugOf(t, configuration, inkID)
	command, skill := executor.resolveCallSkill(skillCommand(refine, configuration, watercolorID,
		map[string]any{"prompt": "a cat", "skill": inkSlug}))
	if command.Options.SkillID != watercolorID {
		t.Fatalf("resolved Skill = %q, want the person's %q", command.Options.SkillID, watercolorID)
	}
	if skill.Used == nil || skill.Used.Source != "user" || skill.Used.ID != watercolorID {
		t.Fatalf("used = %+v", skill.Used)
	}
	if !strings.Contains(skill.Note, "水彩") || !strings.Contains(skill.Note, inkSlug) {
		t.Fatalf("note = %q", skill.Note)
	}
	result := chat.ToolResult{AgentContent: `{"success":true}`}
	executor.attachCallSkill(command, &result, skill)
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil || content[refine.ResultFields.SkillNote] != skill.Note {
		t.Fatalf("result content = %s", result.AgentContent)
	}
	used := chat.UsedSkills([]chat.ToolCall{{ID: "call-1", Summary: result.Summary}})
	if len(used) != 1 || used[0].ID != watercolorID || used[0].Source != "user" {
		t.Fatalf("used skills = %+v", used)
	}
	_, same := executor.resolveCallSkill(skillCommand(refine, configuration, watercolorID,
		map[string]any{"prompt": "a cat", "skill": slugOf(t, configuration, watercolorID)}))
	if same.Note != "" {
		t.Fatalf("the same Skill was reported as ignored: %q", same.Note)
	}
	// A picked tool-binding Skill leaves the Agent's style Skill in effect.
	command, skill = executor.resolveCallSkill(skillCommand(refine, configuration, "system-three-view",
		map[string]any{"prompt": "a cat", "skill": inkSlug}))
	if command.Options.SkillID != inkID || skill.Used == nil || skill.Used.Source != "agent" {
		t.Fatalf("with a picked tool Skill the Agent's Skill resolved to %q / %+v", command.Options.SkillID, skill.Used)
	}
}

// #548: a skill argument that names no listed Skill, or a Skill that does not
// style a prompt, is refused with readable Schema text before anything is
// prepared; an instruction Skill is never applied as a style.
func TestSkillArgumentRefusesUnknownAndNonStyleSkills(t *testing.T) {
	executor, refine, configuration := skillChoiceRig(t)
	for slug, want := range map[string]string{
		"no-such-skill": executor.chat.Skills.Directory.Messages.Unknown,
		"my-howto":      executor.chat.Skills.Directory.Messages.NotStyle,
		"three-view":    executor.chat.Skills.Directory.Messages.NotStyle,
	} {
		command, skill := executor.resolveCallSkill(skillCommand(refine, configuration, "", map[string]any{"prompt": "a cat", "skill": slug}))
		if skill.Refusal != strings.ReplaceAll(want, "{name}", slug) || command.Options.SkillID != "" {
			t.Errorf("skill %q: refusal %q, resolved %q", slug, skill.Refusal, command.Options.SkillID)
		}
		result := chat.ToolResult{AgentContent: `{"success":false}`}
		executor.attachCallSkill(command, &result, skill)
		if len(result.Summary) != 0 {
			t.Errorf("skill %q: a refused call recorded a used Skill", slug)
		}
	}
	skills, execution := executor.promptSkills(skillCommand(refine, configuration, howtoID, nil))
	offering := generation.Offering{Features: []string{executor.chat.Skills.Feature}}
	if _, applied := executor.promptSkillApplication(skillCommand(refine, configuration, howtoID, nil),
		offering, generation.TextToImage, skills, execution); applied {
		t.Fatal("an instruction Skill was applied as a style")
	}
	// A picked instruction Skill styles nothing either.
	command, _ := executor.resolveCallSkill(skillCommand(refine, configuration, howtoID, map[string]any{"prompt": "a cat"}))
	if command.Options.SkillID != "" {
		t.Fatalf("a picked instruction Skill stayed on the generation call: %q", command.Options.SkillID)
	}
	// An Agent Skill on a model without the Skill feature gets a readable answer.
	_, skill := executor.resolveCallSkill(skillCommand(refine, configuration, "", map[string]any{"prompt": "a cat", "skill": slugOf(t, configuration, inkID)}))
	if refusal := executor.skillUnsupported(skill); !strings.Contains(refusal, "水墨") {
		t.Fatalf("unsupported refusal = %q", refusal)
	}
}
