package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #295: the values the production brief gives an Offering are resolved in one
// place for the Agent's generation tools and for the direct task API. These
// tests pin the direct side (the Agent side is pinned by the generationtool
// tests, which run the same resolver): only the parameters the request left
// unset are filled, the advised ones carry the purpose's reason and source, and
// a session without a purpose is left exactly as it was.

const (
	purposeParamsOffering = "test/model/text-to-3d"
	purposeParamsRig      = "test/model/rigging"
	purposeParamsImage    = "test/image/text-to-image"
	purposeParamsReason   = "角色需要受控面数与 UV。"
	purposeParamsImageWhy = "立绘需要竖画幅与高分辨率。"
)

func purposeParamsModel() generation.Offering {
	maximum := 2000000.0
	return generation.Offering{
		ID: purposeParamsOffering, Capability: generation.TextTo3D, Domain: generation.DomainModel3D, Enabled: true,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt},
			{Name: "face_limit", Type: "number", Integer: true, AdjustmentPolicy: "advised",
				Semantic: generation.SemanticPolycount, Default: json.RawMessage(`2000000`), Maximum: &maximum},
			{Name: "quad", Type: "boolean", AdjustmentPolicy: "advised", Default: json.RawMessage(`false`)},
			{Name: "texture_quality", Type: "select", AdjustmentPolicy: "advised", Default: json.RawMessage(`"detailed"`),
				Options: []generation.ParameterOption{{Value: "standard"}, {Value: "detailed"}}},
			{Name: "pose_mode", Type: "select", Default: json.RawMessage(`"free"`),
				Options: []generation.ParameterOption{{Value: "free"}, {Value: "a-pose"}}},
			{Name: "version", Type: "text", Fixed: true, Default: json.RawMessage(`"v1"`)},
		},
	}
}

func purposeParamsRigging() generation.Offering {
	return generation.Offering{
		ID: purposeParamsRig, Capability: generation.Rigging, Domain: generation.DomainProcessing, Enabled: true,
		Parameters: []generation.Parameter{
			{Name: "spec", Type: "select", Default: json.RawMessage(`"tripo"`),
				Options: []generation.ParameterOption{{Value: "tripo"}, {Value: "unity"}, {Value: "unreal"}}},
		},
	}
}

func purposeParamsPicture() generation.Offering {
	return generation.Offering{
		ID: purposeParamsImage, Capability: generation.TextToImage, Domain: generation.DomainImage, Enabled: true,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt},
			{Name: "aspect_ratio", Type: "select", Default: json.RawMessage(`"1:1"`),
				Options: []generation.ParameterOption{{Value: "1:1"}, {Value: "3:4"}, {Value: "16:9"}}},
			{Name: "image_size", Type: "select", AdjustmentPolicy: "advised", Default: json.RawMessage(`"1K"`),
				Options: []generation.ParameterOption{{Value: "1K"}, {Value: "4K"}}},
		},
	}
}

