package generationtool_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #321 G, H and the offering_id leak (dev-test note 99042; user rule 98950):
// the person named a service; it cannot take the selected model. The tool
// says why and asks; it never lets the Agent pay for another service instead,
// in either approval mode. When the person then answers the question the tool
// proposed (the model the selected one was made from), one answer is enough.

// bigGLB is 91.8 MB (87.5 MiB), the size of the dev-test Bang result.
const bigGLB = 91_800_000

// rodinOriginal is the session's first model: a Rodin result too large for
// Bang's local-model path (110 MB, like the dev-test Rodin text-to-3D result),
// which the Rodin-source Bang path still accepts.
func rodinOriginal(h *processingHarness) generation.Task {
	task := rodinTask(h)
	task.Assets[0].Size = 110_770_224
	return task
}

// bigBangResult is the previous Bang result made from rodin-1: one 91.8 MB
// GLB, over the Bang local-model limit; only Tripo Smart Segmentation takes it.
func bigBangResult(h *processingHarness) generation.Task {
	offering, _ := h.gateway.product.Get("hyper3d/bang/rodin-task")
	task := modelTask("bang-big", "owner-a", "session-a", offering.ProviderID, "mesh-segmentation", "glb")
	task.Offering = offering
	task.Input = json.RawMessage(`{"source_task":"rodin-1"}`)
	task.CreatedAt = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	task.Assets[0].MediaType, task.Assets[0].Size = "model/gltf-binary", bigGLB
	return task
}

func namedHarness(t *testing.T, mode string) *processingHarness {
	return newProcessingHarness(t, mode, func(h *processingHarness) []generation.Task {
		return []generation.Task{rodinOriginal(h), bigBangResult(h)}
	})
}

var bigBangSelected = chat.TurnOptions{Selection: []chat.Attachment{
	{Handle: "@model2", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "bang-big"}}}

func namedHistory(userText string) []chat.Message {
	return []chat.Message{
		{ID: "a-1", Role: "assistant", Attachments: []chat.Attachment{
			{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "rodin-1"}}},
		{ID: "a-2", Role: "assistant", Attachments: []chat.Attachment{
			{Handle: "@model2", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "bang-big"}}},
		{ID: "u-1", Role: "user", Content: userText},
	}
}

// awaitedMessage is the waiting text of a step that ends on its approval
// card; since #321 I the result is JSON that also carries the frozen quote.
func awaitedMessage(content string) string {
	var awaiting struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(content), &awaiting) == nil && awaiting.Status == "awaiting_approval" {
		return awaiting.Message
	}
	return content
}

// offeringIDPattern finds an internal Offering ID in text meant for reading.
var offeringIDPattern = regexp.MustCompile(`\b(?:hyper3d|tripo|meshy|local|matting)/[a-z0-9.\-]+`)

func (h *processingHarness) assertAsksAboutService(result chat.ToolResult) (map[string]any, string) {
	h.t.Helper()
	if !result.NoSubmission || result.Suspend || len(h.gateway.frozen) != 0 || h.gateway.submitCount != 0 || len(h.gateway.submitted) != 0 {
		h.t.Fatalf("a paid step was frozen or submitted instead of asking: %+v frozen=%d submits=%d", result, len(h.gateway.frozen), h.gateway.submitCount)
	}
	content := h.content(result)
	message, _ := content["error"].(string)
	if content["success"] != false || content["service_confirmation_required"] != true || message == "" {
		h.t.Fatalf("result = %v", content)
	}
	if found := offeringIDPattern.FindString(message); found != "" {
		h.t.Fatalf("the text names an internal ID %q: %s", found, message)
	}
	if strings.Contains(strings.ToLower(message), "call without") || strings.Contains(message, "without it") {
		h.t.Fatalf("the text still invites a call without the service: %s", message)
	}
	return content, message
}

