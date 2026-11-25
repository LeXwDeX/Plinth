package generationtool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #548: deleting a personal Skill while a generation that uses it waits for
// approval changes nothing of that call. The turn froze its configuration,
// Skill bodies included, into its checkpoint; approving resumes from the
// stored checkpoint, so the resumed call resolves the same Skill, fingerprints
// the same and applies the same style text. Only a new turn, which freezes a
// configuration without the Skill, no longer finds it.
func TestDeletedSkillLeavesAnApprovalInFlightUnchanged(t *testing.T) {
	executor, refine, configuration := skillChoiceRig(t)
	arguments := map[string]any{"prompt": "a cat", "skill": slugOf(t, configuration, watercolorID)}
	offering := generation.Offering{Features: []string{executor.chat.Skills.Feature}}
	submitted := func(command chat.ToolCommand) (string, promptSkillApplication) {
		t.Helper()
		resolved, skill := executor.resolveCallSkill(command)
		if skill.Refusal != "" {
			t.Fatalf("refused: %s", skill.Refusal)
		}
		hash, err := executor.agentCommandHash(resolved)
		if err != nil {
			t.Fatal(err)
		}
		skills, execution := executor.promptSkills(resolved)
		applied, ok := executor.promptSkillApplication(resolved, offering, generation.TextToImage, skills, execution)
		if !ok {
			t.Fatal("the style Skill was not applied")
		}
		return hash, applied
	}
	preparedHash, preparedStyle := submitted(skillCommand(refine, configuration, "", arguments))

	// The checkpoint the turn stored while waiting for approval.
	stored, err := json.Marshal(chat.TurnCheckpoint{Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	// The person deletes the Skill: the next turn's configuration lacks it.
	var next chat.AgentConfigurationSnapshot
	for _, skill := range configuration.Skills {
		if skill.ID != watercolorID {
			next.Skills = append(next.Skills, skill)
		}
	}

	// Approval resumes from the stored checkpoint, not from today's Skills.
	var checkpoint chat.TurnCheckpoint
	if err := json.Unmarshal(stored, &checkpoint); err != nil {
		t.Fatal(err)
	}
	resumedHash, resumedStyle := submitted(skillCommand(refine, checkpoint.Configuration, "", arguments))
	if resumedHash != preparedHash {
		t.Fatalf("resumed fingerprint %s, want the approved %s", resumedHash, preparedHash)
	}
	if resumedStyle.Prompt != preparedStyle.Prompt || resumedStyle.Template != preparedStyle.Template || !strings.Contains(resumedStyle.Prompt, "水彩") {
		t.Fatalf("resumed style %+v, want %+v", resumedStyle, preparedStyle)
	}

	// A new turn no longer finds the deleted Skill.
	_, skill := executor.resolveCallSkill(skillCommand(refine, &next, "", arguments))
	if want := strings.ReplaceAll(executor.chat.Skills.Directory.Messages.Unknown, "{name}", arguments["skill"].(string)); skill.Refusal != want {
		t.Fatalf("new turn refusal = %q, want %q", skill.Refusal, want)
	}
	if _, listed := chat.DirectorySkill(next.Skills, map[string]bool{}, catalog.SkillSlug(catalog.SkillSchema{ID: watercolorID})); listed {
		t.Fatal("the deleted Skill is still in the new turn's directory")
	}
}