func purposeParamsUsage() catalog.ChatModel3DUsageSchema {
	pcConsole := 100000
	return catalog.ChatModel3DUsageSchema{
		DefaultProfile: "render",
		PlatformField:  "target_platform",
		BudgetLimits: []catalog.ChatModel3DUsageBudgetLimit{
			{Semantic: generation.SemanticPolycount, Bound: catalog.BudgetBoundMaxTriangles, ItemTemplate: "{model} {value}"},
		},
		Profiles: []catalog.ChatModel3DUsageProfile{
			{ID: "render", Label: "展示/渲染", Reason: "展示用途优先完整细节。",
				Budget: catalog.ChatModel3DUsageBudget{MaxTriangles: 2000000}},
			{ID: "game_character", Label: "游戏/实时·角色", Reason: purposeParamsReason,
				Budget: catalog.ChatModel3DUsageBudget{MaxTriangles: 60000},
				ParameterOverrides: map[string]map[string]json.RawMessage{
					purposeParamsOffering: {
						"face_limit": json.RawMessage(`10000`), "quad": json.RawMessage(`true`),
						"texture_quality": json.RawMessage(`"standard"`), "pose_mode": json.RawMessage(`"a-pose"`),
						// A fixed parameter is not public: no layer may set it.
						"version": json.RawMessage(`"v9"`),
					},
				},
				Platforms: []catalog.ChatModel3DUsagePlatform{{
					IDs:    []string{"pc_console"},
					Budget: catalog.ChatModel3DUsagePlatformBudget{MaxTriangles: &pcConsole},
					ParameterOverrides: map[string]map[string]json.RawMessage{
						purposeParamsOffering: {"face_limit": json.RawMessage(`25000`)},
					},
				}}},
		},
		Brief: catalog.ChatProductionBriefSchema{
			Fields: []catalog.ChatProductionBriefField{
				{Name: "purpose", Type: "profile", Label: "用途"},
				{Name: "image_use", Type: "image_profile", Label: "2D 用途"},
				{Name: "style", Type: "text", Label: "风格", MaximumCharacters: 20},
				{Name: "target_engine", Type: "select", Label: "目标引擎", Options: []catalog.ChatProductionBriefOption{
					{ID: "unity", Label: "Unity"}, {ID: "unreal", Label: "Unreal"}, {ID: "godot", Label: "Godot"},
				}},
				{Name: "target_platform", Type: "select", Label: "目标平台", Options: []catalog.ChatProductionBriefOption{
					{ID: "mobile", Label: "手机"}, {ID: "pc_console", Label: "PC / 主机"},
				}},
				{Name: "triangle_budget", Type: "integer", Label: "三角面预算", Minimum: 100, Maximum: 2000000},
			},
			ParameterDefaults: []catalog.ChatProductionBriefDefault{{
				Field: "target_engine", Value: "unity",
				ParameterOverrides: map[string]map[string]json.RawMessage{
					purposeParamsRig: {"spec": json.RawMessage(`"unity"`)},
				},
			}},
		},
	}
}

func purposeParamsImageUsage() catalog.ChatImage2DUsageSchema {
	return catalog.ChatImage2DUsageSchema{
		DefaultProfile: "concept_art",
		Profiles: []catalog.ChatImage2DUsageProfile{
			{ID: "concept_art", Label: "概念设定图", Reason: "概念图优先构图。",
				ParameterOverrides: map[string]map[string]json.RawMessage{
					purposeParamsImage: {"aspect_ratio": json.RawMessage(`"16:9"`)},
				}},
			{ID: "illustration", Label: "角色立绘", Reason: purposeParamsImageWhy,
				ParameterOverrides: map[string]map[string]json.RawMessage{
					purposeParamsImage: {"aspect_ratio": json.RawMessage(`"3:4"`), "image_size": json.RawMessage(`"4K"`)},
				}},
		},
	}
}

func purposeParamsResolver() chat.PurposeParameters {
	return chat.PurposeParameters{Model3D: purposeParamsUsage(), Image2D: purposeParamsImageUsage()}
}

func purposeParamsBrief(t *testing.T, values map[string]any) chat.ProductionBrief {
	t.Helper()
	brief := chat.ProductionBrief{}
	for name, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		brief[name] = encoded
	}
	return brief
}

func purposeParamsDecode(t *testing.T, input json.RawMessage) map[string]any {
	t.Helper()
	var values map[string]any
	if err := json.Unmarshal(input, &values); err != nil {
		t.Fatalf("input %s: %v", input, err)
	}
	return values
}

func purposeParamsApply(t *testing.T, brief chat.ProductionBrief, offering generation.Offering, input string,
	reasons map[string]string) chat.DirectPurposeInput {
	t.Helper()
	applied, err := purposeParamsResolver().ApplyToDirectInput(brief, offering, json.RawMessage(input), reasons)
	if err != nil {
		t.Fatal(err)
	}
	return applied
}