// G: the requested service refuses the selected model. The refusal says why
// (size and limit), names services by provider and model, proposes the model
// the selected one was made from, and keeps the IDs in separate fields.
func TestNamedServiceThatCannotTakeTheModelSaysWhyAndAsks(t *testing.T) {
	h := namedHarness(t, "auto")
	result, _ := h.runWith("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model2","offering_id":"hyper3d/bang/local-model"}`,
		namedHistory("再把这个模型分割成部件一次，用 Bang"), bigBangSelected)
	content, message := h.assertAsksAboutService(result)
	// #327: decimal MB, the way the page writes sizes; the Bang limit is 10,485,760 bytes.
	for _, want := range []string{"91.8 MB", "10.48 MB", "Bang", "@model2", "@model1", "Tripo Smart Segmentation"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the refusal does not say %q: %s", want, message)
		}
	}
	requested, _ := content["requested_services"].([]any)
	if len(requested) == 0 || requested[0].(map[string]any)["offering_id"] != "hyper3d/bang/local-model" {
		t.Fatalf("requested_services = %v", content["requested_services"])
	}
	refusal, _ := requested[0].(map[string]any)["refusal"].(map[string]any)
	// The tool result carries the sizes as text to quote, never bytes to convert (#327).
	if refusal["kind"] != "too_large" || refusal["limit"] != "10.48 MB" || refusal["size"] != "91.8 MB" ||
		refusal["limit_bytes"] != nil || refusal["size_bytes"] != nil {
		t.Fatalf("refusal = %v", refusal)
	}
	proposed, _ := content["proposed_source"].(map[string]any)
	if proposed["handle"] != "@model1" || proposed["task_id"] != "rodin-1" {
		t.Fatalf("proposed_source = %v", content["proposed_source"])
	}
	source, _ := content["source"].(map[string]any)
	if source["handle"] != "@model2" || source["capability"] != "mesh-segmentation" || !strings.Contains(source["model"].(string), "Bang") ||
		source["size"] != "91.8 MB" || source["size_bytes"] != nil {
		t.Fatalf("source = %v", content["source"])
	}
	workable, _ := content["workable_services"].([]any)
	if len(workable) != 1 || workable[0].(map[string]any)["offering_id"] != "tripo/geometry/smartsegment-model" {
		t.Fatalf("workable_services = %v", content["workable_services"])
	}
}

// G at the tool layer: after the refusal the Agent calls again without the
// service (or names another one). Only Tripo accepts the model, so before this
// fix the step froze as Tripo; in the automatic mode that is a paid submission
// the person never agreed to. Now neither mode freezes, grants or submits.
func TestAnotherServiceThanTheNamedOneIsNeverFrozenInEitherApprovalMode(t *testing.T) {
	for _, mode := range []string{"auto", "manual"} {
		for _, arguments := range []string{
			`{"step":"mesh-segmentation","source_task_id":"@model2"}`,
			`{"step":"mesh-segmentation","source_task_id":"@model2","offering_id":"tripo/geometry/smartsegment-model"}`,
			`{"step":"mesh-segmentation","source_task_id":"@model2","use_default_offering":true}`,
		} {
			t.Run(mode+" "+arguments, func(t *testing.T) {
				h := namedHarness(t, mode)
				history := namedHistory("再把这个模型分割成部件一次，用 Bang")
				result, _ := h.runWith("owner-a", "session-a", "process_model", arguments, history, bigBangSelected)
				_, message := h.assertAsksAboutService(result)
				if !strings.Contains(message, "Bang") || !strings.Contains(message, "Tripo") {
					t.Fatalf("the question names neither the asked nor the other service: %s", message)
				}
				op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-1")
				if err == nil {
					t.Fatalf("an agent operation was recorded: %+v", op)
				}
			})
		}
	}
}

// The person said not to use a service: it is not run either.
func TestAServiceThePersonRefusedIsNotRun(t *testing.T) {
	h := namedHarness(t, "auto")
	result, _ := h.runWith("owner-a", "session-a", "process_model", `{"step":"mesh-segmentation","source_task_id":"@model2"}`,
		namedHistory("再把这个模型分割成部件一次，不要用 Tripo"), bigBangSelected)
	h.assertAsksAboutService(result)
}

// The named service that can take the model runs silently, with or without
// its offering_id in the call; a qualifier picks one of two paths of a brand.
func TestTheNamedServiceRunsSilently(t *testing.T) {
	h := namedHarness(t, "auto")
	result, _ := h.runWith("owner-a", "session-a", "process_model", `{"step":"mesh-segmentation","source_task_id":"@model2"}`,
		namedHistory("用 Tripo 再分割一次"), bigBangSelected)
	if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "tripo/geometry/smartsegment-model" {
		t.Fatalf("the named Tripo did not run: %s", result.AgentContent)
	}
	// On the original Rodin model "Bang" alone fits only the Rodin-source path
	// (the 110 MB file is over the local-model limit), so it runs, no card.
	h = namedHarness(t, "auto")
	selected := chat.TurnOptions{Selection: []chat.Attachment{
		{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "rodin-1"}}}
	result, _ = h.runWith("owner-a", "session-a", "process_model", `{"step":"mesh-segmentation","source_task_id":"@model1"}`,
		namedHistory("用 Bang 分割这个模型"), selected)
	if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "hyper3d/bang/rodin-task" {
		t.Fatalf("the named Bang did not run: %s", result.AgentContent)
	}
}

// A choice-card answer given on the card is the person naming a service.
func TestACardAnswerNamesTheService(t *testing.T) {
	h, repo := choiceHarness(t, "auto")
	h.gateway.tasks["bang-big"] = bigBangResult(h)
	h.gateway.tasks["rodin-1"] = rodinOriginal(h)
	ctx := context.Background()
	stored, err := repo.CreateChoice(ctx, chat.Choice{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-0",
		CallID: "call-0", ToolName: "process_model", Capability: "mesh-segmentation", StepIndex: -1,
		Options: []chat.ChoiceOption{
			{ID: "tripo/geometry/smartsegment-model", ProviderName: "Tripo3D", ModelName: "Tripo Smart Segmentation", Arguments: json.RawMessage(`{}`)},
			{ID: "hyper3d/bang/local-model", ProviderName: "Hyper3D", ModelName: "Bang", Arguments: json.RawMessage(`{}`)}},
		DefaultOptionID: "tripo/geometry/smartsegment-model", State: chat.ChoicePending, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AnswerChoice(ctx, "owner-a", "session-a", stored.ID, chat.ChoiceRecord{OptionID: "tripo/geometry/smartsegment-model",
		Source: chat.ChoiceAnsweredOnCard, MessageID: "u-1", AnsweredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	result, _ := h.runWith("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model2","offering_id":"hyper3d/bang/rodin-task"}`, namedHistory(""), bigBangSelected)
	h.assertAsksAboutService(result)
	result, _ = h.runWith("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model2","offering_id":"tripo/geometry/smartsegment-model"}`, namedHistory(""), bigBangSelected)
	if result.NoSubmission || len(h.gateway.frozen) != 1 {
		t.Fatalf("the service chosen on the card did not run: %s", result.AgentContent)
	}
}

// answeredProposalHistory is the dev-test conversation: the person asked for
// Bang, the tool refused and proposed @model1 (twice, once after the Agent
// called again without the service), and the person now answers.
func answeredProposalHistory(answer string) []chat.Message {
	history := namedHistory("再把这个模型分割成部件一次，用 Bang")
	return append(history,
		chat.Message{ID: "a-3", Role: "assistant", Content: "Bang 接不了当前选中的 @model2（91.8 MB，超过 10 MB 上限）。要改用原模型 @model1 做 Bang 分割吗？",
			ToolCalls: []chat.ToolCall{
				{ID: "c-1", Name: "process_model", Arguments: []byte(`{"step":"mesh-segmentation","source_task_id":"@model2","offering_id":"hyper3d/bang/local-model"}`)},
				{ID: "c-2", Name: "process_model", Arguments: []byte(`{"step":"mesh-segmentation","source_task_id":"@model2"}`)}}},
		chat.Message{ID: "u-2", Role: "user", Content: answer})
}

// H: one answer to the question the tool proposed is enough, without @model1.
func TestAnAnswerToTheProposedModelIsTheConfirmation(t *testing.T) {
	h := namedHarness(t, "auto")
	result, _ := h.runWith("owner-a", "session-a", "process_model", `{"step":"mesh-segmentation","source_task_id":"@model1"}`,
		answeredProposalHistory("用原模型，用 Bang"), bigBangSelected)
	if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "hyper3d/bang/rodin-task" ||
		h.frozenInput(0)["source_task"] != "rodin-1" {
		t.Fatalf("the answered proposal asked again: %s", result.AgentContent)
	}
	// The explicit service in the call works the same.
	h = namedHarness(t, "auto")
	result, _ = h.runWith("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"hyper3d/bang/rodin-task"}`,
		answeredProposalHistory("好的"), bigBangSelected)
	if result.NoSubmission || len(h.gateway.frozen) != 1 {
		t.Fatalf("the answered proposal asked again: %s", result.AgentContent)
	}
}

