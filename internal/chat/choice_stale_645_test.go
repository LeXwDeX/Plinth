package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// seedReplies stores assistant messages a test's cards belong to: a card is
// read only while the reply that asked it exists (#645).
func seedReplies(t *testing.T, repository *sqlite.Repository, owner, session string, ids ...string) {
	t.Helper()
	now := time.Now().UTC()
	for _, id := range ids {
		if _, err := repository.InsertMessageIfAbsent(context.Background(), owner,
			chat.Message{ID: id, SessionID: session, Role: "configured-assistant", Content: "reply", CreatedAt: now}, now); err != nil {
			t.Fatal(err)
		}
	}
}

// staleChoiceAgent answers every round with one text reply; during runs inside
// the round (the Agent mapping a typed reply, a stop the person asks for) and
// err fails the round.
type staleChoiceAgent struct {
	during func(context.Context)
	err    error
}

func (agent *staleChoiceAgent) Stream(ctx context.Context, _ agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	if agent.during != nil {
		agent.during(ctx)
	}
	if agent.err != nil {
		return agents.Completion{}, agent.err
	}
	if emit != nil {
		if err := emit(agents.Delta{Content: "好的"}); err != nil {
			return agents.Completion{}, err
		}
	}
	return agents.Completion{Content: "好的"}, nil
}

func (*staleChoiceAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "标题"}, nil
}

type staleChoiceRig struct {
	repository *sqlite.Repository
	service    *chat.Service
	session    string
	now        time.Time
}

func newStaleChoiceRig(t *testing.T) *staleChoiceRig {
	t.Helper()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	rig := &staleChoiceRig{repository: repository, now: time.Date(2026, 11, 26, 2, 40, 0, 0, time.UTC)}
	rig.service = newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Clock = func() time.Time { return rig.now }
	})
	rig.service.SetChoices(repository, 20)
	session, err := rig.service.CreateSession(context.Background(), "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	rig.session = session.ID
	return rig
}

// turn starts a turn with the person's typed content and processes it with
// agent; it returns the turn's reply ID.
func (rig *staleChoiceRig) turn(t *testing.T, content string, agent agents.Adapter) string {
	t.Helper()
	ctx := context.Background()
	rig.now = rig.now.Add(time.Minute)
	assistant, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: rig.session, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: rig.service, Schema: configuredChatSchema(t), Model: "m", Agent: agent, Tools: &stepToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: rig.session, AssistantMessageID: assistant.ID})
	return assistant.ID
}

