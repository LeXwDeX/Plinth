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
	"github.com/LeXwDeX/Plinth/internal/skilldraft"
)

type draftAgent struct {
	content string
	err     error
}

func (agent *draftAgent) Stream(ctx context.Context, request agents.Request, _ func(agents.Delta) error) (agents.Completion, error) {
	return agent.Complete(ctx, request)
}

func (agent *draftAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: agent.content}, agent.err
}

// #548: the draft route returns an unsaved draft; saving goes through the
// creation route, and PATCH changes one field at a time with field-named
// validation errors.
func TestPersonalSkillDraftSaveAndPatch(t *testing.T) {
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
	repo, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "personal-draft.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := agentconfiguration.New(repo, schema, defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	agent := &draftAgent{content: `{"slug":"three-view","description":"先列清单：多步任务时使用。","prompt":"先列清单再动手。","apply":"instruction"}`}
	drafter, err := skilldraft.New(skilldraft.Config{Chat: schema.Chat, Agent: agent,
		Model: func(context.Context) (string, string, error) { return "", "m", nil }, MaxPromptCharacters: 4000})
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
	send := func(method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		return response
	}
	creation := schema.Chat.Skills.Creation
	drafted := send(http.MethodPost, creation.DraftRoute, `{"name":"清单做法","requirement":"多步任务先列清单"}`)
	if drafted.Code != http.StatusOK {
		t.Fatalf("draft: %d %s", drafted.Code, drafted.Body.String())
	}
	var draft struct {
		Draft skilldraft.Draft `json:"draft"`
	}
	if err := json.Unmarshal(drafted.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	// The model's slug is a system Skill's: it is dropped, not an error.
	if draft.Draft.Slug != "" || draft.Draft.Apply != catalog.SkillApplyInstruction || draft.Draft.Prompt != "先列清单再动手。" {
		t.Fatalf("draft = %+v", draft.Draft)
	}
	listed := send(http.MethodGet, creation.Route, "")
	if !strings.Contains(listed.Body.String(), `"skills":[]`) {
		t.Fatalf("the draft was saved: %s", listed.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"name": "清单做法", "prompt": draft.Draft.Prompt, "slug": draft.Draft.Slug,
		"description": draft.Draft.Description, "apply": draft.Draft.Apply, "recommended_size": creation.RecommendedSize})
	saved := send(http.MethodPost, creation.Route, string(body))
	if saved.Code != http.StatusCreated {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	var skill struct {
		Skill catalog.SkillSchema `json:"skill"`
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &skill); err != nil {
		t.Fatal(err)
	}
	if skill.Skill.Slug != catalog.SkillSlug(catalog.SkillSchema{ID: skill.Skill.ID}) || skill.Skill.Apply != catalog.SkillApplyInstruction {
		t.Fatalf("saved = %+v", skill.Skill)
	}
	item := strings.ReplaceAll(creation.ItemRoute, "{skillID}", skill.Skill.ID)
	patched := send(http.MethodPatch, item, `{"slug":"checklist-first"}`)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"slug":"checklist-first"`) || !strings.Contains(patched.Body.String(), "先列清单再动手。") {
		t.Fatalf("patch: %d %s", patched.Code, patched.Body.String())
	}
	for body, code := range map[string]string{
		`{"slug":"Bad Slug"}`:     "invalid_personal_skill_slug",
		`{"description":"a\nb"}`:  "invalid_personal_skill_description",
		`{"prompt":""}`:           "invalid_personal_skill_prompt",
		`{"name":" "}`:            "invalid_personal_skill_name",
		`{"apply":"instruction"}`: "invalid_personal_skill",
	} {
		refused := send(http.MethodPatch, item, body)
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(refused.Body.Bytes(), &problem)
		if refused.Code != http.StatusBadRequest || problem.Code != code {
			t.Errorf("%s: %d %s", body, refused.Code, refused.Body.String())
		}
	}
	if missing := send(http.MethodPatch, strings.ReplaceAll(creation.ItemRoute, "{skillID}", "personal-0123456789abcdef0123456789abcdef"), `{"name":"x"}`); missing.Code != http.StatusNotFound {
		t.Fatalf("patch unknown: %d", missing.Code)
	}
	// An empty requirement is refused; a model failure is one readable message.
	if invalid := send(http.MethodPost, creation.DraftRoute, `{"name":"n","requirement":" "}`); invalid.Code != http.StatusBadRequest {
		t.Fatalf("empty requirement: %d", invalid.Code)
	}
	agent.err = errors.New("upstream down")
	failed := send(http.MethodPost, creation.DraftRoute, `{"name":"n","requirement":"r"}`)
	if failed.Code != http.StatusBadGateway || !strings.Contains(failed.Body.String(), schema.Content.Messages["ui.workspace.skills.draft_failed"]) {
		t.Fatalf("model failure: %d %s", failed.Code, failed.Body.String())
	}
}
