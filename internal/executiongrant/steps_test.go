package executiongrant

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

// fakeStepStore is the grant fixture plus the atomic 3D step decision. It
// records what the service asked and answers as scripted.
type fakeStepStore struct {
	*fakeGrantStore
	requests []StepRequest
	// refuse makes the decision a step-cap refusal with the summary below.
	refuse  bool
	summary StepSummary
	err     error
	usage   StepSummary
	// released records the grants the abandon path was asked to release.
	released []string
}

func (f *fakeStepStore) ApproveExecutionGrantWithSteps(_ context.Context, owner, requestID string, request StepRequest) (Grant, StepDecision, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return Grant{}, StepDecision{}, f.err
	}
	if f.grant.OwnerID != owner || f.grant.RequestID != requestID || f.grant.Status != Pending {
		return Grant{}, StepDecision{}, ErrState
	}
	f.grant.DecidedBy = StepsActor
	if f.refuse {
		f.grant.Status = Denied
		return f.grant, StepDecision{Approved: false, Summary: f.summary}, nil
	}
	f.grant.Status = Approved
	f.approved++
	return f.grant, StepDecision{Approved: true, Summary: f.summary}, nil
}

func (f *fakeStepStore) StepUsage(_ context.Context, _, _ string, request StepRequest) (StepSummary, error) {
	f.requests = append(f.requests, request)
	return f.usage, nil
}

func (f *fakeStepStore) ReleaseUnboundBudgetReservation(_ context.Context, _, requestID string) error {
	f.released = append(f.released, requestID)
	return nil
}

// stepModes reads the steps mode and names the entry of the session.
type stepModes struct {
	modeReader
	entry      string
	entryErr   error
	entryCalls int
}

func (m *stepModes) SessionStepEntry(context.Context, string, string) (string, error) {
	m.entryCalls++
	return m.entry, m.entryErr
}

func stepPolicy() approvalpolicy.Policy {
	return approvalpolicy.New("manual", []string{"auto", "manual"},
		[]string{"text-to-3d", "image-to-3d", "retexture"}, []string{"text-to-image", "image-to-3d", "text-to-3d"}).
		WithSteps(approvalpolicy.StepsPolicy{
			Counted:     []string{"text-to-3d", "image-to-3d", "retexture"},
			DefaultList: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
			DefaultMCP:  approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
		})
}

func stepFixture(capability string) (*fakeStepStore, *stepModes, Service) {
	store := &fakeStepStore{fakeGrantStore: policyFixture(capability)}
	store.grant.Source = AgentPending
	modes := &stepModes{modeReader: modeReader{mode: approvalpolicy.ModeSteps}, entry: approvalpolicy.StepEntryList}
	return store, modes, Service{Grants: store, Prepared: store, Policy: stepPolicy(), Modes: modes}
}

