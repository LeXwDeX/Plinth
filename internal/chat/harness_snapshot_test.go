package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

func harnessChatFixture(t *testing.T) (*chat.Service, *memoryChatRepository, chat.Session) {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "Harness chat")
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, session
}
func freeChatHarness(owner, session string) resolve.Snapshot {
	return resolve.Snapshot{Selection: harness.DefaultSelection(owner, session), Status: resolve.StatusFree, Input: map[string]json.RawMessage{}, MissingParameters: []string{}, SourceFingerprints: []resolve.SourceFingerprint{}}
}

func TestStartTurnHarnessIgnoresClientSnapshotAndFreezesServerBaseline(t *testing.T) {
	service, repository, session := harnessChatFixture(t)
	ctx := context.Background()
	calls := 0
	service.SetHarnessSnapshotResolver(func(_ context.Context, owner, sessionID string) (resolve.Snapshot, error) {
		calls++
		if owner != "owner-a" || sessionID != session.ID {
			t.Fatal("resolver scope differs from authenticated turn")
		}
		return freeChatHarness(owner, sessionID), nil
	})
	malicious := freeChatHarness("other", "foreign")
	malicious.Selection.Revision = 999
	malicious.Status = resolve.StatusReady
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello", Options: chat.TurnOptions{Harness: &malicious}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Options.Harness == nil || turn.Options.Harness.Status != resolve.StatusFree || turn.Options.Harness.Selection.Revision != 0 || turn.Options.Harness.Selection.Digest == "" || turn.Options.Harness.Selection.OwnerID != "owner-a" || turn.Options.Harness.Selection.SessionID != session.ID {
		t.Fatalf("server baseline not frozen %+v", turn.Options.Harness)
	}
	if calls != 1 || len(repository.turns) != 1 {
		t.Fatal("unexpected resolver or persistence count")
	}
	raw, err := json.Marshal(turn.Options)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded chat.TurnOptions
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.Harness == nil || reloaded.Harness.Selection.Binding != turn.Options.Harness.Selection.Binding {
		t.Fatal("options serialization lost binding")
	}
}

func TestStartTurnHarnessResolverRunsOnlyForOwnedSession(t *testing.T) {
	service, repository, session := harnessChatFixture(t)
	calls := 0
	service.SetHarnessSnapshotResolver(func(context.Context, string, string) (resolve.Snapshot, error) {
		calls++
		return resolve.Snapshot{}, errors.New("must not be called")
	})
	for _, scope := range [][2]string{{"owner-b", session.ID}, {"owner-a", "missing"}} {
		if _, err := service.StartTurn(context.Background(), chat.StartTurn{OwnerID: scope[0], SessionID: scope[1], Content: "hello"}); !errors.Is(err, chat.ErrSessionNotFound) {
			t.Fatalf("foreign turn error %v", err)
		}
	}
	if calls != 0 || len(repository.turns) != 0 || len(repository.messages[session.ID]) != 0 {
		t.Fatal("foreign request resolved or persisted")
	}
}

func TestStartTurnHarnessReadFailureHasNoTurnOrMessages(t *testing.T) {
	service, repository, session := harnessChatFixture(t)
	failure := errors.New("synthetic Harness storage failure")
	service.SetHarnessSnapshotResolver(func(context.Context, string, string) (resolve.Snapshot, error) { return resolve.Snapshot{}, failure })
	if _, err := service.StartTurn(context.Background(), chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello"}); !errors.Is(err, failure) {
		t.Fatal("resolver failure swallowed", err)
	}
	if len(repository.turns) != 0 || len(repository.messages[session.ID]) != 0 || len(repository.events[session.ID]) != 0 || len(repository.streams) != 0 {
		t.Fatal("failed lookup created durable turn data")
	}
}

