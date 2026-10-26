package generationtool_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func refusalHarness(t *testing.T, prepareErr error) (*processingHarness, *bytes.Buffer) {
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		return []generation.Task{meshyImageTask(h)}
	})
	h.gateway.prepareErr = prepareErr
	var logs bytes.Buffer
	h.config.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var err error
	if h.executor, err = generationtool.New(h.config); err != nil {
		t.Fatal(err)
	}
	return h, &logs
}

// #321 A: a step the Provider refuses reaches the Agent with the Provider's
// own reason, so the Agent repeats it instead of guessing one, and the server
// logs the refusal with the call it belongs to.
func TestProviderRefusalReasonReachesTheAgentAndTheLog(t *testing.T) {
	const reason = "来源 GLB 文件头声明的长度与实际字节数不一致，文件可能不完整"
	refused := &generation.Error{Code: generation.ErrInvalidInput, Operation: "validate_provider_input", Detail: "输入未通过供给项校验",
		Cause: &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureInvalidInput, SafeDetail: reason}}}
	h, logs := refusalHarness(t, refused)
	result, _ := h.run("process_model", `{"step":"mesh-segmentation","source_task_id":"meshy71-1"}`)
	content := h.content(result)
	message, _ := content["error"].(string)
	if !result.NoSubmission || content["success"] != false || !strings.Contains(message, reason) {
		t.Fatalf("refusal = %v", content)
	}
	if !strings.Contains(logs.String(), "process_model") || !strings.Contains(logs.String(), reason) || !strings.Contains(logs.String(), "session-a") {
		t.Fatalf("refusal not logged: %s", logs.String())
	}
}

// A server condition (no room for the upload copy) is said as such: nothing
// was submitted and it can be retried; the Agent does not blame the model.
func TestUnavailableProviderReasonReachesTheAgent(t *testing.T) {
	const reason = "服务端暂时无法写入模型文件的上传副本（临时空间不足或不可写），没有提交，可稍后重试"
	unavailable := &generation.Error{Code: generation.ErrUnavailable, Operation: "validate_provider_input", Detail: "输入未通过供给项校验",
		Cause: &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureTransport, Retryable: true, SafeDetail: reason}}}
	h, logs := refusalHarness(t, unavailable)
	result, _ := h.run("process_model", `{"step":"mesh-segmentation","source_task_id":"meshy71-1"}`)
	content := h.content(result)
	message, _ := content["error"].(string)
	if !result.NoSubmission || content["success"] != false || !strings.Contains(message, reason) {
		t.Fatalf("unavailable = %+v %v", result, content)
	}
	if !strings.Contains(logs.String(), reason) {
		t.Fatalf("not logged: %s", logs.String())
	}
}