func TestStepsApprovesAnUncountedStepWithoutTheStepStoreOrAnEntry(t *testing.T) {
	store, modes, service := stepFixture("text-to-image")
	result, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", approvalpolicy.ModeSteps)
	if err != nil || result.Outcome != PolicyOutcomeApproved || result.Grant.Status != Approved || result.Grant.DecidedBy != StepsActor {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	if len(store.requests) != 0 || modes.entryCalls != 0 {
		t.Fatalf("an uncounted step used the step decision: requests=%d entry reads=%d", len(store.requests), modes.entryCalls)
	}
	if store.approved != 1 {
		t.Fatalf("approvals = %d", store.approved)
	}
}

func TestStepsDecidesACountedStepAgainstItsEntry(t *testing.T) {
	for _, entry := range []string{approvalpolicy.StepEntryList, approvalpolicy.StepEntryMCP} {
		store, modes, service := stepFixture("image-to-3d")
		modes.entry = entry
		store.summary = StepSummary{Entry: entry, Mode: approvalpolicy.StepModeCap, Cap: 5, Used: 2}
		result, err := service.ApplyPolicyDecision(context.Background(), store.grant)
		if err != nil || result.Outcome != PolicyOutcomeApproved || result.Grant.Status != Approved ||
			result.Steps == nil || result.Steps.Used != 2 || result.Steps.Cap != 5 {
			t.Fatalf("%s: result = %+v err=%v", entry, result, err)
		}
		if len(store.requests) != 1 || store.requests[0].Entry != entry || !store.requests[0].Steps.Counts("image-to-3d") ||
			store.requests[0].Steps.DefaultList.Cap != 20 {
			t.Fatalf("%s: request = %+v", entry, store.requests)
		}
	}
}

// A refusal is the answer, not a card: ApproveByPolicyDecision reports it as an
// error so a caller that only checks the error cannot take the denied grant
// for an approved one; ApplyPolicyDecision hands it back like a human wait.
func TestStepsCapRefusalDeniesInPlaceAndNeverLeavesTheStepToAPerson(t *testing.T) {
	store, _, service := stepFixture("text-to-3d")
	store.refuse = true
	store.summary = StepSummary{Entry: approvalpolicy.StepEntryList, Mode: approvalpolicy.StepModeCap, Cap: 3, Used: 3}

	direct, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", approvalpolicy.ModeSteps)
	if !errors.Is(err, ErrStepCapReached) || direct.Outcome != PolicyOutcomeStepCap || direct.Grant.Status != Denied ||
		!direct.Grant.StepCapReached() || direct.Steps == nil || direct.Steps.Used != 3 {
		t.Fatalf("direct = %+v err=%v", direct, err)
	}
	if store.approved != 0 {
		t.Fatal("a refused step was approved")
	}

	store, _, service = stepFixture("text-to-3d")
	store.refuse = true
	store.summary = StepSummary{Entry: approvalpolicy.StepEntryList, Mode: approvalpolicy.StepModeCap, Cap: 3, Used: 3}
	applied, err := service.ApplyPolicyDecision(context.Background(), store.grant)
	if err != nil || applied.Outcome != PolicyOutcomeStepCap || applied.Grant.Status != Denied ||
		!applied.Grant.StepCapReached() || applied.Steps == nil || applied.Steps.Cap != 3 {
		t.Fatalf("applied = %+v err=%v", applied, err)
	}
	// ApproveByPolicy never returns a denied grant without an error.
	store, _, service = stepFixture("text-to-3d")
	store.refuse = true
	if got, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", approvalpolicy.ModeSteps); !errors.Is(err, ErrStepCapReached) || got.Status != Denied {
		t.Fatalf("ApproveByPolicy = %+v err=%v", got, err)
	}
	// ApplyPolicy is the non-Agent entry: it only sees the denied grant.
	store, _, service = stepFixture("text-to-3d")
	store.refuse = true
	if got, err := service.ApplyPolicy(context.Background(), store.grant); err != nil || got.Status != Denied || !got.StepCapReached() {
		t.Fatalf("ApplyPolicy = %+v err=%v", got, err)
	}
}

func TestStepsRepeatsAnEarlierRefusalWithoutDecidingAgain(t *testing.T) {
	store, _, service := stepFixture("text-to-3d")
	store.grant.Status, store.grant.DecidedBy = Denied, StepsActor
	result, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", approvalpolicy.ModeSteps)
	if !errors.Is(err, ErrStepCapReached) || result.Outcome != PolicyOutcomeStepCap || len(store.requests) != 0 {
		t.Fatalf("repeat = %+v err=%v requests=%d", result, err, len(store.requests))
	}
	// A grant a person denied is not a step-cap refusal.
	store.grant.DecidedBy = "person-a"
	if _, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", approvalpolicy.ModeSteps); !errors.Is(err, ErrState) {
		t.Fatalf("a person's denial: %v", err)
	}
	// Another mode's replay is not the steps refusal either.
	store.grant.DecidedBy = StepsActor
	if _, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", approvalpolicy.ModeAuto); !errors.Is(err, ErrState) {
		t.Fatalf("auto replay of a steps refusal: %v", err)
	}
}

