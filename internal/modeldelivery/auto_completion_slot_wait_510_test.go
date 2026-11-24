package modeldelivery

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// occupy fills the platform's plan slots with plans of other Tasks.
func (f *autoFixture) occupy(state string) {
	for index := range f.service.schema.AutoCompletion.MaxInflightPlans {
		id := fmt.Sprintf("other-plan-%d", index)
		f.store.plans[id] = processing.Plan{
			ID: id, OwnerID: "owner", State: state,
			Origin: processing.OriginDelivery, SourceTaskID: generation.TaskID("other-task-" + id),
		}
	}
}

func (f *autoFixture) slotWait() time.Duration {
	return time.Duration(f.service.schema.AutoCompletion.SlotWaitIntervalMilliseconds) * time.Millisecond
}

// #510: a record that only waits for a platform slot is looked at again after
// the Schema's slot-wait interval, not after a whole scan interval: a freed slot
// is taken within seconds instead of leaving a batch to drain one plan per
// scan.
func TestAutoCompletionLooksAgainSoonWhileWaitingForASlot(t *testing.T) {
	f := newAutoFixture(t)
	wait := f.slotWait()
	scan := time.Duration(f.service.schema.AutoCompletion.ScanIntervalMilliseconds) * time.Millisecond
	if wait <= 0 || wait >= scan {
		t.Fatalf("slot wait %s, scan %s: the Schema must declare a shorter slot wait", wait, scan)
	}
	f.occupy(processing.Running)
	task := f.tripoMissing("tripo-task", "owner", "usdz")
	f.candidate(task)

	f.reconcile(t)
	record := f.recordOf(t, task.ID)
	if record.State != AutoStatePending || record.Reason != autoReasonSlotWait {
		t.Fatalf("record = %+v, want it waiting for a slot", record)
	}
	if got := record.NextRunAt.Sub(f.clock); got != wait {
		t.Fatalf("next round in %s, want the slot wait %s", got, wait)
	}

	f.occupy(processing.Succeeded)
	f.clock = f.clock.Add(wait + time.Second)
	f.reconcile(t)
	if plans := f.plansOf(t, task.ID); len(plans) != 1 {
		t.Fatalf("plans = %#v, want the run once a slot freed, without waiting a scan", plans)
	}
}

// #510: the time a record spends waiting for a slot does not count toward the
// Schema's deadline. A batch whose queue is longer than the deadline is still
// completed once its turn comes, instead of being closed unattended.
func TestAutoCompletionDeadlineDoesNotRunWhileWaitingForASlot(t *testing.T) {
	f := newAutoFixture(t)
	f.occupy(processing.Running)
	task := f.tripoMissing("tripo-task", "owner", "usdz")
	f.candidate(task)
	deadline := time.Duration(f.service.schema.AutoCompletion.DeadlineHours) * time.Hour

	end := f.clock.Add(deadline + 30*time.Minute)
	for f.clock.Before(end) {
		f.reconcile(t)
		f.clock = f.clock.Add(f.slotWait() + time.Second)
	}
	if record := f.recordOf(t, task.ID); record.State != AutoStatePending {
		t.Fatalf("record = %+v, want it still waiting for its turn", record)
	}

	f.occupy(processing.Succeeded)
	f.reconcile(t)
	record := f.recordOf(t, task.ID)
	if record.Reason == autoReasonDeadline {
		t.Fatalf("record = %+v: the queue's wait ran the deadline out", record)
	}
	if plans := f.plansOf(t, task.ID); len(plans) != 1 {
		t.Fatalf("plans = %#v, want the run once its turn came", plans)
	}
}

// #510: one record whose round fails does not stop the scan: the records after
// it are still looked at, and the failed one is put off instead of staying at
// the head of every later scan.
func TestAutoCompletionFailedRoundDoesNotBlockTheScan(t *testing.T) {
	f := newAutoFixture(t)
	f.service.schema.AutoCompletion.MaxInflightPlans = 2
	first := f.tripoMissing("a-task", "owner", "usdz")
	second := f.tripoMissing("b-task", "owner", "usdz")
	for _, task := range []generation.Task{first, second} {
		f.record(AutoCompletionRecord{
			TaskID: task.ID, OwnerID: task.OwnerID, State: AutoStatePending,
			FirstRunAt: f.clock, NextRunAt: f.clock,
		})
	}
	readErr := errors.New("task read unavailable")
	f.getErrs[first.ID] = readErr

	if _, err := f.service.ReconcileAutoCompletions(context.Background(), 100); !errors.Is(err, readErr) {
		t.Fatalf("reconcile hid the failed read: %v", err)
	}
	if plans := f.plansOf(t, second.ID); len(plans) != 1 {
		t.Fatalf("plans = %#v, want the record after the failed one looked at", plans)
	}
	if record := f.recordOf(t, first.ID); !record.NextRunAt.After(f.clock) {
		t.Fatalf("record = %+v, want the failed round put off", record)
	}
}
