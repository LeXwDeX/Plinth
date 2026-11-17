package executiongrant

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

// fakeKeyStore is the step store plus the access-key preparation. The decision
// answers as scripted and records the request it was asked.
type fakeKeyStore struct {
	*fakeStepStore
	prepared []string
	prepare  error
	// denied scripts the decision: "" approves, "cap" denies at the cap, "person"
	// denies because only a person could approve.
	denied string
}

func (f *fakeKeyStore) PreparePendingForAccessKey(_ context.Context, identity Identity, accessKeyID string) (Grant, error) {
	f.prepared = append(f.prepared, accessKeyID)
	if f.prepare != nil {
		return Grant{}, f.prepare
	}
	return Grant{RequestID: "request-new", Identity: identity, Status: Pending, Source: AccessKey, AccessKeyID: accessKeyID}, nil
}

func (f *fakeKeyStore) ApproveExecutionGrantWithSteps(ctx context.Context, owner, requestID string, request StepRequest) (Grant, StepDecision, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return Grant{}, StepDecision{}, f.err
	}
	switch f.denied {
	case "person":
		f.grant.Status, f.grant.DecidedBy = Denied, AccessKeyNeedsPersonActor
		return f.grant, StepDecision{NeedsPerson: true}, nil
	case "cap":
		f.grant.Status, f.grant.DecidedBy = Denied, AccessKeyActor
		return f.grant, StepDecision{Summary: f.summary}, nil
	}
	f.grant.Status, f.grant.DecidedBy = Approved, AccessKeyActor
	f.approved++
	return f.grant, StepDecision{Approved: true, Summary: f.summary}, nil
}

func keyFixture(capability string) (*fakeKeyStore, Service) {
	step := &fakeStepStore{fakeGrantStore: policyFixture(capability)}
	step.grant.Source, step.grant.AccessKeyID = AccessKey, "key-one"
	store := &fakeKeyStore{fakeStepStore: step}
	return store, Service{Grants: store, Prepared: store, Policy: stepPolicy()}
}

func prepareCommand(key string) PrepareCommand {
	return PrepareCommand{
		Principal: Principal{OwnerID: "owner-a", ActorID: "access_key:key-one", Source: AccessKey},
		SessionID: "session-a", OfferingID: "offering", OfferingRevision: "r1",
		PreparedInputID: "prepared-a", PreparedInputHash: "hash-a", IdempotencyKey: "key-a", OperationID: "op-a",
		AccessKeyID: key,
	}
}

func TestPrepareIssuesAnAccessKeyGrantThatRecordsTheKey(t *testing.T) {
	store, service := keyFixture("text-to-3d")
	grant, err := service.Prepare(context.Background(), prepareCommand("key-one"))
	if err != nil || grant.Source != AccessKey || grant.AccessKeyID != "key-one" || grant.Status != Pending {
		t.Fatalf("grant = %+v err=%v", grant, err)
	}
	if len(store.prepared) != 1 || store.prepared[0] != "key-one" {
		t.Fatalf("the data layer was asked for %v", store.prepared)
	}
}

func TestPrepareRefusesAnAccessKeyGrantWithoutItsKeyOrWithTheWrongOptions(t *testing.T) {
	_, service := keyFixture("text-to-3d")
	withKeyOnly := prepareCommand("key-one")
	withKeyOnly.Source = HumanBrowser
	withTurn := prepareCommand("key-one")
	withTurn.TriggerTurnID = "turn-a"
	withBudget := prepareCommand("key-one")
	withBudget.BudgetEligible = true
	for name, cmd := range map[string]PrepareCommand{
		"no key":                 prepareCommand(""),
		"key on a browser grant": withKeyOnly,
		"key grant with a turn":  withTurn,
		"budget-eligible key":    withBudget,
	} {
		if _, err := service.Prepare(context.Background(), cmd); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v; want ErrInvalid", name, err)
		}
	}
	// A data layer that cannot record the key cannot issue the grant.
	plain := policyFixture("text-to-3d")
	if _, err := (Service{Grants: plain, Prepared: plain}).Prepare(context.Background(), prepareCommand("key-one")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a store without key support: %v", err)
	}
}

