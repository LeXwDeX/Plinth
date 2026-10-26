package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// Guard (#298): a per-task worker error must not stop the service. The mock
// upstream returns the same job ID for two submissions; recording it for the
// second Task violates the unique upstream-job index. Before the fix the
// coordinator returned that error, Application.Run shut down HTTP and the
// process exited. Now the second Task fails (submission_unknown), HTTP keeps
// answering, readiness stays green and a concurrent Task completes.
func TestWorkerErrorDoesNotStopHTTPServer(t *testing.T) {
	root := t.TempDir()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	_ = probe.Close()
	application, err := New(Config{
		Environment:               "development",
		Address:                   address,
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(filepath.Join(root, "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("application did not shut down")
		}
	})

	base := "http://" + address
	waitStatus(t, base+"/readyz", http.StatusOK, done)

	const owner, session = "owner-a", "guard-session"
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{
		ID: session, OwnerID: owner, Title: "worker guard", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	submit := func(prompt string) generation.TaskID {
		t.Helper()
		input, _ := json.Marshal(map[string]string{"prompt": prompt})
		prepared, err := application.generation.PrepareInput(context.Background(), generation.PrepareInputCommand{
			OwnerID: owner, SessionID: session, Capability: generation.TextToImage,
			OfferingID: "openai/gpt-image-2/text-to-image", Input: input,
		})
		if err != nil {
			t.Fatal(err)
		}
		key := "guard:" + prompt
		grants := executiongrant.Service{Grants: repository, Prepared: repository}
		principal := executiongrant.Principal{OwnerID: owner, ActorID: "guard-human", Source: executiongrant.HumanBrowser}
		grant, err := grants.Prepare(context.Background(), executiongrant.PrepareCommand{
			Principal: principal, SessionID: session,
			OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
			PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
			IdempotencyKey: key, OperationID: "guard-operation:" + prompt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if grant.Status != executiongrant.Approved {
			if grant, err = grants.Decide(context.Background(), principal, grant.RequestID, executiongrant.Approve); err != nil {
				t.Fatal(err)
			}
		}
		accepted, err := application.generation.SubmitPrepared(context.Background(), generation.SubmitPreparedCommand{
			OwnerID: owner, SessionID: session, PreparedInputID: prepared.ID, IdempotencyKey: key,
			OperationID: "guard-operation:" + prompt, AuthorizationRef: grant.RequestID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return accepted.TaskID
	}

	first := submit(mock.MarkerDuplicateRemoteID + " first")
	waitTask(t, application, owner, first, done, func(task generation.Task) bool {
		return task.State == generation.TaskSucceeded
	})
	duplicate := submit(mock.MarkerDuplicateRemoteID + " second")
	other := submit("a concurrent unrelated image")

	failed := waitTask(t, application, owner, duplicate, done, func(task generation.Task) bool { return task.State.Terminal() })
	if failed.State != generation.TaskFailed || failed.Failure == nil || failed.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("duplicate task state=%s failure=%#v", failed.State, failed.Failure)
	}
	if task := waitTask(t, application, owner, other, done, func(task generation.Task) bool { return task.State.Terminal() }); task.State != generation.TaskSucceeded {
		t.Fatalf("concurrent task state=%s failure=%#v", task.State, task.Failure)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		waitStatus(t, base+path, http.StatusOK, done)
	}
	select {
	case err := <-done:
		t.Fatalf("application stopped after a worker error: %v", err)
	default:
	}
}

func waitTask(t *testing.T, application *Application, owner generation.OwnerID, id generation.TaskID, done <-chan error, ready func(generation.Task) bool) generation.Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("application stopped while waiting for task %s: %v", id, err)
		default:
		}
		task, err := application.generation.Get(context.Background(), owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if ready(task) {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s stuck in %s", id, task.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitStatus(t *testing.T, url string, want int, done <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("application stopped: %v", err)
		default:
		}
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
			last = fmt.Sprint(response.StatusCode)
		} else {
			last = err.Error()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d: %s", url, want, last)
}
