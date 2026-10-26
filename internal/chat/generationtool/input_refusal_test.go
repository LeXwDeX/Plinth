package generationtool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type refusingReferences struct{ err error }

func (resolver refusingReferences) Resolve(context.Context, generation.OwnerID, []string) ([]generation.AssetID, error) {
	return nil, resolver.err
}

// #276: a reference the Schema refuses (too large even after compression,
// dimensions out of range) reaches the Agent with the Schema's reason, not as
// the generic "生成异常" that reads like a model fault; nothing is submitted
// and the turn is not marked failed.
func TestReferenceRefusalReachesAgentWithItsReason(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	tool := application.Chat.Tools[0]
	const reason = "参考图为 10.03 MB，超过当前模型的上限 10 MB，这一步没有提交。"
	refusal := &generation.Error{Code: generation.ErrInvalidInput, Operation: "resolve_input_assets", Detail: reason, UserFacing: true}
	offering := generation.Offering{
		ID: "configured/offering", ModelID: "configured-model", Capability: tool.Capability,
		Enabled: true, PollInterval: time.Second, Timeout: 5 * time.Second,
		Parameters: []generation.Parameter{
			{Name: "configured-prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "configured-references", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 1},
		},
	}
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name,
			Arguments: []byte(`{"prompt":"fix the door","reference_image_indices":[1]}`)},
		Tool:    tool,
		History: []chat.Message{{Role: application.Chat.Roles.User, ImageURLs: []string{"/configured/large"}}},
		Options: chat.TurnOptions{AIModel: "configured-model"},
	}
	for name, setup := range map[string]func() (generationtool.ReferenceResolver, *fakeGateway){
		"copying the reference": func() (generationtool.ReferenceResolver, *fakeGateway) {
			return refusingReferences{err: fmt.Errorf("%w: %w", references.ErrUnsupportedReference, refusal)},
				&fakeGateway{offerings: []generation.Offering{offering}}
		},
		"submitting to the kernel": func() (generationtool.ReferenceResolver, *fakeGateway) {
			return &fakeReferences{}, &fakeGateway{offerings: []generation.Offering{offering}, submitErr: refusal}
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolver, gateway := setup()
			executor, err := generationtool.New(generationtool.Config{
				Gateway: gateway, References: resolver, Chat: application.Chat, TaskStates: application.TaskStates,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := executor.Execute(context.Background(), command, nil)
			if err != nil || !result.NoSubmission || result.Error != reason {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			var content map[string]any
			if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
				t.Fatal(err)
			}
			if content[tool.ResultFields.Error] != reason || content[tool.ResultFields.Success] != false {
				t.Fatalf("agent content = %v", content)
			}
			if result.Error == application.Chat.Presentation.Messages.GenerationError {
				t.Fatal("refusal reported as a generic generation error")
			}
		})
	}
}

