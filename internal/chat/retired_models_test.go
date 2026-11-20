package chat_test

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

var meshyRetired = []catalog.RetiredModelSuccessor{{
	ProviderID: "meshy", ModelID: "meshy-7", ModelName: "Meshy 7",
	SuccessorModelID: "meshy-7.1", SuccessorModelName: "Meshy 7.1",
}}

// gptImageRetired is the 2D entry of the shipped Schema (#224), the one a 2D
// lock can name.
var gptImageRetired = catalog.RetiredModelSuccessor{
	ProviderID: "openai", ModelID: "gpt-image-2.5-sunburst", ModelName: "GPT Image 2.5 Sunburst (OpenAI)",
	SuccessorModelID: "gpt-image-2.6-sunburst", SuccessorModelName: "GPT Image 2.6 Sunburst (OpenAI)",
}

// #329 follow-up C: a turn that names a retired model (an old tab, an API
// client) is moved to the successor on the server, for the domain in view and
// for the other domains, and the turn records the switch so its generation
// step can say so. The session then remembers the successor.
func TestStartTurnMovesRetiredModelsToTheirSuccessor(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.RetiredModels = meshyRetired
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "Retired")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello",
		Options: chat.TurnOptions{CreativeDomain: "model-3d", AIModel: "meshy-7",
			DomainModels: map[string]string{"model-3d": "meshy-7", "image": "image-model"}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Options.AIModel != "meshy-7.1" || turn.Options.DomainModels["model-3d"] != "meshy-7.1" || turn.Options.DomainModels["image"] != "image-model" {
		t.Fatalf("stored models = %q %v", turn.Options.AIModel, turn.Options.DomainModels)
	}
	want := []chat.RetiredModelSwitch{{Domain: "model-3d", From: "meshy-7", FromName: "Meshy 7", To: "meshy-7.1", ToName: "Meshy 7.1"}}
	if !reflect.DeepEqual(turn.Options.RetiredModels, want) {
		t.Fatalf("recorded switches = %+v", turn.Options.RetiredModels)
	}
	models, retired, err := service.SessionModels(ctx, "owner-a", session.ID)
	if err != nil || models["model-3d"] != "meshy-7.1" || models["image"] != "image-model" || len(retired) != 1 || retired["model-3d"] != "meshy-7" {
		t.Fatalf("session models = %v, retired %v, %v", models, retired, err)
	}
}

// A turn stored before the model was retired keeps its frozen options, but
// the session's remembered pick is read as the successor.
func TestSessionModelsReadRetiredPicksAsTheirSuccessor(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.RetiredModels = meshyRetired
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "Old")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	repository.turns["old"] = chat.Turn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: "old",
		Options:   chat.TurnOptions{CreativeDomain: "image", AIModel: "image-model", DomainModels: map[string]string{"model-3d": "meshy-7"}},
		CreatedAt: at, UpdatedAt: at}
	models, retired, err := service.SessionModels(ctx, "owner-a", session.ID)
	if err != nil || models["model-3d"] != "meshy-7.1" || models["image"] != "image-model" || len(retired) != 1 || retired["model-3d"] != "meshy-7" {
		t.Fatalf("session models = %v, retired %v, %v", models, retired, err)
	}
	if repository.turns["old"].Options.DomainModels["model-3d"] != "meshy-7" {
		t.Fatal("the frozen turn was rewritten")
	}
}

// lockedSession creates an owner-a session whose explicit 3D and 2D locks are
// the given ones (empty: no lock), written the way the person's pick writes
// them.
func lockedSession(t *testing.T, service *chat.Service, lock3D, lockImage string) chat.Session {
	t.Helper()
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "Locked")
	if err != nil {
		t.Fatal(err)
	}
	if lock3D != "" {
		if _, err := service.SetExplicitModel3D(ctx, "owner-a", session.ID, lock3D); err != nil {
			t.Fatal(err)
		}
	}
	if lockImage != "" {
		if _, err := service.SetExplicitModelImage(ctx, "owner-a", session.ID, lockImage); err != nil {
			t.Fatal(err)
		}
	}
	return session
}

