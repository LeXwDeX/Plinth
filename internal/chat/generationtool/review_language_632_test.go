package generationtool_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// englishSentence is the #407 evaluation's mixed-language mark: six or more
// English words in a row. Identifiers (snake_case, handles, placeholders,
// numbers, file and model names) are machine tokens, not words of a sentence.
var (
	englishSentence  = regexp.MustCompile(`[A-Za-z][A-Za-z'’]*(?:[ ,;:()/-]+[A-Za-z][A-Za-z'’]*){5,}`)
	machineTokenText = regexp.MustCompile(`[A-Za-z0-9_@#./{}=<>%-]*[_@#{}0-9=<>%][A-Za-z0-9_@#./{}=<>%-]*`)
)

func englishSentenceIn(text string) string {
	return englishSentence.FindString(machineTokenText.ReplaceAllString(text, "|"))
}

// englishLeaves lists every string value of a tool result that carries an
// English sentence, with its JSON path.
func englishLeaves(value any, path string, found map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			englishLeaves(child, path+"."+key, found)
		}
	case []any:
		for _, child := range typed {
			englishLeaves(child, path+"[]", found)
		}
	case string:
		if match := englishSentenceIn(typed); match != "" {
			found[path] = match
		}
	}
}

func resultEnglish(t *testing.T, result chat.ToolResult) map[string]string {
	t.Helper()
	found := map[string]string{}
	if match := englishSentenceIn(result.Error); match != "" {
		found["error"] = match
	}
	if result.AgentContent != "" {
		var payload any
		if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
			t.Fatalf("tool result is not JSON: %v\n%s", err, result.AgentContent)
		}
		englishLeaves(payload, "", found)
	}
	return found
}

func reviewLanguageExecutor(t *testing.T, gateway *reviewGateway, assets *reviewAssets,
	pictures *reviewPictures, store *reviewSelfCheckStore, localized bool) *generationtool.Executor {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	app.Chat.ModelReview.Render.Size = 24
	app.Chat.ModelReview.Render.MaxSize = 48
	app.Chat.ModelReview.Render.Supersample = 1
	app.Chat.ModelReview.Render.Workers = 1
	app.Chat.ModelReview.Render.DefaultViews = []string{"front"}
	app.Chat.SelfCheck.Reference.Size = 24
	app.Chat.SelfCheck.Reference.Supersample = 1
	var texts *catalog.TextLocalizer
	if localized {
		if texts, err = catalog.NewTextLocalizer(app); err != nil {
			t.Fatal(err)
		}
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: app.Chat, Texts: texts,
		TaskStates: app.TaskStates, ModelAssets: assets, InputAssets: assets,
		InputAssetRoute: app.Runtime.HTTP.Routes.AssetItem,
		PictureReader:   pictures,
		SelfChecks:      chat.NewSelfCheckService(store, app.Chat.SelfCheck),
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

type reviewLanguageCall struct {
	name, tool, arguments string
}

// The read-only 3D self-check tools speak the turn's locale (#632): in a
// Chinese session nothing the Agent reads back from inspect_model,
// render_model_views, compare_model_reference or record_self_check is an
// English sentence it could copy into its reply, refusals included. The same
// calls in an English turn still read English, from the Schema's en bundle.
func TestReviewToolResultsCarryNoEnglishSentenceInAChineseSession(t *testing.T) {
	calls := []reviewLanguageCall{
		{"inspect", "inspect_model", `{"model":"task-a"}`},
		{"render", "render_model_views", `{"model":"task-a"}`},
		{"reference", "compare_model_reference", `{"model":"task-a"}`},
		{"conclusion", "record_self_check", `{"model":"task-a","status":"passed","summary":"轮廓与参考图一致"}`},
		{"unknown status", "record_self_check", `{"model":"task-a","status":"bogus","summary":"x"}`},
		{"unknown model", "inspect_model", `{"model":"task-missing"}`},
		{"no model", "inspect_model", `{}`},
		{"unknown view", "render_model_views", `{"model":"task-a","views":["underneath"]}`},
		{"unknown mode", "render_model_views", `{"model":"task-a","mode":"sketch"}`},
	}
	run := func(t *testing.T, locale string, localized bool) map[string]chat.ToolResult {
		glb := reviewModelGLB(t, 1, 1)
		gateway := newReviewGateway(referenceTask("task-a", "owner-a", "session-a", glb))
		assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
		pictures := &reviewPictures{pictures: map[string][]byte{"ref-asset": referencePNG(t, 32, 32)}}
		executor := reviewLanguageExecutor(t, gateway, assets, pictures, newReviewSelfCheckStore(), localized)
		results := map[string]chat.ToolResult{}
		for _, call := range calls {
			command := reviewCommand(call.tool, call.arguments)
			command.Options.Locale = locale
			var result chat.ToolResult
			var err error
			switch call.tool {
			case "inspect_model":
				result, err = executor.InspectModel(context.Background(), command)
			case "render_model_views":
				result, err = executor.RenderModelViews(context.Background(), command)
			case "compare_model_reference":
				result, err = executor.CompareModelReference(context.Background(), command)
			case "record_self_check":
				result, err = executor.RecordSelfCheck(context.Background(), command)
			}
			if err != nil {
				t.Fatalf("%s: %v", call.name, err)
			}
			if result.Error == "" && result.AgentContent == "" {
				t.Fatalf("%s returned nothing", call.name)
			}
			results[call.name] = result
		}
		return results
	}

	for name, result := range run(t, "zh-CN", true) {
		for path, match := range resultEnglish(t, result) {
			t.Errorf("zh-CN %s: %s carries the English sentence %q", name, path, match)
		}
	}

	english := run(t, "en", true)
	for _, name := range []string{"inspect", "reference", "unknown model"} {
		if len(resultEnglish(t, english[name])) == 0 {
			t.Errorf("en %s lost its English copy: %+v", name, english[name])
		}
	}
	if caveats := reviewPayload(t, english["reference"])["caveats"]; !strings.Contains(caveats.(string), "PBR") ||
		englishSentenceIn(caveats.(string)) == "" {
		t.Errorf("en caveats = %v", caveats)
	}
}
