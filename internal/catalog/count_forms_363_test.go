package catalog

import (
	"strings"
	"testing"
)

// #363: the project copy contract requires a `_one` pair for every base
// message containing {count}. This is stricter than English grammar: a text
// whose wording does not change with the count repeats the same text.
func TestCountForms363RequirePairs(t *testing.T) {
	cases := map[string]func(application ApplicationSchema){
		"missing English singular": func(application ApplicationSchema) {
			delete(application.Localization.Bundles["en"].Messages, "ui.workspace.notice.tab_title_one")
		},
		"default singular placeholder drift": func(application ApplicationSchema) {
			application.Content.Messages["ui.workspace.notice.tab_title_one"] = "（{count}）有任务已结束"
		},
		"English singular placeholder drift": func(application ApplicationSchema) {
			application.Localization.Bundles["en"].Messages["ui.workspace.notice.tab_title_one"] = "({count}) Task finished"
		},
		"new message missing singular": func(application ApplicationSchema) {
			application.Content.Messages["ui.test.count"] = "{count} items"
			application.Localization.Bundles["en"].Messages["ui.test.count"] = "{count} items"
		},
		"new message placeholder drift": func(application ApplicationSchema) {
			application.Content.Messages["ui.test.count"] = "{count} items"
			application.Content.Messages["ui.test.count_one"] = "{total} item"
			application.Localization.Bundles["en"].Messages["ui.test.count"] = "{count} items"
			application.Localization.Bundles["en"].Messages["ui.test.count_one"] = "{total} item"
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			application := cloneLocalization(t, defaultApplicationForLocalization(t))
			breakIt(application)
			err := ValidateApplicationSchema(application)
			if err == nil {
				t.Fatal("incomplete count form accepted")
			}
			if !strings.Contains(err.Error(), "ui.test.count") && !strings.Contains(err.Error(), "ui.workspace.notice.tab_title") {
				t.Fatalf("count form error does not name the key: %v", err)
			}
		})
	}
}

func TestCountForms363RejectSingularWithoutBase(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Content.Messages["ui.test.count_one"] = "{count} item"
	application.Localization.Bundles["en"].Messages["ui.test.count_one"] = "{count} item"
	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "ui.test.count") {
		t.Fatalf("orphan singular accepted or not named: %v", err)
	}
}

func TestCountForms363AcceptCompletePair(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Content.Messages["ui.test.count"] = "{count} items"
	application.Content.Messages["ui.test.count_one"] = "{count} item"
	application.Localization.Bundles["en"].Messages["ui.test.count"] = "{count} items"
	application.Localization.Bundles["en"].Messages["ui.test.count_one"] = "{count} item"
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("complete pair rejected: %v", err)
	}
}

func TestCanvasCountForms363RequirePairs(t *testing.T) {
	cases := map[string]func(application ApplicationSchema){
		"missing singular": func(application ApplicationSchema) {
			delete(application.Canvas.Presentation.Text, "relative.minutes_ago_one")
		},
		"placeholder drift": func(application ApplicationSchema) {
			application.Canvas.Presentation.Text["relative.minutes_ago_one"] = "{count} 分钟前 {extra}"
		},
		"orphan singular": func(application ApplicationSchema) {
			application.Canvas.Presentation.Text["ui.test.count_one"] = "{count} item"
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			application := defaultApplicationForLocalization(t)
			texts := make(map[string]string, len(application.Canvas.Presentation.Text))
			for key, value := range application.Canvas.Presentation.Text {
				texts[key] = value
			}
			application.Canvas.Presentation.Text = texts
			breakIt(application)
			err := ValidateApplicationSchema(application)
			if err == nil || !strings.Contains(err.Error(), "presentation.text") {
				t.Fatalf("canvas count form accepted or not named: %v", err)
			}
		})
	}
}