// startedTurn starts a turn of owner-a with the options and reads it back as
// stored, the frozen copy the orchestrator and the tools run on.
func startedTurn(t *testing.T, service *chat.Service, sessionID string, options chat.TurnOptions) chat.Turn {
	t.Helper()
	ctx := context.Background()
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: sessionID, Content: "hello", Options: options})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", sessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

// requireLocksStored checks the session's own lock rows, which a turn does not
// rewrite.
func requireLocksStored(t *testing.T, service *chat.Service, sessionID, want3D, wantImage string) {
	t.Helper()
	ctx := context.Background()
	if got, err := service.SessionExplicitModel3D(ctx, "owner-a", sessionID); err != nil || got != want3D {
		t.Fatalf("stored 3D lock = %q, %v, want %q", got, err, want3D)
	}
	if got, err := service.SessionExplicitModelImage(ctx, "owner-a", sessionID); err != nil || got != wantImage {
		t.Fatalf("stored 2D lock = %q, %v, want %q", got, err, wantImage)
	}
}

// #380 S1: the session's explicit 3D and 2D locks follow a retired model to
// its successor when a turn starts. The stored turn carries the moved lock, so
// every tool of the domain (ExplicitModelFor, what the executor reads) sees a
// lock that names an available Offering instead of refusing it, and the lock
// stays a lock. The session's own rows are the person's click and stay as
// they are, as the picks' restore does not write them either.
func TestStartTurnMovesExplicitModelLocksOfRetiredModels(t *testing.T) {
	cases := []struct {
		name              string
		domain            string
		lock3D, lockImage string
		want3D, wantImage string
	}{
		{"a 3D lock on a retired model", "model-3d", "meshy-7", "", "meshy-7.1", ""},
		{"a 2D lock on a retired model", "image", "", "gpt-image-2.5-sunburst", "", "gpt-image-2.6-sunburst"},
		{"both locks while the other domain is in view", "image", "meshy-7", "gpt-image-2.5-sunburst", "meshy-7.1", "gpt-image-2.6-sunburst"},
		{"locks on models that are not retired", "model-3d", "meshy-7.1", "gpt-image-2.6-sunburst", "meshy-7.1", "gpt-image-2.6-sunburst"},
		{"one retired and one current lock", "model-3d", "meshy-7", "gpt-image-2.6-sunburst", "meshy-7.1", "gpt-image-2.6-sunburst"},
		{"no locks", "model-3d", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
				config.RetiredModels = append(slices.Clone(meshyRetired), gptImageRetired)
			})
			session := lockedSession(t, service, tc.lock3D, tc.lockImage)
			turn := startedTurn(t, service, session.ID, chat.TurnOptions{CreativeDomain: tc.domain})
			if turn.Options.ExplicitModel3D != tc.want3D || turn.Options.ExplicitModelImage != tc.wantImage {
				t.Fatalf("frozen locks = %q %q, want %q %q", turn.Options.ExplicitModel3D, turn.Options.ExplicitModelImage, tc.want3D, tc.wantImage)
			}
			if turn.Options.ExplicitModelFor("model-3d") != tc.want3D || turn.Options.ExplicitModelFor("image") != tc.wantImage {
				t.Fatalf("locks by domain = %q %q", turn.Options.ExplicitModelFor("model-3d"), turn.Options.ExplicitModelFor("image"))
			}
			if len(turn.Options.RetiredModels) != 0 {
				t.Fatalf("a lock switch was recorded: %+v", turn.Options.RetiredModels)
			}
			requireLocksStored(t, service, session.ID, tc.lock3D, tc.lockImage)
		})
	}
}

// The session row keeps the retired name, so every later turn moves the lock
// again and runs on the successor.
func TestStartTurnMovesARetiredLockOnEveryTurn(t *testing.T) {
	service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
		config.RetiredModels = meshyRetired
	})
	session := lockedSession(t, service, "meshy-7", "")
	for turnNumber := 1; turnNumber <= 2; turnNumber++ {
		turn := startedTurn(t, service, session.ID, chat.TurnOptions{CreativeDomain: "model-3d"})
		if turn.Options.ExplicitModel3D != "meshy-7.1" {
			t.Fatalf("turn %d froze the lock %q", turnNumber, turn.Options.ExplicitModel3D)
		}
	}
	requireLocksStored(t, service, session.ID, "meshy-7", "")
}

