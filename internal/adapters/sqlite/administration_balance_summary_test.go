package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	_ "modernc.org/sqlite"
)

func TestAdministrationBalanceSnapshotsCombineGoAndLegacyHistory(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "balances.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE balance_snapshots (
			id TEXT PRIMARY KEY,
			balance REAL NOT NULL,
			capturedAt DATETIME NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO balance_snapshots (id, balance, capturedAt)
		 VALUES (?, ?, ?)`,
		"legacy",
		90.0,
		now.Add(-2*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordBalanceSnapshot(
		context.Background(),
		administration.BalanceSnapshot{
			ID:         "go-current",
			Balance:    88.5,
			CapturedAt: now,
		},
	); err != nil {
		t.Fatal(err)
	}

	history, err := repository.QueryBalanceSnapshots(
		context.Background(),
		now.Add(-24*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 ||
		history[0].ID != "legacy" ||
		history[0].Balance != 90 ||
		!history[0].CapturedAt.Equal(now.Add(-2*time.Hour)) ||
		history[1].ID != "go-current" ||
		history[1].Balance != 88.5 ||
		!history[1].CapturedAt.Equal(now) {
		t.Fatalf("balance history = %#v", history)
	}

	filtered, err := repository.QueryBalanceSnapshots(
		context.Background(),
		now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != "go-current" {
		t.Fatalf("filtered history = %#v", filtered)
	}
}
