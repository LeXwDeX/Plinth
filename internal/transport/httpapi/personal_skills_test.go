package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestPersonalSkillRoutesScopeContentAndRejectInvalidWrites(t *testing.T) {
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
	repo, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "personal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := agentconfiguration.New(repo, schema, defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := DevelopmentIdentity{DefaultClaims: access.Claims{ID: "owner-a", Roles: []string{"user"}}, Policy: policy}
	server := &Server{identity: identity, application: schema, agentConfiguration: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux()}
	server.registerAgentConfigurationContracts()
	server.mux.HandleFunc("GET "+schema.Runtime.HTTP.Routes.ChatSkills, server.skillCatalogSchema)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		return response
	}
	route := schema.Chat.Skills.Creation.Route
	valid := `{"name":"My watercolor","prompt":"soft light and blue ink","recommended_size":"1536x1024"}`
	for _, body := range []string{
		`{"name":"","prompt":"p","category":"anime","recommended_size":"1536x1024"}`,
		`{"name":"x","prompt":"p","category":"unknown","recommended_size":"1536x1024"}`,
		`{"name":"x","prompt":"p","category":"anime","recommended_size":"invalid"}`,
		`{"name":"x","prompt":"p","category":"anime","recommended_size":"1536x1024","tool_grant":"all"}`,
	} {
		if response := send(http.MethodPost, route, body); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid body accepted: %s, %d", body, response.Code)
		} else if strings.Contains(response.Body.String(), "tool_grant") || strings.Contains(response.Body.String(), "SQL") {
			t.Fatalf("invalid input leaked parser or storage detail: %s", response.Body.String())
		}
	}
	created := send(http.MethodPost, route, valid)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var creation struct {
		Skill catalog.SkillSchema `json:"skill"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &creation); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(creation.Skill.ID, "personal-") || creation.Skill.Category != "" || creation.Skill.ThumbnailPath != "" || creation.Skill.RawImageURL != "" {
		t.Fatalf("invalid text-only Skill: %#v", creation.Skill)
	}
	listed := send(http.MethodGet, route, "")
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte(creation.Skill.ID)) || !bytes.Contains(listed.Body.Bytes(), []byte(`"category":""`)) {
		t.Fatalf("owner list: %d %s", listed.Code, listed.Body.String())
	}
	catalogA := send(http.MethodGet, schema.Runtime.HTTP.Routes.ChatSkills, "")
	if catalogA.Code != http.StatusOK || !bytes.Contains(catalogA.Body.Bytes(), []byte(creation.Skill.ID)) || !bytes.Contains(catalogA.Body.Bytes(), []byte(`"category":""`)) {
		t.Fatalf("owner catalog: %d %s", catalogA.Code, catalogA.Body.String())
	}
	// The Schema's three-view Skill is offered with the tool it binds.
	if !bytes.Contains(catalogA.Body.Bytes(), []byte(`"tool":"create_three_view"`)) || !bytes.Contains(catalogA.Body.Bytes(), []byte(`"requires_image":true`)) || !bytes.Contains(catalogA.Body.Bytes(), []byte(`"one_shot":true`)) {
		t.Fatalf("catalog lacks the three-view Skill: %s", catalogA.Body.String())
	}
	if cache := catalogA.Header().Get("Cache-Control"); !strings.Contains(cache, "private") || !strings.Contains(cache, "no-store") {
		t.Fatalf("owner Skill catalog can enter a shared cache: %q", cache)
	}
	ownerA, err := service.SnapshotForOwner(context.Background(), "owner-a")
	if err != nil || len(ownerA.Configuration.Skills) != 2 || ownerA.Configuration.Skills[1].Category != "" {
		t.Fatalf("category-free owner effective: %#v %v", ownerA, err)
	}
	legacy := send(http.MethodPost, route, `{"name":"Legacy category","prompt":"ink wash","category":"anime","recommended_size":"1536x1024"}`)
	if legacy.Code != http.StatusCreated {
		t.Fatalf("legacy category create: %d %s", legacy.Code, legacy.Body.String())
	}
	identity.DefaultClaims.ID = "owner-b"
	server.identity = identity
	catalogB := send(http.MethodGet, schema.Runtime.HTTP.Routes.ChatSkills, "")
	if catalogB.Code != http.StatusOK || bytes.Contains(catalogB.Body.Bytes(), []byte(creation.Skill.ID)) {
		t.Fatalf("cross-owner catalog: %d %s", catalogB.Code, catalogB.Body.String())
	}
	if catalogA.Header().Get("ETag") == catalogB.Header().Get("ETag") {
		t.Fatal("owner-scoped catalogs have the same ETag")
	}
	if response := send(http.MethodGet, route, ""); response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(creation.Skill.ID)) {
		t.Fatalf("cross-owner list: %d %s", response.Code, response.Body.String())
	}
	snap, err := service.SnapshotForOwner(context.Background(), "owner-b")
	if err != nil || len(snap.Configuration.Skills) != 1 || snap.Configuration.Skills[0].Tool == "" {
		t.Fatalf("cross-owner effective: %#v %v", snap, err)
	}
	_ = io.Discard
}

// #238: a user deletes only their own personal Skill. Other owners, system
// Skills and unknown IDs get 404; the deletion frees the owner's slot and
// drops the Skill from that owner's catalog and personal list.
func TestPersonalSkillDeleteIsOwnerScoped(t *testing.T) {
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
	repo, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "personal-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := agentconfiguration.New(repo, schema, defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := DevelopmentIdentity{DefaultClaims: access.Claims{ID: "owner-a", Roles: []string{"user"}}, Policy: policy}
	server := &Server{identity: identity, application: schema, agentConfiguration: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux()}
	server.registerAgentConfigurationContracts()
	server.mux.HandleFunc("GET "+schema.Runtime.HTTP.Routes.ChatSkills, server.skillCatalogSchema)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		return response
	}
	itemRoute := schema.Chat.Skills.Creation.ItemRoute
	if !strings.HasPrefix(itemRoute, schema.Chat.Skills.Creation.Route+"/") || !strings.HasSuffix(itemRoute, "/{skillID}") {
		t.Fatalf("item route = %q", itemRoute)
	}
	item := func(id string) string { return strings.Replace(itemRoute, "{skillID}", id, 1) }
	created := send(http.MethodPost, schema.Chat.Skills.Creation.Route, `{"name":"Keep","prompt":"p","recommended_size":"1536x1024"}`)
	var creation struct {
		Skill catalog.SkillSchema `json:"skill"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &creation) != nil {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	identity.DefaultClaims.ID = "owner-b"
	server.identity = identity
	if response := send(http.MethodDelete, item(creation.Skill.ID), ""); response.Code != http.StatusNotFound {
		t.Fatalf("cross-owner delete = %d %s", response.Code, response.Body.String())
	}
	identity.DefaultClaims.ID = "owner-a"
	server.identity = identity
	for _, id := range []string{"system-three-view", "personal-missing"} {
		if response := send(http.MethodDelete, item(id), ""); response.Code != http.StatusNotFound {
			t.Fatalf("delete %s = %d", id, response.Code)
		}
	}
	if response := send(http.MethodDelete, item(creation.Skill.ID), ""); response.Code != http.StatusNoContent {
		t.Fatalf("owner delete = %d %s", response.Code, response.Body.String())
	}
	if response := send(http.MethodDelete, item(creation.Skill.ID), ""); response.Code != http.StatusNotFound {
		t.Fatalf("repeated delete = %d", response.Code)
	}
	if listed := send(http.MethodGet, schema.Chat.Skills.Creation.Route, ""); bytes.Contains(listed.Body.Bytes(), []byte(creation.Skill.ID)) {
		t.Fatalf("deleted Skill still listed: %s", listed.Body.String())
	}
	if catalogA := send(http.MethodGet, schema.Runtime.HTTP.Routes.ChatSkills, ""); bytes.Contains(catalogA.Body.Bytes(), []byte(creation.Skill.ID)) || !bytes.Contains(catalogA.Body.Bytes(), []byte("system-three-view")) {
		t.Fatalf("catalog after delete: %s", catalogA.Body.String())
	}
}
