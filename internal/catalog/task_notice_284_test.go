package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var taskNoticeKeys = []string{
	"task.notice.partial_inputs",
	"task.notice.partial_inputs_named",
	"task.notice.input",
	"task.notice.input_separator",
}

func chatToolOf(t *testing.T, document map[string]any, name string) map[string]any {
	t.Helper()
	chat := applicationOf(t, document)["chat"].(map[string]any)
	for _, candidate := range chat["tools"].([]any) {
		if tool := candidate.(map[string]any); tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not declared", name)
	return nil
}

// #284: a succeeded Task's notice reads from the Schema in both locales, and a
// client never composes a sentence of its own.
func TestDefaultCatalogCarriesTheTaskNoticeCopyInBothLocales(t *testing.T) {
	document := defaultCatalogDocument(t)
	application := applicationOf(t, document)
	messages := application["content"].(map[string]any)["messages"].(map[string]any)
	english := application["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)["messages"].(map[string]any)
	for _, key := range taskNoticeKeys {
		if text, _ := messages[key].(string); strings.TrimSpace(text) == "" {
			t.Errorf("content.messages has no %s", key)
		}
		if text, _ := english[key].(string); strings.TrimSpace(text) == "" {
			t.Errorf("the English bundle has no %s", key)
		}
	}
}

func TestTaskNoticeCopyKeepsItsPlaceholders(t *testing.T) {
	for name, edit := range map[string]func(messages, english map[string]any){
		"the named sentence loses {inputs}": func(messages, _ map[string]any) {
			messages["task.notice.partial_inputs_named"] = "有图片没有处理成功。"
		},
		"the named sentence in English loses {inputs}": func(_, english map[string]any) {
			english["task.notice.partial_inputs_named"] = "Some pictures were not processed."
		},
		"the item loses {ordinal}": func(messages, _ map[string]any) {
			messages["task.notice.input"] = "一张"
		},
		"the English item gains another placeholder": func(_, english map[string]any) {
			english["task.notice.input"] = "picture {ordinal} of {total}"
		},
		"the generic sentence names inputs it cannot know": func(messages, _ map[string]any) {
			messages["task.notice.partial_inputs"] = "{inputs}没有处理成功。"
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := defaultCatalogDocument(t)
			application := applicationOf(t, document)
			edit(
				application["content"].(map[string]any)["messages"].(map[string]any),
				application["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)["messages"].(map[string]any),
			)
			if _, err := loadDocument(t, document); err == nil || !strings.Contains(err.Error(), "task.notice.") {
				t.Fatalf("err = %v, want a rejection naming the notice message", err)
			}
		})
	}
}

// ComposeTaskNotice builds the sentence from Schema messages: the generic one,
// or the one that lists the positions, each position through the item text and
// the separator. A code the Schema has no text for says nothing.
func TestComposeTaskNoticeBuildsTheSentenceFromSchemaMessages(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(loaded.Application())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		locale string
		code   string
		inputs []int
		want   string
	}{
		{"default locale, two positions", "", "partial_inputs", []int{2, 4}, "第 2 张、第 4 张没有处理成功，其余已完成。"},
		{"default locale, one position", "zh-CN", "partial_inputs", []int{3}, "第 3 张没有处理成功，其余已完成。"},
		{"default locale, positions unknown", "", "partial_inputs", nil, "部分图片没有处理成功，其余已完成。"},
		{"English, two positions", "en", "partial_inputs", []int{2, 4}, "Not processed: picture 2, picture 4. The rest are done."},
		{"English, positions unknown", "en", "partial_inputs", nil, "Some pictures were not processed; the rest are done."},
		{"a locale with no bundle falls back to the default text", "fr", "partial_inputs", []int{1}, "第 1 张没有处理成功，其余已完成。"},
		{"a code the Schema has no text for", "", "no_such_notice", []int{1}, ""},
		{"no code", "", "", []int{1}, ""},
	} {
		if got := texts.TaskNoticeText(test.locale, test.code, test.inputs); got != test.want {
			t.Errorf("%s: %q, want %q", test.name, got, test.want)
		}
	}
	if got := (*catalog.TextLocalizer)(nil).TaskNoticeText("en", "partial_inputs", []int{1}); got != "" {
		t.Errorf("a nil localizer composed %q", got)
	}
	// A Schema without the sentence that names positions says the generic one.
	generic := func(key string) string {
		if key == "task.notice.partial_inputs" {
			return "Some pictures failed."
		}
		return ""
	}
	if got := catalog.ComposeTaskNotice(generic, "partial_inputs", []int{2}); got != "Some pictures failed." {
		t.Errorf("without the named sentence: %q", got)
	}
}

// remove_background is the tool whose result reports notices; the key is its
// own, and a tool that builds a plan, not a Task, may not declare one.
func TestRemoveBackgroundDeclaresTheNoticesResultKey(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range loaded.Application().Chat.Tools {
		if tool.Name == "remove_background" {
			found = true
			if tool.ResultFields.Notices != "notices" {
				t.Fatalf("result_fields.notices = %q", tool.ResultFields.Notices)
			}
			if !strings.Contains(tool.Description, "notices") {
				t.Fatalf("the tool description does not tell the Agent what notices mean: %s", tool.Description)
			}
		}
	}
	if !found {
		t.Fatal("remove_background is not declared")
	}
}

func TestCatalogRefusesTheNoticesKeyWhereTheExecutorDoesNotWriteIt(t *testing.T) {
	for name, test := range map[string]struct {
		edit func(document map[string]any)
		want string
	}{
		"on the processing plan tool": {func(document map[string]any) {
			chatToolOf(t, document, "plan_model_processing")["result_fields"].(map[string]any)["notices"] = "notices"
		}, "不适用于加工计划工具"},
		"under the key of another result field": {func(document map[string]any) {
			chatToolOf(t, document, "remove_background")["result_fields"].(map[string]any)["notices"] = "task_id"
		}, "使用了同一个键"},
		"under a key the executor writes itself": {func(document map[string]any) {
			chatToolOf(t, document, "remove_background")["result_fields"].(map[string]any)["notices"] = "native_quote"
		}, "保留键"},
	} {
		t.Run(name, func(t *testing.T) {
			document := defaultCatalogDocument(t)
			test.edit(document)
			if _, err := loadDocument(t, document); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want one containing %q", err, test.want)
			}
		})
	}
}
