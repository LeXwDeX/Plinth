package assetlist_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type gateRunStore struct {
	assetlist.RunStore
	item    assetlist.Item
	found   bool
	itemErr error

	run    assetlist.Run
	runErr error
}

func (s *gateRunStore) ItemBySession(_ context.Context, ownerID, sessionID string) (assetlist.Item, bool, error) {
	if s.itemErr != nil {
		return assetlist.Item{}, false, s.itemErr
	}
	if !s.found {
		return assetlist.Item{}, false, nil
	}
	return s.item, true, nil
}

// RunSummaries keeps list reads undecorated in this fixture.
func (s *gateRunStore) RunSummaries(_ context.Context, _, _ string) (map[string]assetlist.RunSummary, error) {
	return nil, nil
}

func (s *gateRunStore) GetRunByListID(_ context.Context, ownerID, listID string) (assetlist.Run, error) {
	if s.runErr != nil {
		return assetlist.Run{}, s.runErr
	}
	return s.run, nil
}

type gatePresence struct {
	assetlist.PresenceStore
	active  bool
	err     error
	owner   string
	project string
	since   time.Time
}

func (p *gatePresence) Active(_ context.Context, ownerID, projectID string, since time.Time) (bool, error) {
	p.owner, p.project, p.since = ownerID, projectID, since
	if p.err != nil {
		return false, p.err
	}
	return p.active, nil
}

func newGateService(
	t *testing.T,
	runs *gateRunStore,
	presence assetlist.PresenceStore,
) (*assetlist.Service, *testClock) {
	t.Helper()
	service, _, clock := newService(t, func(config *assetlist.Config) {
		config.Runs = runs
		config.Presence = presence
		config.Schema.Execution = &catalog.AssetListExecutionSchema{
			Presence: catalog.AssetListPresenceSchema{GraceSeconds: 120},
		}
	})
	return service, clock
}

func TestSubmissionAllowedForOrdinarySession(t *testing.T) {
	t.Parallel()
	service, _ := newGateService(t, &gateRunStore{}, &gatePresence{active: true})
	decision, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_plain")
	if err != nil || !decision.Allowed || decision.Reason != "" {
		t.Fatalf("ordinary session = %+v, %v; want allowed", decision, err)
	}
}

func TestSubmissionAllowedRefusesNonWorkingItem(t *testing.T) {
	t.Parallel()
	for _, state := range []string{
		assetlist.StateTodo, assetlist.StateReview, assetlist.StateAccepted,
		assetlist.StateReturned, assetlist.StateFailed, assetlist.StateSkipped,
	} {
		state := state
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			runs := &gateRunStore{
				found: true,
				item:  assetlist.Item{ID: "item_1", ListID: "list_1", State: state},
			}
			service, _ := newGateService(t, runs, &gatePresence{active: true})
			decision, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item")
			if err != nil || decision.Allowed || decision.Reason != assetlist.RunStopped {
				t.Fatalf("state %s = %+v, %v; want stopped", state, decision, err)
			}
		})
	}
}

func TestSubmissionAllowedWithoutRunDoesNotBlock(t *testing.T) {
	t.Parallel()
	runs := &gateRunStore{
		found:  true,
		item:   assetlist.Item{ID: "item_1", ListID: "list_1", State: assetlist.StateWorking},
		runErr: assetlist.ErrNotFound,
	}
	presence := &gatePresence{active: false}
	service, _ := newGateService(t, runs, presence)
	decision, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item")
	if err != nil || !decision.Allowed || decision.Reason != "" {
		t.Fatalf("missing run = %+v, %v; want allowed", decision, err)
	}
	if presence.project != "" {
		t.Fatalf("missing run checked presence for %q", presence.project)
	}
}

func TestSubmissionAllowedReportsPauseReason(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{
		assetlist.PauseUser, assetlist.PausePresence,
		assetlist.PauseStepCap, assetlist.PauseFailure,
	} {
		reason := reason
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			runs := &gateRunStore{
				found: true,
				item:  assetlist.Item{ID: "item_1", ListID: "list_1", State: assetlist.StateWorking},
				run: assetlist.Run{
					OwnerID: "owner-a", ProjectID: "prj_1", ListID: "list_1",
					Status: assetlist.RunPaused, PauseReason: reason,
				},
			}
			service, _ := newGateService(t, runs, &gatePresence{active: true})
			decision, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item")
			if err != nil || decision.Allowed || decision.Reason != reason {
				t.Fatalf("paused(%s) = %+v, %v", reason, decision, err)
			}
		})
	}
	runs := &gateRunStore{
		found: true,
		item:  assetlist.Item{ID: "item_1", ListID: "list_1", State: assetlist.StateWorking},
		run: assetlist.Run{
			OwnerID: "owner-a", ProjectID: "prj_1", ListID: "list_1",
			Status: assetlist.RunPaused,
		},
	}
	service, _ := newGateService(t, runs, &gatePresence{active: true})
	if decision, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item"); err != nil ||
		decision.Allowed || decision.Reason != assetlist.PauseUser {
		t.Fatalf("paused without a reason = %+v, %v; want sticky user", decision, err)
	}
}

