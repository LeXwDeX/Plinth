package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiimagesv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/images-v1"
)

// TestRateLimitedImageQueuesThenSucceedsAgainstFakeUpstream：端到端——默认
// 目录 + 真实 openai-images 适配器 + 仓库内的假上游。上游前 2 次明确返回
// 429（rate_limited），任务回到 QUEUED 自动重试且不消耗 max_attempts；
// 第 3 次上游恢复，任务成功。
func TestRateLimitedImageQueuesThenSucceedsAgainstFakeUpstream(t *testing.T) {
	fake, err := fakeupstream.New(
		fakeupstream.Config{FailTimes: 2},
		openaiimagesv1.Version(),
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// 密钥前缀 fake-rate-limit 选中限流场景；FailTimes 按 API key 计数。
	settings := map[string]string{
		"openai_base_url": server.URL + openaiimagesv1.Prefix,
		"openai_api_key":  fakeupstream.KeyPrefix + "rate-limit-e2e",
	}
	harness := newAcmeHarness(t, taskCatalog, settings)
	accepted := harness.submitApproved(t, generation.TextToImage,
		"openai/gpt-image-2/text-to-image",
		json.RawMessage(`{"prompt":"rate limited still"}`), "rate-limit-e2e-1")

	// 第一轮：命中 429，排队而不是失败。
	harness.reconcile(t)
	queued, err := harness.module.Get(context.Background(), "owner-acme", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != generation.TaskQueued || !queued.RateLimitQueued || queued.Failure != nil {
		t.Fatalf("429 应排队: state=%s queued=%v failure=%#v",
			queued.State, queued.RateLimitQueued, queued.Failure)
	}

	// 之后上游恢复，任务自动成功。
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task state = %s failure=%#v", task.State, task.Failure)
	}
	if len(task.Assets) == 0 {
		t.Fatalf("成功的任务应有产物: %#v", task)
	}

	events, err := harness.module.Events(context.Background(), "owner-acme", accepted.TaskID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	countEvents := func(eventType string) int {
		n := 0
		for _, event := range events {
			if event.Type == eventType {
				n++
			}
		}
		return n
	}
	if got := countEvents("attempt.rate_limit_queued"); got != 2 {
		t.Fatalf("attempt.rate_limit_queued = %d, want 2", got)
	}
	// 限流排队不走普通重试，也不消耗 max_attempts。
	if got := countEvents("attempt.retry_scheduled"); got != 0 {
		t.Fatalf("attempt.retry_scheduled = %d, want 0", got)
	}
	if attempts := attemptCount(t, harness, "owner-acme", accepted.TaskID); attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}
