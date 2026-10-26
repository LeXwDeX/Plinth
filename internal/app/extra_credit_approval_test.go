package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// extraCreditStack is the whole mock stack (real kernel, approval policy,
// pending approvals and the mock Agent) for the paid-step approval card of a
// Tripo 3D request whose default extra export formats cost extra credits.
type extraCreditStack struct {
	t           *testing.T
	application *Application
	repository  *sqlite.Repository
	database    *sql.DB
}

const (
	extraCreditPrompt = "做一个简单的木凳 3D 模型"
	extraCreditModel  = "tripo-v3.1-full"
)

func newExtraCreditStack(t *testing.T) *extraCreditStack {
	t.Helper()
	root := t.TempDir()
	application, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		RuntimeSettings:           map[string]string{"llm_model": "configured-test-model"},
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	generationDone := make(chan error, 1)
	go func() {
		generationDone <- application.generation.Run(runCtx,
			generation.CoordinatorConfig{PollEvery: 5 * time.Millisecond, LeaseFor: time.Second, BatchSize: 10})
	}()
	agentDone := make(chan error, 1)
	go func() { agentDone <- application.chatAgent.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		<-generationDone
		<-agentDone
		_ = application.generation.Close()
	})
	repository, err := sqlite.Open(filepath.Join(root, "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return &extraCreditStack{t: t, application: application, repository: repository,
		database: openRawAppDatabase(t, filepath.Join(root, "art-ai.db"))}
}

func (s *extraCreditStack) newSession(mode string) string {
	s.t.Helper()
	created := appChatRequest(s.application.server.Handler, http.MethodPost, "/api/chat/sessions", `{}`)
	var session map[string]any
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &session) != nil {
		s.t.Fatalf("create session = %d %s", created.Code, created.Body)
	}
	id, _ := session["id"].(string)
	if _, found, err := s.repository.SetSessionApprovalMode(context.Background(), "owner-a", id, mode); err != nil || !found {
		s.t.Fatalf("set approval mode %q: found=%v err=%v", mode, found, err)
	}
	return id
}

// ask sends the Lex's request on the Tripo model and waits for the turn to end.
func (s *extraCreditStack) ask(session, content string) {
	s.t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content, "ai_model": extraCreditModel, "creative_domain": "model-3d"})
	send := appChatRequest(s.application.server.Handler, http.MethodPost, "/api/chat/sessions/"+session+"/messages", string(body))
	if send.Code != http.StatusAccepted {
		s.t.Fatalf("send = %d %s", send.Code, send.Body)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var running int
		if s.database.QueryRow(`SELECT COUNT(*) FROM go_chat_turns WHERE session_id=? AND state NOT IN (?, ?)`, session,
			s.application.schema.Chat.State.TurnDone, s.application.schema.Chat.State.TurnError).Scan(&running) == nil && running == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatal("the Agent turn did not end")
}

type pendingCard struct {
	RequestID        string                      `json:"request_id"`
	Capability       string                      `json:"capability"`
	Model            string                      `json:"model"`
	NativeQuote      *schemacontract.NativeQuote `json:"native_quote"`
	EstimatedCredits int                         `json:"estimated_credits"`
	BaseCredits      int                         `json:"base_credits"`
	ExtraCredits     int                         `json:"extra_credits"`
	ExtraLabel       string                      `json:"extra_label"`
	ExtraItems       []string                    `json:"extra_items"`
}

func (s *extraCreditStack) cards(session string) []pendingCard {
	s.t.Helper()
	listed := appChatRequest(s.application.server.Handler, http.MethodGet, "/api/v1/chat/sessions/"+session+"/pending-approvals", "")
	var body struct {
		Items []pendingCard `json:"items"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &body) != nil {
		s.t.Fatalf("pending approvals = %d %s", listed.Code, listed.Body)
	}
	return body.Items
}

func (s *extraCreditStack) tasks(session string) int {
	s.t.Helper()
	var count int
	if err := s.database.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE owner_id='owner-a' AND session_id=?`, session).Scan(&count); err != nil {
		s.t.Fatal(err)
	}
	return count
}

func (s *extraCreditStack) decide(card pendingCard, decision executiongrant.Decision) {
	s.t.Helper()
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	if _, handled, err := s.application.pendingApprovals.Decide(context.Background(), principal, card.RequestID, decision); err != nil || !handled {
		s.t.Fatalf("decide %s: handled=%v err=%v", decision, handled, err)
	}
}

