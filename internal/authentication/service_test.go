package authentication_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const testPasswordHash = "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2."

type memoryDirectory struct {
	byUsername map[string]authentication.UserRecord
	byID       map[string]authentication.UserRecord
	lastLogin  time.Time
}

type bootstrapDirectory struct {
	memoryDirectory
	created int
	updated int
}

func (m *memoryDirectory) FindActiveByUsername(
	_ context.Context,
	username string,
) (authentication.UserRecord, bool, error) {
	record, exists := m.byUsername[username]
	if !exists || !record.Active || record.Deleted {
		return authentication.UserRecord{}, false, nil
	}
	return record, true, nil
}

func (m *memoryDirectory) FindByID(
	_ context.Context,
	id string,
) (authentication.UserRecord, bool, error) {
	record, exists := m.byID[id]
	return record, exists, nil
}

func (m *memoryDirectory) UpdateLastLogin(
	_ context.Context,
	_ string,
	at time.Time,
) error {
	m.lastLogin = at
	return nil
}

func (b *bootstrapDirectory) FindByUsername(
	_ context.Context,
	username string,
) (authentication.UserRecord, bool, error) {
	record, exists := b.byUsername[username]
	return record, exists, nil
}

func (b *bootstrapDirectory) CreateBootstrapAdmin(
	_ context.Context,
	record authentication.UserRecord,
	_ time.Time,
) error {
	b.created++
	b.byUsername[record.Username] = record
	b.byID[record.ID] = record
	return nil
}

func (b *bootstrapDirectory) UpdatePassword(
	_ context.Context,
	id string,
	passwordHash string,
	_ time.Time,
) error {
	b.updated++
	record := b.byID[id]
	record.PasswordHash = passwordHash
	b.byID[id] = record
	b.byUsername[record.Username] = record
	return nil
}

