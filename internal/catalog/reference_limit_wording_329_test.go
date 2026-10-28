package catalog_test

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func fillReferenceLimit(template, model string, count, total int) string {
	return strings.NewReplacer("{model}", model, "{count}", strconv.Itoa(count), "{total}", strconv.Itoa(total)).Replace(template)
}

func TestReferenceCapacityNoPromptIsOptionalAndPrivate(t *testing.T) {
	data, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	app := doc["application"].(map[string]any)
	chat := app["chat"].(map[string]any)
	for _, item := range chat["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["name"] == "create_model_3d_from_image" {
			note := tool["reference_capacity_note"].(map[string]any)
			if note["no_prompt"] == nil {
				t.Fatal("default catalog has no no_prompt guidance")
			}
			delete(note, "no_prompt")
		}
	}
	old, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.Load(strings.NewReader(string(old)))
	if err != nil {
		t.Fatalf("old catalog without no_prompt: %v", err)
	}
	for _, tool := range product.Application().Chat.Tools {
		if tool.Name == "create_model_3d_from_image" && tool.ReferenceCapacityNote.NoPrompt != "" {
			t.Fatal("omitted no_prompt did not remain empty")
		}
	}
	current, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(current.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "no_prompt") || strings.Contains(string(public), "reference_capacity_note") {
		t.Fatal("agent-only guidance leaked into public schema")
	}
}

// #329 dev-test note 99647, observations 8 and 9: the note that only the
// first pictures are used read three ways (composer, task API, tool/card),
// and its English said "1 reference picture(s)" and "the first 1 of the 2".
// Now one Schema pair says it everywhere — the general text and a
// one-picture text chosen when the model takes exactly one, in every
// language — and the English reads right for one and for several.
func TestReferenceLimitIsOneSchemaPairWithOnePictureWording(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	messages := application.Runtime.GenerationMessages
	english := application.Localization.Bundles["en"].Fields
	if messages.ReferenceLimitText(1) != messages.ReferenceLimitOne || messages.ReferenceLimitText(2) != messages.ReferenceLimit {
		t.Fatalf("ReferenceLimitText must pick the one-picture text for exactly one: %+v", messages)
	}
	for _, template := range []string{messages.ReferenceLimit, messages.ReferenceLimitOne,
		english["runtime.generation_messages.reference_limit"], english["runtime.generation_messages.reference_limit_one"]} {
		if !strings.Contains(template, "{model}") || !strings.Contains(template, "{total}") {
			t.Fatalf("reference limit text %q must name the model and the total", template)
		}
	}
	if messages.ReferenceLimit == messages.ReferenceLimitOne {
		t.Fatal("the one-picture text must differ from the general one (Chinese words one picture differently too)")
	}

	one := fillReferenceLimit(messages.ReferenceLimitOne, "Meshy 7.1", 1, 2)
	for _, fact := range []string{"Meshy 7.1", "第 1 张", "2 张", "支持多图的模型"} {
		if !strings.Contains(one, fact) {
			t.Fatalf("one-picture note %q must state %q", one, fact)
		}
	}
	for _, awkward := range []string{"前 1 张", "最多用 1 张"} {
		if strings.Contains(one, awkward) {
			t.Fatalf("one-picture note %q says %q", one, awkward)
		}
	}
	oneEnglish := fillReferenceLimit(english["runtime.generation_messages.reference_limit_one"], "Meshy 7.1", 1, 2)
	several := fillReferenceLimit(english["runtime.generation_messages.reference_limit"], "Model X", 2, 5)
	for text, wants := range map[string][]string{
		oneEnglish: {"1 reference picture ", "the 2 pictures", "only the first is used"},
		several:    {"2 reference pictures", "the 5 pictures", "the first 2 are used"},
	} {
		if strings.Contains(text, "(s)") || strings.Contains(text, "first 1 ") || strings.Contains(text, "were used") {
			t.Fatalf("English note %q is not worded for its count", text)
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("English note %q must say %q", text, want)
			}
		}
	}

	// Single source: the composer, the tool and its card no longer keep their own copies.
	for _, key := range []string{"ui.studio.composer.reference_limit.one", "ui.studio.composer.reference_limit.many"} {
		if _, kept := application.Content.Messages[key]; kept {
			t.Fatalf("content.messages still declares %s", key)
		}
	}
	for path := range english {
		if strings.HasSuffix(path, ".reference_limit_note") || strings.Contains(path, "composer.reference_limit") {
			t.Fatalf("the English bundle still translates %s", path)
		}
	}

	// The composer reads the same pair from the public Schema, in the reader's language.
	projections, err := catalog.NewLocalizedPublicSchemas(application)
	if err != nil {
		t.Fatal(err)
	}
	for locale, want := range map[string][3]string{
		"zh-CN": {messages.ReferenceLimit, messages.ReferenceLimitOne, messages.ReferenceCapacity},
		"en":    {english["runtime.generation_messages.reference_limit"], english["runtime.generation_messages.reference_limit_one"], english["runtime.generation_messages.reference_capacity"]},
	} {
		var public struct {
			Runtime struct {
				GenerationMessages map[string]any `json:"generation_messages"`
			} `json:"runtime"`
		}
		if err := json.Unmarshal(projections.For(locale).JSON, &public); err != nil {
			t.Fatal(err)
		}
		got := public.Runtime.GenerationMessages
		if got["reference_limit"] != want[0] || got["reference_limit_one"] != want[1] ||
			got["reference_capacity"] != want[2] || len(got) != 3 {
			t.Fatalf("%s public generation_messages = %v", locale, got)
		}
	}
}