// The request's own values stay its own; the purpose fills what it left unset,
// and only the advised values carry its reason and source. A fixed parameter is
// never filled.
func TestApplyToDirectInputFillsOnlyWhatTheRequestLeftUnset(t *testing.T) {
	brief := purposeParamsBrief(t, map[string]any{"purpose": "game_character"})
	applied := purposeParamsApply(t, brief, purposeParamsModel(), `{"prompt":"a knight","quad":false}`, nil)
	want := map[string]any{
		"prompt": "a knight", "quad": false, "face_limit": float64(10000),
		"texture_quality": "standard", "pose_mode": "a-pose",
	}
	if got := purposeParamsDecode(t, applied.Input); !reflect.DeepEqual(got, want) {
		t.Fatalf("input = %v, want %v", got, want)
	}
	wantReasons := map[string]string{"face_limit": purposeParamsReason, "texture_quality": purposeParamsReason}
	if !reflect.DeepEqual(applied.Reasons, wantReasons) {
		t.Fatalf("reasons = %v, want %v (quad is the request's, pose_mode is not advised)", applied.Reasons, wantReasons)
	}
	wantSources := map[string]string{"face_limit": "profile:game_character", "texture_quality": "profile:game_character"}
	if !reflect.DeepEqual(applied.Sources, wantSources) {
		t.Fatalf("sources = %v, want %v", applied.Sources, wantSources)
	}
}

// A request that sets the same value as the purpose still owns it: no purpose
// credit, so an advised change is the caller's to explain.
func TestApplyToDirectInputKeepsAnEqualRequestValueTheCallers(t *testing.T) {
	brief := purposeParamsBrief(t, map[string]any{"purpose": "game_character"})
	applied := purposeParamsApply(t, brief, purposeParamsModel(), `{"prompt":"x","face_limit":10000}`, nil)
	if _, credited := applied.Reasons["face_limit"]; credited {
		t.Fatalf("an equal request value was credited to the purpose: %v", applied.Reasons)
	}
	if _, credited := applied.Sources["face_limit"]; credited {
		t.Fatalf("an equal request value got the purpose source: %v", applied.Sources)
	}
	if _, filled := applied.Reasons["quad"]; !filled {
		t.Fatalf("the unset advised quad lost its purpose reason: %v", applied.Reasons)
	}
}

// A reason the caller gave for a parameter the purpose fills stays the caller's:
// the frozen adjustment keeps the agent source, as an Agent's own reason does.
func TestApplyToDirectInputKeepsTheCallersReason(t *testing.T) {
	brief := purposeParamsBrief(t, map[string]any{"purpose": "game_character"})
	given := map[string]string{"face_limit": "手调"}
	applied := purposeParamsApply(t, brief, purposeParamsModel(), `{"prompt":"x"}`, given)
	if applied.Reasons["face_limit"] != "手调" {
		t.Fatalf("reasons = %v, want the caller's reason for face_limit", applied.Reasons)
	}
	if _, labelled := applied.Sources["face_limit"]; labelled {
		t.Fatalf("a caller's reason got a purpose source: %v", applied.Sources)
	}
	if applied.Reasons["quad"] != purposeParamsReason || applied.Sources["quad"] != "profile:game_character" {
		t.Fatalf("quad lost its purpose evidence: %v / %v", applied.Reasons, applied.Sources)
	}
	if len(given) != 1 || given["face_limit"] != "手调" {
		t.Fatalf("the caller's map was changed: %v", given)
	}
}

// A session with no brief, a brief without a purpose and an input that is not
// an object are left exactly as they were: the request's own bytes.
func TestApplyToDirectInputLeavesASessionWithoutAPurposeAlone(t *testing.T) {
	raw := `{ "prompt" : "a knight" }`
	tests := []struct {
		name  string
		brief chat.ProductionBrief
		input string
	}{
		{"no brief", nil, raw},
		{"an empty brief", chat.ProductionBrief{}, raw},
		{"a brief without a purpose", purposeParamsBrief(t, map[string]any{"style": "写实"}), raw},
		{"a purpose that is no longer declared", purposeParamsBrief(t, map[string]any{"purpose": "retired"}), raw},
		{"an input that is no object", purposeParamsBrief(t, map[string]any{"purpose": "game_character"}), `[1,2]`},
		{"a null input", purposeParamsBrief(t, map[string]any{"purpose": "game_character"}), `null`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reasons := map[string]string{"face_limit": "reason"}
			applied := purposeParamsApply(t, test.brief, purposeParamsModel(), test.input, reasons)
			if !bytes.Equal(applied.Input, []byte(test.input)) {
				t.Fatalf("input = %s, want the request's bytes %s", applied.Input, test.input)
			}
			if !reflect.DeepEqual(applied.Reasons, reasons) || len(applied.Sources) != 0 {
				t.Fatalf("reasons = %v, sources = %v", applied.Reasons, applied.Sources)
			}
		})
	}
}

