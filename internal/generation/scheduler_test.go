package generation

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"syscall"
	"testing"
	"time"
)

type sqliteFailure int

func (e sqliteFailure) Error() string { return "sqlite fault" }
func (e sqliteFailure) Code() int     { return int(e) }
func TestRecoveryClassifiesSQLiteFaults(t *testing.T) {
	for _, code := range []int{5, 6, 10, 13, 5 | (2 << 8)} {
		if !recoverableCoordinatorError(&Error{Cause: sqliteFailure(code)}) {
			t.Errorf("recoverable SQLite code %d rejected", code)
		}
	}
	for _, code := range []int{11, 26, 19} {
		if recoverableCoordinatorError(sqliteFailure(code)) {
			t.Errorf("permanent SQLite code %d accepted", code)
		}
	}
	if !recoverableCoordinatorError(syscall.ENOSPC) || !recoverableCoordinatorError(syscall.EIO) || recoverableCoordinatorError(errors.New("corrupt")) {
		t.Fatal("unexpected disk error classification")
	}
}
func TestBackoffJitterIsBoundedAndDeterministic(t *testing.T) {
	m := &Module{random: func() float64 { return 0.25 }}
	if delay := m.jitter(time.Second); delay != 625*time.Millisecond {
		t.Fatalf("delay=%v", delay)
	}
	if delay := exponentialDelay(time.Second, time.Minute, math.MaxInt32); delay != time.Minute {
		t.Fatalf("overflow delay=%v", delay)
	}
	if delay := m.taskRetryDelay(RetryPolicy{InitialBackoff: time.Second, MaxBackoff: time.Minute}, 1, time.Hour); delay != time.Minute {
		t.Fatalf("retry exceeded configured maximum: %v", delay)
	}
}
func TestCoordinatorRejectsUnsafeRenewalConfiguration(t *testing.T) {
	cfg := CoordinatorConfig{LeaseFor: time.Second, LeaseRenewEvery: time.Second}
	if validateCoordinatorConfig(cfg) == nil {
		t.Fatal("lease can expire before first renewal")
	}
}

// #298: only a database unusable as a whole stops the coordinator; a step
// error of one Task (constraint violation, unclassified error, ErrUnavailable
// without such a cause) is logged and backed off.
func TestOnlyGlobalPersistenceFailuresStopCoordinator(t *testing.T) {
	for _, err := range []error{
		sqliteFailure(11), sqliteFailure(26), sqliteFailure(14), sqliteFailure(8),
		sqliteFailure(3), sqliteFailure(23), sqliteFailure(11 | (1 << 8)),
		&Error{Code: ErrUnavailable, Cause: sqliteFailure(11)},
		fmt.Errorf("task t: %w", sql.ErrConnDone),
		errors.Join(sqliteFailure(19), sqliteFailure(11)),
	} {
		if !globalPersistenceFailure(err) {
			t.Errorf("global failure %v not recognized", err)
		}
	}
	for _, err := range []error{
		sqliteFailure(19), sqliteFailure(2067), sqliteFailure(1), sqliteFailure(20),
		errors.New("row cannot be written"),
		&Error{Code: ErrUnavailable, Operation: "resolve_task_dependencies", Detail: "来源资产暂不可读取"},
		mapRepositoryError("apply_observation", fmt.Errorf("%w: %v", RepositoryUpstreamJobConflict, sqliteFailure(2067))),
		errors.Join(sqliteFailure(19), errors.New("bad row")),
	} {
		if globalPersistenceFailure(err) {
			t.Errorf("per-task error %v treated as global", err)
		}
	}
}

func TestMixedBatchPermanentErrorStillStopsCoordinator(t *testing.T) {
	if recoverableCoordinatorError(errors.Join(sqliteFailure(5), sqliteFailure(11))) {
		t.Fatal("busy error hid database corruption")
	}
}

func TestEqualSourceTimestampCannotReplaceSameProgressMetadata(t *testing.T) {
	now := time.Now()
	task := Task{State: TaskRunning, Progress: 80}
	attempt := &Attempt{State: AttemptRunning, LastObservationPhase: UpstreamRunning, LastObservationAt: now}
	if !staleObservation(task, attempt, Observation{Phase: UpstreamRunning, Progress: 80, ObservedAt: now}) {
		t.Fatal("equal timestamp admitted same-progress overwrite")
	}
	if staleObservation(task, attempt, Observation{Phase: UpstreamCompleted, Progress: 100, ObservedAt: now}) {
		t.Fatal("same timestamp blocked terminal progress")
	}
}