func TestSessionCookieIsVersionedAndSchemaDriven(t *testing.T) {
	schema := authenticationSchema(t)
	schema.Cookie.Name = "schema_session"
	schema.Cookie.TTLSeconds = 90
	schema.Cookie.SameSite = "strict"
	schema.Cookie.SecureMode = "never"
	now := time.Date(2026, 7, 26, 15, 4, 5, 0, time.UTC)
	secret := strings.Repeat("s", 32)
	service := newService(t, authentication.Config{
		Schema:        schema,
		SessionSecret: secret,
		Environment:   "test",
		Policy:        testPolicy(t),
		Directory:     &memoryDirectory{byID: map[string]authentication.UserRecord{"user-1": {ID: "user-1", Name: "Schema User", Email: "schema@example.test", Roles: []string{"admin"}, Active: true}}},
		Clock:         func() time.Time { return now },
	})
	user := authentication.User{
		ID:    "user-1",
		Name:  "Schema User",
		Email: "schema@example.test",
		Roles: []string{"admin"},
	}

	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/schema-login", nil)
	if err := service.IssueSession(writer, request, user); err != nil {
		t.Fatal(err)
	}
	response := writer.Result()
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != "schema_session" ||
		cookie.Path != "/" ||
		cookie.MaxAge != 90 ||
		cookie.HttpOnly != schema.Cookie.HTTPOnly ||
		cookie.SameSite != http.SameSiteStrictMode ||
		cookie.Secure {
		t.Fatalf("cookie = %#v", cookie)
	}

	decodedValue, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	lastDot := strings.LastIndexByte(decodedValue, '.')
	if lastDot < 0 {
		t.Fatalf("signed cookie = %q", cookie.Value)
	}
	payload := decodedValue[:lastDot]
	signature := decodedValue[lastDot+1:]
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		t.Fatalf("signature = %q, expected %q", signature, expected)
	}
	var claims struct {
		Version   int                 `json:"version"`
		SessionID string              `json:"session_id"`
		IssuedAt  int64               `json:"issued_at"`
		ExpiresAt int64               `json:"expires_at"`
		User      authentication.User `json:"user"`
	}
	if err := json.Unmarshal([]byte(payload), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Version != 2 || claims.SessionID == "" || claims.IssuedAt != now.Unix() || claims.ExpiresAt != now.Add(90*time.Second).Unix() || claims.User.ID != user.ID {
		t.Fatalf("invalid session claims: %+v", claims)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/schema-session", nil)
	sessionRequest.AddCookie(cookie)
	resolved, exists, err := service.CurrentUser(
		context.Background(),
		sessionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || resolved.ID != user.ID || resolved.Roles[0] != "admin" {
		t.Fatalf("resolved user = %#v, exists = %v", resolved, exists)
	}

	clearWriter := httptest.NewRecorder()
	service.ClearSession(clearWriter)
	cleared := clearWriter.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != "schema_session" ||
		cleared[0].MaxAge >= 0 {
		t.Fatalf("cleared cookie = %#v", cleared)
	}
}

func TestMockUserIsSchemaDrivenAndFallsBackAfterCookie(t *testing.T) {
	schema := authenticationSchema(t)
	schema.API.MockIDPrefix = "schema_mock_"
	now := time.UnixMilli(1785056645123)
	service := newService(t, authentication.Config{
		Schema:        schema,
		SessionSecret: strings.Repeat("m", 32),
		Environment:   "test",
		MockEnabled:   true,
		MockUserJSON: `{
			"id":"configured-user",
			"name":"Configured",
			"email":"configured@example.test"
		}`,
		Policy:    testPolicy(t),
		Directory: &memoryDirectory{},
		Clock:     func() time.Time { return now },
	})

	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := service.IssueSession(writer, request, authentication.User{
		ID: "cookie-user", Name: "Cookie", Email: "cookie@example.test",
	}); err != nil {
		t.Fatal(err)
	}
	currentRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	currentRequest.AddCookie(writer.Result().Cookies()[0])
	current, exists, err := service.CurrentUser(context.Background(), currentRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || current.ID != "cookie-user" {
		t.Fatalf("current user = %#v, exists = %v", current, exists)
	}

	fallback, exists, err := service.CurrentUser(
		context.Background(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || fallback.ID != "configured-user" {
		t.Fatalf(
			"mock fallback = %#v, exists = %v",
			fallback,
			exists,
		)
	}

	supplied, err := service.MockUser("Supplied", "supplied@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if supplied.ID != "schema_mock_1785056645123" ||
		supplied.Name != "Supplied" ||
		supplied.Email != "supplied@example.test" {
		t.Fatalf("supplied mock user = %#v", supplied)
	}
}

func TestPasswordAuthenticationUsesDirectoryAndBcrypt(t *testing.T) {
	now := time.Date(2026, 7, 26, 16, 0, 0, 0, time.UTC)
	record := authentication.UserRecord{
		ID:           "db-user",
		Username:     "schema-admin",
		PasswordHash: testPasswordHash,
		Nickname:     "昵称",
		Name:         "名称",
		Email:        "db@example.test",
		Roles:        []string{"admin"},
		Admin:        true,
		Active:       true,
	}
	directory := &memoryDirectory{
		byUsername: map[string]authentication.UserRecord{
			record.Username: record,
		},
		byID: map[string]authentication.UserRecord{record.ID: record},
	}
	service := newService(t, authentication.Config{
		Schema:        authenticationSchema(t),
		SessionSecret: strings.Repeat("p", 32),
		Environment:   "test",
		Policy:        testPolicy(t),
		Directory:     directory,
		Clock:         func() time.Time { return now },
	})

	user, err := service.AuthenticatePassword(
		context.Background(),
		"schema-admin",
		"correct-horse",
	)
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "db-user" || user.Name != "昵称" ||
		user.Email != "db@example.test" ||
		len(user.Roles) != 1 || user.Roles[0] != "admin" {
		t.Fatalf("session user = %#v", user)
	}
	if !directory.lastLogin.Equal(now) {
		t.Fatalf("last login = %s", directory.lastLogin)
	}

	_, err = service.AuthenticatePassword(
		context.Background(),
		"schema-admin",
		"wrong-password",
	)
	if !errors.Is(err, authentication.ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
}

func TestCurrentUserRejectsDirectoryUserDisabledAfterSessionIssue(
	t *testing.T,
) {
	record := authentication.UserRecord{
		ID:       "revoked-user",
		Username: "revoked",
		Name:     "Revoked User",
		Email:    "revoked@example.test",
		Roles:    []string{"user"},
		Active:   true,
	}
	directory := &memoryDirectory{
		byUsername: map[string]authentication.UserRecord{
			record.Username: record,
		},
		byID: map[string]authentication.UserRecord{record.ID: record},
	}
	service := newService(t, authentication.Config{
		Schema:        authenticationSchema(t),
		SessionSecret: strings.Repeat("r", 32),
		Environment:   "test",
		Policy:        testPolicy(t),
		Directory:     directory,
	})
	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	if err := service.IssueSession(writer, request, authentication.User{
		ID:    record.ID,
		Name:  record.Name,
		Email: record.Email,
		Roles: record.Roles,
	}); err != nil {
		t.Fatal(err)
	}
	cookie := writer.Result().Cookies()[0]
	record.Active = false
	directory.byID[record.ID] = record

	sessionRequest := httptest.NewRequest(http.MethodGet, "/session", nil)
	sessionRequest.AddCookie(cookie)
	user, exists, err := service.CurrentUser(
		context.Background(),
		sessionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if exists || user.ID != "" {
		t.Fatalf("revoked user=%#v exists=%v", user, exists)
	}
}

func TestBootstrapAdminCreatesOnceAndRotatesOnlyOnReset(
	t *testing.T,
) {
	directory := &bootstrapDirectory{memoryDirectory: memoryDirectory{
		byUsername: map[string]authentication.UserRecord{},
		byID:       map[string]authentication.UserRecord{},
	}}
	schema := authenticationSchema(t)
	schema.API.PasswordHashCost = 4
	service := newService(t, authentication.Config{
		Schema:        schema,
		SessionSecret: strings.Repeat("b", 32),
		Environment:   "test",
		Policy:        testPolicy(t),
		Directory:     directory,
		Clock: func() time.Time {
			return time.Date(2026, 7, 26, 16, 30, 0, 0, time.UTC)
		},
	})
	bootstrap := authentication.BootstrapAdmin{
		Username: "schema-bootstrap",
		Password: "first-password",
		Name:     "Schema Bootstrap",
		Email:    "bootstrap@example.test",
		Role:     "admin",
	}
	if err := service.BootstrapAdmin(context.Background(), bootstrap); err != nil {
		t.Fatal(err)
	}
	if directory.created != 1 || directory.updated != 0 {
		t.Fatalf(
			"created = %d, updated = %d",
			directory.created,
			directory.updated,
		)
	}
	first := directory.byUsername[bootstrap.Username]
	if cost, err := bcrypt.Cost([]byte(first.PasswordHash)); err != nil ||
		cost != schema.API.PasswordHashCost {
		t.Fatalf("bcrypt cost = %d, error = %v", cost, err)
	}

	// #256: an existing administrator keeps the password it has (changed in the
	// product, say) unless the restart explicitly asks for a reset.
	bootstrap.Password = "rotated-password"
	if err := service.BootstrapAdmin(context.Background(), bootstrap); err != nil {
		t.Fatal(err)
	}
	if directory.created != 1 || directory.updated != 0 ||
		bcrypt.CompareHashAndPassword([]byte(directory.byUsername[bootstrap.Username].PasswordHash), []byte("first-password")) != nil {
		t.Fatalf("restart without reset changed the admin password: created=%d updated=%d", directory.created, directory.updated)
	}
	bootstrap.Reset = true
	weak := bootstrap
	weak.Password = strings.Repeat("x", schema.API.PasswordMinimumCharacters-1)
	if err := service.BootstrapAdmin(context.Background(), weak); !errors.Is(err, authentication.ErrWeakPassword) {
		t.Fatalf("weak reset error = %v", err)
	}
	if err := service.BootstrapAdmin(context.Background(), bootstrap); err != nil {
		t.Fatal(err)
	}
	if directory.created != 1 || directory.updated != 1 {
		t.Fatalf(
			"created = %d, updated = %d",
			directory.created,
			directory.updated,
		)
	}
	if bcrypt.CompareHashAndPassword(
		[]byte(directory.byUsername[bootstrap.Username].PasswordHash),
		[]byte("rotated-password"),
	) != nil {
		t.Fatal("rotated password does not match")
	}
}

func TestAdminDecisionUsesDirectoryFlagAndAllowlistOnly(t *testing.T) {
	directory := &memoryDirectory{
		byID: map[string]authentication.UserRecord{
			"database-admin": {ID: "database-admin", Admin: true, Active: true},
			"allowlisted":    {ID: "allowlisted", Email: "allowlisted@example.test", Active: true},
			"role-admin":     {ID: "role-admin", Roles: []string{"schema-admin-role"}, Active: true},
		},
	}
	service := newService(t, authentication.Config{
		Schema:         authenticationSchema(t),
		SessionSecret:  strings.Repeat("a", 32),
		Environment:    "test",
		Policy:         testPolicy(t),
		Directory:      directory,
		AdminAllowlist: []string{"allowlisted@example.test"},
	})
	for _, user := range []authentication.User{
		{ID: "database-admin"},
		{ID: "allowlisted", Email: "allowlisted@example.test"},
	} {
		admin, err := service.IsAdmin(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		if !admin {
			t.Errorf("user should be admin: %#v", user)
		}
	}
	// A role that the retired single sign-on mapped to administrator no
	// longer grants it (#249); only the directory flag and allowlist do.
	for _, user := range []authentication.User{
		{ID: "ordinary"},
		{ID: "role-admin", Roles: []string{"schema-admin-role"}},
	} {
		admin, err := service.IsAdmin(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		if admin {
			t.Fatalf("user was treated as admin: %#v", user)
		}
	}
}

func TestSessionSecretMissingPolicyRejectsProductionAndCanBeSchemaDisabled(
	t *testing.T,
) {
	schema := authenticationSchema(t)
	_, err := authentication.New(authentication.Config{
		Schema:      schema,
		Environment: "production",
		Policy:      testPolicy(t),
		Directory:   &memoryDirectory{},
	})
	if err == nil {
		t.Fatal("production accepted an empty session secret")
	}

	schema.API.SessionSecretMissingPolicy = "reject"
	_, err = authentication.New(authentication.Config{
		Schema:      schema,
		Environment: "test",
		Policy:      testPolicy(t),
		Directory:   &memoryDirectory{},
	})
	if err == nil {
		t.Fatal("reject policy accepted an empty session secret")
	}

	schema.API.SessionSecretMissingPolicy = "ephemeral-non-production"
	if _, err := authentication.New(authentication.Config{
		Schema:      schema,
		Environment: "test",
		Policy:      testPolicy(t),
		Directory:   &memoryDirectory{},
	}); err != nil {
		t.Fatalf("ephemeral policy: %v", err)
	}
}

func authenticationSchema(t *testing.T) catalog.AuthenticationSchema {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Authentication
	schema.API.PasswordHashCost = 4
	return schema
}

func testPolicy(t *testing.T) *access.Policy {
	t.Helper()
	policy, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: "user",
		AdminRole:   "admin",
		Roles: []access.RoleDefinition{
			{Key: "user"},
			{Key: "admin"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func newService(
	t *testing.T,
	config authentication.Config,
) *authentication.Service {
	t.Helper()
	service, err := authentication.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// ADMIN_PASSWORD_RESET rotates the admin password the same way a password
// change in the product does: sessions issued under the old password stop
// working (SEC-06). A restart that leaves the password alone keeps them.
func TestBootstrapAdminResetRevokesExistingSessions(t *testing.T) {
	directory := &bootstrapDirectory{memoryDirectory: memoryDirectory{
		byUsername: map[string]authentication.UserRecord{},
		byID:       map[string]authentication.UserRecord{},
	}}
	schema := authenticationSchema(t)
	schema.API.PasswordHashCost = 4
	service := newService(t, authentication.Config{
		Schema:        schema,
		SessionSecret: strings.Repeat("v", 32),
		Environment:   "test",
		Policy:        testPolicy(t),
		Directory:     directory,
	})
	bootstrap := authentication.BootstrapAdmin{
		Username: "reset-admin",
		Password: "first-password",
		Name:     "Reset Admin",
		Email:    "reset@example.test",
		Role:     "admin",
	}
	if err := service.BootstrapAdmin(context.Background(), bootstrap); err != nil {
		t.Fatal(err)
	}
	record := directory.byUsername[bootstrap.Username]
	writer := httptest.NewRecorder()
	if err := service.IssueSession(writer, httptest.NewRequest(http.MethodPost, "/login", nil), authentication.User{
		ID:    record.ID,
		Name:  record.Name,
		Email: record.Email,
		Roles: record.Roles,
	}); err != nil {
		t.Fatal(err)
	}
	cookie := writer.Result().Cookies()[0]
	signedIn := func() bool {
		request := httptest.NewRequest(http.MethodGet, "/session", nil)
		request.AddCookie(cookie)
		_, exists, err := service.CurrentUser(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		return exists
	}
	bootstrap.Reset = true
	if err := service.BootstrapAdmin(context.Background(), bootstrap); err != nil {
		t.Fatal(err)
	}
	if !signedIn() {
		t.Fatal("a reset to the same password must not sign the admin out")
	}
	bootstrap.Password = "second-password"
	if err := service.BootstrapAdmin(context.Background(), bootstrap); err != nil {
		t.Fatal(err)
	}
	if signedIn() {
		t.Fatal("a session issued before ADMIN_PASSWORD_RESET still works")
	}
}

// #319: the startup bootstrap reports what it did so the operator can see it
// in the log and the audit trail: created, password_reset or unchanged, with
// the administrator's user ID. Nothing in the outcome carries the password.
func TestBootstrapAdminReportsItsOutcome(t *testing.T) {
	directory := &bootstrapDirectory{memoryDirectory: memoryDirectory{
		byUsername: map[string]authentication.UserRecord{},
		byID:       map[string]authentication.UserRecord{},
	}}
	schema := authenticationSchema(t)
	schema.API.PasswordHashCost = 4
	service := newService(t, authentication.Config{
		Schema:        schema,
		SessionSecret: strings.Repeat("o", 32),
		Environment:   "test",
		Policy:        testPolicy(t),
		Directory:     directory,
	})
	bootstrap := authentication.BootstrapAdmin{Username: "outcome-admin", Password: "first-password", Name: "Admin", Email: "a@example.test", Role: "admin"}
	created, err := service.BootstrapAdminOutcome(context.Background(), bootstrap)
	if err != nil || created.Action != authentication.BootstrapAdminCreated || created.UserID == "" ||
		created.UserID != directory.byUsername["outcome-admin"].ID {
		t.Fatalf("create outcome = %#v, %v", created, err)
	}
	unchanged, err := service.BootstrapAdminOutcome(context.Background(), bootstrap)
	if err != nil || unchanged.Action != authentication.BootstrapAdminUnchanged || unchanged.UserID != created.UserID {
		t.Fatalf("restart outcome = %#v, %v", unchanged, err)
	}
	bootstrap.Reset = true
	same, err := service.BootstrapAdminOutcome(context.Background(), bootstrap)
	if err != nil || same.Action != authentication.BootstrapAdminUnchanged {
		t.Fatalf("reset to the same password = %#v, %v", same, err)
	}
	bootstrap.Password = "second-password"
	reset, err := service.BootstrapAdminOutcome(context.Background(), bootstrap)
	if err != nil || reset.Action != authentication.BootstrapAdminPasswordReset || reset.UserID != created.UserID || !reset.SessionsRevoked {
		t.Fatalf("reset outcome = %#v, %v", reset, err)
	}
	if encoded := fmt.Sprintf("%#v", reset); strings.Contains(encoded, "second-password") {
		t.Fatalf("outcome carries the password: %s", encoded)
	}
	bootstrap.Password = ""
	if skipped, err := service.BootstrapAdminOutcome(context.Background(), bootstrap); err != nil || skipped.Action != authentication.BootstrapAdminSkipped {
		t.Fatalf("no password outcome = %#v, %v", skipped, err)
	}
}