// Observation 9: the finished message named the model twice ("Meshy 7.1
// finished generating. Meshy 7.1 uses …"); the note already names it.
// Observation 7: the reply may put the note in its own words but must state
// its facts.
func TestFinishedNoteNamesTheModelOnceAndTheToolAsksForTheFacts(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	english := application.Localization.Bundles["en"].Fields
	for _, template := range []string{application.Chat.PendingApprovals.Messages.SucceededWithNote, english["chat.pending_approvals.messages.succeeded_with_note"]} {
		if !strings.Contains(template, "{note}") || strings.Contains(template, "{model}") {
			t.Fatalf("succeeded_with_note %q must carry the note and leave the model name to it", template)
		}
	}
	var description string
	for _, tool := range application.Chat.Tools {
		if tool.Name == "create_model_3d_from_image" {
			description = tool.Description
		}
	}
	for _, want := range []string{"reference_note", "which pictures are used", "how many were given", "several pictures", "your own words"} {
		if !strings.Contains(description, want) {
			t.Fatalf("the image-to-3d tool description must ask for the note's facts (%q): %s", want, description)
		}
	}
	if strings.Contains(description, "as it is") {
		t.Fatalf("the tool description still asks for the sentence word for word: %s", description)
	}
}

func TestReferenceLimitDeclarationsAreValidated(t *testing.T) {
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"general text names the count": func(a *catalog.ApplicationSchema) {
			a.Runtime.GenerationMessages.ReferenceLimit = "{model}: {total}"
		},
		"one-picture text names the total": func(a *catalog.ApplicationSchema) {
			a.Runtime.GenerationMessages.ReferenceLimitOne = "{model} uses one"
		},
		"one-picture text needs the general text": func(a *catalog.ApplicationSchema) {
			a.Runtime.GenerationMessages.ReferenceLimit = ""
		},
		"finished text leaves the model to the note": func(a *catalog.ApplicationSchema) {
			a.Chat.PendingApprovals.Messages.SucceededWithNote = "{model} done. {note}"
		},
	} {
		t.Run(name, func(t *testing.T) {
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			application := product.Application()
			mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid declaration")
			}
		})
	}
}

// #356: the Agent could not compare reference-picture capacities because the
// asset-array inputs are not writable. list_offerings now states the read-only
// reference_image_max and requires querying it before comparing models the
// person did not select; the selected model's note says the same and never
// switches the model on its own.
func TestListOfferingsStatesReferenceCapacityAndQueryRule(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var listOfferings *catalog.ChatAgentToolSchema
	var capacity catalog.ChatToolReferenceCapacityNote
	for index := range application.Chat.AgentTools {
		if application.Chat.AgentTools[index].Name == "list_offerings" {
			listOfferings = &application.Chat.AgentTools[index]
		}
	}
	for index := range application.Chat.Tools {
		if note := application.Chat.Tools[index].ReferenceCapacityNote; note != (catalog.ChatToolReferenceCapacityNote{}) {
			capacity = note
		}
	}
	if listOfferings == nil {
		t.Fatal("the Schema declares no list_offerings tool")
	}
	for _, want := range []string{"reference_image_max", "query", "before", "unselected"} {
		if !strings.Contains(strings.ToLower(listOfferings.Description), want) {
			t.Fatalf("list_offerings description does not state %q: %s", want, listOfferings.Description)
		}
	}
	if capacity.Single == "" || capacity.Multiple == "" {
		t.Fatalf("no reference capacity note: %+v", capacity)
	}
	for name, text := range map[string]string{"single": capacity.Single, "multiple": capacity.Multiple} {
		if !strings.Contains(text, "list_offerings") || !strings.Contains(text, "reference_image_max") {
			t.Fatalf("the %s note must point to list_offerings and reference_image_max: %s", name, text)
		}
		if !strings.Contains(text, "do not switch") {
			t.Fatalf("the %s note must forbid switching the selected model on its own: %s", name, text)
		}
		for _, keepOut := range []string{"never mention", "reply to the person"} {
			if !strings.Contains(text, keepOut) {
				t.Fatalf("the %s note must keep internal tool and field names out of replies (%q): %s", name, keepOut, text)
			}
		}
	}
	var mcpDescription string
	for _, preset := range application.AgentConfiguration.MCPPresets {
		for _, tool := range preset.Tools {
			if tool.Name == "mcp_list_offerings" {
				mcpDescription = tool.Description
			}
		}
	}
	if mcpDescription == "" {
		t.Fatal("the Schema declares no mcp_list_offerings tool")
	}
	const mcpKey = "agent_configuration.mcp_presets[id=workspace-read].tools[name=mcp_list_offerings].description"
	englishMCP, localized := application.Localization.Bundles["en"].Fields[mcpKey]
	if !localized || englishMCP == "" {
		t.Fatalf("the English bundle does not translate %s", mcpKey)
	}
	for name, test := range map[string]struct {
		text  string
		wants []string
	}{
		"mcp_list_offerings description": {mcpDescription, []string{"reference_image_max", "只读", "缺席", "未知", "比较", "先查询", "不要自行切换"}},
		"English mcp_list_offerings":     {englishMCP, []string{"reference_image_max", "read-only", "absent", "unknown", "Query this list before comparing", "never switch the selected model on your own"}},
	} {
		for _, want := range test.wants {
			if !strings.Contains(test.text, want) {
				t.Fatalf("%s does not state %q: %s", name, want, test.text)
			}
		}
	}
}