func (s *extraCreditStack) assistantTexts(session string) []string {
	s.t.Helper()
	listed := appChatRequest(s.application.server.Handler, http.MethodGet, "/api/chat/sessions/"+session+"/messages", "")
	var messages []map[string]any
	if json.Unmarshal(listed.Body.Bytes(), &messages) != nil {
		s.t.Fatalf("messages = %s", listed.Body)
	}
	var texts []string
	for _, message := range messages {
		if message["role"] == "assistant" {
			content, _ := message["content"].(string)
			texts = append(texts, content)
		}
	}
	return texts
}

// The observed bug: extra export formats ticked, manual mode, and the Agent
// answered that the step was not submitted and the user must find a consent
// checkbox. One approval card now carries the whole consent.
func TestExtraExportFormatsAskForApprovalOnACardInManualMode(t *testing.T) {
	stack := newExtraCreditStack(t)
	session := stack.newSession("manual")
	stack.ask(session, extraCreditPrompt)

	cards := stack.cards(session)
	if len(cards) != 1 {
		t.Fatalf("manual mode must show exactly one approval card, got %+v", cards)
	}
	card := cards[0]
	if card.Capability != "text-to-3d" || card.NativeQuote == nil || card.NativeQuote.Unit != "tripo.credit" ||
		card.NativeQuote.BaseAmount != "10" || card.NativeQuote.ExtraAmount != "80" || card.NativeQuote.TotalAmount != "90" ||
		card.BaseCredits != 0 || card.ExtraCredits != 0 || card.EstimatedCredits != 0 {
		t.Fatalf("the current H card must freeze native 10 + 80 = 90, not a legacy estimate: %+v", card)
	}
	components := map[string]string{}
	for _, item := range card.NativeQuote.Components {
		components[item.ID] = item.Amount
	}
	for id, amount := range map[string]string{"standard_texture": "10", "extreme_texture": "20", "detailed_geometry": "20", "format_conversions": "30"} {
		if components[id] != amount {
			t.Fatalf("approved component %s = %q, want %s: %+v", id, components[id], amount, card.NativeQuote)
		}
	}
	if len(components) != 4 {
		t.Fatalf("approved quote has unexpected components: %+v", card.NativeQuote)
	}
	if stack.tasks(session) != 0 {
		t.Fatal("nothing may be submitted before the person approves")
	}
	for _, text := range stack.assistantTexts(session) {
		if strings.Contains(text, "勾选") || strings.Contains(text, "复选") {
			t.Fatalf("the Agent sent the user to a checkbox: %q", text)
		}
	}

	// Asking for the very same thing again reuses the waiting card.
	stack.ask(session, extraCreditPrompt)
	if again := stack.cards(session); len(again) != 1 || again[0].RequestID != card.RequestID {
		t.Fatalf("the same request must not make a second card: %+v", again)
	}

	// Approving, however often, submits exactly one task that carries the
	// consent of the card and charges exactly the extra credits it showed.
	for range 3 {
		stack.decide(card, executiongrant.Approve)
	}
	deadline := time.Now().Add(10 * time.Second)
	for stack.tasks(session) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := stack.tasks(session); got != 1 {
		t.Fatalf("approval must submit exactly one task, got %d", got)
	}
	var input, offering string
	if err := stack.database.QueryRow(`SELECT input_json, offering_json FROM generation_tasks WHERE owner_id='owner-a' AND session_id=?`, session).Scan(&input, &offering); err != nil {
		t.Fatal(err)
	}
	var task schemacontract.Offering
	if err := json.Unmarshal([]byte(offering), &task); err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(input), &values); err != nil || values["authorize_extra_credits"] != true {
		t.Fatalf("the task must carry the consent the card decided: %v %v", values, err)
	}
	quote, err := task.EvaluateNativeQuote(json.RawMessage(input))
	if err != nil || quote == nil || quote.TotalAmount != card.NativeQuote.TotalAmount || quote.ExtraAmount != card.NativeQuote.ExtraAmount {
		t.Fatalf("frozen task quote %+v differs from approved card %+v: %v", quote, card.NativeQuote, err)
	}
	var status, decidedBy string
	if err := stack.database.QueryRow(`SELECT status, COALESCE(decided_by,'') FROM execution_grants WHERE owner_id='owner-a' AND request_id=?`, card.RequestID).Scan(&status, &decidedBy); err != nil ||
		status != "bound" || decidedBy != "owner-a" {
		t.Fatalf("grant = %q decided by %q (%v), want bound by the person", status, decidedBy, err)
	}
	if left := stack.cards(session); len(left) != 0 {
		t.Fatalf("a decided card must leave the pending list: %+v", left)
	}
}

