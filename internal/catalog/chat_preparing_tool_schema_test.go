package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// #375: the default Schema declares the preparing-tool status of the chat stream in Chinese and
// English; the public projection carries the status text to the client. #546 removed the task
// drawer's Agent row, and with it the "no task submitted yet" note: the drawer lists Tasks only.
func TestDefaultCatalogDeclaresPreparingToolAndNoTaskTexts(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	english := application.Localization.Bundles["en"]
	const status = "chat.presentation.status.preparing_tool"
	if got := application.Chat.Presentation.Status.PreparingTool; got != "正在准备调用工具…" {
		t.Fatalf("%s = %q", status, got)
	}
	if got := english.Fields[status]; got != "Preparing to call a tool…" {
		t.Fatalf("en %s = %q", status, got)
	}
	for locale, want := range map[string]string{"zh-CN": "正在准备调用工具…", "en": "Preparing to call a tool…"} {
		public := decodeLocalizedPublic(t, application, locale)
		if got := lookup(t, public, "chat", "presentation", "status", "preparing_tool"); got != want {
			t.Fatalf("%s public %s = %v", locale, status, got)
		}
	}
	for _, note := range []string{"ui.workspace.tasks.agent_no_task", "ui.workspace.tasks.agent_preparing"} {
		if got, ok := application.Content.Messages[note]; ok {
			t.Fatalf("%s = %q, want it removed with the drawer's Agent row", note, got)
		}
		if got, ok := english.Messages[note]; ok {
			t.Fatalf("en %s = %q, want it removed with the drawer's Agent row", note, got)
		}
	}
}

// The status text is required like the other chat status texts: the orchestrator shows it for
// every tool call, so a Schema without it is refused at load.
func TestPreparingToolStatusIsRequired(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(source, &document); err != nil {
		t.Fatal(err)
	}
	application := document["application"].(map[string]any)
	status := application["chat"].(map[string]any)["presentation"].(map[string]any)["status"].(map[string]any)
	status["preparing_tool"] = ""
	// Dropping the text drops its translation, which would otherwise point at nothing.
	fields := application["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)["fields"].(map[string]any)
	delete(fields, "chat.presentation.status.preparing_tool")
	changed, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(bytes.NewReader(changed))
	if err == nil || !strings.Contains(err.Error(), "presentation.status.preparing_tool") {
		t.Fatalf("error = %v, want one naming presentation.status.preparing_tool", err)
	}
}