func TestStartTurnHarnessSnapshotIsDetachedFromLaterSelectionChanges(t *testing.T) {
	service, _, session := harnessChatFixture(t)
	ctx := context.Background()
	server := freeChatHarness("owner-a", session.ID)
	selected := server.Selection
	selected.OperationID = "remesh"
	selected.EnabledOperations = []string{"remesh", "rigging"}
	selected.OfferingID = "meshy/processing/remesh"
	selected.OfferingRevision = "revision"
	selected.FixedParameters = map[string]json.RawMessage{"topology": json.RawMessage(`"quad"`)}
	selected.Sources = []harness.Source{{Parameter: "source_task", TaskID: "task"}}
	selected.Revision = 1
	var err error
	selected, err = harness.NormalizeSelection(selected)
	if err != nil {
		t.Fatal(err)
	}
	server.Selection = selected
	server.Status = resolve.StatusReady
	server.ToolArguments = map[string]json.RawMessage{"step": json.RawMessage(`"remesh"`)}
	server.Input = map[string]json.RawMessage{"topology": json.RawMessage(`"quad"`)}
	server.MissingParameters = []string{"prompt"}
	server.SourceFingerprints = []resolve.SourceFingerprint{{TaskID: "task", AssetID: "asset", SHA256: "hash", Size: 123}}
	server.Tool = &catalog.ChatToolSchema{Name: "private-tool"}
	server.Offering = &generation.Offering{ProviderOptions: json.RawMessage(`{"secret":"private-wire"}`)}
	calls := 0
	service.SetHarnessSnapshotResolver(func(context.Context, string, string) (resolve.Snapshot, error) { calls++; return server, nil })
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "remesh"})
	if err != nil {
		t.Fatal(err)
	}
	original := server.Selection.Binding
	server.Selection.FixedParameters["topology"][1] = 'x'
	server.Selection.EnabledOperations[0] = "animation"
	server.Selection.Sources[0].TaskID = "other"
	server.Input["topology"][1] = 'x'
	server.ToolArguments["step"][1] = 'x'
	server.MissingParameters[0] = "other"
	server.SourceFingerprints[0].SHA256 = "changed"
	server.Selection.Revision = 2
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen := turn.Options.Harness
	if frozen == nil || frozen.Selection.Binding != original || string(frozen.Input["topology"]) != `"quad"` || string(frozen.ToolArguments["step"]) != `"remesh"` || string(frozen.Selection.FixedParameters["topology"]) != `"quad"` || frozen.Selection.EnabledOperations[0] != "remesh" || frozen.Selection.Sources[0].TaskID != "task" || frozen.MissingParameters[0] != "prompt" || frozen.SourceFingerprints[0].SHA256 != "hash" {
		t.Fatalf("later state mutated frozen turn %+v", frozen)
	}
	if frozen.Tool != nil || frozen.Offering != nil || frozen.Operation != nil {
		t.Fatal("private transient pointers retained in durable snapshot")
	}
	if _, err := service.RecoverTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("recovery replaced frozen snapshot")
	}
}

func TestStartTurnWithoutHarnessHookDropsClientDataAndHistoricalTurnsStayNil(t *testing.T) {
	service, _, session := harnessChatFixture(t)
	ctx := context.Background()
	client := freeChatHarness("owner-a", session.ID)
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "legacy", Options: chat.TurnOptions{Harness: &client}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	service.SetHarnessSnapshotResolver(func(context.Context, string, string) (resolve.Snapshot, error) {
		calls++
		return freeChatHarness("owner-a", session.ID), nil
	})
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil || turn.Options.Harness != nil {
		t.Fatalf("legacy was upgraded %+v %v", turn.Options.Harness, err)
	}
	turns, err := service.RecoverTurns(ctx)
	if err != nil || len(turns) != 1 || turns[0].Options.Harness != nil || calls != 0 {
		t.Fatal("recovery upgraded legacy turn", err)
	}
}

func TestStartTurnHarnessRejectsMiswiredServerSnapshotBeforePersistence(t *testing.T) {
	for _, name := range []string{"owner", "session", "legacy-empty", "changed-content", "malformed-input"} {
		t.Run(name, func(t *testing.T) {
			service, repository, session := harnessChatFixture(t)
			snapshot := freeChatHarness("owner-a", session.ID)
			switch name {
			case "owner":
				snapshot.Selection.OwnerID = "foreign"
			case "session":
				snapshot.Selection.SessionID = "foreign"
			case "legacy-empty":
				snapshot.Selection.Digest = ""
			case "changed-content":
				snapshot.Selection.OperationID = "tampered"
			case "malformed-input":
				snapshot.Input["bad"] = json.RawMessage(`invalid-json`)
			}
			service.SetHarnessSnapshotResolver(func(context.Context, string, string) (resolve.Snapshot, error) { return snapshot, nil })
			if _, err := service.StartTurn(context.Background(), chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello"}); err == nil {
				t.Fatal("invalid trusted snapshot accepted")
			}
			if len(repository.turns) != 0 || len(repository.messages[session.ID]) != 0 || len(repository.streams) != 0 {
				t.Fatal("invalid snapshot persisted a turn")
			}
		})
	}
}
