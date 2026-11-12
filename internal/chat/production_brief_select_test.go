package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// selectBriefUsage is the small purpose table with the two select fields the
// shipped brief declares for game assets (#286).
func selectBriefUsage() catalog.ChatModel3DUsageSchema {
	usage := testModel3DUsage()
	usage.Brief.Fields = append(usage.Brief.Fields,
		catalog.ChatProductionBriefField{Name: "target_engine", Type: "select", Label: "目标引擎",
			Options: []catalog.ChatProductionBriefOption{
				{ID: "unity", Label: "Unity"}, {ID: "unreal", Label: "Unreal Engine"},
				{ID: "godot", Label: "Godot"}, {ID: "web", Label: "Web / three.js"}, {ID: "other", Label: "其他引擎"},
			}},
		catalog.ChatProductionBriefField{Name: "target_platform", Type: "select", Label: "目标平台",
			Options: []catalog.ChatProductionBriefOption{
				{ID: "mobile", Label: "手机"}, {ID: "pc_console", Label: "PC / 主机"},
				{ID: "web", Label: "网页"}, {ID: "xr", Label: "XR（VR / AR / MR）"},
			}},
	)
	return usage
}

func briefReason(t *testing.T, err error) string {
	t.Helper()
	var invalid *chat.BriefError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want a brief refusal", err)
	}
	return invalid.Reason
}

// #286: a select field stores one of its option ids. The stored value is the
// trimmed id, null and an empty value clear it, and any other text is refused
// with the allowed ids and without echoing the refused text.
func TestValidateProductionBriefAcceptsOnlySelectOptions(t *testing.T) {
	usage := selectBriefUsage()
	brief, err := chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, map[string]json.RawMessage{
		"target_engine":   json.RawMessage(`" unity "`),
		"target_platform": json.RawMessage(`"pc_console"`),
		"purpose":         json.RawMessage(`"game_character"`),
	})
	if err != nil {
		t.Fatalf("option ids refused: %v", err)
	}
	if brief.Text("target_engine") != "unity" || brief.Text("target_platform") != "pc_console" {
		t.Fatalf("normalized brief = %+v", brief)
	}
	if string(brief["target_engine"]) != `"unity"` {
		t.Fatalf("stored engine = %s, want the trimmed id", brief["target_engine"])
	}
	for _, clear := range []string{`null`, `""`, `"  "`} {
		cleared, err := chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, map[string]json.RawMessage{
			"target_engine": json.RawMessage(clear),
		})
		if err != nil || cleared.Text("target_engine") != "" {
			t.Fatalf("clearing with %s = %+v %v", clear, cleared, err)
		}
	}

	for _, test := range []struct {
		name  string
		field string
		value string
		want  string
	}{
		{"another case of an id", "target_engine", `"Unity"`, `字段 "target_engine" 只能取这些值之一：unity, unreal, godot, web, other`},
		{"a label instead of an id", "target_platform", `"手机"`, `字段 "target_platform" 只能取这些值之一：mobile, pc_console, web, xr`},
		{"an id of another select", "target_platform", `"unity"`, `只能取这些值之一：mobile`},
		{"a number", "target_engine", `3`, `字段 "target_engine" 必须是字符串`},
		{"a boolean", "target_engine", `true`, `字段 "target_engine" 必须是字符串`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, map[string]json.RawMessage{
				test.field: json.RawMessage(test.value),
			})
			if reason := briefReason(t, err); !strings.Contains(reason, test.want) {
				t.Fatalf("reason = %q, want mention of %q", reason, test.want)
			}
		})
	}

	_, err = chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, map[string]json.RawMessage{
		"target_engine": json.RawMessage(`"SECRET-ENGINE-TEXT"`),
	})
	if reason := briefReason(t, err); strings.Contains(reason, "SECRET-ENGINE-TEXT") {
		t.Fatalf("the refusal echoes the refused text: %q", reason)
	}

	usage.Brief.Messages.SelectUndeclared = `"{field}" must be one of {options}`
	_, err = chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, map[string]json.RawMessage{
		"target_platform": json.RawMessage(`"tablet"`),
	})
	if reason := briefReason(t, err); reason != `"target_platform" must be one of mobile, pc_console, web, xr` {
		t.Fatalf("Schema wording = %q", reason)
	}
}

