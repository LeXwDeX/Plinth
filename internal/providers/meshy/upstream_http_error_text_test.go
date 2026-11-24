package meshy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	meshyopenapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v1"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
)

// scenarioUpstream is the Meshy fake answering scenario to the requests of
// one method; the fake itself renders the error.
func scenarioUpstream(t *testing.T, method string, scenario fakeupstream.Scenario) *httptest.Server {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{RetryAfter: 3 * time.Second, FailTimes: 100},
		meshyopenapiv1.Version(), meshyopenapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == method {
			r.Header.Set(fakeupstream.ScenarioHeader, string(scenario))
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func providerFailure(t *testing.T, err error) *generation.Failure {
	t.Helper()
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("err = %v, want a ProviderError", err)
	}
	return provider.Failure
}

// The person reads the upstream HTTP error as "MeshyAI 网关返回 {status}，原因：
// {reason}" on the task card and in the Agent's tool result, whatever the
// upstream's own wording (it stays in the private evidence).
func TestMeshyHTTPErrorReadsAsUpstreamText(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	provider := catalog.ProviderDisplayName(application.Runtime.ProviderConnections, "meshy.v2")
	classText := func(failure *generation.Failure) string {
		return application.Content.Messages["failure."+string(failure.Class)]
	}
	start := func(t *testing.T, server *httptest.Server) (*meshy.Adapter, generation.StartRequest) {
		taskCatalog, offering := upgradeOffering(t, "meshy/meshy-7.1/text-to-3d", server)
		adapter := newMeshyAdapter(t, meshy.Config{
			BaseURL: server.URL + "/meshy", APIKey: "test", HTTPClient: server.Client(), AllowHTTPAssets: true,
		})
		return adapter, generation.StartRequest{
			TaskID: "task-1", AttemptID: "attempt-1", SubmissionKey: "submission-1", Offering: offering,
			Input: normalizedOfferingInput(t, taskCatalog, offering, `{"prompt":"a small robot"}`),
		}
	}

	t.Run("429 on create", func(t *testing.T) {
		adapter, request := start(t, scenarioUpstream(t, http.MethodPost, fakeupstream.ScenarioRateLimit))
		_, err := adapter.Start(context.Background(), request)
		failure := providerFailure(t, err)
		if failure.UpstreamStatus != http.StatusTooManyRequests || failure.Class != generation.FailureRateLimited {
			t.Fatalf("failure = %+v", failure)
		}
		want := "MeshyAI 网关返回 429，原因：上游服务器限流，请稍后再试。"
		if got := generationtool.PublicFailureDetail(application.Chat, provider, failure, classText(failure), nil); got != want {
			t.Fatalf("task card = %q, want %q", got, want)
		}
		if got := generationtool.LocalizedFailureMessage(application.Chat, provider, true, failure, nil); got != want {
			t.Fatalf("tool result = %q, want %q", got, want)
		}
		// The retry semantics stay the class's own.
		report := generation.ClassifyFailure(failure.Class, failure.Retryable, failure.Ambiguous)
		if report.Action != generation.ActionRetryLater || !report.Retryable {
			t.Fatalf("report = %+v", report)
		}
	})

	t.Run("5xx on poll", func(t *testing.T) {
		server := scenarioUpstream(t, http.MethodGet, fakeupstream.ScenarioServerError)
		adapter, request := start(t, server)
		started, err := adapter.Start(context.Background(), request)
		if err != nil || started.Job == nil {
			t.Fatalf("start = %+v, %v", started, err)
		}
		_, err = adapter.Observe(context.Background(), generation.ObserveRequest{
			TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: request.Offering,
			Job: *started.Job, Input: request.Input, Opaque: started.Opaque,
		})
		failure := providerFailure(t, err)
		if failure.UpstreamStatus < 500 || failure.Ambiguous {
			t.Fatalf("failure = %+v", failure)
		}
		want := "MeshyAI 网关返回 500，原因：上游服务器故障。"
		if got := generationtool.PublicFailureDetail(application.Chat, provider, failure, classText(failure), nil); got != want {
			t.Fatalf("task card = %q, want %q", got, want)
		}
		if got := generationtool.LocalizedFailureMessage(application.Chat, provider, true, failure, nil); got != want {
			t.Fatalf("tool result = %q, want %q", got, want)
		}
	})

	t.Run("5xx on create keeps the may-have-charged text", func(t *testing.T) {
		adapter, request := start(t, scenarioUpstream(t, http.MethodPost, fakeupstream.ScenarioServerError))
		_, err := adapter.Start(context.Background(), request)
		failure := providerFailure(t, err)
		if !failure.Ambiguous {
			t.Fatalf("failure = %+v", failure)
		}
		// The kernel stores an ambiguous failure as submission_unknown with
		// its evidence (handleAttemptFailure).
		failure.Class = generation.FailureSubmissionUnknown
		want := "MeshyAI 网关返回 500，原因：上游服务器故障。" + classText(failure)
		if got := generationtool.PublicFailureDetail(application.Chat, provider, failure, classText(failure), nil); got != want {
			t.Fatalf("task card = %q, want %q", got, want)
		}
		got := generationtool.LocalizedFailureMessage(application.Chat, provider, true, failure, nil)
		if want := "MeshyAI 网关返回 500，原因：上游服务器故障。" + application.Chat.Presentation.Messages.GenerationFailedAmbiguous; got != want {
			t.Fatalf("tool result = %q, want %q", got, want)
		}
	})
}
