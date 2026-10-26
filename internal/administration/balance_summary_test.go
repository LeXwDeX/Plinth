package administration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBalanceSummaryReadsConfiguredAccountRecordsAndQueriesHistory(
	t *testing.T,
) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	accounts := &recordingAccountBalanceReader{balance: 88.5}
	snapshots := &recordingBalanceSnapshotRepository{
		history: []BalanceSnapshot{{
			ID:         "old",
			Balance:    90,
			CapturedAt: now.Add(-time.Hour),
		}},
	}
	service := newTestBalanceSummaryService(
		t,
		accounts,
		snapshots,
		now,
		"best-effort",
	)

	summary, err := service.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if accounts.accountID != "meshy" {
		t.Fatalf("account = %q", accounts.accountID)
	}
	if snapshots.recorded.ID != "balance-fixed" ||
		snapshots.recorded.Balance != 88.5 ||
		!snapshots.recorded.CapturedAt.Equal(now) {
		t.Fatalf("recorded snapshot = %#v", snapshots.recorded)
	}
	expectedSince := now.AddDate(0, 0, -7)
	if !snapshots.since.Equal(expectedSince) {
		t.Fatalf("history since=%s want=%s", snapshots.since, expectedSince)
	}
	if summary.CurrentBalance != 88.5 ||
		len(summary.History) != 1 ||
		summary.History[0].Balance != 90 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestBalanceSummaryAppliesSchemaSnapshotWritePolicy(
	t *testing.T,
) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	writeFailure := errors.New("write failed")
	bestEffort := newTestBalanceSummaryService(
		t,
		&recordingAccountBalanceReader{balance: 10},
		&recordingBalanceSnapshotRepository{recordErr: writeFailure},
		now,
		"best-effort",
	)
	if _, err := bestEffort.Summary(context.Background()); err != nil {
		t.Fatalf("best-effort summary = %v", err)
	}

	required := newTestBalanceSummaryService(
		t,
		&recordingAccountBalanceReader{balance: 10},
		&recordingBalanceSnapshotRepository{recordErr: writeFailure},
		now,
		"required",
	)
	if _, err := required.Summary(context.Background()); !errors.Is(
		err,
		ErrBalanceSummaryFailed,
	) {
		t.Fatalf("required summary = %v", err)
	}

	readFailure := newTestBalanceSummaryService(
		t,
		&recordingAccountBalanceReader{err: errors.New("upstream failed")},
		&recordingBalanceSnapshotRepository{},
		now,
		"best-effort",
	)
	if _, err := readFailure.Summary(context.Background()); !errors.Is(
		err,
		ErrBalanceSummaryFailed,
	) {
		t.Fatalf("read failure = %v", err)
	}
}

func newTestBalanceSummaryService(
	t *testing.T,
	accounts AccountBalanceReader,
	snapshots BalanceSnapshotRepository,
	now time.Time,
	writePolicy string,
) *BalanceSummaryService {
	t.Helper()
	service, err := NewBalanceSummaryService(BalanceSummaryServiceConfig{
		Accounts:            accounts,
		Snapshots:           snapshots,
		AccountID:           "meshy",
		HistoryDays:         7,
		SnapshotIDPrefix:    "balance",
		SnapshotWritePolicy: writePolicy,
		Now:                 func() time.Time { return now },
		NewID: func(prefix string) (string, error) {
			return prefix + "-fixed", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type recordingAccountBalanceReader struct {
	accountID string
	balance   float64
	err       error
}

func (reader *recordingAccountBalanceReader) Balance(
	_ context.Context,
	accountID string,
) (float64, error) {
	reader.accountID = accountID
	return reader.balance, reader.err
}

type recordingBalanceSnapshotRepository struct {
	recorded  BalanceSnapshot
	since     time.Time
	history   []BalanceSnapshot
	recordErr error
	queryErr  error
}

func (repository *recordingBalanceSnapshotRepository) RecordBalanceSnapshot(
	_ context.Context,
	snapshot BalanceSnapshot,
) error {
	repository.recorded = snapshot
	return repository.recordErr
}

func (repository *recordingBalanceSnapshotRepository) QueryBalanceSnapshots(
	_ context.Context,
	since time.Time,
) ([]BalanceSnapshot, error) {
	repository.since = since
	return repository.history, repository.queryErr
}