// The brief's target platform picks the purpose's variant, and the brief's own
// triangle budget wins over both, exactly as it does for the Agent.
func TestApplyToDirectInputFollowsThePlatformAndTheTriangleBudget(t *testing.T) {
	tests := []struct {
		name  string
		brief map[string]any
		want  float64
	}{
		{"the purpose as declared", map[string]any{"purpose": "game_character"}, 10000},
		{"a platform without a variant", map[string]any{"purpose": "game_character", "target_platform": "mobile"}, 10000},
		{"the platform variant", map[string]any{"purpose": "game_character", "target_platform": "pc_console"}, 25000},
		{"a budget below the override", map[string]any{"purpose": "game_character", "triangle_budget": 5000}, 5000},
		{"a budget above the override", map[string]any{"purpose": "game_character", "triangle_budget": 30000}, 10000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applied := purposeParamsApply(t, purposeParamsBrief(t, test.brief), purposeParamsModel(), `{"prompt":"x"}`, nil)
			if got := purposeParamsDecode(t, applied.Input)["face_limit"]; got != test.want {
				t.Fatalf("face_limit = %v, want %v", got, test.want)
			}
			if applied.Sources["face_limit"] != "profile:game_character" || applied.Reasons["face_limit"] != purposeParamsReason {
				t.Fatalf("face_limit evidence = %v / %v", applied.Reasons, applied.Sources)
			}
		})
	}
}

// A purpose the Offering gives no override still bounds the face count by its
// budget (#129): a value the budget fits is credited to the purpose.
func TestApplyToDirectInputFitsTheBudgetOfAnOfferingTheTableDoesNotName(t *testing.T) {
	offering := purposeParamsModel()
	offering.ID = "test/other/text-to-3d"
	applied := purposeParamsApply(t, purposeParamsBrief(t, map[string]any{"purpose": "game_character"}),
		offering, `{"prompt":"x"}`, nil)
	if got := purposeParamsDecode(t, applied.Input)["face_limit"]; got != float64(60000) {
		t.Fatalf("face_limit = %v, want the purpose budget 60000", got)
	}
	if applied.Sources["face_limit"] != "profile:game_character" {
		t.Fatalf("sources = %v", applied.Sources)
	}
	render := purposeParamsApply(t, purposeParamsBrief(t, map[string]any{"purpose": "render"}),
		offering, `{"prompt":"x"}`, nil)
	if _, filled := purposeParamsDecode(t, render.Input)["face_limit"]; filled {
		t.Fatalf("a budget the default already fits filled a value: %s", render.Input)
	}
}

// The target engine fills the skeleton naming of a rigging step with or without
// a purpose (#277); it is not an advised value, so it needs no reason.
func TestApplyToDirectInputFillsTheEngineDefaultWithoutAPurpose(t *testing.T) {
	brief := purposeParamsBrief(t, map[string]any{"target_engine": "unity"})
	applied := purposeParamsApply(t, brief, purposeParamsRigging(), `{}`, nil)
	if got := purposeParamsDecode(t, applied.Input); !reflect.DeepEqual(got, map[string]any{"spec": "unity"}) {
		t.Fatalf("input = %v", got)
	}
	if len(applied.Reasons) != 0 || len(applied.Sources) != 0 {
		t.Fatalf("a non-advised default got evidence: %v / %v", applied.Reasons, applied.Sources)
	}
	kept := purposeParamsApply(t, brief, purposeParamsRigging(), `{"spec":"unreal"}`, nil)
	if got := purposeParamsDecode(t, kept.Input)["spec"]; got != "unreal" {
		t.Fatalf("the request's spec was replaced by %v", got)
	}
	other := purposeParamsApply(t, purposeParamsBrief(t, map[string]any{"target_engine": "godot"}),
		purposeParamsRigging(), `{}`, nil)
	if !bytes.Equal(other.Input, []byte(`{}`)) {
		t.Fatalf("an engine without a default changed the input: %s", other.Input)
	}
}