// The steps mode never sends a step to a person; a deployment that cannot name
// the entry or cannot decide atomically falls back to the stricter result.
func TestStepsFallsBackToAPersonWhenItCannotDecide(t *testing.T) {
	ctx := context.Background()
	// The mode reader cannot name an entry.
	store, _, service := stepFixture("image-to-3d")
	service.Modes = &modeReader{mode: approvalpolicy.ModeSteps}
	if _, err := service.ApproveByPolicyDecision(ctx, "owner-a", "request-a", approvalpolicy.ModeSteps); !errors.Is(err, ErrHumanRequired) || store.grant.Status != Pending {
		t.Fatalf("no entry reader: %v grant=%+v", err, store.grant)
	}
	// The entry is not one the policy knows.
	store, modes, service := stepFixture("image-to-3d")
	modes.entry = "web"
	if _, err := service.ApproveByPolicyDecision(ctx, "owner-a", "request-a", approvalpolicy.ModeSteps); !errors.Is(err, ErrHumanRequired) || store.grant.Status != Pending {
		t.Fatalf("unknown entry: %v grant=%+v", err, store.grant)
	}
	// The data layer cannot decide atomically.
	plain := policyFixture("image-to-3d")
	plain.grant.Source = AgentPending
	modes = &stepModes{modeReader: modeReader{mode: approvalpolicy.ModeSteps}, entry: approvalpolicy.StepEntryList}
	service = Service{Grants: plain, Prepared: plain, Policy: stepPolicy(), Modes: modes}
	if _, err := service.ApproveByPolicyDecision(ctx, "owner-a", "request-a", approvalpolicy.ModeSteps); !errors.Is(err, ErrHumanRequired) || plain.grant.Status != Pending {
		t.Fatalf("no step store: %v grant=%+v", err, plain.grant)
	}
	// ApplyPolicyDecision leaves the grant pending and reports it as a human wait.
	if result, err := service.ApplyPolicyDecision(ctx, plain.grant); err != nil || result.Outcome != PolicyOutcomeHuman || result.Grant.Status != Pending {
		t.Fatalf("applied = %+v err=%v", result, err)
	}
}

func TestStepsFailsClosedWhenTheEntryLookupFails(t *testing.T) {
	store, modes, service := stepFixture("image-to-3d")
	modes.entryErr = errors.New("session lookup failed")
	if _, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", approvalpolicy.ModeSteps); !errors.Is(err, modes.entryErr) ||
		store.grant.Status != Pending || len(store.requests) != 0 {
		t.Fatalf("err=%v grant=%+v requests=%d", err, store.grant, len(store.requests))
	}
	if _, err := service.ApplyPolicyDecision(context.Background(), store.grant); !errors.Is(err, modes.entryErr) {
		t.Fatalf("ApplyPolicyDecision swallowed the lookup error: %v", err)
	}
	// A store failure is an error too, never a silent approval or denial.
	store, _, service = stepFixture("image-to-3d")
	store.err = errors.New("database is locked")
	if _, err := service.ApplyPolicyDecision(context.Background(), store.grant); !errors.Is(err, store.err) || store.grant.Status != Pending {
		t.Fatalf("store failure: %v grant=%+v", err, store.grant)
	}
}

// Without application.step_policy the steps word is any unknown mode: manual,
// the stricter one, so nothing is approved that the Schema modes would not.
func TestStepsWordWithoutAStepPolicyBehavesAsManual(t *testing.T) {
	ctx := context.Background()
	store, _, service := stepFixture("image-to-3d")
	service.Policy = approvalpolicy.New("manual", []string{"auto", "manual"},
		[]string{"text-to-3d", "image-to-3d", "retexture"}, []string{"text-to-image", "image-to-3d"})
	if _, err := service.ApproveByPolicyDecision(ctx, "owner-a", "request-a", approvalpolicy.ModeSteps); !errors.Is(err, ErrHumanRequired) || store.grant.Status != Pending {
		t.Fatalf("3D under an unknown mode: %v grant=%+v", err, store.grant)
	}
	twoD, _, service := stepFixture("text-to-image")
	service.Policy = approvalpolicy.New("manual", []string{"auto", "manual"},
		[]string{"image-to-3d"}, []string{"text-to-image", "image-to-3d"})
	got, err := service.ApproveByPolicy(ctx, "owner-a", "request-a", approvalpolicy.ModeSteps)
	if err != nil || got.DecidedBy != "policy:manual" || twoD.approved != 1 {
		t.Fatalf("2D under an unknown mode = %+v err=%v", got, err)
	}
}