// #286: a brief written while target_platform was free text keeps that text
// when something else is edited, and shows it as written, but nobody can write
// a new free-text value.
func TestValidateProductionBriefChangeKeepsAStoredFreeTextSelectValue(t *testing.T) {
	usage := selectBriefUsage()
	stored := chat.ProductionBrief{"target_platform": json.RawMessage(`"手机"`), "style": json.RawMessage(`"写实"`)}
	validate := func(previous chat.ProductionBrief, raw map[string]json.RawMessage) (chat.ProductionBrief, error) {
		return chat.ValidateProductionBriefChange(usage.Brief, usage.Profiles, nil, previous, raw)
	}

	kept, err := validate(stored, map[string]json.RawMessage{
		"target_platform": json.RawMessage(`" 手机 "`), "style": json.RawMessage(`"卡通"`),
	})
	if err != nil || kept.Text("target_platform") != "手机" || kept.Text("style") != "卡通" {
		t.Fatalf("the stored value was not kept: %+v %v", kept, err)
	}
	if replaced, err := validate(stored, map[string]json.RawMessage{
		"target_platform": json.RawMessage(`"mobile"`),
	}); err != nil || replaced.Text("target_platform") != "mobile" {
		t.Fatalf("an option over the stored value: %+v %v", replaced, err)
	}

	for _, test := range []struct {
		name     string
		previous chat.ProductionBrief
		raw      map[string]json.RawMessage
	}{
		{"a different free-text value", stored, map[string]json.RawMessage{"target_platform": json.RawMessage(`"平板"`)}},
		{"free text with nothing stored", nil, map[string]json.RawMessage{"target_platform": json.RawMessage(`"手机"`)}},
		{"free text with an empty brief", chat.ProductionBrief{}, map[string]json.RawMessage{"target_platform": json.RawMessage(`"手机"`)}},
		{"the stored text moved to another select", stored, map[string]json.RawMessage{"target_engine": json.RawMessage(`"手机"`)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := validate(test.previous, test.raw)
			if reason := briefReason(t, err); !strings.Contains(reason, "只能取这些值之一") {
				t.Fatalf("reason = %q", reason)
			}
		})
	}
}

// #286: select values round-trip through the stored form, and a brief stored
// before the field became a select decodes with its free text intact and keeps
// it when it is validated against itself.
func TestSelectValuesSurviveEncodeAndDecode(t *testing.T) {
	usage := selectBriefUsage()
	brief, err := chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, map[string]json.RawMessage{
		"purpose": json.RawMessage(`"game_character"`), "target_engine": json.RawMessage(`"godot"`),
		"target_platform": json.RawMessage(`"xr"`), "needs_rig": json.RawMessage(`true`),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := chat.EncodeProductionBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	decoded := chat.DecodeProductionBrief(encoded)
	if decoded.Text("target_engine") != "godot" || decoded.Text("target_platform") != "xr" || !decoded.Flag("needs_rig") {
		t.Fatalf("decoded brief = %+v from %s", decoded, encoded)
	}

	// The form a brief had while target_platform was free text.
	legacy := chat.DecodeProductionBrief(`{"purpose":"game_character","target_platform":"手机 + Switch","style":"写实"}`)
	if legacy.Text("target_platform") != "手机 + Switch" || legacy.Empty() {
		t.Fatalf("legacy brief = %+v", legacy)
	}
	raw := map[string]json.RawMessage{}
	for name, value := range legacy {
		raw[name] = value
	}
	kept, err := chat.ValidateProductionBriefChange(usage.Brief, usage.Profiles, nil, legacy, raw)
	if err != nil || kept.Text("target_platform") != "手机 + Switch" || kept.Text("style") != "写实" {
		t.Fatalf("legacy brief revalidated against itself = %+v %v", kept, err)
	}
	reencoded, err := chat.EncodeProductionBrief(kept)
	if err != nil || chat.DecodeProductionBrief(reencoded).Text("target_platform") != "手机 + Switch" {
		t.Fatalf("legacy brief after the round trip = %q %v", reencoded, err)
	}
}

// #286: through the service, a legacy free-text platform survives a whole
// replacement and a patch that leave it alone, and is gone for good once an
// option replaces it.
func TestProductionBriefWritesKeepALegacyFreeTextPlatform(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Model3DUsage = selectBriefUsage()
	})
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now,
		Brief: chat.ProductionBrief{"purpose": json.RawMessage(`"game_character"`), "target_platform": json.RawMessage(`"手机"`)},
	}); err != nil {
		t.Fatal(err)
	}

	// The brief panel sends the whole declared object back, legacy value
	// included, when only the style changed.
	_, stored, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"game_character"`), "target_platform": json.RawMessage(`"手机"`),
		"style": json.RawMessage(`"卡通"`),
	})
	if err != nil || stored.Text("target_platform") != "手机" || stored.Text("style") != "卡通" {
		t.Fatalf("whole replacement = %+v %v", stored, err)
	}
	// The Agent patches another select; the legacy text is untouched.
	_, stored, err = service.UpdateProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"target_engine": json.RawMessage(`"godot"`),
	})
	if err != nil || stored.Text("target_engine") != "godot" || stored.Text("target_platform") != "手机" {
		t.Fatalf("patch = %+v %v", stored, err)
	}
	// An option replaces it, and the free text can no longer be written back.
	_, stored, err = service.UpdateProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"target_platform": json.RawMessage(`"xr"`),
	})
	if err != nil || stored.Text("target_platform") != "xr" {
		t.Fatalf("option over legacy = %+v %v", stored, err)
	}
	if _, _, err := service.UpdateProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"target_platform": json.RawMessage(`"手机"`),
	}); briefReason(t, err) == "" {
		t.Fatal("the legacy text was accepted after an option replaced it")
	}
	brief, err := service.SessionBrief(ctx, "owner-a", "session-a")
	if err != nil || brief.Text("target_platform") != "xr" {
		t.Fatalf("stored after the refused write = %+v %v", brief, err)
	}
}