// card stores a pending next-step card on reply, made a little after the
// reply's turn started.
func (rig *staleChoiceRig) card(t *testing.T, reply string) chat.Choice {
	t.Helper()
	card, err := rig.repository.CreateChoice(context.Background(), chat.Choice{OwnerID: "owner-a", SessionID: rig.session,
		AssistantMessageID: reply, CallID: "call-" + reply, Kind: "next_step", Capability: "next_step", StepIndex: -1,
		Options: []chat.ChoiceOption{
			{ID: "reupload", Label: "换一张图重新上传", Arguments: json.RawMessage(`{}`)},
			{ID: "split", Label: "拆成散图", Arguments: json.RawMessage(`{}`)},
		}, CreatedAt: rig.now.Add(10 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func (rig *staleChoiceRig) state(t *testing.T, id string) chat.Choice {
	t.Helper()
	choice, err := rig.repository.GetChoice(context.Background(), "owner-a", rig.session, id)
	if err != nil {
		t.Fatal(err)
	}
	return choice
}

// peek reads a card's state from inside a turn, where no test is at hand.
func (rig *staleChoiceRig) peek(id string) chat.ChoiceState {
	choice, err := rig.repository.GetChoice(context.Background(), "owner-a", rig.session, id)
	if err != nil {
		return chat.ChoiceState("unreadable: " + err.Error())
	}
	return choice.State
}

// #645: the person typed a new message instead of answering the open card.
// The card stays pending while the turn runs (the Agent could still map the
// text to it) and is superseded when the turn ends, whether the turn completed,
// failed or was stopped.
func TestATypedMessageSupersedesTheOpenCardWhenItsTurnEnds_645(t *testing.T) {
	for _, test := range []struct {
		name  string
		agent func(rig *staleChoiceRig, card chat.Choice, seen *chat.ChoiceState) *staleChoiceAgent
	}{
		{name: "completed", agent: func(rig *staleChoiceRig, card chat.Choice, seen *chat.ChoiceState) *staleChoiceAgent {
			return &staleChoiceAgent{during: func(context.Context) { *seen = rig.peek(card.ID) }}
		}},
		{name: "failed", agent: func(rig *staleChoiceRig, card chat.Choice, seen *chat.ChoiceState) *staleChoiceAgent {
			return &staleChoiceAgent{during: func(context.Context) { *seen = rig.peek(card.ID) },
				err: errors.New("upstream down")}
		}},
		{name: "stopped", agent: func(rig *staleChoiceRig, card chat.Choice, seen *chat.ChoiceState) *staleChoiceAgent {
			return &staleChoiceAgent{during: func(ctx context.Context) {
				*seen = rig.peek(card.ID)
				_, _, _ = rig.service.RequestTurnStop(context.WithoutCancel(ctx), "owner-a", rig.session)
			}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newStaleChoiceRig(t)
			first := rig.turn(t, "先拆这两条鱼", &staleChoiceAgent{})
			card := rig.card(t, first)
			var seen chat.ChoiceState
			second := rig.turn(t, "把这两条鱼基于顶视角拆成散图", test.agent(rig, card, &seen))
			if seen != chat.ChoicePending {
				t.Fatalf("while the turn ran the card was %q, want pending", seen)
			}
			if got := rig.state(t, card.ID); got.State != chat.ChoiceSuperseded {
				t.Fatalf("after the turn the card is %q, want superseded", got.State)
			}
			turn, err := rig.service.GetTurn(context.Background(), "owner-a", rig.session, second)
			if err != nil || turn.State == "configured-running" {
				t.Fatalf("turn = %+v %v", turn, err)
			}
		})
	}
}

// #645: when the Agent maps the typed reply to the open card during the turn,
// the card keeps the Agent's answer; a card the turn itself opens stays open.
func TestATypedReplyTheAgentMapsKeepsItsAnswer_645(t *testing.T) {
	rig := newStaleChoiceRig(t)
	first := rig.turn(t, "先拆这两条鱼", &staleChoiceAgent{})
	card := rig.card(t, first)
	var own chat.Choice
	rig.turn(t, "拆成散图吧", &staleChoiceAgent{during: func(ctx context.Context) {
		if _, err := rig.repository.AnswerChoice(ctx, "owner-a", rig.session, card.ID, chat.ChoiceRecord{
			OptionID: "split", Source: chat.ChoiceAnsweredByAgent, AnsweredAt: rig.now}); err != nil {
			panic(err)
		}
	}})
	got := rig.state(t, card.ID)
	if got.State != chat.ChoiceAnswered || got.AnswerSource != chat.ChoiceAnsweredByAgent || got.AnswerOptionID != "split" {
		t.Fatalf("mapped card = %+v", got)
	}
	// A card the next turn opens on its own reply is not touched by its end.
	third := rig.turn(t, "下一步呢", &staleChoiceAgent{})
	own = rig.card(t, third)
	if err := rig.service.SupersedeStaleChoices(context.Background(), chat.Turn{OwnerID: "owner-a", SessionID: rig.session,
		AssistantMessageID: third, CreatedAt: rig.now}); err != nil {
		t.Fatal(err)
	}
	if got := rig.state(t, own.ID); got.State != chat.ChoicePending {
		t.Fatalf("the turn's own card = %q, want pending", got.State)
	}
}

// #645: a server-started turn (a production-plan continuation, a production
// list item) is not the person's message and leaves the open card alone.
func TestAServerStartedTurnLeavesTheOpenCard_645(t *testing.T) {
	rig := newStaleChoiceRig(t)
	first := rig.turn(t, "先拆这两条鱼", &staleChoiceAgent{})
	card := rig.card(t, first)
	rig.now = rig.now.Add(time.Minute)
	for _, options := range []chat.TurnOptions{{PlanContinuation: "budget-1"}, {AssetListItemID: "item-1"}} {
		if err := rig.service.SupersedeStaleChoices(context.Background(), chat.Turn{OwnerID: "owner-a", SessionID: rig.session,
			AssistantMessageID: "server-turn", Options: options, CreatedAt: rig.now}); err != nil {
			t.Fatal(err)
		}
	}
	if got := rig.state(t, card.ID); got.State != chat.ChoicePending {
		t.Fatalf("a server-started turn ended the card: %q", got.State)
	}
}

// #645 A: replacing the last turn (regenerate, edit and resend) deletes only
// when the named messages are exactly the session's last turn now. A page that
// missed a newer turn names an older one and is refused without deleting.
func TestDeleteLastTurnMessageRefusesAnOutOfDatePage_645(t *testing.T) {
	ctx := context.Background()
	rig := newStaleChoiceRig(t)
	rig.turn(t, "先拆这两条鱼", &staleChoiceAgent{})
	rig.turn(t, "选择：换一张图重新上传", &staleChoiceAgent{})
	messages, err := rig.service.ListMessages(ctx, "owner-a", rig.session)
	if err != nil || len(messages) != 4 {
		t.Fatalf("messages = %+v %v", messages, err)
	}
	user1, reply1, user2, reply2 := messages[0].ID, messages[1].ID, messages[2].ID, messages[3].ID
	for _, refused := range []struct {
		message  string
		lastTurn []string
	}{
		{reply1, []string{reply1, user1}},           // the page missed the second turn
		{user2, []string{user2}},                    // the page missed a reply
		{reply2, []string{reply2, user2, reply1}},   // more than the last turn
		{reply2, []string{user2}},                   // the message is not named
		{reply2, []string{reply2, "not-a-message"}}, // a message the server does not have
	} {
		if err := rig.service.DeleteLastTurnMessage(ctx, "owner-a", rig.session, refused.message, refused.lastTurn); !errors.Is(err, chat.ErrNotLastTurn) {
			t.Fatalf("delete %s of %v = %v, want ErrNotLastTurn", refused.message, refused.lastTurn, err)
		}
	}
	if after, _ := rig.service.ListMessages(ctx, "owner-a", rig.session); len(after) != 4 {
		t.Fatalf("a refused replacement deleted messages: %d left", len(after))
	}
	// The real last turn is deleted reply first, then its user message.
	if err := rig.service.DeleteLastTurnMessage(ctx, "owner-a", rig.session, reply2, []string{reply2, user2}); err != nil {
		t.Fatal(err)
	}
	if err := rig.service.DeleteLastTurnMessage(ctx, "owner-a", rig.session, user2, []string{user2}); err != nil {
		t.Fatal(err)
	}
	after, err := rig.service.ListMessages(ctx, "owner-a", rig.session)
	if err != nil || len(after) != 2 || after[0].ID != user1 || after[1].ID != reply1 {
		t.Fatalf("after replacing the last turn = %+v %v", after, err)
	}
	if err := rig.service.DeleteLastTurnMessage(ctx, "owner-b", rig.session, reply1, []string{reply1, user1}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("another owner's session = %v", err)
	}
}
