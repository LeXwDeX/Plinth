package generation_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/assetrecovery"
)

type navigationRepository struct {
	*sqlite.Repository
	lookups       int
	lookupError   error
	mutateTask    func(*generation.Task)
	mutateReceipt func(*generation.RecoveryReceipt)
}

func (r *navigationRepository) GetByIdempotency(ctx context.Context, owner generation.OwnerID, key string) (generation.Task, error) {
	r.lookups++
	if r.lookupError != nil {
		return generation.Task{}, r.lookupError
	}
	task, err := r.Repository.GetByIdempotency(ctx, owner, key)
	if err == nil && r.mutateTask != nil {
		r.mutateTask(&task)
	}
	return task, err
}
func (r *navigationRepository) FindRecoveryReceipt(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (generation.RecoveryReceipt, error) {
	receipt, err := r.Repository.FindRecoveryReceipt(ctx, owner, task)
	if err == nil && r.mutateReceipt != nil {
		r.mutateReceipt(&receipt)
	}
	return receipt, err
}

type navigationAssets struct {
	*local.Store
	opens int
}

func (s *navigationAssets) Open(ctx context.Context, a generation.Asset) (io.ReadCloser, error) {
	s.opens++
	return s.Store.Open(ctx, a)
}

type navigationPort struct {
	*assetrecovery.Adapter
	validations int
}

func (p *navigationPort) ValidateInput(ctx context.Context, r generation.InputValidationRequest) error {
	p.validations++
	return p.Adapter.ValidateInput(ctx, r)
}

func navigationModule(t *testing.T, h *recoveryHarness, available bool) (*navigationRepository, *navigationPort, *navigationAssets) {
	t.Helper()
	repo := &navigationRepository{Repository: h.repo}
	assets := &navigationAssets{Store: h.store}
	adapter, err := assetrecovery.New(repo, assets)
	if err != nil {
		t.Fatal(err)
	}
	port := &navigationPort{Adapter: adapter}
	registry := providers.NewRegistry()
	if available {
		if err = registry.Register(h.offering.AdapterKey, port); err != nil {
			t.Fatal(err)
		}
	}
	h.module, err = generation.New(generation.Dependencies{Repository: repo, Catalog: rigReportCatalog{[]generation.Offering{h.offering}}, Providers: registry, Assets: assets, Clock: h.clock, SchemaVersion: "recovery-navigation-test"})
	if err != nil {
		t.Fatal(err)
	}
	return repo, port, assets
}
func acceptNavigationTask(t *testing.T, h *recoveryHarness) generation.TaskID {
	t.Helper()
	ctx := context.Background()
	prepared, view, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repo, prepared, view.IdempotencyKey))
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}
func damageNavigationBytes(t *testing.T, h *recoveryHarness, damage string) {
	t.Helper()
	path := filepath.Join(h.cas, h.receipt.Slots[0].StorageKey)
	if damage == "missing" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	} else if damage == "corrupt" {
		if err := os.WriteFile(path, []byte("changed staged bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestAssetRecoveryExistingNavigationSkipsByteValidation(t *testing.T) {
	for _, damage := range []string{"valid", "missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			h := newRecoveryHarness(t)
			ctx := context.Background()
			id := acceptNavigationTask(t, h)
			if err := h.module.Close(); err != nil {
				t.Fatal(err)
			}
			h.open(t)
			before, err := h.module.RecoveryEligibility(ctx, "owner", "source-task")
			if err != nil {
				t.Fatal(err)
			}
			damageNavigationBytes(t, h, damage)
			repo, port, assets := navigationModule(t, h, true)
			// Any already-bound Task state remains navigable, without creating another Task or Grant.
			for _, state := range []generation.TaskState{generation.TaskQueued, generation.TaskRunning, generation.TaskMaterializing, generation.TaskSucceeded, generation.TaskFailed, generation.TaskCanceled} {
				repo.mutateTask = func(task *generation.Task) { task.State = state }
				got, err := h.module.RecoveryEligibility(ctx, "owner", "source-task")
				if err != nil || !reflect.DeepEqual(got, before) || got.RecoveryTaskID != id {
					t.Fatalf("state=%s got=%+v err=%v", state, got, err)
				}
			}
			prepared, view, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest)
			if err != nil || prepared.ID != "" || view.RecoveryTaskID != id {
				t.Fatalf("prepared=%+v view=%+v err=%v", prepared, view, err)
			}
			if port.validations != 0 || assets.opens != 0 || repo.lookups != 7 {
				t.Fatalf("validation=%d opens=%d lookups=%d", port.validations, assets.opens, repo.lookups)
			}
			var tasks, grants, preparedCount int
			if err = h.db.QueryRow(`SELECT count(*) FROM generation_tasks`).Scan(&tasks); err != nil {
				t.Fatal(err)
			}
			if err = h.db.QueryRow(`SELECT count(*) FROM execution_grants`).Scan(&grants); err != nil {
				t.Fatal(err)
			}
			if err = h.db.QueryRow(`SELECT count(*) FROM generation_prepared_inputs`).Scan(&preparedCount); err != nil {
				t.Fatal(err)
			}
			if tasks != 2 || grants != 1 || preparedCount != 1 {
				t.Fatalf("navigation wrote tasks/grants/prepared: %d/%d/%d", tasks, grants, preparedCount)
			}
			h.assertSourceUnchanged(t)
		})
	}
}
func TestAssetRecoveryExistingNavigationRejectsIdentityChanges(t *testing.T) {
	cases := []struct {
		name   string
		change func(*navigationRepository, *recoveryHarness)
	}{
		{"session", func(r *navigationRepository, _ *recoveryHarness) {
			r.mutateTask = func(task *generation.Task) { task.SessionID = "another-session" }
		}},
		{"offering", func(r *navigationRepository, _ *recoveryHarness) {
			r.mutateTask = func(task *generation.Task) { task.Offering.ID = "another-offering" }
		}},
		{"input", func(r *navigationRepository, _ *recoveryHarness) {
			r.mutateTask = func(task *generation.Task) { task.Input = json.RawMessage(`{}`) }
		}},
		{"receipt", func(r *navigationRepository, _ *recoveryHarness) {
			r.mutateReceipt = func(receipt *generation.RecoveryReceipt) { receipt.Digest = "changed" }
		}},
		{"source", func(_ *navigationRepository, h *recoveryHarness) {
			if _, err := h.db.Exec(`UPDATE generation_attempts SET opaque_json='{"changed":true}' WHERE id='source-attempt'`); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecoveryHarness(t)
			acceptNavigationTask(t, h)
			repo, port, assets := navigationModule(t, h, true)
			tc.change(repo, h)
			if _, err := h.module.RecoveryEligibility(context.Background(), "owner", "source-task"); err == nil {
				t.Fatal("identity change accepted")
			}
			if port.validations != 0 || assets.opens != 0 {
				t.Fatal("rejected identity reached byte validation")
			}
		})
	}
}
func TestAssetRecoveryExistingNavigationKeepsAvailabilityAndOwnership(t *testing.T) {
	for _, kind := range []string{"owner", "source-state", "source-session", "disabled", "adapter"} {
		t.Run(kind, func(t *testing.T) {
			h := newRecoveryHarness(t)
			acceptNavigationTask(t, h)
			owner := generation.OwnerID("owner")
			switch kind {
			case "owner":
				owner = "other"
			case "source-state":
				if _, err := h.db.Exec(`UPDATE generation_tasks SET state='SUCCEEDED' WHERE id='source-task'`); err != nil {
					t.Fatal(err)
				}
			case "source-session":
				if _, err := h.db.Exec(`UPDATE generation_tasks SET session_id='' WHERE id='source-task'`); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				h.offering.Enabled = false
			}
			repo, port, assets := navigationModule(t, h, kind != "adapter")
			view, err := h.module.RecoveryEligibility(context.Background(), owner, "source-task")
			if kind == "owner" {
				if !generation.IsCode(err, generation.ErrNotFound) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if view.Eligible || view.RecoveryTaskID != "" || repo.lookups != 0 || port.validations != 0 || assets.opens != 0 {
				t.Fatalf("guard bypassed: %+v", view)
			}
		})
	}
}
func TestAssetRecoveryNavigationPropagatesLookupFailureBeforeBytes(t *testing.T) {
	h := newRecoveryHarness(t)
	damageNavigationBytes(t, h, "missing")
	repo, port, assets := navigationModule(t, h, true)
	sentinel := errors.New("repository temporarily unavailable")
	repo.lookupError = sentinel
	if _, err := h.module.RecoveryEligibility(context.Background(), "owner", "source-task"); !errors.Is(err, sentinel) {
		t.Fatalf("lookup error replaced: %v", err)
	}
	if repo.lookups != 1 || port.validations != 0 || assets.opens != 0 {
		t.Fatalf("lookups=%d validations=%d opens=%d", repo.lookups, port.validations, assets.opens)
	}
}
func TestAssetRecoveryNewNavigationStillValidatesBytes(t *testing.T) {
	for _, damage := range []string{"valid", "missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			h := newRecoveryHarness(t)
			damageNavigationBytes(t, h, damage)
			repo, port, assets := navigationModule(t, h, true)
			view, err := h.module.RecoveryEligibility(context.Background(), "owner", "source-task")
			if damage == "valid" {
				if err != nil || !view.Eligible || view.RecoveryTaskID != "" {
					t.Fatalf("view=%+v err=%v", view, err)
				}
			} else if err == nil {
				t.Fatal("damaged staged bytes accepted")
			}
			if repo.lookups != 1 || port.validations != 1 || assets.opens == 0 {
				t.Fatalf("lookups=%d validations=%d opens=%d", repo.lookups, port.validations, assets.opens)
			}
		})
	}
}