// A 2D call follows the brief's 2D purpose. The Agent falls back to the Schema's
// default purpose for an open question; a person who named none asked for none.
func TestApplyToDirectInputFollowsOnlyAnExplicit2DPurpose(t *testing.T) {
	picture := purposeParamsPicture()
	applied := purposeParamsApply(t, purposeParamsBrief(t, map[string]any{"image_use": "illustration"}),
		picture, `{"prompt":"a knight"}`, nil)
	want := map[string]any{"prompt": "a knight", "aspect_ratio": "3:4", "image_size": "4K"}
	if got := purposeParamsDecode(t, applied.Input); !reflect.DeepEqual(got, want) {
		t.Fatalf("input = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(applied.Reasons, map[string]string{"image_size": purposeParamsImageWhy}) ||
		!reflect.DeepEqual(applied.Sources, map[string]string{"image_size": "profile:illustration"}) {
		t.Fatalf("evidence = %v / %v (aspect_ratio is not advised)", applied.Reasons, applied.Sources)
	}
	for name, brief := range map[string]chat.ProductionBrief{
		"a brief without a 2D purpose": purposeParamsBrief(t, map[string]any{"style": "写实"}),
		"a 3D purpose only":            purposeParamsBrief(t, map[string]any{"purpose": "game_character"}),
	} {
		t.Run(name, func(t *testing.T) {
			raw := `{"prompt":"a knight"}`
			unchanged := purposeParamsApply(t, brief, picture, raw, nil)
			if !bytes.Equal(unchanged.Input, []byte(raw)) {
				t.Fatalf("input = %s, want the request's bytes", unchanged.Input)
			}
		})
	}
	resolver := purposeParamsResolver()
	open := purposeParamsBrief(t, map[string]any{"style": "写实"})
	if profile, found := resolver.Profile(string(generation.DomainImage), open, false); !found || profile.ID != "concept_art" {
		t.Fatalf("the Agent's open question lost the default purpose: %+v %v", profile, found)
	}
	if _, found := resolver.Profile(string(generation.DomainImage), open, true); found {
		t.Fatal("an explicit-only lookup fell back to the default purpose")
	}
}

// The shipped Catalog: the budget measures a face count by the step's whole
// combination (#139), so the direct path chooses the purpose's value again once
// the request's own switches are known, as the Agent does. The brief's own
// triangle budget keeps these cases independent of the shipped numbers.
func TestApplyToDirectInputChoosesBudgetValuesAgainstTheWholeInput(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	resolver := chat.PurposeParameters{Model3D: application.Chat.Model3DUsage, Image2D: application.Chat.Image2DUsage}
	offering, found := product.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !found {
		t.Fatal("tripo-v3.1-full text-to-3d missing")
	}
	reason := ""
	for _, profile := range application.Chat.Model3DUsage.Profiles {
		if profile.ID == "game_prop" {
			reason = profile.Reason
		}
	}
	if reason == "" {
		t.Fatal("game_prop declares no reason")
	}
	tests := []struct {
		name     string
		input    string
		wantFace any
	}{
		{"nothing set", `{"prompt":"x"}`, float64(15000)},
		{"smart low poly leaves the budget the stricter bound", `{"prompt":"x","smart_low_poly":true}`, float64(15000)},
		{"quad and smart low poly are stricter than the budget", `{"prompt":"x","smart_low_poly":true,"quad":true}`, nil},
		{"the request's own face count stays", `{"prompt":"x","face_limit":12345}`, float64(12345)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			brief := purposeParamsBrief(t, map[string]any{"purpose": "game_prop", "triangle_budget": 15000})
			applied, err := resolver.ApplyToDirectInput(brief, offering, json.RawMessage(test.input), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, present := purposeParamsDecode(t, applied.Input)["face_limit"]
			if test.wantFace == nil {
				if present {
					t.Fatalf("face_limit = %v, want it left to the Offering's converged default", got)
				}
			} else if got != test.wantFace {
				t.Fatalf("face_limit = %v, want %v", got, test.wantFace)
			}
			credited := test.wantFace != nil && test.name != "the request's own face count stays"
			if credited && (applied.Sources["face_limit"] != "profile:game_prop" || applied.Reasons["face_limit"] != reason) {
				t.Fatalf("face_limit evidence = %v / %v", applied.Reasons, applied.Sources)
			}
			if !credited {
				if _, labelled := applied.Sources["face_limit"]; labelled {
					t.Fatalf("face_limit got a purpose source: %v", applied.Sources)
				}
			}
		})
	}
}

func TestRefreshedPurposeValuesLeavesALaterLayersValueAlone(t *testing.T) {
	first := map[string]json.RawMessage{
		"kept": json.RawMessage(`1`), "moved": json.RawMessage(`2`), "gone": json.RawMessage(`3`), "replaced": json.RawMessage(`4`),
	}
	current := map[string]json.RawMessage{
		"kept": json.RawMessage(`1`), "moved": json.RawMessage(`2`), "gone": json.RawMessage(`3`), "replaced": json.RawMessage(`5`),
	}
	final := map[string]json.RawMessage{
		"kept": json.RawMessage(`1.0`), "moved": json.RawMessage(`20`), "replaced": json.RawMessage(`40`), "new": json.RawMessage(`9`),
	}
	refreshed, dropped := chat.RefreshedPurposeValues(first, current, final)
	if len(refreshed) != 2 || string(refreshed["kept"]) != "1.0" || string(refreshed["moved"]) != "20" {
		t.Fatalf("refreshed = %v", refreshed)
	}
	if !reflect.DeepEqual(dropped, []string{"gone"}) {
		t.Fatalf("dropped = %v, want [gone]", dropped)
	}
}

func TestAdvisedAdjustmentNamesTheProfileOnlyForAnAdvisedParameter(t *testing.T) {
	offering := purposeParamsModel()
	profile := chat.PurposeProfile{ID: "game_character", Reason: purposeParamsReason}
	reason, source, ok := profile.AdvisedAdjustment(offering, "face_limit")
	if !ok || reason != purposeParamsReason || source != "profile:game_character" {
		t.Fatalf("advised = %q %q %v", reason, source, ok)
	}
	for _, name := range []string{"pose_mode", "version", "unknown"} {
		if _, _, ok := profile.AdvisedAdjustment(offering, name); ok {
			t.Fatalf("%s is not advised but got a purpose adjustment", name)
		}
	}
	if _, _, ok := (chat.PurposeProfile{}).AdvisedAdjustment(offering, "face_limit"); ok {
		t.Fatal("a value no purpose supplied got a purpose adjustment")
	}
}

// The service reads the owner's session brief itself, so a caller names the
// session and never carries a brief.
func TestApplyPurposeToDirectInputReadsTheSessionBrief(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	for _, session := range []chat.Session{
		{ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now},
		{ID: "session-b", OwnerID: "owner-a", OwnerName: "A", Title: "b", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Model3DUsage = purposeParamsUsage()
		config.Image2DUsage = purposeParamsImageUsage()
	})
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a",
		map[string]json.RawMessage{"purpose": json.RawMessage(`"game_character"`)}); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"prompt":"a knight"}`)
	applied, err := service.ApplyPurposeToDirectInput(ctx, "owner-a", "session-a", purposeParamsModel(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if purposeParamsDecode(t, applied.Input)["face_limit"] != float64(10000) ||
		applied.Sources["face_limit"] != "profile:game_character" {
		t.Fatalf("applied = %s %v", applied.Input, applied.Sources)
	}
	plain, err := service.ApplyPurposeToDirectInput(ctx, "owner-a", "session-b", purposeParamsModel(), input, nil)
	if err != nil || !bytes.Equal(plain.Input, input) {
		t.Fatalf("a session without a brief changed its input: %s %v", plain.Input, err)
	}
	for _, owner := range []string{"owner-b", ""} {
		if _, err := service.ApplyPurposeToDirectInput(ctx, owner, "session-a", purposeParamsModel(), input, nil); !errors.Is(err, chat.ErrSessionNotFound) {
			t.Fatalf("owner %q read the session brief: %v", owner, err)
		}
	}
}