func TestSubmissionAllowedRefusesNonRunningRun(t *testing.T) {
	t.Parallel()
	for _, status := range []string{assetlist.RunIdle, assetlist.RunStopped, assetlist.RunCompleted} {
		status := status
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			runs := &gateRunStore{
				found: true,
				item:  assetlist.Item{ID: "item_1", ListID: "list_1", State: assetlist.StateWorking},
				run: assetlist.Run{
					OwnerID: "owner-a", ProjectID: "prj_1", ListID: "list_1", Status: status,
				},
			}
			service, _ := newGateService(t, runs, &gatePresence{active: true})
			decision, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item")
			if err != nil || decision.Allowed || decision.Reason != assetlist.RunStopped {
				t.Fatalf("run %s = %+v, %v; want stopped", status, decision, err)
			}
		})
	}
}

func TestSubmissionAllowedRequiresActivePresence(t *testing.T) {
	t.Parallel()
	runs := &gateRunStore{
		found: true,
		item:  assetlist.Item{ID: "item_1", ListID: "list_1", State: assetlist.StateWorking},
		run: assetlist.Run{
			OwnerID: "owner-a", ProjectID: "prj_1", ListID: "list_1", Status: assetlist.RunRunning,
		},
	}
	presence := &gatePresence{active: true}
	service, clock := newGateService(t, runs, presence)
	decision, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item")
	if err != nil || !decision.Allowed {
		t.Fatalf("present run = %+v, %v; want allowed", decision, err)
	}
	wantSince := clock.Now().Add(-120 * time.Second)
	if presence.owner != "owner-a" || presence.project != "prj_1" || !presence.since.Equal(wantSince) {
		t.Fatalf("presence read = %q/%q since %s; want owner-a/prj_1 since %s",
			presence.owner, presence.project, presence.since, wantSince)
	}

	presence.active = false
	decision, err = service.SubmissionAllowed(context.Background(), "owner-a", "ses_item")
	if err != nil || decision.Allowed || decision.Reason != assetlist.PausePresence {
		t.Fatalf("expired presence = %+v, %v; want presence pause", decision, err)
	}
}

func TestSubmissionAllowedWithoutPresenceStoreKeepsRunGate(t *testing.T) {
	t.Parallel()
	runs := &gateRunStore{
		found: true,
		item:  assetlist.Item{ID: "item_1", ListID: "list_1", State: assetlist.StateWorking},
		run: assetlist.Run{
			OwnerID: "owner-a", ProjectID: "prj_1", ListID: "list_1", Status: assetlist.RunRunning,
		},
	}
	service, _ := newGateService(t, runs, nil)
	if decision, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item"); err != nil || !decision.Allowed {
		t.Fatalf("running run without presence = %+v, %v; want allowed", decision, err)
	}
}

func TestSubmissionAllowedPropagatesReadErrors(t *testing.T) {
	t.Parallel()
	itemErr := errors.New("item read failed")
	runs := &gateRunStore{itemErr: itemErr}
	service, _ := newGateService(t, runs, &gatePresence{active: true})
	if _, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item"); !errors.Is(err, itemErr) {
		t.Fatalf("item error = %v; want %v", err, itemErr)
	}

	runErr := errors.New("run read failed")
	runs = &gateRunStore{
		found:  true,
		item:   assetlist.Item{ID: "item_1", ListID: "list_1", State: assetlist.StateWorking},
		runErr: runErr,
	}
	service, _ = newGateService(t, runs, &gatePresence{active: true})
	if _, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item"); !errors.Is(err, runErr) {
		t.Fatalf("run error = %v; want %v", err, runErr)
	}

	presenceErr := errors.New("presence read failed")
	runs = &gateRunStore{
		found: true,
		item:  assetlist.Item{ID: "item_1", ListID: "list_1", State: assetlist.StateWorking},
		run: assetlist.Run{
			OwnerID: "owner-a", ProjectID: "prj_1", ListID: "list_1", Status: assetlist.RunRunning,
		},
	}
	service, _ = newGateService(t, runs, &gatePresence{err: presenceErr})
	if _, err := service.SubmissionAllowed(context.Background(), "owner-a", "ses_item"); !errors.Is(err, presenceErr) {
		t.Fatalf("presence error = %v; want %v", err, presenceErr)
	}
}