// Only the model just proposed counts, and only when the answer names no
// other model; without a proposal the question stays.
func TestOnlyTheJustProposedModelIsConfirmedByAnAnswer(t *testing.T) {
	h := namedHarness(t, "auto")
	result, _ := h.runWith("owner-a", "session-a", "process_model", `{"step":"mesh-segmentation","source_task_id":"@model1"}`,
		answeredProposalHistory("用 @model3 吧"), bigBangSelected)
	if content := h.content(result); content["source_confirmation_required"] != true || len(h.gateway.frozen) != 0 {
		t.Fatalf("an answer naming another model confirmed @model1: %v", content)
	}
	// The Agent only wrote the question; no tool proposed the model.
	h = namedHarness(t, "auto")
	history := append(namedHistory("再把这个模型分割成部件一次"),
		chat.Message{ID: "a-3", Role: "assistant", Content: "要改用 @model1 吗？"},
		chat.Message{ID: "u-2", Role: "user", Content: "用原模型"})
	result, _ = h.runWith("owner-a", "session-a", "process_model", `{"step":"mesh-segmentation","source_task_id":"@model1"}`, history, bigBangSelected)
	if content := h.content(result); content["source_confirmation_required"] != true || len(h.gateway.frozen) != 0 {
		t.Fatalf("a model no tool proposed ran without a question: %v", content)
	}
}

