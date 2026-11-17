package accesskeys_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/authentication"
)

// memoryStore is the Store contract in memory: a cap counted over the owner's
// active keys, a revoke that reports whether it changed anything.
type memoryStore struct {
	mu      sync.Mutex
	rows    map[string]accesskeys.Record
	touches []string
	// fail makes every call return the error.
	fail error
	// touchFail makes only the last-use write fail.
	touchFail error
}

func newMemoryStore() *memoryStore { return &memoryStore{rows: map[string]accesskeys.Record{}} }

func (m *memoryStore) CreateAccessKey(_ context.Context, record accesskeys.Record, maxActive int, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	active := 0
	for _, row := range m.rows {
		if row.OwnerID == record.OwnerID && row.Active(now) {
			active++
		}
	}
	if active >= maxActive {
		return accesskeys.ErrLimit
	}
	m.rows[record.ID] = record
	return nil
}

func (m *memoryStore) AccessKey(_ context.Context, id string) (accesskeys.Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return accesskeys.Record{}, false, m.fail
	}
	record, found := m.rows[id]
	return record, found, nil
}

func (m *memoryStore) ListAccessKeys(_ context.Context, ownerID string) ([]accesskeys.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []accesskeys.Record
	for _, row := range m.rows {
		if row.OwnerID == ownerID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *memoryStore) RevokeAccessKey(_ context.Context, ownerID, id string, now time.Time) (accesskeys.Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, found := m.rows[id]
	if !found || row.OwnerID != ownerID {
		return accesskeys.Record{}, false, accesskeys.ErrNotFound
	}
	if !row.RevokedAt.IsZero() {
		return row, false, nil
	}
	row.RevokedAt = now
	m.rows[id] = row
	return row, true, nil
}

func (m *memoryStore) TouchAccessKey(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.touchFail != nil {
		return m.touchFail
	}
	m.touches = append(m.touches, id)
	row := m.rows[id]
	row.LastUsedAt = at
	m.rows[id] = row
	return nil
}

func (m *memoryStore) touchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.touches)
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newService(t *testing.T, store *memoryStore, clk *clock, logger *slog.Logger) *accesskeys.Service {
	t.Helper()
	service, err := accesskeys.New(store, accesskeys.Config{
		KeyPrefix: "plk", MaxKeysPerUser: 3, DefaultTTL: 90 * 24 * time.Hour, MaxTTL: 365 * 24 * time.Hour,
		NameMaxLength: 20, Scopes: []string{"read", "generate"}, LastUsedInterval: time.Minute,
		Clock: clk.Now, Logger: logger, Async: func(f func()) { f() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func fixedClock() *clock { return &clock{now: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)} }

var keyShape = regexp.MustCompile(`^plk_[a-z2-7]{16}_[a-z2-7]{52}$`)

func mustCreate(t *testing.T, service *accesskeys.Service, owner, name string, scopes ...string) accesskeys.Created {
	t.Helper()
	created, err := service.Create(context.Background(), accesskeys.CreateRequest{OwnerID: owner, Name: name, Scopes: scopes})
	if err != nil {
		t.Fatalf("create %q: %v", name, err)
	}
	return created
}

// The plaintext key is returned once and only its digest is kept. The record
// carries the visible prefix, the scopes and the default lifetime.
func TestCreateReturnsThePlaintextOnceAndStoresOnlyItsDigest(t *testing.T) {
	t.Parallel()
	store, clk := newMemoryStore(), fixedClock()
	service := newService(t, store, clk, nil)
	created := mustCreate(t, service, "owner-a", "  build bot  ", "read", "generate", "read")
	if !keyShape.MatchString(created.Key) {
		t.Fatalf("key = %q; want plk_<16>_<52> lowercase base32", created.Key)
	}
	record := created.Record
	digest := sha256.Sum256([]byte(created.Key))
	if record.SecretHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("stored hash %q is not the SHA-256 of the whole key", record.SecretHash)
	}
	if record.Name != "build bot" || len(record.Scopes) != 2 || record.Scopes[0] != "read" || record.Scopes[1] != "generate" {
		t.Fatalf("name/scopes = %q %v", record.Name, record.Scopes)
	}
	if want := "plk_" + record.ID; record.Prefix != want || !strings.HasPrefix(created.Key, record.Prefix+"_") {
		t.Fatalf("prefix = %q id = %q", record.Prefix, record.ID)
	}
	if !record.ExpiresAt.Equal(clk.now.Add(90*24*time.Hour)) || !record.CreatedAt.Equal(clk.now) || !record.RevokedAt.IsZero() {
		t.Fatalf("dates = %+v", record)
	}
	// Nothing the service hands back or keeps holds the plaintext or its secret.
	secret := created.Key[len(record.Prefix)+1:]
	for name, value := range map[string]string{
		"record":  fmt.Sprintf("%+v", record),
		"stored":  fmt.Sprintf("%+v", store.rows),
		"listing": fmt.Sprintf("%+v", mustList(t, service, "owner-a")),
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("%s contains the key secret", name)
		}
	}
	if listed := mustList(t, service, "owner-a"); len(listed) != 1 || listed[0].SecretHash != "" {
		t.Fatalf("listing exposes the digest: %+v", listed)
	}
	// Two keys never share a plaintext.
	if other := mustCreate(t, service, "owner-a", "other", "read"); other.Key == created.Key || other.Record.ID == record.ID {
		t.Fatal("two keys are the same")
	}
}

func mustList(t *testing.T, service *accesskeys.Service, owner string) []accesskeys.Record {
	t.Helper()
	records, err := service.List(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestCreateValidatesNameScopesAndLifetime(t *testing.T) {
	t.Parallel()
	service := newService(t, newMemoryStore(), fixedClock(), nil)
	day := 24 * time.Hour
	for name, request := range map[string]accesskeys.CreateRequest{
		"no owner":         {Name: "k", Scopes: []string{"read"}},
		"empty name":       {OwnerID: "o", Name: "   ", Scopes: []string{"read"}},
		"name too long":    {OwnerID: "o", Name: strings.Repeat("长", 21), Scopes: []string{"read"}},
		"control in name":  {OwnerID: "o", Name: "a\nb", Scopes: []string{"read"}},
		"no scopes":        {OwnerID: "o", Name: "k"},
		"unknown scope":    {OwnerID: "o", Name: "k", Scopes: []string{"read", "admin"}},
		"negative ttl":     {OwnerID: "o", Name: "k", Scopes: []string{"read"}, TTL: -day},
		"ttl over maximum": {OwnerID: "o", Name: "k", Scopes: []string{"read"}, TTL: 366 * day},
	} {
		if _, err := service.Create(context.Background(), request); !errors.Is(err, accesskeys.ErrInvalid) {
			t.Errorf("%s: err = %v; want ErrInvalid", name, err)
		}
	}
	// The name bound counts characters, not bytes; the maximum lifetime is allowed.
	if _, err := service.Create(context.Background(), accesskeys.CreateRequest{
		OwnerID: "o", Name: strings.Repeat("长", 20), Scopes: []string{"generate"}, TTL: 365 * day,
	}); err != nil {
		t.Fatalf("a name of 20 characters and the maximum lifetime: %v", err)
	}
}

// At most MaxKeysPerUser keys are active per person; a revoked or an expired
// key gives its place back; another person's keys never count.
func TestCreateEnforcesTheActiveKeyLimitPerPerson(t *testing.T) {
	t.Parallel()
	store, clk := newMemoryStore(), fixedClock()
	service := newService(t, store, clk, nil)
	first := mustCreate(t, service, "owner-a", "one", "read")
	short, err := service.Create(context.Background(), accesskeys.CreateRequest{
		OwnerID: "owner-a", Name: "short", Scopes: []string{"read"}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, service, "owner-a", "three", "read")
	if _, err := service.Create(context.Background(), accesskeys.CreateRequest{OwnerID: "owner-a", Name: "four", Scopes: []string{"read"}}); !errors.Is(err, accesskeys.ErrLimit) {
		t.Fatalf("fourth key: %v; want ErrLimit", err)
	}
	mustCreate(t, service, "owner-b", "elsewhere", "read")
	if _, _, err := service.Revoke(context.Background(), "owner-a", first.Record.ID); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, service, "owner-a", "after revoke", "read")
	if _, err := service.Create(context.Background(), accesskeys.CreateRequest{OwnerID: "owner-a", Name: "five", Scopes: []string{"read"}}); !errors.Is(err, accesskeys.ErrLimit) {
		t.Fatalf("limit after the place was taken again: %v", err)
	}
	clk.now = clk.now.Add(2 * time.Hour)
	if short.Record.Active(clk.now) {
		t.Fatal("the one-hour key is still active")
	}
	mustCreate(t, service, "owner-a", "after expiry", "read")
}

func TestRevokeIsIdempotentAndOwnerScoped(t *testing.T) {
	t.Parallel()
	service := newService(t, newMemoryStore(), fixedClock(), nil)
	created := mustCreate(t, service, "owner-a", "k", "read")
	if _, _, err := service.Revoke(context.Background(), "owner-b", created.Record.ID); !errors.Is(err, accesskeys.ErrNotFound) {
		t.Fatalf("another person revoked the key: %v", err)
	}
	record, changed, err := service.Revoke(context.Background(), "owner-a", created.Record.ID)
	if err != nil || !changed || record.RevokedAt.IsZero() || record.SecretHash != "" {
		t.Fatalf("revoke = %+v changed=%v err=%v", record, changed, err)
	}
	if _, changed, err = service.Revoke(context.Background(), "owner-a", created.Record.ID); err != nil || changed {
		t.Fatalf("second revoke changed=%v err=%v; a repeat is not a change", changed, err)
	}
	if _, _, err := service.Revoke(context.Background(), "owner-a", "no-such-key"); !errors.Is(err, accesskeys.ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
}

// Every way a key can fail to authenticate is one and the same error, so a
// probe learns nothing about which keys exist or why one is refused.
func TestAuthenticateRefusesEveryBadKeyWithTheSameError(t *testing.T) {
	t.Parallel()
	store, clk := newMemoryStore(), fixedClock()
	service := newService(t, store, clk, nil)
	good := mustCreate(t, service, "owner-a", "good", "read", "generate")
	revoked := mustCreate(t, service, "owner-a", "revoked", "read")
	if _, _, err := service.Revoke(context.Background(), "owner-a", revoked.Record.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := service.Create(context.Background(), accesskeys.CreateRequest{
		OwnerID: "owner-a", Name: "expired", Scopes: []string{"read"}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongSecret := good.Record.Prefix + "_" + strings.Repeat("a", 52)
	unknownID := "plk_" + strings.Repeat("a", 16) + "_" + strings.Repeat("a", 52)
	cases := map[string]string{
		"empty":            "",
		"other scheme":     "Bearer " + good.Key,
		"wrong prefix":     strings.Replace(good.Key, "plk_", "abc_", 1),
		"no secret":        good.Record.Prefix,
		"short secret":     good.Record.Prefix + "_abc",
		"uppercase":        strings.ToUpper(good.Key),
		"wrong secret":     wrongSecret,
		"unknown id":       unknownID,
		"revoked":          revoked.Key,
		"oversized":        good.Key + strings.Repeat("a", 300),
		"extra segment":    good.Key + "_extra",
		"trailing newline": good.Key + "\n",
	}
	clk.now = clk.now.Add(2 * time.Hour)
	cases["expired"] = expired.Key
	for name, presented := range cases {
		_, err := service.Authenticate(context.Background(), presented)
		if err != accesskeys.ErrUnauthorized {
			t.Errorf("%s: err = %v; want exactly ErrUnauthorized", name, err)
		}
	}
	got, err := service.Authenticate(context.Background(), good.Key)
	if err != nil || got.OwnerID != "owner-a" || got.KeyID != good.Record.ID || len(got.Scopes) != 2 {
		t.Fatalf("good key = %+v err=%v", got, err)
	}
	if store.touchCount() != 1 {
		t.Fatalf("a refused key recorded a use: touches = %v", store.touches)
	}
}

// A store failure is not a refusal: it is reported as a lookup failure, never
// as ErrUnauthorized, so an outage is not taken for a bad key.
func TestAuthenticateReportsALookupFailureApartFromARefusal(t *testing.T) {
	t.Parallel()
	store, clk := newMemoryStore(), fixedClock()
	service := newService(t, store, clk, nil)
	created := mustCreate(t, service, "owner-a", "k", "read")
	store.fail = errors.New("disk on fire")
	if _, err := service.Authenticate(context.Background(), created.Key); err == nil || errors.Is(err, accesskeys.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

// The last use is written at most once per interval per key, after the fact:
// a failed write neither fails the authentication nor logs the key.
func TestAuthenticateRecordsTheLastUseThrottledAndNeverFails(t *testing.T) {
	t.Parallel()
	store, clk := newMemoryStore(), fixedClock()
	var logs bytes.Buffer
	service := newService(t, store, clk, slog.New(slog.NewTextHandler(&logs, nil)))
	created := mustCreate(t, service, "owner-a", "k", "read")
	authenticate := func() {
		t.Helper()
		if _, err := service.Authenticate(context.Background(), created.Key); err != nil {
			t.Fatal(err)
		}
	}
	authenticate()
	clk.now = clk.now.Add(30 * time.Second)
	authenticate()
	if store.touchCount() != 1 {
		t.Fatalf("touches within the interval = %d; want 1", store.touchCount())
	}
	clk.now = clk.now.Add(31 * time.Second)
	authenticate()
	if store.touchCount() != 2 || !store.rows[created.Record.ID].LastUsedAt.Equal(clk.now) {
		t.Fatalf("touches after the interval = %d last=%v", store.touchCount(), store.rows[created.Record.ID].LastUsedAt)
	}
	// A failing write is logged without the key and does not fail anything.
	store.touchFail = errors.New("database is locked")
	clk.now = clk.now.Add(2 * time.Minute)
	authenticate()
	if !strings.Contains(logs.String(), "last use not recorded") || !strings.Contains(logs.String(), created.Record.ID) {
		t.Fatalf("log = %q", logs.String())
	}
	if strings.Contains(logs.String(), created.Key) || strings.Contains(logs.String(), created.Key[len(created.Record.Prefix)+1:]) {
		t.Fatal("the log contains the key")
	}
}

func testPolicy(t *testing.T) *access.Policy {
	t.Helper()
	policy, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: "user", AdminRole: "admin",
		Roles: []access.RoleDefinition{
			{Key: "user", Permissions: []access.Permission{access.PermissionTasksReadOwn}},
			{Key: "admin", Permissions: []access.Permission{access.PermissionTasksReadOwn, access.PermissionTasksReadAny}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

type directory map[string]authentication.UserRecord

func (d directory) FindByID(_ context.Context, id string) (authentication.UserRecord, bool, error) {
	record, found := d[id]
	return record, found, nil
}

func bearer(token string) *http.Request {
	request, _ := http.NewRequest(http.MethodGet, "http://mcp.test/", nil)
	if token != "" {
		request.Header.Set("Authorization", token)
	}
	return request
}

// The authenticator turns a key into its owner's scope and nothing more: an
// administrator's key has no administrator permissions, and the principal is
// always the person who holds the key.
func TestAuthenticatorResolvesAKeyToTheOwnersScopeOnly(t *testing.T) {
	t.Parallel()
	store, clk := newMemoryStore(), fixedClock()
	service := newService(t, store, clk, nil)
	users := directory{
		"admin-a": {ID: "admin-a", Username: "root", Nickname: "Root", Email: "root@example.test", Roles: []string{"admin"}, Admin: true, Active: true},
		"owner-b": {ID: "owner-b", Username: "bee", Name: "Bee", Email: "bee@example.test", Active: true},
	}
	authenticator := &accesskeys.Authenticator{Keys: service, Users: users, Policy: testPolicy(t)}
	adminKey := mustCreate(t, service, "admin-a", "admin key", "read", "generate")
	beeKey := mustCreate(t, service, "owner-b", "bee key", "read")

	principal, err := authenticator.Identify(bearer("Bearer " + adminKey.Key))
	if err != nil {
		t.Fatal(err)
	}
	credential := principal.Credential()
	if principal.ID() != "admin-a" || principal.Claims.Name != "Root" ||
		credential.Kind != access.CredentialAccessKey || credential.KeyID != adminKey.Record.ID ||
		!credential.HasScope("read") || !credential.HasScope("generate") || credential.HasScope("admin") {
		t.Fatalf("principal = %+v credential = %+v", principal, credential)
	}
	if principal.IsAdmin() || principal.Can(access.PermissionTasksReadAny) || !principal.Can(access.PermissionTasksReadOwn) {
		t.Fatal("an administrator's access key carries administrator permissions")
	}
	for _, role := range principal.Claims.Roles {
		if role == "admin" {
			t.Fatalf("roles = %v; the administrator role travelled with the key", principal.Claims.Roles)
		}
	}
	// Another person's key is that person: no key reaches across owners.
	bee, err := authenticator.Identify(bearer("Bearer " + beeKey.Key))
	if err != nil || bee.ID() != "owner-b" || bee.Credential().HasScope("generate") {
		t.Fatalf("bee = %+v err=%v", bee, err)
	}
	// The credential returns copies: changing them does not change the principal.
	credential.Scopes[0] = "mutated"
	if principal.Credential().Scopes[0] != "read" {
		t.Fatal("Credential() shares its scopes with the principal")
	}
}

// A key that may generate may also read: the scopes the identity carries are
// the effective ones, while the key itself keeps what it was issued with.
func TestEffectiveScopesAddWhatTheIssuedOnesInclude(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct{ issued, want []string }{
		"read stays read":             {[]string{"read"}, []string{"read"}},
		"generate includes read":      {[]string{"generate"}, []string{"generate", "read"}},
		"both keep their order":       {[]string{"generate", "read"}, []string{"generate", "read"}},
		"read then generate":          {[]string{"read", "generate"}, []string{"read", "generate"}},
		"an unknown scope is carried": {[]string{"later"}, []string{"later"}},
		"nothing":                     {nil, nil},
	} {
		input := append([]string(nil), test.issued...)
		got := accesskeys.EffectiveScopes(input)
		if strings.Join(got, ",") != strings.Join(test.want, ",") {
			t.Errorf("%s: EffectiveScopes(%v) = %v, want %v", name, test.issued, got, test.want)
		}
		if strings.Join(input, ",") != strings.Join(test.issued, ",") {
			t.Errorf("%s: the issued scopes were changed: %v", name, input)
		}
	}
}

func TestAuthenticatorGivesAGenerateKeyTheReadScopeToo(t *testing.T) {
	t.Parallel()
	store, clk := newMemoryStore(), fixedClock()
	service := newService(t, store, clk, nil)
	users := directory{"owner-b": {ID: "owner-b", Username: "bee", Name: "Bee", Active: true}}
	authenticator := &accesskeys.Authenticator{Keys: service, Users: users, Policy: testPolicy(t)}
	generateOnly := mustCreate(t, service, "owner-b", "generate only", "generate")
	readOnly := mustCreate(t, service, "owner-b", "read only", "read")

	principal, err := authenticator.Identify(bearer("Bearer " + generateOnly.Key))
	if err != nil {
		t.Fatal(err)
	}
	if credential := principal.Credential(); !credential.HasScope("generate") || !credential.HasScope("read") || credential.HasScope("admin") {
		t.Fatalf("a generate key carries %v", credential.Scopes)
	}
	// The key itself is stored and listed as it was issued.
	if listed := mustList(t, service, "owner-b"); len(listed) != 2 {
		t.Fatalf("listed = %+v", listed)
	} else {
		for _, record := range listed {
			if record.ID == generateOnly.Record.ID && strings.Join(record.Scopes, ",") != "generate" {
				t.Fatalf("the stored scopes of the generate key = %v", record.Scopes)
			}
		}
	}
	// A read key does not gain the right to generate.
	reader, err := authenticator.Identify(bearer("Bearer " + readOnly.Key))
	if err != nil || reader.Credential().HasScope("generate") || !reader.Credential().HasScope("read") {
		t.Fatalf("a read key carries %v err=%v", reader.Credential().Scopes, err)
	}
}

func TestAuthenticatorRefusesWhatIsNotAUsableKey(t *testing.T) {
	t.Parallel()
	store, clk := newMemoryStore(), fixedClock()
	service := newService(t, store, clk, nil)
	users := directory{
		"owner-a":  {ID: "owner-a", Username: "a", Active: true},
		"gone":     {ID: "gone", Username: "g", Active: true, Deleted: true},
		"disabled": {ID: "disabled", Username: "d", Active: false},
	}
	authenticator := &accesskeys.Authenticator{Keys: service, Users: users, Policy: testPolicy(t)}
	good := mustCreate(t, service, "owner-a", "k", "read")
	deleted := mustCreate(t, service, "gone", "k", "read")
	disabled := mustCreate(t, service, "disabled", "k", "read")
	orphan := mustCreate(t, service, "nobody", "k", "read")

	for name, header := range map[string]string{
		"no header":          "",
		"basic":              "Basic " + good.Key,
		"another token":      "Bearer some-deployment-token-of-24-chars",
		"key without scheme": good.Key,
	} {
		if _, err := authenticator.Identify(bearer(header)); !errors.Is(err, accesskeys.ErrNotAKey) {
			t.Errorf("%s: err = %v; want ErrNotAKey", name, err)
		}
	}
	for name, token := range map[string]string{
		"wrong secret":     good.Record.Prefix + "_" + strings.Repeat("b", 52),
		"deleted owner":    deleted.Key,
		"disabled owner":   disabled.Key,
		"owner not in dir": orphan.Key,
	} {
		if _, err := authenticator.Identify(bearer("Bearer " + token)); err != accesskeys.ErrUnauthorized {
			t.Errorf("%s: err = %v; want exactly ErrUnauthorized", name, err)
		}
	}
	// A revoked key stops authenticating at once.
	if _, err := authenticator.Identify(bearer("Bearer " + good.Key)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Revoke(context.Background(), "owner-a", good.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.Identify(bearer("Bearer " + good.Key)); err != accesskeys.ErrUnauthorized {
		t.Fatalf("revoked key: %v", err)
	}
	var unconfigured *accesskeys.Authenticator
	if _, err := unconfigured.Identify(bearer("Bearer " + good.Key)); err == nil {
		t.Fatal("an unconfigured authenticator accepted a key")
	}
}