func TestApproveForAccessKeyApprovesInThePlaceOfAPerson(t *testing.T) {
	for _, capability := range []string{"text-to-image", "image-to-3d"} {
		store, service := keyFixture(capability)
		store.summary = StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: approvalpolicy.StepModeCap, Cap: 5, Used: 2}
		result, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{})
		if err != nil || result.Outcome != PolicyOutcomeApproved || result.Grant.Status != Approved ||
			result.Grant.DecidedBy != AccessKeyActor || !result.Grant.ApprovedByPolicy() ||
			result.Steps == nil || result.Steps.Used != 2 || result.Steps.Cap != 5 {
			t.Fatalf("%s: result = %+v err=%v", capability, result, err)
		}
		// The data layer is asked as the key, under the MCP entry, never as a person.
		if len(store.requests) != 1 || store.requests[0].Actor != AccessKeyActor || store.requests[0].Entry != approvalpolicy.StepEntryMCP ||
			store.requests[0].NeedsPerson || !store.requests[0].Steps.Counts("image-to-3d") {
			t.Fatalf("%s: request = %+v", capability, store.requests)
		}
	}
	// A step the policy does not count carries no summary.
	store, service := keyFixture("text-to-image")
	result, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{})
	if err != nil || result.Steps != nil || store.approved != 1 {
		t.Fatalf("2D result = %+v err=%v", result, err)
	}
}

// A refusal is the answer, not a card: it comes back as an error with the
// denied grant, so a caller that only checks the error cannot take it for an
// approval, and nothing is ever left pending.
func TestApproveForAccessKeyRefusalsAreErrorsWithTheirOwnReason(t *testing.T) {
	store, service := keyFixture("text-to-3d")
	store.denied = "cap"
	store.summary = StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: approvalpolicy.StepModeCap, Cap: 3, Used: 3}
	capped, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{})
	if !errors.Is(err, ErrStepCapReached) || errors.Is(err, ErrAccessKeyNeedsPerson) || capped.Outcome != PolicyOutcomeStepCap ||
		capped.Grant.Status != Denied || !capped.Grant.StepCapReached() || capped.Grant.AccessKeyNeedsPerson() ||
		capped.Steps == nil || capped.Steps.Used != 3 || capped.Steps.Cap != 3 {
		t.Fatalf("cap refusal = %+v err=%v", capped, err)
	}
	if store.approved != 0 {
		t.Fatal("a refused step was approved")
	}

	store, service = keyFixture("text-to-3d")
	store.denied = "person"
	person, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{NeedsPerson: true})
	if !errors.Is(err, ErrAccessKeyNeedsPerson) || errors.Is(err, ErrStepCapReached) || person.Outcome != PolicyOutcomeAccessKeyNeedsPerson ||
		person.Grant.Status != Denied || !person.Grant.AccessKeyNeedsPerson() || person.Grant.StepCapReached() || person.Steps != nil {
		t.Fatalf("needs-person refusal = %+v err=%v", person, err)
	}
	if len(store.requests) != 1 || !store.requests[0].NeedsPerson || store.requests[0].Actor != AccessKeyActor {
		t.Fatalf("request = %+v; the flag must reach the transaction", store.requests)
	}
	// The two reasons are different actors, neither a person's ID.
	if AccessKeyActor == AccessKeyNeedsPersonActor || AccessKeyActor != "policy:access_key" {
		t.Fatalf("actors = %q %q", AccessKeyActor, AccessKeyNeedsPersonActor)
	}
}