func TestRefusingTheExtraCreditCardSubmitsNothingAndTheChatProposesAnAlternative(t *testing.T) {
	stack := newExtraCreditStack(t)
	session := stack.newSession("manual")
	stack.ask(session, extraCreditPrompt)
	cards := stack.cards(session)
	if len(cards) != 1 {
		t.Fatalf("cards = %+v", cards)
	}
	stack.decide(cards[0], executiongrant.Deny)
	if stack.tasks(session) != 0 {
		t.Fatal("a refused card submitted a task")
	}
	texts := stack.assistantTexts(session)
	last := texts[len(texts)-1]
	for _, want := range []string{"没有执行", "附加导出格式转换", "80", "10", "90", "Tripo credits"} {
		if !strings.Contains(last, want) {
			t.Fatalf("the refusal %q must mention %q", last, want)
		}
	}
	if strings.Contains(last, "勾选") || strings.ContainsAny(last, "{}") {
		t.Fatalf("refusal = %q", last)
	}
	// The same request after a refusal is a new card, not the refused one.
	stack.ask(session, extraCreditPrompt)
	if again := stack.cards(session); len(again) != 1 || again[0].RequestID == cards[0].RequestID {
		t.Fatalf("a refused request must be askable again: %+v", again)
	}
}

func TestAutoModeApprovesTheExtraCreditsItselfAndShowsThemInTheCredits(t *testing.T) {
	stack := newExtraCreditStack(t)
	session := stack.newSession("auto")
	stack.ask(session, extraCreditPrompt)
	if cards := stack.cards(session); len(cards) != 0 {
		t.Fatalf("auto mode must not ask: %+v", cards)
	}
	deadline := time.Now().Add(10 * time.Second)
	for stack.tasks(session) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := stack.tasks(session); got != 1 {
		t.Fatalf("auto mode must submit exactly one task, got %d", got)
	}
	var status, decidedBy string
	if err := stack.database.QueryRow(`SELECT status, COALESCE(decided_by,'') FROM execution_grants WHERE owner_id='owner-a' AND session_id=?`, session).Scan(&status, &decidedBy); err != nil ||
		decidedBy != "policy:auto" {
		t.Fatalf("grant = %q decided by %q (%v), want the automatic mode", status, decidedBy, err)
	}
	listed := appChatRequest(stack.application.server.Handler, http.MethodGet, "/api/chat/sessions/"+session+"/messages", "")
	var messages []map[string]any
	if err := json.Unmarshal(listed.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	var calls []map[string]any
	if err := json.Unmarshal([]byte(messages[len(messages)-1]["toolCalls"].(string)), &calls); err != nil || len(calls) == 0 {
		t.Fatalf("tool calls = %v %v", messages[len(messages)-1]["toolCalls"], err)
	}
	quote, ok := calls[0]["native_quote"].(map[string]any)
	if !ok || quote["base_amount"] != "10" || quote["extra_amount"] != "80" || quote["total_amount"] != "90" || quote["unit"] != "tripo.credit" || calls[0]["approvalSource"] != "policy" {
		t.Fatalf("auto-approved H step must show the frozen native quote: %v", calls[0])
	}
	if _, legacy := calls[0]["estimatedCredits"]; legacy {
		t.Fatalf("native H step leaked legacy credit estimate: %v", calls[0])
	}
}

// The case that blocked a real user: a 3D request in chat with Tripo selected
// by someone who never consented to anything. The consent flag is not a
// setting, so even a stale `false` saved in the session settings (from before
// the flag was hidden) cannot stop the card from appearing, and nothing is
// charged before the person approves it.
func TestTripoRequestWithNoPriorConsentShowsTheCardEvenWithStaleSessionSettings(t *testing.T) {
	stack := newExtraCreditStack(t)
	session := stack.newSession("manual")
	put := appChatRequest(stack.application.server.Handler, http.MethodPut, "/api/v1/agent/params?scope="+session,
		`{"params":{"authorize_extra_credits":false}}`)
	if put.Code != http.StatusOK {
		t.Fatalf("session settings = %d %s", put.Code, put.Body)
	}
	stack.ask(session, extraCreditPrompt)

	cards := stack.cards(session)
	if len(cards) != 1 || cards[0].NativeQuote == nil || cards[0].NativeQuote.ExtraAmount != "80" || cards[0].NativeQuote.TotalAmount != "90" {
		t.Fatalf("the person who never consented must get the approval card: %+v", cards)
	}
	if stack.tasks(session) != 0 {
		t.Fatal("nothing may be submitted before the person approves")
	}
	for _, text := range stack.assistantTexts(session) {
		if strings.Contains(text, "勾选") || strings.Contains(text, "复选") || strings.Contains(text, "同意额外积分消耗") {
			t.Fatalf("the Agent sent the user looking for a checkbox: %q", text)
		}
	}
}