// An Offering ID the Agent made up is not echoed in the text.
func TestAnUnknownServiceIsRefusedWithoutEchoingItsID(t *testing.T) {
	h := namedHarness(t, "auto")
	result, _ := h.runWith("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model2","offering_id":"acme/made-up"}`, namedHistory("再分割一次"), bigBangSelected)
	content := h.content(result)
	message, _ := content["error"].(string)
	if content["success"] != false || message == "" || strings.Contains(message, "acme/made-up") || len(h.gateway.frozen) != 0 {
		t.Fatalf("unknown service = %v", content)
	}
	if content["requested_offering_id"] != "acme/made-up" {
		t.Fatalf("the ID belongs in its own field: %v", content)
	}
}

// I: a step that waits on its approval card tells the Agent the frozen price
// as it is: base, extra and what the extra pays for, and the total.
func TestAStepOnItsApprovalCardReportsTheExactQuote(t *testing.T) {
	h := namedHarness(t, "manual")
	h.gateway.normalize = true // the kernel freezes the fixed export formats
	result, _ := h.runWith("owner-a", "session-a", "process_model", `{"step":"mesh-segmentation","source_task_id":"@model2"}`,
		namedHistory("用 Tripo 再分割一次"), bigBangSelected)
	if !result.NoSubmission || h.gateway.submitCount != 0 || len(h.gateway.frozen) != 1 {
		t.Fatalf("manual Tripo with extra formats must wait on its card: %+v", result)
	}
	content := h.content(result)
	if content["status"] != "awaiting_approval" || content["message"] != h.app.Chat.Agent.ConsentWaitingContent {
		t.Fatalf("waiting result = %v", content)
	}
	// #327 (dev-test 4876a4f8): a reply said "已重新提交请求" while the step waited on its card. The
	// result carries the status in words to use as they are: a card waits, nothing runs yet.
	status, _ := content["status_text"].(string)
	if status == "" || status != h.app.Chat.Agent.ConsentWaitingStatus || !strings.Contains(status, "批准卡") ||
		strings.Contains(status, "已提交") || strings.Contains(status, "已重新提交") {
		t.Fatalf("status_text = %q", status)
	}
	quote, _ := content["quote"].(map[string]any)
	if quote["base_credits"] != float64(55) || quote["extra_credits"] != float64(50) || quote["total_credits"] != float64(105) {
		t.Fatalf("quote = %v", content["quote"])
	}
	if extra, _ := quote["extra_for"].(string); !strings.Contains(extra, "FBX") || !strings.Contains(extra, "3MF") {
		t.Fatalf("extra_for = %v", quote["extra_for"])
	}
	if content["provider"] != "Tripo3D" || offeringIDPattern.MatchString(content["message"].(string)) {
		t.Fatalf("service = %v", content)
	}
}

// I: list_session_models names, per next step, the services that take this
// model, so the Agent does not recommend one that cannot.
func TestSessionModelsListTheServicesThatTakeEachModel(t *testing.T) {
	h := namedHarness(t, "auto")
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Models []struct {
			TaskID    string `json:"task_id"`
			NextSteps []struct {
				Tool     string `json:"tool"`
				Step     string `json:"step"`
				Services []struct {
					Provider string `json:"provider"`
					Model    string `json:"model"`
				} `json:"services"`
			} `json:"next_steps"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	for _, model := range listed.Models {
		if model.TaskID != "bang-big" {
			continue
		}
		for _, step := range model.NextSteps {
			if step.Step != "mesh-segmentation" {
				continue
			}
			if len(step.Services) != 1 || !strings.Contains(step.Services[0].Model, "Smart Segmentation") {
				t.Fatalf("segmentation services of the 91.8 MB Bang result = %+v", step.Services)
			}
			return
		}
	}
	t.Fatalf("no segmentation step listed: %s", raw)
}

