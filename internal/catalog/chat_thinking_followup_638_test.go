package catalog_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #638: every shipped profile that declares tool_followup names a level it
// offers and below its default; the qwen family, which offers xhigh alone,
// declares none and keeps it. The level applies only when the person chose
// none, and only when the administrator opened it.
func TestShippedToolFollowupLevelsAreCheaperThanTheDefault(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	thinking := loaded.Application().Chat.Agent.Thinking
	for _, model := range []string{"gpt-astra", "gpt-6.1", "claude-x", "qwen3.8-plus", "glm-5.3", "deepseek-flash"} {
		profile, matched := thinking.ProfileFor(model)
		if !matched {
			t.Fatalf("%s matches no profile", model)
		}
		level, declared := profile.TurnEfforts[catalog.ChatTurnEffortToolFollowup]
		if !declared || !slices.Contains(profile.EffortLevels, level) ||
			slices.Index(profile.EffortLevels, level) > slices.Index(profile.EffortLevels, profile.DefaultEffort) {
			t.Fatalf("%s tool_followup = %q (declared %v), levels %v default %q",
				model, level, declared, profile.EffortLevels, profile.DefaultEffort)
		}
	}
	astra, _ := thinking.ProfileFor("gpt-astra")
	for _, test := range []struct {
		turn    string
		chosen  bool
		offered []string
		want    string
	}{
		{"high", false, nil, "medium"},
		{"xhigh", true, nil, "xhigh"},
		{"high", true, nil, "high"},
		{"high", false, []string{"high", "max"}, "high"},
	} {
		if got := astra.EffortForFollowUp(test.turn, test.chosen, test.offered); got != test.want {
			t.Fatalf("EffortForFollowUp(%q, %v, %v) = %q, want %q", test.turn, test.chosen, test.offered, got, test.want)
		}
	}
	qwen, _ := thinking.ProfileFor("qwen-max")
	if got := qwen.EffortForFollowUp("xhigh", false, nil); got != "xhigh" {
		t.Fatalf("qwen-max follow-up = %q", got)
	}
}

// A turn_efforts kind no request reads is refused, as an empty kind is.
func TestTurnEffortsRefuseAnUnknownKind(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	profiles := slices.Clone(app.Chat.Agent.Thinking.ModelProfiles)
	for index := range profiles {
		if len(profiles[index].TurnEfforts) == 0 {
			continue
		}
		efforts := map[string]string{}
		for kind, level := range profiles[index].TurnEfforts {
			efforts[kind] = level
		}
		efforts["tool_follow_up"] = profiles[index].DefaultEffort
		profiles[index].TurnEfforts = efforts
		break
	}
	thinking := *app.Chat.Agent.Thinking
	thinking.ModelProfiles = profiles
	app.Chat.Agent.Thinking = &thinking
	if err := catalog.ValidateApplicationSchema(app); err == nil || !strings.Contains(err.Error(), "未知回合类型") {
		t.Fatalf("an unknown turn_efforts kind = %v", err)
	}
}

// #638: the empty-response retry is bounded.
func TestEmptyResponseRetriesAreBounded(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	if retry := app.Chat.Agent.Retry; retry == nil || retry.EmptyResponseRetries != 1 {
		t.Fatalf("shipped retry = %+v", app.Chat.Agent.Retry)
	}
	retry := *app.Chat.Agent.Retry
	retry.EmptyResponseRetries = 4
	app.Chat.Agent.Retry = &retry
	if err := catalog.ValidateApplicationSchema(app); err == nil || !strings.Contains(err.Error(), "empty_response_retries") {
		t.Fatalf("empty_response_retries 4 = %v", err)
	}
}