// A lock moves together with the picks of its turn, whether the turn names
// other domains' picks or not, and only the picks are recorded as switches.
func TestStartTurnMovesRetiredLocksTogetherWithPicks(t *testing.T) {
	service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
		config.RetiredModels = append(slices.Clone(meshyRetired), gptImageRetired)
	})
	session := lockedSession(t, service, "meshy-7", "gpt-image-2.5-sunburst")
	turn := startedTurn(t, service, session.ID, chat.TurnOptions{CreativeDomain: "model-3d", AIModel: "meshy-7",
		DomainModels: map[string]string{"model-3d": "meshy-7", "image": "gpt-image-2.5-sunburst"}})
	if turn.Options.AIModel != "meshy-7.1" || turn.Options.DomainModels["image"] != "gpt-image-2.6-sunburst" {
		t.Fatalf("picks = %q %v", turn.Options.AIModel, turn.Options.DomainModels)
	}
	if turn.Options.ExplicitModel3D != "meshy-7.1" || turn.Options.ExplicitModelImage != "gpt-image-2.6-sunburst" {
		t.Fatalf("locks = %q %q", turn.Options.ExplicitModel3D, turn.Options.ExplicitModelImage)
	}
	want := []chat.RetiredModelSwitch{
		{Domain: "model-3d", From: "meshy-7", FromName: "Meshy 7", To: "meshy-7.1", ToName: "Meshy 7.1"},
		{Domain: "image", From: "gpt-image-2.5-sunburst", FromName: "GPT Image 2.5 Sunburst (OpenAI)",
			To: "gpt-image-2.6-sunburst", ToName: "GPT Image 2.6 Sunburst (OpenAI)"},
	}
	if !reflect.DeepEqual(turn.Options.RetiredModels, want) {
		t.Fatalf("recorded switches = %+v", turn.Options.RetiredModels)
	}
}

// A model retired without a successor is not in ServiceConfig.RetiredModels
// (catalog.RetiredModelSuccessors lists only entries whose successor is
// offered), so its picks are left as they are and so are the locks naming it:
// the lock keeps the person's choice and the disabled Offering is refused
// later, as before. Nothing retired at all is the same.
func TestStartTurnLeavesLocksOnModelsWithoutASuccessorAsItLeavesPicks(t *testing.T) {
	for name, retired := range map[string][]catalog.RetiredModelSuccessor{
		"another model is retired": meshyRetired,
		"nothing is retired":       nil,
	} {
		t.Run(name, func(t *testing.T) {
			service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
				config.RetiredModels = retired
			})
			session := lockedSession(t, service, "legacy-3d", "legacy-image")
			turn := startedTurn(t, service, session.ID, chat.TurnOptions{CreativeDomain: "model-3d", AIModel: "legacy-3d",
				DomainModels: map[string]string{"image": "legacy-image"}})
			if turn.Options.AIModel != "legacy-3d" || turn.Options.DomainModels["image"] != "legacy-image" {
				t.Fatalf("picks = %q %v", turn.Options.AIModel, turn.Options.DomainModels)
			}
			if turn.Options.ExplicitModel3D != "legacy-3d" || turn.Options.ExplicitModelImage != "legacy-image" {
				t.Fatalf("locks = %q %q", turn.Options.ExplicitModel3D, turn.Options.ExplicitModelImage)
			}
			if len(turn.Options.RetiredModels) != 0 {
				t.Fatalf("recorded switches = %+v", turn.Options.RetiredModels)
			}
		})
	}
}

// The moved lock is what the turn is stored with: it survives the repository's
// encoding of the options, and the session's own row is not rewritten.
func TestStartTurnStoresAMovedLockWithTheTurn(t *testing.T) {
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.RetiredModels = meshyRetired
	})
	session := lockedSession(t, service, "meshy-7", "")
	turn := startedTurn(t, service, session.ID, chat.TurnOptions{CreativeDomain: "model-3d"})
	if turn.Options.ExplicitModel3D != "meshy-7.1" || turn.Options.ExplicitModelImage != "" {
		t.Fatalf("stored locks = %q %q", turn.Options.ExplicitModel3D, turn.Options.ExplicitModelImage)
	}
	requireLocksStored(t, service, session.ID, "meshy-7", "")
}
