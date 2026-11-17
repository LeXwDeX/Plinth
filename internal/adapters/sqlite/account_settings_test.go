package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

func openAccountSettingsRepo(t *testing.T) *Repository {
	t.Helper()
	r, err := openTestRepository(filepath.Join(t.TempDir(), "account-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func stepSettings(listMode string, listCap int, mcpMode string, mcpCap int) accountsettings.Settings {
	return accountsettings.Settings{
		List: approvalpolicy.StepSetting{Mode: listMode, Cap: listCap},
		MCP:  approvalpolicy.StepSetting{Mode: mcpMode, Cap: mcpCap},
	}
}

func TestAccountSettingsRoundTripWithOptimisticVersion(t *testing.T) {
	ctx := context.Background()
	r := openAccountSettingsRepo(t)
	if _, found, err := r.AccountSettings(ctx, "owner-a"); err != nil || found {
		t.Fatalf("a person who never saved has a row: found=%v err=%v", found, err)
	}
	now := time.Date(2026, 11, 17, 4, 0, 0, 0, time.UTC)
	first, err := r.PutAccountSettings(ctx, "owner-a", 0, stepSettings("cap", 30, "agent", 20), now)
	if err != nil || first.Version != 1 {
		t.Fatalf("first save = %+v err=%v", first, err)
	}
	stored, found, err := r.AccountSettings(ctx, "owner-a")
	if err != nil || !found || stored.Version != 1 || stored.List.Mode != "cap" || stored.List.Cap != 30 ||
		stored.MCP.Mode != "agent" || stored.MCP.Cap != 20 || !stored.UpdatedAt.Equal(now) {
		t.Fatalf("stored = %+v found=%v err=%v", stored, found, err)
	}
	// The cap is kept while the mode is not cap, so switching back restores it.
	second, err := r.PutAccountSettings(ctx, "owner-a", 1, stepSettings("agent", 30, "unlimited", 20), now.Add(time.Minute))
	if err != nil || second.Version != 2 {
		t.Fatalf("second save = %+v err=%v", second, err)
	}
	if stored, _, _ := r.AccountSettings(ctx, "owner-a"); stored.List.Cap != 30 || stored.MCP.Mode != "unlimited" || stored.Version != 2 {
		t.Fatalf("stored after update = %+v", stored)
	}
}

func TestAccountSettingsConflictOnAStaleOrImpossibleVersion(t *testing.T) {
	ctx := context.Background()
	r := openAccountSettingsRepo(t)
	now := time.Now().UTC()
	if _, err := r.PutAccountSettings(ctx, "owner-a", 0, stepSettings("cap", 5, "agent", 20), now); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]int64{
		"first save repeated": 0,
		"version ahead":       7,
	} {
		if _, err := r.PutAccountSettings(ctx, "owner-a", expected, stepSettings("unlimited", 5, "agent", 20), now); !errors.Is(err, accountsettings.ErrConflict) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := r.PutAccountSettings(ctx, "owner-a", 1, stepSettings("unlimited", 5, "agent", 20), now); err != nil {
		t.Fatal(err)
	}
	// The stale writer lost; its values were not written.
	if _, err := r.PutAccountSettings(ctx, "owner-a", 1, stepSettings("cap", 99, "cap", 99), now); !errors.Is(err, accountsettings.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	if stored, _, _ := r.AccountSettings(ctx, "owner-a"); stored.Version != 2 || stored.List.Mode != "unlimited" || stored.List.Cap != 5 {
		t.Fatalf("stored after the lost write = %+v", stored)
	}
	// A positive version cannot create a row.
	if _, err := r.PutAccountSettings(ctx, "owner-b", 3, stepSettings("cap", 5, "agent", 20), now); !errors.Is(err, accountsettings.ErrConflict) {
		t.Fatalf("update of a missing row: %v", err)
	}
	if _, found, _ := r.AccountSettings(ctx, "owner-b"); found {
		t.Fatal("an update created a row")
	}
	for name, call := range map[string]func() error{
		"empty owner": func() error {
			_, err := r.PutAccountSettings(ctx, "", 0, stepSettings("cap", 5, "cap", 5), now)
			return err
		},
		"negative version": func() error {
			_, err := r.PutAccountSettings(ctx, "owner-a", -1, stepSettings("cap", 5, "cap", 5), now)
			return err
		},
		"empty mode": func() error {
			_, err := r.PutAccountSettings(ctx, "owner-a", 2, stepSettings("", 5, "cap", 5), now)
			return err
		},
		"zero cap": func() error {
			_, err := r.PutAccountSettings(ctx, "owner-a", 2, stepSettings("cap", 0, "cap", 5), now)
			return err
		},
		"zero time": func() error {
			_, err := r.PutAccountSettings(ctx, "owner-a", 2, stepSettings("cap", 5, "cap", 5), time.Time{})
			return err
		},
		"read of empty key": func() error { _, _, err := r.AccountSettings(ctx, ""); return err },
	} {
		if err := call(); !errors.Is(err, accountsettings.ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestAccountSettingsAreIsolatedPerOwner(t *testing.T) {
	ctx := context.Background()
	r := openAccountSettingsRepo(t)
	now := time.Now().UTC()
	if _, err := r.PutAccountSettings(ctx, "owner-a", 0, stepSettings("cap", 5, "cap", 6), now); err != nil {
		t.Fatal(err)
	}
	if _, found, err := r.AccountSettings(ctx, "owner-b"); err != nil || found {
		t.Fatalf("another owner reads a row: found=%v err=%v", found, err)
	}
	if _, err := r.PutAccountSettings(ctx, "owner-b", 0, stepSettings("unlimited", 7, "agent", 8), now); err != nil {
		t.Fatalf("another owner's first save conflicts with a stranger's row: %v", err)
	}
	a, _, _ := r.AccountSettings(ctx, "owner-a")
	b, _, _ := r.AccountSettings(ctx, "owner-b")
	if a.List.Cap != 5 || a.MCP.Cap != 6 || b.List.Mode != "unlimited" || b.MCP.Cap != 8 || a.Version != 1 || b.Version != 1 {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

// Two writers that read the same version cannot both win.
func TestAccountSettingsConcurrentWritersOfOneVersionHaveOneWinner(t *testing.T) {
	ctx := context.Background()
	r := openAccountSettingsRepo(t)
	now := time.Now().UTC()
	if _, err := r.PutAccountSettings(ctx, "owner-a", 0, stepSettings("agent", 20, "agent", 20), now); err != nil {
		t.Fatal(err)
	}
	const writers = 8
	results := make(chan error, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := r.PutAccountSettings(ctx, "owner-a", 1, stepSettings("cap", 10+index, "agent", 20), now)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, accountsettings.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected result: %v", err)
		}
	}
	if wins != 1 || conflicts != writers-1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
}
