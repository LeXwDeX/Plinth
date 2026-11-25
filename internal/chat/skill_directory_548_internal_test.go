package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func personalStyle(index int, name string) catalog.SkillSchema {
	return catalog.SkillSchema{ID: fmt.Sprintf("%s%032x", catalog.PersonalSkillIDPrefix, index), Name: name,
		Prompt: name + "的画风，笔触与配色都很讲究。"}
}

// #548: the directory lists the turn's system and personal Skills by standard
// name, display name and description, leaves out the person's own pick for
// the turn and the published Skills, and lists a tool-bound system Skill only
// while its tool is offered.
func TestSkillDirectoryListsTheTurnsSkills(t *testing.T) {
	orchestrator, schema := scopeTestOrchestrator(t)
	directory := schema.Skills.Directory
	snapshot := &AgentConfigurationSnapshot{Skills: append(append([]catalog.SkillSchema{}, schema.Skills.SystemSkills...),
		catalog.SkillSchema{ID: "shared-1", Name: "共享画风", Prompt: "共享。"},
		personalStyle(1, "水彩"),
		catalog.SkillSchema{ID: catalog.PersonalSkillIDPrefix + strings.Repeat("e", 32), Name: "做法", Slug: "my-howto",
			Apply: catalog.SkillApplyInstruction, Description: "整理需求清单的做法。用户要清单时使用。", Prompt: "先列清单。"},
	)}
	ctx := context.WithValue(context.Background(), configurationContextKey{}, snapshot)
	block := orchestrator.skillDirectoryPrompt(ctx, orchestrator.registry, "2d", TurnOptions{})
	for _, want := range []string{"- three-view（三视图）：", "- four-view（四视图）：", "（水彩）：画风 Skill「水彩」",
		"- my-howto（做法）：整理需求清单的做法。"} {
		if !strings.Contains(block, want) {
			t.Errorf("the directory lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "共享画风") || strings.Contains(block, "{entries}") || strings.HasSuffix(block, "\n") {
		t.Errorf("directory = %q", block)
	}
	if !strings.HasPrefix(block, strings.SplitN(directory.Template, "{entries}", 2)[0]) {
		t.Errorf("the directory does not start with its Schema frame: %q", block)
	}
	// The person's own pick is already in the turn: it is not listed again,
	// nor is another Skill bound to the same tool (#611).
	picked := orchestrator.skillDirectoryPrompt(ctx, orchestrator.registry, "2d", TurnOptions{SkillID: "system-three-view"})
	if strings.Contains(picked, "- three-view（") || strings.Contains(picked, "- four-view（") || !strings.Contains(picked, "- my-howto（") {
		t.Errorf("with three-view picked the directory = %q", picked)
	}
	// A 3D-only turn offers neither create_three_view nor a 2D generation
	// tool: the tool-bound Skills and the style Skill are not listed.
	threeD := orchestrator.skillDirectoryPrompt(ctx, orchestrator.registry, "3d", TurnOptions{})
	if strings.Contains(threeD, "three-view") || strings.Contains(threeD, "水彩") || !strings.Contains(threeD, "my-howto") {
		t.Errorf("3d directory = %q", threeD)
	}
}

// #548: personal Skills fill their bounded part newest first; the rest are
// counted in one closing line and stay selectable by hand.
func TestPersonalDirectoryPartIsNewestFirstAndBounded(t *testing.T) {
	_, schema := scopeTestOrchestrator(t)
	directory := schema.Skills.Directory
	var personal []catalog.SkillSchema
	for index := 1; index <= 20; index++ {
		personal = append(personal, personalStyle(index, fmt.Sprintf("风格%02d", index)))
	}
	part := personalDirectoryPart(personal, directory)
	if length := utf8.RuneCountInString(part); length > directory.MaxPersonalCharacters {
		t.Fatalf("personal part is %d characters, over %d", length, directory.MaxPersonalCharacters)
	}
	lines := strings.Split(strings.TrimSuffix(part, "\n"), "\n")
	listed := len(lines) - 1
	if listed < 1 || listed >= len(personal) {
		t.Fatalf("listed %d of %d:\n%s", listed, len(personal), part)
	}
	for position, line := range lines[:listed] {
		if want := fmt.Sprintf("（风格%02d）", len(personal)-position); !strings.Contains(line, want) {
			t.Fatalf("line %d = %q, want %s (newest first)", position, line, want)
		}
	}
	closing := strings.ReplaceAll(directory.OmittedTemplate, "{count}", fmt.Sprint(len(personal)-listed))
	if lines[listed] != closing {
		t.Fatalf("closing line = %q, want %q", lines[listed], closing)
	}
	// Few enough Skills are all listed without a closing line.
	if part := personalDirectoryPart(personal[:2], directory); strings.Count(part, "\n") != 2 || strings.Contains(part, "未列出") {
		t.Fatalf("two Skills part = %q", part)
	}
}

// #548: the reply's used Skills come from its persisted call summaries, once
// per Skill and source, in call order.
func TestUsedSkillsAreReadFromPersistedCalls(t *testing.T) {
	agent := UsedSkillSummary(UsedSkill{ID: "system-three-view", DisplayName: "三视图", Source: "agent", Applied: true})
	user := UsedSkillSummary(UsedSkill{ID: "personal-a", DisplayName: "水彩", Source: "user", Applied: true})
	calls := []ToolCall{
		{ID: "1", Name: "use_skill", Summary: agent},
		{ID: "2", Name: "inspect_model", Summary: json.RawMessage(`{"tool":"inspect"}`)},
		{ID: "3", Name: "refine_image", Summary: user},
		{ID: "4", Name: "refine_image", Summary: user},
		{ID: "5", Name: "create_three_view"},
	}
	used := UsedSkills(calls)
	if len(used) != 2 || used[0].ID != "system-three-view" || used[1].ID != "personal-a" || used[1].Source != "user" {
		t.Fatalf("used = %+v", used)
	}
	if UsedSkills(nil) != nil {
		t.Fatal("a reply without calls used Skills")
	}
}