func TestAssetRecoverySubmitRevalidatesBytesAfterGrant(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			h := newRecoveryHarness(t)
			ctx := context.Background()
			prepared, view, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest)
			if err != nil {
				t.Fatal(err)
			}
			command := authorizePrepared(t, ctx, h.repo, prepared, view.IdempotencyKey)
			before, err := h.repo.GetExecutionGrant(ctx, "owner", command.AuthorizationRef)
			if err != nil || before.TaskID != "" {
				t.Fatalf("grant not initially unbound: %+v %v", before, err)
			}
			damageNavigationBytes(t, h, damage)
			if _, err = h.module.SubmitPrepared(ctx, command); err == nil {
				t.Fatal("submission accepted staged bytes changed after approval")
			}
			after, err := h.repo.GetExecutionGrant(ctx, "owner", command.AuthorizationRef)
			if err != nil || !reflect.DeepEqual(after, before) || after.TaskID != "" {
				t.Fatalf("rejected submit changed grant: before=%+v after=%+v err=%v", before, after, err)
			}
			var tasks, attempts int
			if err = h.db.QueryRow(`SELECT count(*) FROM generation_tasks`).Scan(&tasks); err != nil {
				t.Fatal(err)
			}
			if err = h.db.QueryRow(`SELECT count(*) FROM generation_attempts`).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if tasks != 1 || attempts != 1 || h.starts != 0 {
				t.Fatalf("rejected submit wrote Task/Attempt or started adapter: %d/%d/%d", tasks, attempts, h.starts)
			}
			h.assertSourceUnchanged(t)
		})
	}
}