// The plan tool follows the same rule: no plan with another service than the
// one the person named.
func TestAPlanDoesNotSwapTheNamedService(t *testing.T) {
	for _, steps := range []string{
		`[{"capability":"mesh-segmentation","parameters":{}}]`,
		`[{"capability":"mesh-segmentation","offering_id":"tripo/geometry/smartsegment-model","parameters":{}}]`,
	} {
		h := newAgentPlanHarness(t, "auto", false)
		h.gateway.tasks["rodin-1"] = rodinOriginal(h.processingHarness)
		h.gateway.tasks["bang-big"] = bigBangResult(h.processingHarness)
		command := h.command(`{"source_task_id":"@model2","steps":` + steps + `}`)
		command.History, command.Options = namedHistory("把这个模型分割成部件，用 Bang"), bigBangSelected
		result, err := h.executor.Execute(t.Context(), command, nil)
		if err != nil {
			t.Fatal(err)
		}
		content := h.content(result)
		message, _ := content["error"].(string)
		if content["service_confirmation_required"] != true || content["plan_id"] != nil || offeringIDPattern.MatchString(message) {
			t.Fatalf("plan with another service than the named one = %v", content)
		}
	}
}

// How names are read: a brand counts only as its own word, a Latin negation
// only as a word, and words that merely contain a negation do not negate.
func TestServiceNamesAreReadAsWords(t *testing.T) {
	for text, runs := range map[string]bool{
		"try another Tripo segmentation":     true,  // "another" holds "not" but is not a negation
		"特别想用 Tripo 再分割一次":                   true,  // "特别" is not "别用"
		"Bangalore 风格的模型再分割一次":               true,  // "Bang" is not a word of "Bangalore"
		"再分割一次，用 tripo":                      true,  // case-insensitive
		"use Bang, not Tripo, segment again": false, // Bang named, Tripo declined
		"除了 Tripo 都行，再分割一次":                  false,
	} {
		h := namedHarness(t, "auto")
		result, _ := h.runWith("owner-a", "session-a", "process_model", `{"step":"mesh-segmentation","source_task_id":"@model2"}`,
			namedHistory(text), bigBangSelected)
		if ran := !result.NoSubmission && len(h.gateway.frozen) == 1; ran != runs {
			t.Errorf("%q: ran=%v, want %v: %s", text, ran, runs, result.AgentContent)
		}
	}
}

// With #325, a service may refuse a model because the provider no longer
// keeps its original task; the named-service refusal says so instead of
// claiming the service only takes another kind of result.
func TestANamedServiceRefusedForAnExpiredSourceSaysItExpired(t *testing.T) {
	h := newProcessingHarness(t, "auto", expiredMeshyTasks)
	result, _ := h.runWith("owner-a", "session-a", "process_model", `{"step":"animation","source_task_id":"old-rig","action_id":3}`,
		[]chat.Message{{ID: "u-1", Role: "user", Content: "用 Meshy 给这个模型做动画"}}, chat.TurnOptions{})
	_, message := h.assertAsksAboutService(result)
	if !strings.Contains(message, "no longer keeps") || strings.Contains(message, "only takes") {
		t.Fatalf("the refusal does not say the original task expired: %s", message)
	}
}
