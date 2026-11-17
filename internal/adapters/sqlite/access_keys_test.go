package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accesskeys"
)

func openAccessKeyRepo(t *testing.T) *Repository {
	t.Helper()
	r, err := openTestRepository(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func keyRecord(id, owner string, created time.Time, ttl time.Duration) accesskeys.Record {
	return accesskeys.Record{
		ID: id, OwnerID: owner, Name: "key " + id, Prefix: "plk_" + id, SecretHash: "hash-" + id,
		Scopes: []string{"read", "generate"}, CreatedAt: created, ExpiresAt: created.Add(ttl),
	}
}

func TestAccessKeyStoreRoundTripsEveryField(t *testing.T) {
	r := openAccessKeyRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 9, 30, 15, 123456789, time.UTC)
	record := keyRecord("abc", "owner-a", now, 90*24*time.Hour)
	if err := r.CreateAccessKey(ctx, record, 10, now); err != nil {
		t.Fatal(err)
	}
	got, found, err := r.AccessKey(ctx, "abc")
	if err != nil || !found {
		t.Fatalf("read = %+v found=%v err=%v", got, found, err)
	}
	if got.ID != "abc" || got.OwnerID != "owner-a" || got.Name != "key abc" || got.Prefix != "plk_abc" || got.SecretHash != "hash-abc" ||
		len(got.Scopes) != 2 || got.Scopes[0] != "read" || got.Scopes[1] != "generate" ||
		!got.CreatedAt.Equal(now) || !got.ExpiresAt.Equal(now.Add(90*24*time.Hour)) || !got.LastUsedAt.IsZero() || !got.RevokedAt.IsZero() {
		t.Fatalf("stored = %+v", got)
	}
	// Unset times are the empty string in the column, not a zero date.
	var lastUsed, revoked string
	if err := r.db.QueryRow(`SELECT last_used_at, revoked_at FROM access_keys WHERE id = 'abc'`).Scan(&lastUsed, &revoked); err != nil || lastUsed != "" || revoked != "" {
		t.Fatalf("unset columns = %q %q err=%v", lastUsed, revoked, err)
	}
	if _, found, err := r.AccessKey(ctx, "missing"); err != nil || found {
		t.Fatalf("missing key found=%v err=%v", found, err)
	}
	// The key id is the primary key.
	if err := r.CreateAccessKey(ctx, record, 10, now); err == nil {
		t.Fatal("a key id was stored twice")
	}
}

func TestAccessKeyStoreRefusesIncompleteRecords(t *testing.T) {
	r := openAccessKeyRepo(t)
	now := time.Now().UTC()
	good := keyRecord("abc", "owner-a", now, time.Hour)
	for name, mutate := range map[string]func(*accesskeys.Record){
		"no id":     func(k *accesskeys.Record) { k.ID = "" },
		"no owner":  func(k *accesskeys.Record) { k.OwnerID = "" },
		"no hash":   func(k *accesskeys.Record) { k.SecretHash = "" },
		"no scopes": func(k *accesskeys.Record) { k.Scopes = nil },
	} {
		record := good
		mutate(&record)
		if err := r.CreateAccessKey(context.Background(), record, 10, now); !errors.Is(err, accesskeys.ErrInvalid) {
			t.Errorf("%s: err = %v; want ErrInvalid", name, err)
		}
	}
	if err := r.CreateAccessKey(context.Background(), good, 0, now); !errors.Is(err, accesskeys.ErrInvalid) {
		t.Errorf("zero limit: %v", err)
	}
}

// Revoked and expired keys do not count against the limit, another owner's keys
// never do, and the expiry is compared as a time, not as RFC3339Nano text.
func TestAccessKeyStoreCountsOnlyTheOwnersActiveKeys(t *testing.T) {
	r := openAccessKeyRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	mustCreate := func(record accesskeys.Record) {
		t.Helper()
		if err := r.CreateAccessKey(ctx, record, 2, now); err != nil {
			t.Fatalf("create %s: %v", record.ID, err)
		}
	}
	// Two instants whose text order is the reverse of their time order: the
	// shorter fraction "…:00Z" sorts after "…:00.5Z" although it is earlier.
	mustCreate(keyRecord("a1", "owner-a", now, 90*24*time.Hour))
	expiring := keyRecord("a2", "owner-a", now.Add(-time.Hour), time.Hour+500*time.Millisecond)
	mustCreate(expiring)
	if err := r.CreateAccessKey(ctx, keyRecord("a3", "owner-a", now, time.Hour), 2, now); !errors.Is(err, accesskeys.ErrLimit) {
		t.Fatalf("third active key: %v", err)
	}
	mustCreate(keyRecord("b1", "owner-b", now, time.Hour))
	mustCreate(keyRecord("b2", "owner-b", now, time.Hour))
	// a2 expires at 09:00:00.5; at 09:00:01 it no longer counts.
	later := now.Add(time.Second)
	if err := r.CreateAccessKey(ctx, keyRecord("a3", "owner-a", later, time.Hour), 2, later); err != nil {
		t.Fatalf("an expired key still counted: %v", err)
	}
	if _, _, err := r.RevokeAccessKey(ctx, "owner-a", "a1", later); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateAccessKey(ctx, keyRecord("a4", "owner-a", later, time.Hour), 2, later); err != nil {
		t.Fatalf("a revoked key still counted: %v", err)
	}
}

// Two creations racing for the last place never both get it.
func TestAccessKeyStoreLimitHoldsUnderConcurrentCreation(t *testing.T) {
	r := openAccessKeyRepo(t)
	now := time.Now().UTC()
	var wg sync.WaitGroup
	var mu sync.Mutex
	created, limited := 0, 0
	for index := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := r.CreateAccessKey(context.Background(), keyRecord(fmt.Sprintf("k%02d", index), "owner-a", now, time.Hour), 4, now)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created++
			case errors.Is(err, accesskeys.ErrLimit):
				limited++
			default:
				t.Errorf("create: %v", err)
			}
		}()
	}
	wg.Wait()
	if created != 4 || limited != 8 {
		t.Fatalf("created %d limited %d; want 4 and 8 under a limit of 4", created, limited)
	}
}