// #358: the ordinary generation tool accepts a UserFacing unavailable refusal
// and the preserved model-input codes. The model-input reason is the existing
// error.* Schema message, never the raw diagnostic.
func TestSchemaUnavailableAndModelInputRefusalsReachTheAgent(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	tool := application.Chat.Tools[0]
	offering := generation.Offering{
		ID: "configured/offering", ModelID: "configured-model", Capability: tool.Capability,
		Enabled: true, PollInterval: time.Second, Timeout: 5 * time.Second,
		Parameters: []generation.Parameter{
			{Name: "configured-prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "configured-references", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 1},
		},
	}
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name,
			Arguments: []byte(`{"prompt":"fix the door","reference_image_indices":[1]}`)},
		Tool:    tool,
		History: []chat.Message{{Role: application.Chat.Roles.User, ImageURLs: []string{"/configured/image"}}},
		Options: chat.TurnOptions{AIModel: "configured-model", Locale: "en"},
	}
	unavailable := application.Runtime.GenerationMessages.InputRefusal(schema.InputRefusalUnavailable, schema.InputRefusalValues{})
	modelLimit := &generation.Error{Code: generation.ErrModelInputLimit, Operation: "validate_provider_input",
		Detail: "model profile internal detail"}
	modelInvalid := &generation.Error{Code: generation.ErrInvalidModelInput, Operation: "validate_provider_input",
		Detail: "model structure internal detail"}
	modelUnsupported := &generation.Error{Code: generation.ErrUnsupportedModelInput, Operation: "validate_provider_input",
		Detail: "model feature internal detail"}
	parameter := application.Runtime.GenerationMessages.InputRefusal(schema.InputRefusalParameter,
		schema.InputRefusalValues{Label: "Description"})
	parameter.Cause = errors.New(`parameter "prompt": invalid`)
	parameterErr := &generation.ProviderError{Failure: &generation.Failure{
		Class: generation.FailureInvalidInput, SafeDetail: "input parameter invalid"}, Cause: parameter}
	for _, test := range []struct {
		name   string
		err    error
		expect string
	}{
		{"user-facing unavailable", &generation.Error{Code: generation.ErrUnavailable, Operation: "validate_provider_input",
			Detail: "temporary internal detail", Cause: &generation.ProviderError{
				Failure: &generation.Failure{Class: generation.FailureTransport, Retryable: true,
					SafeDetail: "temporary internal detail"},
				Cause: unavailable},
		}, unavailable.LocalizedDetail(func(text string) string { return texts.Text("en", text) })},
		{"model input limit", modelLimit, texts.Message("en", "error."+string(generation.ErrModelInputLimit))},
		{"model input invalid", modelInvalid, texts.Message("en", "error."+string(generation.ErrInvalidModelInput))},
		{"model input unsupported", modelUnsupported, texts.Message("en", "error."+string(generation.ErrUnsupportedModelInput))},
		{"parameter refusal", parameterErr, parameter.LocalizedDetail(func(text string) string { return texts.Text("en", text) })},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor, err := generationtool.New(generationtool.Config{
				Gateway:    &fakeGateway{offerings: []generation.Offering{offering}, submitErr: test.err},
				References: &fakeReferences{}, Chat: application.Chat, Texts: texts,
				TaskStates: application.TaskStates,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := executor.Execute(context.Background(), command, nil)
			if err != nil || !result.NoSubmission || result.Error != test.expect {
				t.Fatalf("result=%+v error=%v want=%q", result, err, test.expect)
			}
			var content map[string]any
			if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
				t.Fatal(err)
			}
			if content[tool.ResultFields.Error] != test.expect || content[tool.ResultFields.Success] != false {
				t.Fatalf("agent content = %v", content)
			}
			if strings.Contains(result.Error, "internal detail") || strings.Contains(result.Error, `"prompt"`) {
				t.Fatalf("internal diagnostic leaked: %q", result.Error)
			}
		})
	}
}

// #358: a kernel diagnostic (an internal dependency parameter name) is never
// returned to the Agent; the public invalid-parameters copy replaces it.
func TestKernelDiagnosticNeverReachesTheAgent(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	tool := application.Chat.Tools[0]
	offering := generation.Offering{
		ID: "configured/offering", ModelID: "configured-model", Capability: tool.Capability,
		Enabled: true, PollInterval: time.Second, Timeout: 5 * time.Second,
		Parameters: []generation.Parameter{
			{Name: "configured-prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "configured-references", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 1},
		},
	}
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name,
			Arguments: []byte(`{"prompt":"fix the door","reference_image_indices":[1]}`)},
		Tool:    tool,
		History: []chat.Message{{Role: application.Chat.Roles.User, ImageURLs: []string{"/configured/image"}}},
		Options: chat.TurnOptions{AIModel: "configured-model"},
	}
	kernel := &generation.Error{Code: generation.ErrInvalidInput, Operation: "resolve_task_dependencies",
		Detail: `任务依赖参数 "source_task" 不存在、不属于当前用户、尚未成功、能力或供给方不匹配`}
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    &fakeGateway{offerings: []generation.Offering{offering}, submitErr: kernel},
		References: &fakeReferences{}, Chat: application.Chat, TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	want := application.Chat.Presentation.Messages.InvalidParameters
	if result.Error != want || strings.Contains(result.AgentContent, "source_task") {
		t.Fatalf("result=%+v want=%q", result, want)
	}
}