func TestApproveForAccessKeyOnlyDecidesTheOwnersAccessKeyGrants(t *testing.T) {
	for name, mutate := range map[string]func(*fakeKeyStore){
		"browser grant":           func(s *fakeKeyStore) { s.grant.Source, s.grant.AccessKeyID = HumanBrowser, "" },
		"agent grant":             func(s *fakeKeyStore) { s.grant.Source, s.grant.AccessKeyID = AgentPending, "" },
		"key grant without a key": func(s *fakeKeyStore) { s.grant.AccessKeyID = "" },
	} {
		store, service := keyFixture("text-to-3d")
		mutate(store)
		result, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{})
		if !errors.Is(err, ErrConflict) || result.Outcome != PolicyOutcomeNone || len(store.requests) != 0 {
			t.Errorf("%s: result = %+v err=%v requests=%d", name, result, err, len(store.requests))
		}
	}
	store, service := keyFixture("text-to-3d")
	if _, err := service.ApproveForAccessKey(context.Background(), "owner-b", "request-a", AccessKeyStep{}); err == nil || len(store.requests) != 0 {
		t.Fatalf("another owner decided the grant: %v", err)
	}
	// A frozen input that changed since the grant was issued is a conflict.
	store, service = keyFixture("text-to-3d")
	store.snapshot.InputHash = "other"
	if _, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{}); !errors.Is(err, ErrConflict) || len(store.requests) != 0 {
		t.Fatalf("a changed frozen input: %v", err)
	}
	// Without a step policy, or a store that decides atomically, nothing is decided.
	store, service = keyFixture("text-to-3d")
	service.Policy = approvalpolicy.New("manual", []string{"manual"}, nil, nil)
	if _, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no step policy: %v", err)
	}
	plain := policyFixture("text-to-3d")
	plain.grant.Source, plain.grant.AccessKeyID = AccessKey, "key-one"
	if _, err := (Service{Grants: plain, Prepared: plain, Policy: stepPolicy()}).ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a store without the step decision: %v", err)
	}
	// A data-layer failure is passed on, never turned into an approval.
	store, service = keyFixture("text-to-3d")
	store.err = errors.New("database is locked")
	if result, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{}); err == nil ||
		result.Grant.Status == Approved || result.Outcome != PolicyOutcomeNone {
		t.Fatalf("store failure: %+v err=%v", result, err)
	}
}

// A grant that is not pending is not checked against the frozen input again:
// the decision it carries is repeated by the data layer.
func TestApproveForAccessKeyRepeatsAnAlreadyDecidedGrant(t *testing.T) {
	store, service := keyFixture("text-to-3d")
	store.grant.Status, store.grant.DecidedBy = Approved, AccessKeyActor
	store.snapshot.InputHash = "changed since"
	result, err := service.ApproveForAccessKey(context.Background(), "owner-a", "request-a", AccessKeyStep{})
	if err != nil || result.Outcome != PolicyOutcomeApproved || len(store.requests) != 1 {
		t.Fatalf("repeat = %+v err=%v", result, err)
	}
}

// No person and no generic policy can decide a key's grant: the usual paths
// refuse the source, so no card or click can ever approve one.
func TestAccessKeyGrantsAreOutOfReachOfPeopleAndTheGenericPolicy(t *testing.T) {
	store, service := keyFixture("text-to-3d")
	service.Modes = &modeReader{mode: "auto"}
	person := Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: HumanBrowser}
	if _, err := service.Decide(context.Background(), person, "request-a", Approve); !errors.Is(err, ErrConflict) {
		t.Fatalf("a person decided a key grant: %v", err)
	}
	// A key is not a person either: it cannot decide the grant it asked for.
	keyPrincipal := Principal{OwnerID: "owner-a", ActorID: "access_key:key-one", Source: AccessKey}
	if _, err := service.Decide(context.Background(), keyPrincipal, "request-a", Approve); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a key decided its own grant: %v", err)
	}
	for _, mode := range []string{"auto", "manual", approvalpolicy.ModeSteps} {
		if _, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", mode); !errors.Is(err, ErrConflict) {
			t.Fatalf("the %s policy decided a key grant: %v", mode, err)
		}
	}
	// ApplyPolicy, which every card-creating path calls, leaves it alone too.
	if got, err := service.ApplyPolicy(context.Background(), store.grant); !errors.Is(err, ErrConflict) || got.Status != Pending {
		t.Fatalf("ApplyPolicy = %+v err=%v", got, err)
	}
	if store.approved != 0 || len(store.requests) != 0 {
		t.Fatal("something decided the key grant")
	}
}

func TestAccessKeyStepStatusReadsTheMCPEntry(t *testing.T) {
	store, service := keyFixture("text-to-3d")
	store.usage = StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: approvalpolicy.StepModeCap, Cap: 4, Used: 1}
	summary, err := service.AccessKeyStepStatus(context.Background(), "owner-a", "session-a")
	if err != nil || summary.Used != 1 || summary.Cap != 4 {
		t.Fatalf("status = %+v err=%v", summary, err)
	}
	if len(store.requests) != 1 || store.requests[0].Entry != approvalpolicy.StepEntryMCP {
		t.Fatalf("requests = %+v", store.requests)
	}
	for name, call := range map[string]func() error{
		"no owner":   func() error { _, err := service.AccessKeyStepStatus(context.Background(), "", "session-a"); return err },
		"no session": func() error { _, err := service.AccessKeyStepStatus(context.Background(), "owner-a", ""); return err },
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
