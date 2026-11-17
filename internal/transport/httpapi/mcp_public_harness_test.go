package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/mcppublic"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/transport/httpapi"
)

// syncBuffer collects the server log of one test.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// harnessLogs maps a test to the log its server wrote.
var harnessLogs sync.Map

func harnessLogger(t *testing.T) *slog.Logger {
	buffer := &syncBuffer{}
	harnessLogs.Store(t, buffer)
	t.Cleanup(func() { harnessLogs.Delete(t) })
	return slog.New(slog.NewTextHandler(buffer, nil))
}

// mcpClock is the balance cache's time source in the harness.
type mcpClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mcpClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mcpClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// mcpDirectory is the person lookup the key authenticator uses in the harness.
type mcpDirectory struct{}

func (mcpDirectory) FindByID(_ context.Context, id string) (authentication.UserRecord, bool, error) {
	switch id {
	case "owner-a", "owner-b":
		return authentication.UserRecord{ID: id, Username: id, Nickname: strings.ToUpper(id), Active: true}, true, nil
	case "owner-gone":
		return authentication.UserRecord{ID: id, Username: id, Active: true, Deleted: true}, true, nil
	case "owner-off":
		return authentication.UserRecord{ID: id, Username: id}, true, nil
	}
	return authentication.UserRecord{}, false, nil
}

type mcpHarnessParts struct {
	identity httpapi.IdentityResolver
	status   *mcppublic.Builder
	assets   *mcppublic.Assets
	clock    *mcpClock
}

