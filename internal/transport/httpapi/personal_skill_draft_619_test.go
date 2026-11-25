package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/skilldraft"
)

// #619: the draft follows the chat model of the session it was opened from,
// and a used-up quota on that model is the Schema's own answer, not a 502.
func TestPersonalSkillDraftFollowsTheSessionModelAndWordsAUsedUpQuota(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	defaults, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "personal-draft-619.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := agentconfiguration.New(repo, schema, defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	agent := &draftAgent{content: `{"slug":"ink","description":"水墨：画角色时使用。","prompt":"水墨晕染。","apply":"generation-prompt"}`}
	var asked []string
	drafter, err := skilldraft.New(skilldraft.Config{Chat: schema.Chat, Agent: agent,
		Model: func(_ context.Context, ownerID, sessionID string) (string, string, error) {
			asked = append(asked, ownerID+"/"+sessionID)
			if sessionID == "session-gone" {
				return "", "", chat.ErrSessionNotFound
			}
			return "QWEN", "qwen-max", nil
		}, MaxPromptCharacters: 4000})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := DevelopmentIdentity{DefaultClaims: access.Claims{ID: "owner-a", Roles: []string{"user"}}, Policy: policy}
	server := &Server{identity: identity, application: schema, agentConfiguration: service, skillDrafter: drafter,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux()}
	server.registerAgentConfigurationContracts()
	route := schema.Chat.Skills.Creation.DraftRoute
	send := func(body string) (*httptest.ResponseRecorder, string) {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, route, strings.NewReader(body)))
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &problem)
		return response, problem.Code
	}
	if response, _ := send(`{"name":"水墨","requirement":"用水墨","session_id":"session-1"}`); response.Code != http.StatusOK {
		t.Fatalf("draft: %d %s", response.Code, response.Body.String())
	}
	if response, _ := send(`{"name":"水墨","requirement":"用水墨"}`); response.Code != http.StatusOK {
		t.Fatalf("draft without a session: %d %s", response.Code, response.Body.String())
	}
	if strings.Join(asked, ",") != "owner-a/session-1,owner-a/" {
		t.Fatalf("model source asked for %v", asked)
	}
	if response, code := send(`{"name":"水墨","requirement":"用水墨","session_id":"session-gone"}`); response.Code != http.StatusNotFound || code != "session_not_found" {
		t.Fatalf("another owner's session: %d %s", response.Code, response.Body.String())
	}
	agent.err = errors.Join(&agents.QuotaExhaustion{Class: "insufficient_balance"}, errors.New("HTTP 429"))
	response, code := send(`{"name":"水墨","requirement":"用水墨","session_id":"session-1"}`)
	want := schema.Content.Messages["ui.workspace.skills.draft_quota_exhausted"]
	if want == "" || response.Code != http.StatusServiceUnavailable || code != "skill_draft_quota_exhausted" || !strings.Contains(response.Body.String(), want) {
		t.Fatalf("used-up quota: %d %s (want %q)", response.Code, response.Body.String(), want)
	}
}
