package executiongrant_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

const (
	itemSessionID  = "session-item"
	plainSessionID = "session-plain"
)

// itemRuns answers the item-session lookup; the embedded nil interface keeps
// the rest of assetlist.RunStore out of the fixture.
type itemRuns struct {
	assetlist.RunStore
	items map[string]assetlist.Item
}

func (r *itemRuns) ItemBySession(_ context.Context, ownerID, sessionID string) (assetlist.Item, bool, error) {
	item, found := r.items[ownerID+"\x00"+sessionID]
	return item, found, nil
}

// itemListRepository is only the identity assetlist.New requires; the
// item-session lookups under test go through the run store.
type itemListRepository struct{ assetlist.Repository }

// itemSessions satisfies chat.Repository for the derivation under test: only
// Session is read.
type itemSessions struct {
	chat.Repository
	sessions map[string]chat.Session
}

func (r *itemSessions) Session(_ context.Context, ownerID, sessionID string) (chat.Session, bool, error) {
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return chat.Session{}, false, nil
	}
	return session, true, nil
}

// itemStepStore is the grant repository plus the atomic step decision. setting
// stands for the account's saved list setting; the zero value falls back to the
// Schema default carried by the request, like the SQLite store.
type itemStepStore struct {
	grant    executiongrant.Grant
	snapshot executiongrant.PreparedSnapshot
	setting  approvalpolicy.StepSetting
	used     int
	requests []executiongrant.StepRequest
}

func (s *itemStepStore) PreparePending(context.Context, executiongrant.Identity, string, string) (executiongrant.Grant, error) {
	return executiongrant.Grant{}, executiongrant.ErrInvalid
}

func (s *itemStepStore) DecideExecutionGrant(context.Context, string, string, string, executiongrant.Decision) (executiongrant.Grant, error) {
	return executiongrant.Grant{}, executiongrant.ErrState
}

func (s *itemStepStore) GetExecutionGrant(_ context.Context, owner, requestID string) (executiongrant.Grant, error) {
	if s.grant.OwnerID != owner || s.grant.RequestID != requestID {
		return executiongrant.Grant{}, executiongrant.ErrConflict
	}
	return s.grant, nil
}

func (s *itemStepStore) ApproveExecutionGrantByPolicy(_ context.Context, owner, requestID, decidedBy string) (executiongrant.Grant, error) {
	if s.grant.OwnerID != owner || s.grant.RequestID != requestID || s.grant.Status != executiongrant.Pending {
		return executiongrant.Grant{}, executiongrant.ErrState
	}
	s.grant.Status, s.grant.DecidedBy = executiongrant.Approved, decidedBy
	return s.grant, nil
}

func (s *itemStepStore) GetExecutionPreparedSnapshot(_ context.Context, owner, preparedID string) (executiongrant.PreparedSnapshot, error) {
	if s.snapshot.OwnerID != owner || s.snapshot.ID != preparedID {
		return executiongrant.PreparedSnapshot{}, executiongrant.ErrConflict
	}
	return s.snapshot, nil
}

func (s *itemStepStore) ApproveExecutionGrantWithSteps(_ context.Context, owner, requestID string,
	request executiongrant.StepRequest,
) (executiongrant.Grant, executiongrant.StepDecision, error) {
	s.requests = append(s.requests, request)
	setting := s.setting
	if setting.Mode == "" {
		setting = request.Steps.DefaultList
	}
	summary := executiongrant.StepSummary{
		Entry: request.Entry, Mode: setting.Mode, Cap: setting.Cap, Used: s.used,
	}
	if setting.Mode == approvalpolicy.StepModeCap && s.used >= setting.Cap {
		s.grant.Status, s.grant.DecidedBy = executiongrant.Denied, executiongrant.StepsActor
		return s.grant, executiongrant.StepDecision{Approved: false, Summary: summary}, nil
	}
	if s.grant.OwnerID != owner || s.grant.RequestID != requestID || s.grant.Status != executiongrant.Pending {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, executiongrant.ErrState
	}
	s.grant.Status, s.grant.DecidedBy = executiongrant.Approved, executiongrant.StepsActor
	return s.grant, executiongrant.StepDecision{Approved: true, Summary: summary}, nil
}

func (s *itemStepStore) StepUsage(context.Context, string, string, executiongrant.StepRequest) (executiongrant.StepSummary, error) {
	return executiongrant.StepSummary{}, nil
}

func newItemGrant(sessionID string) (executiongrant.Grant, executiongrant.PreparedSnapshot) {
	identity := executiongrant.Identity{
		OwnerID: "owner-a", SessionID: sessionID, OfferingID: "offering", OfferingRevision: "r1",
		PreparedInputID: "prepared-a", PreparedInputHash: "hash-a",
		IdempotencyKey: "key-a", OperationID: "op-a",
	}
	return executiongrant.Grant{
		RequestID: "request-a", Identity: identity,
		Status: executiongrant.Pending, Source: executiongrant.AgentPending,
	}, executiongrant.PreparedSnapshot{
		ID: "prepared-a", OwnerID: "owner-a", SessionID: sessionID,
		OfferingID: "offering", OfferingRevision: "r1", InputHash: "hash-a",
		Capability: "image-to-3d",
	}
}

