package generationtool

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func projectTestExecutor(t *testing.T) (*Executor, chat.ToolCommand) {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	executor := &Executor{
		chat:       application.Chat,
		project:    application.Project,
		inputAsset: application.Runtime.HTTP.Routes.AssetItem,
	}
	command := chat.ToolCommand{
		Tool: catalog.ChatToolSchema{Capability: generation.TextToImage},
		Options: chat.TurnOptions{
			ProjectID: "prj_1",
			ProjectContext: &chat.ProjectContext{
				ID: "prj_1", Name: "Star Harbor",
				StyleGuide: chat.ProjectStyleGuideContext{
					Enabled: true, Text: "warm ink",
					References: []string{"/api/v1/tasks/t1/assets/a1", "/api/v1/assets/upload-1"},
				},
				Assets: []chat.ProjectAssetContext{{
					Handle: "@project1", Kind: "character", Name: "Role A",
					Description: "red coat",
					Images:      []string{"/api/v1/tasks/t2/assets/a2"},
				}},
			},
		},
	}
	return executor, command
}

func TestProjectStyleReferencesFollowTheProjectSourceAndNeverMergeSessionLock(t *testing.T) {
	executor, command := projectTestExecutor(t)
	command.Options.StyleLock = chat.StyleLock{
		Enabled: true, Source: chat.StyleLockSourceProject, Text: "warm ink",
		ProjectName: "Star Harbor", ProjectID: "prj_1",
		ProjectReferences: []string{
			"/api/v1/tasks/t1/assets/a1", "/api/v1/assets/upload-1",
			"/api/v1/tasks/t1/assets/a1",
		},
	}
	urls, purposes, err := executor.styleReferenceURLs(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 || urls[0] != "/api/v1/tasks/t1/assets/a1" || urls[1] != "/api/v1/assets/upload-1" {
		t.Fatalf("project style urls = %v", urls)
	}
	if purposes["/api/v1/tasks/t1/assets/a1"] == "" {
		t.Fatalf("project style purpose missing: %v", purposes)
	}
	command.Options.StyleLock = chat.StyleLock{
		Enabled: true, Source: chat.StyleLockSourceSession, Text: "session ink",
		References: []string{"upload-9"},
	}
	urls, _, err = executor.styleReferenceURLs(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || strings.Contains(urls[0], "a1") {
		t.Fatalf("session source must not merge project pictures: %v", urls)
	}
}

func TestProjectStylePromptUsesTheProjectTemplate(t *testing.T) {
	executor, command := projectTestExecutor(t)
	command.Options.StyleLock = chat.StyleLock{
		Enabled: true, Source: chat.StyleLockSourceProject, Text: "warm ink",
		ProjectName: "Star Harbor",
	}
	prompt := executor.applyStyleLockPrompt(command, generation.Offering{Capability: generation.TextToImage}, "draw a hero")
	template := executor.chat.StyleLock.ProjectPromptTemplate
	if template == "" {
		t.Fatal("default catalog must declare the project prompt template")
	}
	if !strings.Contains(prompt, "Star Harbor") || !strings.Contains(prompt, "warm ink") {
		t.Fatalf("project prompt = %q", prompt)
	}
	if strings.Contains(prompt, executor.chat.StyleLock.PromptTemplate) {
		t.Fatalf("project source used the session prompt: %q", prompt)
	}
	command.Options.StyleLock = chat.StyleLock{
		Enabled: true, Source: chat.StyleLockSourceSession, Text: "session ink",
	}
	sessionPrompt := executor.applyStyleLockPrompt(command, generation.Offering{Capability: generation.TextToImage}, "draw")
	if !strings.Contains(sessionPrompt, "Style lock") || strings.Contains(sessionPrompt, "Star Harbor") {
		t.Fatalf("session prompt = %q", sessionPrompt)
	}
}

func TestProjectHandlesResolveFromTheFrozenContextAndRefuseUnknownTokens(t *testing.T) {
	executor, command := projectTestExecutor(t)
	urls, err := executor.resolveImageHandles(command, []string{"@project1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0] != "/api/v1/tasks/t2/assets/a2" {
		t.Fatalf("project handle urls = %v", urls)
	}
	_, err = executor.resolveImageHandles(command, []string{"@project9"})
	if err == nil || !strings.Contains(err.Error(), "@project9") {
		t.Fatalf("unknown project handle err = %v", err)
	}
	if tool := executor.project.Agent.ToolName; !strings.Contains(err.Error(), tool) {
		t.Fatalf("unknown project handle must name %q: %v", tool, err)
	}
	// A session handle still resolves through the session history/handles.
	command.Options.ProjectContext = nil
	command.History = []chat.Message{{
		Role: "assistant", ImageURLs: []string{"/api/v1/tasks/t3/assets/a3"},
		Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, ImageSlot: 1}},
	}}
	urls, err = executor.resolveImageHandles(command, []string{"@img1"})
	if err != nil || len(urls) != 1 || urls[0] != "/api/v1/tasks/t3/assets/a3" {
		t.Fatalf("session handle urls=%v err=%v", urls, err)
	}
}

func TestProjectStyleSourcesNameAssetsOnlyWhenTheirPictureWasInjected(t *testing.T) {
	executor, command := projectTestExecutor(t)
	names := executor.projectStyleSources(command, []string{"/api/v1/tasks/t2/assets/a2"})
	if len(names) != 1 || names[0] != "Role A" {
		t.Fatalf("style sources = %v", names)
	}
	if names := executor.projectStyleSources(command, []string{"/api/v1/tasks/t9/assets/a9"}); names != nil {
		t.Fatalf("unused asset named: %v", names)
	}
	// The project guide picture that is also an asset picture names the asset.
	command.Options.ProjectContext.Assets[0].Images = append(command.Options.ProjectContext.Assets[0].Images,
		"/api/v1/tasks/t1/assets/a1")
	if names := executor.projectStyleSources(command, []string{"/api/v1/tasks/t1/assets/a1"}); len(names) != 1 || names[0] != "Role A" {
		t.Fatalf("guide picture style sources = %v", names)
	}
}