func TestAccessKeyStoreListsNewestFirstAndOnlyTheOwners(t *testing.T) {
	r := openAccessKeyRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	for index, id := range []string{"oldest", "middle", "newest"} {
		if err := r.CreateAccessKey(ctx, keyRecord(id, "owner-a", base.Add(time.Duration(index)*time.Minute), time.Hour), 10, base); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.CreateAccessKey(ctx, keyRecord("foreign", "owner-b", base, time.Hour), 10, base); err != nil {
		t.Fatal(err)
	}
	records, err := r.ListAccessKeys(ctx, "owner-a")
	if err != nil || len(records) != 3 || records[0].ID != "newest" || records[1].ID != "middle" || records[2].ID != "oldest" {
		t.Fatalf("list = %+v err=%v", records, err)
	}
	if none, err := r.ListAccessKeys(ctx, "nobody"); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("empty list = %#v err=%v", none, err)
	}
}

func TestAccessKeyStoreRevokeIsOwnerScopedAndIdempotent(t *testing.T) {
	r := openAccessKeyRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	if err := r.CreateAccessKey(ctx, keyRecord("abc", "owner-a", now, time.Hour), 10, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RevokeAccessKey(ctx, "owner-b", "abc", now); !errors.Is(err, accesskeys.ErrNotFound) {
		t.Fatalf("a foreign revoke: %v", err)
	}
	if stored, _, _ := r.AccessKey(ctx, "abc"); !stored.RevokedAt.IsZero() {
		t.Fatal("a foreign revoke changed the key")
	}
	at := now.Add(time.Minute)
	record, changed, err := r.RevokeAccessKey(ctx, "owner-a", "abc", at)
	if err != nil || !changed || !record.RevokedAt.Equal(at) {
		t.Fatalf("revoke = %+v changed=%v err=%v", record, changed, err)
	}
	// A repeat keeps the first revocation time.
	record, changed, err = r.RevokeAccessKey(ctx, "owner-a", "abc", at.Add(time.Hour))
	if err != nil || changed || !record.RevokedAt.Equal(at) {
		t.Fatalf("repeat = %+v changed=%v err=%v", record, changed, err)
	}
	if _, _, err := r.RevokeAccessKey(ctx, "owner-a", "missing", at); !errors.Is(err, accesskeys.ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestAccessKeyStoreTouchRecordsTheLastUseOfALiveKeyOnly(t *testing.T) {
	r := openAccessKeyRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{"live", "dead"} {
		if err := r.CreateAccessKey(ctx, keyRecord(id, "owner-a", now, time.Hour), 10, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.RevokeAccessKey(ctx, "owner-a", "dead", now); err != nil {
		t.Fatal(err)
	}
	used := now.Add(5 * time.Minute)
	for _, id := range []string{"live", "dead", "missing"} {
		if err := r.TouchAccessKey(ctx, id, used); err != nil {
			t.Fatalf("touch %s: %v", id, err)
		}
	}
	live, _, _ := r.AccessKey(ctx, "live")
	dead, _, _ := r.AccessKey(ctx, "dead")
	if !live.LastUsedAt.Equal(used) || !dead.LastUsedAt.IsZero() {
		t.Fatalf("live last used %v, revoked last used %v", live.LastUsedAt, dead.LastUsedAt)
	}
}

// The service on the real store: the plaintext never reaches the database.
func TestAccessKeyServiceStoresOnlyTheDigest(t *testing.T) {
	r := openAccessKeyRepo(t)
	ctx := context.Background()
	service, err := accesskeys.New(r, accesskeys.Config{
		KeyPrefix: "plk", MaxKeysPerUser: 3, DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour, NameMaxLength: 20,
		Scopes: []string{"read"}, LastUsedInterval: time.Minute, Async: func(f func()) { f() },
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, accesskeys.CreateRequest{OwnerID: "owner-a", Name: "disk", Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	result, err := r.db.Query(`SELECT id || '|' || owner_id || '|' || name || '|' || prefix || '|' || secret_hash || '|' || scopes_json FROM access_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	for result.Next() {
		var row string
		if err := result.Scan(&row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	secret := created.Key[len(created.Record.Prefix)+1:]
	if len(rows) != 1 || len(created.Record.SecretHash) != 64 {
		t.Fatalf("rows = %v", rows)
	}
	for _, row := range rows {
		if strings.Contains(row, secret) || strings.Contains(row, created.Key) {
			t.Fatalf("the database row holds the key: %q", row)
		}
	}
	// And a real authentication through it updates the last use.
	if _, err := service.Authenticate(ctx, created.Key); err != nil {
		t.Fatal(err)
	}
	if stored, _, _ := r.AccessKey(ctx, created.Record.ID); stored.LastUsedAt.IsZero() {
		t.Fatal("authentication did not record the last use")
	}
}