// newItemSessionGrantService assembles the production wiring of the
// derivation: the real chat service as the mode and entry reader over the real
// asset-list service, and a scripted grant store for the step decision.
func newItemSessionGrantService(t *testing.T) (executiongrant.Service, *itemStepStore) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	if application.AssetList == nil {
		t.Fatal("the default catalog must declare application.asset_list")
	}
	runs := &itemRuns{items: map[string]assetlist.Item{
		"owner-a\x00" + itemSessionID: {ID: "aitem_1", SessionID: itemSessionID},
	}}
	listService, err := assetlist.New(assetlist.Config{
		Repository:      itemListRepository{},
		Schema:          *application.AssetList,
		ProjectMaxItems: 20,
		Runs:            runs,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatService, err := chat.NewService(chat.ServiceConfig{
		Repository: &itemSessions{sessions: map[string]chat.Session{
			itemSessionID:  {ID: itemSessionID, OwnerID: "owner-a", ApprovalMode: "manual"},
			plainSessionID: {ID: plainSessionID, OwnerID: "owner-a", ApprovalMode: "manual"},
		}},
		ApprovalPolicy: approvalpolicy.New("manual", []string{"auto", "manual"},
			[]string{"image-to-3d"}, []string{"text-to-image", "image-to-3d"}),
		Clock:          func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) },
		DefaultTitle:   "Session",
		UserRole:       "user",
		AssistantRole:  "assistant",
		PendingContent: "pending",
		EventFields:    chat.EventFieldPolicy{Type: "kind", MessageID: "message"},
		EventTypes:     chat.EventTypePolicy{MessageCreated: "message-created"},
		StreamActive:   "active", StreamDone: "done", StreamError: "error",
		TurnQueued: "queued", TurnRunning: "running", TurnWaitingTask: "waiting",
		TurnDone: "done", TurnError: "error",
		PlaceholderHealing: chat.PlaceholderHealingPolicy{
			StaleAfter: time.Minute, PromptArgument: "prompt", PromptPreviewCharacters: 5,
			InterruptedMessage: "interrupted", InterruptedPromptTemplate: "interrupted {prompt}",
		},
		StreamCleanupTTL: time.Minute, SubscriberBufferSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatService.SetAssetLists(chat.AssetListPolicy{
		Service: listService, Schema: *application.AssetList, StepsEnabled: true,
	})

	store := &itemStepStore{}
	policy := approvalpolicy.New("manual", []string{"auto", "manual"},
		[]string{"image-to-3d", "text-to-3d"}, []string{"text-to-image", "image-to-3d", "text-to-3d"}).
		WithSteps(approvalpolicy.StepsPolicy{
			Counted:     []string{"text-to-3d", "image-to-3d"},
			DefaultList: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
			DefaultMCP:  approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
		})
	return executiongrant.Service{Grants: store, Prepared: store, Policy: policy, Modes: chatService}, store
}

// An item sub-session under the list agent setting approves a counted 3D step
// by policy: no card, and the decision names the list entry.
func TestItemSessionStepsApproveACounted3DStepUnderTheListSetting(t *testing.T) {
	service, store := newItemSessionGrantService(t)
	store.grant, store.snapshot = newItemGrant(itemSessionID)

	result, err := service.ApplyPolicyDecision(context.Background(), store.grant)
	if err != nil || result.Outcome != executiongrant.PolicyOutcomeApproved ||
		store.grant.Status != executiongrant.Approved || store.grant.DecidedBy != executiongrant.StepsActor {
		t.Fatalf("result = %+v err=%v grant=%+v", result, err, store.grant)
	}
	if len(store.requests) != 1 || store.requests[0].Entry != approvalpolicy.StepEntryList ||
		!store.requests[0].Steps.Counts("image-to-3d") {
		t.Fatalf("step requests = %+v; want one list entry", store.requests)
	}
}

// The list cap setting refuses the step in place at the limit: the grant is
// denied, no approval card follows, and the list entry is what was read.
func TestItemSessionStepsRefuseACounted3DStepAtTheListCap(t *testing.T) {
	service, store := newItemSessionGrantService(t)
	store.grant, store.snapshot = newItemGrant(itemSessionID)
	store.setting = approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: 3}
	store.used = 3

	result, err := service.ApplyPolicyDecision(context.Background(), store.grant)
	if err != nil || result.Outcome != executiongrant.PolicyOutcomeStepCap ||
		store.grant.Status != executiongrant.Denied || !store.grant.StepCapReached() ||
		result.Steps == nil || result.Steps.Used != 3 {
		t.Fatalf("result = %+v err=%v grant=%+v", result, err, store.grant)
	}
	if len(store.requests) != 1 || store.requests[0].Entry != approvalpolicy.StepEntryList {
		t.Fatalf("step requests = %+v; want one list entry", store.requests)
	}
}

// A session that runs no item keeps its stored manual mode: the counted step
// waits for a person exactly as before, and the step store is never consulted.
func TestNonItemSessionKeepsTheStoredMode(t *testing.T) {
	service, store := newItemSessionGrantService(t)
	store.grant, store.snapshot = newItemGrant(plainSessionID)

	result, err := service.ApplyPolicyDecision(context.Background(), store.grant)
	if err != nil || result.Outcome != executiongrant.PolicyOutcomeHuman ||
		store.grant.Status != executiongrant.Pending || len(store.requests) != 0 {
		t.Fatalf("result = %+v err=%v grant=%+v requests=%+v", result, err, store.grant, store.requests)
	}
}