// harnessMCP assembles what the application assembles for the external MCP
// listener: the key authenticator and the status block builder over a balance
// cache whose clock a test can move. Zero parts for a Catalog without the
// service.
func harnessMCP(t *testing.T, repository *sqlite.Repository, application catalog.ApplicationSchema,
	settings *accountsettings.Service, keys *accesskeys.Service, policy *access.Policy, accounts *provideraccounts.Service,
	sessions *chat.Service,
) mcpHarnessParts {
	t.Helper()
	if application.MCPPublic == nil || keys == nil || settings == nil || application.StepPolicy == nil {
		return mcpHarnessParts{}
	}
	clock := &mcpClock{now: time.Now().UTC()}
	cache, err := provideraccounts.NewCache(accounts, provideraccounts.CacheConfig{
		TTL:     time.Duration(application.StepPolicy.BalanceCacheSeconds) * time.Second,
		Timeout: time.Duration(application.StepPolicy.BalanceReadTimeoutSeconds) * time.Second,
		Clock:   clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, account := range application.Runtime.HTTP.LegacyProviderBalance.Accounts {
		labels[account.AccountID] = account.Label
	}
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	status, err := mcppublic.New(mcppublic.Config{
		StatusBlock: application.MCPPublic.StatusBlock, StepPolicy: *application.StepPolicy, ProviderAccounts: application.Runtime.ProviderAccounts,
		AccountLabels: labels,
		Settings:      settings, Usage: repository, Balances: cache, Localize: texts.Text,
	})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := mcppublic.NewAssets(mcppublic.AssetsConfig{
		Schema: application.MCPPublic.Assets, Sessions: sessions, Store: repository, Localize: texts.Text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mcpHarnessParts{
		identity: &accesskeys.Authenticator{Keys: keys, Users: mcpDirectory{}, Policy: policy},
		status:   status, assets: assets, clock: clock,
	}
}

// mcpFixture is the external MCP listener of one harness, with the means to
// seed what its tools read.
type mcpFixture struct {
	t            *testing.T
	module       *generation.Module
	web          http.Handler
	handler      http.Handler
	keys         *accesskeys.Service
	repository   *sqlite.Repository
	clock        *mcpClock
	databasePath string
	steps        approvalpolicy.StepsPolicy
	application  catalog.ApplicationSchema
	sequence     atomic.Int64
}

var mcpFixtures sync.Map

func registerHarnessMCP(t *testing.T, module *generation.Module, api *httpapi.Server, parts mcpHarnessParts, keys *accesskeys.Service, repository *sqlite.Repository) {
	t.Helper()
	handler, err := api.MCPPublicHandler()
	if err != nil {
		t.Fatal(err)
	}
	if handler == nil {
		return
	}
	mcpFixtures.Store(module, &mcpFixture{handler: handler, keys: keys, repository: repository, clock: parts.clock})
	t.Cleanup(func() { mcpFixtures.Delete(module) })
}

// newMCPFixture builds a harness whose provider accounts read from readers
// (nil keeps the fixtures of the Schema's mock balances). A customize function
// changes the Product Schema before the application is assembled.
func newMCPFixture(t *testing.T, readers map[string]provideraccounts.BalanceReader, customize ...func(*catalog.ApplicationSchema)) *mcpFixture {
	t.Helper()
	var databasePath string
	// Only a deployment Bearer token answers on the main server, so a key that
	// reaches it is refused for being a key and not taken for the development
	// identity.
	module, web, _, _ := newHTTPHarnessWithCatalogOverride(t, []string{"user"},
		func(application *catalog.ApplicationSchema) {
			for _, change := range customize {
				change(application)
			}
		},
		func(config *authentication.Config) {
			bearer, err := httpapi.NewBearerIdentity(map[string]access.Claims{deployToken: {ID: "deployment-owner"}}, config.Policy)
			if err != nil {
				t.Fatal(err)
			}
			config.Fallback = bearer
		}, nil, readers, nil,
		func(_ *testing.T, path string) { databasePath = path })
	value, found := mcpFixtures.Load(module)
	if !found {
		t.Fatal("the harness has no external MCP listener")
	}
	fixture := value.(*mcpFixture)
	fixture.t, fixture.module, fixture.web, fixture.databasePath = t, module, web, databasePath
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fixture.application = product.Application()
	counted := make([]string, 0)
	for _, capability := range fixture.application.StepPolicy.CountedCapabilities {
		counted = append(counted, string(capability))
	}
	fixture.steps = approvalpolicy.StepsPolicy{
		Counted:     counted,
		DefaultList: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
		DefaultMCP:  approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
	}
	return fixture
}

// countingReader is a provider balance reader that counts its reads and can
// be made to fail.
type countingReader struct {
	calls  atomic.Int32
	amount schema.NativeAmount
	fail   atomic.Bool
}

func (r *countingReader) Balance(context.Context) (float64, error) {
	return 0, fmt.Errorf("native reader only")
}

func (r *countingReader) NativeBalance(context.Context) (schema.NativeAmount, error) {
	r.calls.Add(1)
	if r.fail.Load() {
		return schema.NativeAmount{}, fmt.Errorf("provider balance endpoint is down")
	}
	return r.amount, nil
}

// balanceReaders gives each of the Schema's three 3D providers a counting
// reader holding the amount.
func balanceReaders(amounts map[string]string) (map[string]provideraccounts.BalanceReader, map[string]*countingReader) {
	readers := map[string]provideraccounts.BalanceReader{}
	counting := map[string]*countingReader{}
	units := map[string][2]string{
		"meshy": {"meshy.credit", "Meshy credits"}, "tripo": {"tripo.credit", "Tripo credits"}, "hyper3d": {"hyper3d.credit", "Hyper3D credits"},
	}
	for id, unit := range units {
		reader := &countingReader{amount: schema.NativeAmount{Unit: unit[0], UnitLabel: unit[1], Amount: amounts[id]}}
		readers[id], counting[id] = reader, reader
	}
	return readers, counting
}

func (f *mcpFixture) issueKey(owner string, scopes ...string) string {
	f.t.Helper()
	created, err := f.keys.Create(context.Background(), accesskeys.CreateRequest{OwnerID: owner, Name: "mcp test", Scopes: scopes})
	if err != nil {
		f.t.Fatal(err)
	}
	return created.Key
}

func (f *mcpFixture) revoke(owner, key string) {
	f.t.Helper()
	id := strings.Split(key, "_")[1]
	if _, _, err := f.keys.Revoke(context.Background(), owner, id); err != nil {
		f.t.Fatal(err)
	}
}

const mcpTestProtocol = "2026-07-28"

// post sends one JSON-RPC request to the listener.
func (f *mcpFixture) post(key, method string, params map[string]any, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    mcpTestProtocol,
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test", "version": "1"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": f.sequence.Add(1), "method": method, "params": params})
	if err != nil {
		f.t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("MCP-Protocol-Version", mcpTestProtocol)
	request.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		request.Header.Set("Mcp-Name", name)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for _, change := range mutate {
		if change != nil {
			change(request)
		}
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

// toolResult is one decoded tools/call answer.
type toolResult struct {
	code       int
	header     http.Header
	raw        string
	isError    bool
	structured map[string]any
	text       []string
	rpcError   map[string]any
}

func decodeToolResult(t *testing.T, recorder *httptest.ResponseRecorder) toolResult {
	t.Helper()
	result := toolResult{code: recorder.Code, header: recorder.Header(), raw: recorder.Body.String()}
	var envelope struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("answer %q: %v", recorder.Body.String(), err)
	}
	result.isError, result.structured, result.rpcError = envelope.Result.IsError, envelope.Result.StructuredContent, envelope.Error
	for _, item := range envelope.Result.Content {
		result.text = append(result.text, item.Text)
	}
	return result
}

// call runs one tool and decodes the answer.
func (f *mcpFixture) call(key, tool string, arguments map[string]any) toolResult {
	f.t.Helper()
	if arguments == nil {
		arguments = map[string]any{}
	}
	return decodeToolResult(f.t, f.post(key, "tools/call", map[string]any{"name": tool, "arguments": arguments}))
}

func (r toolResult) status(t *testing.T) map[string]any {
	t.Helper()
	status, ok := r.structured["status"].(map[string]any)
	if !ok {
		t.Fatalf("the answer has no status block: %s", r.raw)
	}
	return status
}

func (r toolResult) problemCode() string {
	problem, _ := r.structured["error"].(map[string]any)
	code, _ := problem["code"].(string)
	return code
}

// succeededTask runs a text-to-image Task of the owner to success in a session
// that is an MCP asset of the owner (the tools of the service only name those).
func (f *mcpFixture) succeededTask(owner, session, idempotencyKey string) generation.Task {
	f.t.Helper()
	task := f.succeededPlainTask(owner, session, idempotencyKey)
	f.markAsset(owner, task.SessionID)
	return task
}

// markAsset makes a session of the owner an MCP asset, as the service does for
// a name, without making a session.
func (f *mcpFixture) markAsset(owner, session string) {
	f.t.Helper()
	f.exec(`INSERT OR IGNORE INTO mcp_assets (owner_id, name_key, name, session_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		owner, strings.ToLower(session), session, session, time.Now().UTC().Format(time.RFC3339Nano))
}

// succeededPlainTask runs a text-to-image Task of the owner to success in a
// session the browser could have made: no MCP asset.
func (f *mcpFixture) succeededPlainTask(owner, session, idempotencyKey string) generation.Task {
	f.t.Helper()
	accepted, err := submitHarnessTask(f.t, f.module, context.Background(), generation.SubmitCommand{
		OwnerID: generation.OwnerID(owner), SessionID: session, Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"an mcp test picture"}`),
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	for range 20 {
		if _, err := f.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID: "mcp-test", LeaseFor: time.Minute, BatchSize: 10,
		}); err != nil {
			f.t.Fatal(err)
		}
		task, err := f.module.Get(context.Background(), generation.OwnerID(owner), accepted.TaskID)
		if err != nil {
			f.t.Fatal(err)
		}
		if task.State == generation.TaskSucceeded {
			return task
		}
		if task.State.Terminal() {
			f.t.Fatalf("task = %#v", task)
		}
		time.Sleep(12 * time.Millisecond)
	}
	f.t.Fatal("task did not succeed")
	return generation.Task{}
}

// exec changes the database the harness runs on, behind the application's back.
func (f *mcpFixture) exec(query string, args ...any) {
	f.t.Helper()
	database, err := sql.Open("sqlite", f.databasePath)
	if err != nil {
		f.t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(query, args...); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
}

func (f *mcpFixture) countRows(query string, args ...any) int {
	f.t.Helper()
	database, err := sql.Open("sqlite", f.databasePath)
	if err != nil {
		f.t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(query, args...).Scan(&count); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
	return count
}

// keySteps seeds n approved 3D steps of a key in the owner's asset, with no
// Task yet: each one counts as a used step of the asset and of the person.
func (f *mcpFixture) keySteps(owner, session, keyID string, n int) {
	f.t.Helper()
	ctx := context.Background()
	if owned, err := f.repository.OwnsSession(ctx, owner, session); err != nil {
		f.t.Fatal(err)
	} else if !owned {
		now := time.Now().UTC()
		if _, _, err := f.repository.CreateMCPAsset(ctx, chat.Session{
			ID: session, OwnerID: owner, OwnerName: "Test human", Title: "MCP · " + session, CreatedAt: now, UpdatedAt: now,
		}, strings.ToLower(session), session); err != nil {
			f.t.Fatal(err)
		}
	}
	for range n {
		sequence := f.sequence.Add(1)
		identity := executiongrant.Identity{
			OwnerID: owner, SessionID: session, OfferingID: "meshy/meshy-7.1/text-to-3d", OfferingRevision: "rev-1",
			PreparedInputID: fmt.Sprintf("prepared-mcp-%d", sequence), PreparedInputHash: fmt.Sprintf("hash-mcp-%d", sequence),
			IdempotencyKey: fmt.Sprintf("key-mcp-%d", sequence), OperationID: fmt.Sprintf("op-mcp-%d", sequence),
		}
		if err := f.repository.CreatePreparedInput(ctx, generation.PreparedInput{
			ID: generation.PreparedInputID(identity.PreparedInputID), OwnerID: generation.OwnerID(owner), SessionID: session,
			Capability: generation.TextTo3D, OfferingID: generation.OfferingID(identity.OfferingID), OfferingRevision: identity.OfferingRevision,
			NormalizedInput: []byte(`{}`), InputHash: identity.PreparedInputHash, CreatedAt: time.Now().UTC(),
		}); err != nil {
			f.t.Fatal(err)
		}
		grant, err := f.repository.PreparePendingForAccessKey(ctx, identity, keyID)
		if err != nil {
			f.t.Fatal(err)
		}
		if _, decision, err := f.repository.ApproveExecutionGrantWithSteps(ctx, owner, grant.RequestID, executiongrant.StepRequest{
			Entry: approvalpolicy.StepEntryMCP, Steps: f.steps, Actor: executiongrant.AccessKeyActor,
		}); err != nil || !decision.Approved {
			f.t.Fatalf("seed step: %+v %v", decision, err)
		}
	}
}

func (f *mcpFixture) serverLog() string {
	value, found := harnessLogs.Load(f.t)
	if !found {
		return ""
	}
	return value.(*syncBuffer).String()
}
