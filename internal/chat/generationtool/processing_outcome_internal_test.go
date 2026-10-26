package generationtool

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #358: an unavailable failure without UserFacing copy or a Provider SafeDetail
// stays a tool execution error. Its internal Detail must not become public.
func TestProcessingOutcomeDoesNotExposeRawUnavailableDetail(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Processing != nil {
			tool = candidate
			break
		}
	}
	if tool.Processing == nil {
		t.Fatal("default Schema declares no processing tool")
	}
	internal := &generation.Error{Code: generation.ErrUnavailable,
		Operation: "validate_provider_input", Detail: "SECRET-INTERNAL-DIAGNOSTIC"}
	result, gotErr := (&Executor{}).processingOutcome(chat.ToolCommand{Tool: tool},
		chat.ToolResult{NoSubmission: true}, internal)
	if gotErr != internal {
		t.Fatalf("error = %v", gotErr)
	}
	if result.Error != "" || result.AgentContent != "" || strings.Contains(result.AgentContent, "SECRET") {
		t.Fatalf("raw detail leaked: %+v", result)
	}
}

// #358: a processing step that fails on a model-input limit keeps its original
// code and the existing error.* Schema message in the turn's language.
func TestProcessingOutcomeUsesModelInputMessage(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range application.Chat.Tools {
		if candidate.Processing == nil {
			continue
		}
		err := &generation.Error{Code: generation.ErrModelInputLimit,
			Operation: "validate_provider_input", Detail: "SECRET-INTERNAL-DIAGNOSTIC"}
		result, gotErr := (&Executor{texts: texts, logger: slog.New(slog.DiscardHandler)}).processingOutcome(chat.ToolCommand{
			Tool: candidate, Options: chat.TurnOptions{Locale: "en"},
		}, chat.ToolResult{NoSubmission: true}, err)
		want := texts.Message("en", "error."+string(generation.ErrModelInputLimit))
		if gotErr != nil || !result.NoSubmission || result.Error != want || strings.Contains(result.Error, "SECRET") {
			t.Fatalf("result=%+v error=%v want=%q", result, gotErr, want)
		}
		return
	}
	t.Fatal("default Schema declares no processing tool")
}

// #358: without a localizer the model-input refusal still uses the public chat
// fallback, never the internal detail.
func TestModelInputDetailWithoutTextsUsesChatFallback(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	executor := Executor{chat: application.Chat}
	command := chat.ToolCommand{Tool: application.Chat.Tools[0]}
	internal := &generation.Error{Code: generation.ErrInvalidModelInput,
		Operation: "validate_provider_input", Detail: "SECRET-INTERNAL-DIAGNOSTIC"}
	message, ok := executor.modelInputDetail(command, internal)
	want := application.Chat.Presentation.Messages.InvalidParameters
	if !ok || message != want || strings.Contains(message, "SECRET") {
		t.Fatalf("message = %q ok=%v want=%q", message, ok, want)
	}
}

// #358: processingResult never exposes a kernel dependency diagnostic either.
func TestProcessingOutcomeDoesNotExposeKernelDiagnostic(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Processing != nil {
			tool = candidate
			break
		}
	}
	if tool.Processing == nil {
		t.Fatal("default Schema declares no processing tool")
	}
	internal := &generation.Error{Code: generation.ErrInvalidInput,
		Operation: "resolve_task_dependencies",
		Detail:    `任务依赖参数 "source_task" 不存在、不属于当前用户、尚未成功、能力或供给方不匹配`}
	var logs bytes.Buffer
	result, gotErr := (&Executor{chat: application.Chat,
		logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}).
		processingOutcome(chat.ToolCommand{Tool: tool}, chat.ToolResult{NoSubmission: true}, internal)
	if gotErr != nil || !result.NoSubmission {
		t.Fatalf("result=%+v error=%v", result, gotErr)
	}
	if strings.Contains(result.Error, "source_task") || strings.Contains(result.AgentContent, "source_task") {
		t.Fatalf("kernel diagnostic leaked: %+v", result)
	}
	if result.Error == "" {
		t.Fatal("refusal has no public reason")
	}
	if strings.Contains(logs.String(), "source_task") || !strings.Contains(logs.String(), "code=invalid_input") {
		t.Fatalf("unsafe refusal log: %s", logs.String())
	}
}
