package executiongrant

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

// checkpointService is the #702 policy: strict asks for every step, budget
// ("approve") for the 3D list, and auto and budget ask once every 100
// approved steps of the session.
func checkpointService(store *fakeGrantStore, approved *int) Service {
	policy := approvalpolicy.New("auto", []string{"auto", "budget", "manual"},
		[]string{"text-to-3d", "image-to-3d"}, []string{"text-to-image", "image-to-3d"}).
		WithCheckpoints(map[string]int{"auto": 100, "budget": 100})
	return Service{Grants: store, Prepared: store, Policy: policy,
		SessionPaidSteps: func(_ context.Context, owner, session string) (int, error) {
			if owner != "owner-a" || session != "session-a" {
				return 0, errors.New("wrong session")
			}
			return *approved, nil
		}}
}

func TestCheckpointAsksOnceEveryHundredApprovedSteps(t *testing.T) {
	for _, mode := range []string{"auto", "budget"} {
		for _, source := range []string{AgentPending, HumanBrowser} {
			approved := 100
			store := policyFixture("text-to-image")
			store.grant.Source = source
			service := checkpointService(store, &approved)
			_, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", mode)
			if !errors.Is(err, ErrHumanRequired) || store.grant.Status != Pending || store.approved != 0 {
				t.Fatalf("%s/%s at 100 approved: err=%v grant=%+v", mode, source, err, store.grant)
			}
			// A denial leaves the count at 100: the next step still asks.
			if _, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", mode); !errors.Is(err, ErrHumanRequired) {
				t.Fatalf("%s/%s asked once and then approved at the same count: %v", mode, source, err)
			}
			// The person's approval moved the count on: the window starts again.
			approved = 101
			got, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", mode)
			if err != nil || got.Status != Approved {
				t.Fatalf("%s/%s at 101 approved: %+v %v", mode, source, got, err)
			}
			// A replay of the approved grant is unchanged, even at the next checkpoint.
			approved = 200
			if again, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", mode); err != nil || again.Status != Approved {
				t.Fatalf("%s/%s replay at 200: %+v %v", mode, source, again, err)
			}
		}
	}
}

func TestCheckpointIgnoresStrictStepsAndDelivery(t *testing.T) {
	approved := 100
	// Strict asks for every step anyway, a 2D one included.
	store := policyFixture("text-to-image")
	store.grant.Source = AgentPending
	if _, err := checkpointService(store, &approved).ApproveByPolicy(context.Background(), "owner-a", "request-a", "manual"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("strict 2D step was not left to a person: %v", err)
	}
	approved = 37
	if _, err := checkpointService(store, &approved).ApproveByPolicy(context.Background(), "owner-a", "request-a", "manual"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("strict 2D step off the checkpoint was approved: %v", err)
	}
	// A delivery grant is the platform's own promise: never carded.
	approved = 100
	delivery := policyFixture("text-to-image")
	delivery.grant.Source = Delivery
	if got, err := checkpointService(delivery, &approved).ApproveByPolicy(context.Background(), "owner-a", "request-a", "auto"); err != nil || got.Status != Approved {
		t.Fatalf("delivery grant was carded at the checkpoint: %+v %v", got, err)
	}
}

func TestBudgetModeAsksForThreeDButNotTwoDBeforeTheCheckpoint(t *testing.T) {
	approved := 42
	store := policyFixture("image-to-3d")
	store.grant.Source = AgentPending
	if _, err := checkpointService(store, &approved).ApproveByPolicy(context.Background(), "owner-a", "request-a", "budget"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("budget 3D step was approved: %v", err)
	}
	image := policyFixture("text-to-image")
	image.grant.Source = AgentPending
	if got, err := checkpointService(image, &approved).ApproveByPolicy(context.Background(), "owner-a", "request-a", "budget"); err != nil || got.Status != Approved {
		t.Fatalf("budget 2D step before the checkpoint: %+v %v", got, err)
	}
}

func TestCheckpointCountFailureIsAnError(t *testing.T) {
	store := policyFixture("text-to-image")
	service := checkpointService(store, new(int))
	failure := errors.New("count failed")
	service.SessionPaidSteps = func(context.Context, string, string) (int, error) { return 0, failure }
	if _, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", "auto"); !errors.Is(err, failure) ||
		store.grant.Status != Pending {
		t.Fatalf("a failed count approved the step: %v %+v", err, store.grant)
	}
}

// #702: strict asks for every step the Agent starts. A 2D step the person
// started in the browser was their own click, so it runs; a 3D step they
// started, and a step of an Agent-created plan, still ask on a card.
func TestStrictSparesThePersonsOwnTwoDClick(t *testing.T) {
	approved := 3
	cases := []struct {
		name       string
		capability string
		source     string
		eligible   bool
		human      bool
	}{
		{"browser 2D", "text-to-image", HumanBrowser, false, false},
		{"browser 3D", "image-to-3d", HumanBrowser, false, true},
		{"agent 2D", "text-to-image", AgentPending, false, true},
		{"agent plan 2D", "text-to-image", HumanBrowser, true, true},
	}
	for _, c := range cases {
		store := policyFixture(c.capability)
		store.grant.Source, store.grant.BudgetEligible = c.source, c.eligible
		got, err := checkpointService(store, &approved).ApproveByPolicy(context.Background(), "owner-a", "request-a", "manual")
		if c.human && (!errors.Is(err, ErrHumanRequired) || store.grant.Status != Pending) {
			t.Fatalf("%s: err=%v grant=%+v, want left to a person", c.name, err, store.grant)
		}
		if !c.human && (err != nil || got.Status != Approved || got.DecidedBy != PolicyActorPrefix+"manual") {
			t.Fatalf("%s: %+v err=%v, want approved on the click", c.name, got, err)
		}
	}
}
