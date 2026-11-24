package httpapi

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A Task the upstream answered with an HTTP error reads, on the task card, the
// task stream and the external MCP, "{connection display name} 网关返回
// {status}，原因：{reason}"; the upstream's own words stay private.
func TestTaskFailureNamesTheUpstreamHTTPError(t *testing.T) {
	t.Parallel()
	server, application, _ := localizedTestServer(t)
	task := func(failure generation.Failure) generation.Task {
		var meshy generation.Offering
		meshy.AdapterKey = "meshy.v2"
		return generation.Task{ID: "task-1", Offering: meshy, State: generation.TaskFailed, Failure: &failure}
	}

	for _, tc := range []struct {
		failure generation.Failure
		want    string
	}{
		{generation.Failure{Class: generation.FailureRateLimited, Retryable: true, UpstreamStatus: 429, UpstreamExcerpt: "Rate limit exceeded."},
			"MeshyAI 网关返回 429，原因：上游服务器限流，请稍后再试。"},
		{generation.Failure{Class: generation.FailureUpstreamUnavailable, Retryable: true, UpstreamStatus: 502},
			"MeshyAI 网关返回 502，原因：上游服务器故障。"},
		{generation.Failure{Class: generation.FailureInsufficientBalance, UpstreamStatus: 402},
			"MeshyAI 网关返回 402，原因：上游账户余额不足。"},
		{generation.Failure{Class: generation.FailureTransport, Retryable: true},
			"MeshyAI 网关连接失败，原因：无法连接上游服务器。"},
		// Ambiguous: the may-have-charged text stays after the upstream text.
		{generation.Failure{Class: generation.FailureSubmissionUnknown, Ambiguous: true, UpstreamStatus: 502},
			"MeshyAI 网关返回 502，原因：上游服务器故障。" + application.Content.Messages["failure.submission_unknown"]},
		// A content-safety refusal keeps its own, more specific text.
		{generation.Failure{Class: generation.FailureContentRejected, UpstreamStatus: 400},
			application.Content.Messages["failure.content_rejected"]},
		// No HTTP status: the class text.
		{generation.Failure{Class: generation.FailureRateLimited},
			application.Content.Messages["failure.rate_limited"]},
	} {
		if got := server.publicTaskFailure("", task(tc.failure)).Detail; got != tc.want {
			t.Errorf("%s/%d detail = %q, want %q", tc.failure.Class, tc.failure.UpstreamStatus, got, tc.want)
		}
	}

	failed := task(generation.Failure{Class: generation.FailureRateLimited, UpstreamStatus: 429})
	if got, want := server.publicTaskFailure("en", failed).Detail,
		"MeshyAI gateway returned 429: The upstream server is rate limiting requests; please try again later."; got != want {
		t.Fatalf("en detail = %q, want %q", got, want)
	}
	// A failure without its Task (an event) names no upstream: the class text.
	if got := server.publicFailure("", failed.Failure).Detail; got != application.Content.Messages["failure.rate_limited"] {
		t.Fatalf("event detail = %q", got)
	}
	// The external MCP's failure message is the same text.
	view, _ := server.mcpTaskView("", failed)
	if view.Failure == nil || view.Failure.Detail != "MeshyAI 网关返回 429，原因：上游服务器限流，请稍后再试。" || !view.Failure.upstream {
		t.Fatalf("mcp view = %+v", view.Failure)
	}
}