// Money is not the quota: a step that spends extra credits is still approved by
// the steps mode, where manual would show it on a card.
func TestStepsApprovesAStepThatSpendsExtraCredits(t *testing.T) {
	store, _, service := stepFixture("image-to-3d")
	store.snapshot.ExtraCredits = 40
	store.summary = StepSummary{Entry: approvalpolicy.StepEntryList, Mode: approvalpolicy.StepModeAgent, Cap: 20}
	if result, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", approvalpolicy.ModeSteps); err != nil || result.Outcome != PolicyOutcomeApproved {
		t.Fatalf("steps with extra credits = %+v err=%v", result, err)
	}
	// Manual still asks for the same step.
	store, _, service = stepFixture("text-to-image")
	store.snapshot.ExtraCredits = 40
	if _, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", approvalpolicy.ModeManual); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("manual with extra credits: %v", err)
	}
}

// The existing modes decide exactly as before when the steps mode is enabled.
func TestEnablingStepsDoesNotChangeTheOtherModes(t *testing.T) {
	for _, tc := range []struct {
		mode, capability string
		human            bool
	}{
		{"manual", "image-to-3d", true},
		{"manual", "text-to-image", false},
		{"auto", "image-to-3d", false},
		{"yolo", "image-to-3d", true},
	} {
		store, _, service := stepFixture(tc.capability)
		_, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", tc.mode)
		if tc.human != errors.Is(err, ErrHumanRequired) || (!tc.human && err != nil) {
			t.Fatalf("%s %s: err=%v grant=%+v", tc.mode, tc.capability, err, store.grant)
		}
		if len(store.requests) != 0 {
			t.Fatalf("%s %s used the step decision", tc.mode, tc.capability)
		}
	}
}

func TestStepStatusReadsTheEntryAndTheStore(t *testing.T) {
	store, modes, service := stepFixture("text-to-3d")
	modes.entry = approvalpolicy.StepEntryMCP
	store.usage = StepSummary{Entry: "mcp", Mode: "cap", Cap: 9, Used: 9}
	got, err := service.StepStatus(context.Background(), "owner-a", "session-a")
	if err != nil || got != store.usage || store.requests[0].Entry != approvalpolicy.StepEntryMCP {
		t.Fatalf("status = %+v err=%v requests=%+v", got, err, store.requests)
	}
	for name, mutate := range map[string]func(*Service){
		"no step policy": func(s *Service) { s.Policy = approvalpolicy.New("manual", []string{"manual"}, nil, nil) },
		"no step store":  func(s *Service) { s.Grants = policyFixture("text-to-3d") },
		"no entry":       func(s *Service) { s.Modes = &modeReader{} },
	} {
		broken := service
		mutate(&broken)
		if _, err := broken.StepStatus(context.Background(), "owner-a", "session-a"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := service.StepStatus(context.Background(), "", "session-a"); !errors.Is(err, ErrInvalid) {
		t.Errorf("blank owner: %v", err)
	}
}

// A step the steps mode approved gives its step back through the same abandon
// path as a budget reservation; a deployment without plan storage still has
// the grant store's releaser.
func TestAbandonFallsBackToTheGrantStoreReleaser(t *testing.T) {
	store, _, service := stepFixture("text-to-3d")
	service.Budgets = nil
	if err := service.AbandonBudgetReservation(context.Background(), "owner-a", "request-a"); err != nil || len(store.released) != 1 || store.released[0] != "request-a" {
		t.Fatalf("released = %v err=%v", store.released, err)
	}
	// Neither store can release: nothing to do, no error.
	plain := policyFixture("text-to-3d")
	service = Service{Grants: plain, Prepared: plain, Policy: stepPolicy()}
	if err := service.AbandonBudgetReservation(context.Background(), "owner-a", "request-a"); err != nil {
		t.Fatalf("plain store: %v", err)
	}
	if err := service.AbandonBudgetReservation(context.Background(), "", "request-a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank owner: %v", err)
	}
}

func TestStepCapReachedIsOnlyAPolicyDenial(t *testing.T) {
	for name, tc := range map[string]struct {
		status Status
		by     string
		want   bool
	}{
		"steps denial":      {Denied, StepsActor, true},
		"steps approval":    {Approved, StepsActor, false},
		"person denial":     {Denied, "person-a", false},
		"stopped turn":      {Denied, StoppedActor, false},
		"abandoned":         {Denied, AbandonedActor, false},
		"other policy deny": {Denied, "policy:manual", false},
	} {
		if got := (Grant{Status: tc.status, DecidedBy: tc.by}).StepCapReached(); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
