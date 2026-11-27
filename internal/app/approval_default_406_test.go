package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #406: the default approval mode is the Product Schema's and nothing else's,
// and it applies to sessions created after the change only.

// A database left by the release before the change: sessions in every mode the
// column accepts, and one inserted the way the first releases did, without any
// mode. The assembled application, with the shipped Schema, must read each of
// them as stored and must create the next session in the Schema default.
func TestAssembledAppKeepsStoredSessionModesAndCreatesAutoSessions_406(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "art-ai.db")
	seed, err := sqlitetest.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	raw := openRawAppDatabase(t, databasePath)
	const insert = `INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at,approval_mode)
 VALUES (?,'owner-a','owner','old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',?)`
	for id, mode := range map[string]string{"old-budget": "budget", "old-manual": "manual", "old-auto": "auto"} {
		if _, err := raw.Exec(insert, id, mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
 VALUES ('old-bare','owner-a','owner','old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	application, err := New(Config{
		Environment: "development", Address: "127.0.0.1:0",
		DatabasePath: databasePath, StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock", AgentMode: "mock",
		RuntimeSettings: map[string]string{"llm_model": "configured-test-model"},
		AuthMode:        "development", DevelopmentUser: "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	handler := application.server.Handler

	mode := func(sessionID string) string {
		t.Helper()
		response := appChatRequest(handler, http.MethodGet, "/api/v1/chat/sessions/"+sessionID+"/approval", "")
		var body map[string]any
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("approval of %s: %d %s", sessionID, response.Code, response.Body.String())
		}
		value, _ := body["approvalMode"].(string)
		return value
	}
	for id, want := range map[string]string{"old-budget": "budget", "old-manual": "manual", "old-auto": "auto", "old-bare": "manual"} {
		if got := mode(id); got != want {
			t.Errorf("%s reads %q, want the stored %q", id, got, want)
		}
	}

	created := appChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`)
	var session map[string]any
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &session) != nil {
		t.Fatalf("create session: %d %s", created.Code, created.Body.String())
	}
	declared := application.schema.Chat.ApprovalPolicy.DefaultMode
	if declared != "auto" || session["approvalMode"] != declared {
		t.Fatalf("new session mode = %v, Schema default = %q, want auto", session["approvalMode"], declared)
	}
	if got := mode(session["id"].(string)); got != declared {
		t.Fatalf("new session reads %q from the approval route, want %q", got, declared)
	}
	// Creating the session moved none of the stored ones.
	for id, want := range map[string]string{"old-budget": "budget", "old-manual": "manual", "old-auto": "auto", "old-bare": "manual"} {
		if got := mode(id); got != want {
			t.Errorf("after the new session, %s reads %q, want %q", id, got, want)
		}
	}
}

// What each mode asks a person about, for every capability the shipped Schema
// declares. The default is auto, so this is the list of paid steps that run
// with nobody confirming them in a new session: every capability, extra
// credits included. Strict mode asks about every step (#702); budget
// ("approve") without an approved plan asks about four capabilities and about
// extra credits. This keeps the list that docs/product.md §6.1 states in step
// with the Schema.
func TestApprovalPolicyScopeByModeForTheShippedSchema_406(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	policy := approvalPolicyFromSchema(application)
	if policy.DefaultMode != approvalpolicy.ModeAuto {
		t.Fatalf("default mode = %q, want auto", policy.DefaultMode)
	}
	strict := []string{"text-to-3d", "image-to-3d", "image-to-splat", "retexture"}
	var askedInStrict []string
	for _, capability := range application.Capabilities {
		id := string(capability.ID)
		if approvalpolicy.RequiresHuman(policy, approvalpolicy.ModeAuto, id) {
			t.Errorf("auto asks a person about %s", id)
		}
		inStrict := slices.Contains(strict, id)
		// #702: strict asks for every step; budget ("approve") for the 3D list.
		if !approvalpolicy.RequiresHuman(policy, approvalpolicy.ModeManual, id) {
			t.Errorf("manual does not ask a person about %s", id)
		}
		if got := approvalpolicy.RequiresHuman(policy, approvalpolicy.ModeBudget, id); got != inStrict {
			t.Errorf("budget asks a person about %s = %v, want %v", id, got, inStrict)
		}
		if inStrict {
			askedInStrict = append(askedInStrict, id)
		}
	}
	if len(askedInStrict) != len(strict) {
		t.Fatalf("the Schema declares %v of the strict capabilities %v", askedInStrict, strict)
	}
	// Extra credits: a person decides them in strict and budget, nobody in auto.
	if approvalpolicy.RequiresHumanForExtraCredits(policy, approvalpolicy.ModeAuto, 1) {
		t.Error("auto asks a person about extra credits")
	}
	for _, mode := range []string{approvalpolicy.ModeManual, approvalpolicy.ModeBudget} {
		if !approvalpolicy.RequiresHumanForExtraCredits(policy, mode, 1) {
			t.Errorf("%s does not ask a person about extra credits", mode)
		}
	}
}
