package sqlite

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/harness"
)

func TestHarnessSelectionDurableCASAndVersions(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	zero, err := r.GetHarnessSelection(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if zero.Revision != 0 || zero.Digest == "" {
		t.Fatalf("default %+v", zero)
	}
	selected := zero
	selected.OperationID = "model.generate"
	selected.OfferingID = "provider/model/action"
	one, err := r.CompareAndSwapHarnessSelection(ctx, selected, 0)
	if err != nil {
		t.Fatal(err)
	}
	if one.Revision != 1 || one.Digest == zero.Digest {
		t.Fatalf("CAS %+v", one)
	}
	var seq int
	var name, path string
	if err := r.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	loaded, err := other.GetHarnessSelection(ctx, "owner-a", "session-a")
	if err != nil || loaded.Binding != one.Binding {
		t.Fatalf("refresh %+v %v", loaded, err)
	}
	if _, err := other.CompareAndSwapHarnessSelection(ctx, selected, 0); !errors.Is(err, harness.ErrConflict) {
		t.Fatalf("stale CAS %v", err)
	}
	// #327: saving what is already saved (清空选择 with nothing selected, a restore of the same
	// content) keeps the revision; only a change makes a new one, and A→B→A still does.
	same, err := other.CompareAndSwapHarnessSelection(ctx, selected, 1)
	if err != nil || same.Binding != one.Binding {
		t.Fatalf("same content must keep the revision %+v %v", same, err)
	}
	changed := selected
	changed.OfferingID = "provider/model/other"
	if _, err := other.CompareAndSwapHarnessSelection(ctx, changed, 1); err != nil {
		t.Fatal(err)
	}
	two, err := other.CompareAndSwapHarnessSelection(ctx, selected, 2)
	if err != nil || two.Revision != 3 || two.Digest != one.Digest {
		t.Fatalf("A→B→A must still be a new revision %+v %v", two, err)
	}
	for _, want := range []harness.Selection{zero, one, two} {
		got, err := r.GetHarnessSelectionVersion(ctx, "owner-a", "session-a", want.Binding)
		if err != nil || got.Binding != want.Binding {
			t.Fatalf("version %+v %v", got, err)
		}
	}
	for _, pair := range [][2]string{{"owner-b", "session-a"}, {"owner-a", "missing"}} {
		if _, err := r.GetHarnessSelection(ctx, pair[0], pair[1]); !errors.Is(err, harness.ErrNotFound) {
			t.Fatalf("owner isolation %v", err)
		}
	}
	if _, err := r.CompareAndSwapHarnessSelection(ctx, selected, math.MaxInt64); !errors.Is(err, harness.ErrInvalid) {
		t.Fatal("overflow", err)
	}
}

func TestHarnessSelectionIndependentConnectionsOneCASWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES('s','o','','','','')`); err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, r := range []*Repository{a, b} {
		wg.Add(1)
		go func(r *Repository) {
			defer wg.Done()
			<-start
			s := harness.DefaultSelection("o", "s")
			s.OperationID = "generate"
			_, err := r.CompareAndSwapHarnessSelection(context.Background(), s, 0)
			results <- err
		}(r)
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, harness.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins %d conflicts %d", wins, conflicts)
	}
}

func TestHarnessSelectionDatabaseFailureClosed(t *testing.T) {
	r := openGrantTestRepo(t)
	ctx := context.Background()
	if _, err := r.db.Exec(`DROP TABLE harness_selections`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetHarnessSelection(ctx, "owner-a", "session-a"); err == nil {
		t.Fatal("read fell back to default")
	}
	if _, err := r.CompareAndSwapHarnessSelection(ctx, harness.DefaultSelection("owner-a", "session-a"), 0); err == nil {
		t.Fatal("CAS fell back")
	}
	if _, err := r.GetHarnessSelectionVersion(ctx, "owner-a", "session-a", harness.DefaultSelection("owner-a", "session-a").Binding); err == nil {
		t.Fatal("version read fell back")
	}
}

func TestHarnessSelectionOwnershipAndImmutableContent(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	one := changeHarness(t, r)
	for _, pair := range [][2]string{{"owner-b", "session-a"}, {"owner-a", "missing"}} {
		candidate := one
		candidate.OwnerID = pair[0]
		candidate.SessionID = pair[1]
		if _, err := r.CompareAndSwapHarnessSelection(ctx, candidate, 0); !errors.Is(err, harness.ErrNotFound) {
			t.Fatalf("CAS owner/session isolation %v", err)
		}
		if _, err := r.GetHarnessSelectionVersion(ctx, pair[0], pair[1], one.Binding); !errors.Is(err, harness.ErrNotFound) {
			t.Fatalf("version owner/session isolation %v", err)
		}
	}
	bad := one.Binding
	bad.Digest = harness.DefaultSelection("owner-a", "session-a").Digest
	if _, err := r.GetHarnessSelectionVersion(ctx, "owner-a", "session-a", bad); !errors.Is(err, harness.ErrNotFound) {
		t.Fatalf("wrong version digest accepted %v", err)
	}
	if _, err := r.db.Exec(`UPDATE harness_selection_versions SET selection_json='{}' WHERE owner_id='owner-a'`); err == nil {
		t.Fatal("immutable versions changed")
	}
	if _, err := r.db.Exec(`UPDATE harness_selections SET selection_json='{}' WHERE owner_id='owner-a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetHarnessSelection(ctx, "owner-a", "session-a"); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("corrupt current selection accepted %v", err)
	}
}
