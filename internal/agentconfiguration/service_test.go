package agentconfiguration_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestPersonalSkillsPersistAndKeepSharedRevisionIndependent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "personal.db")
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	open := func() (*sqlite.Repository, *agentconfiguration.Service) {
		repo, err := sqlitetest.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		service, err := agentconfiguration.New(repo, product.Application(), defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
		if err != nil {
			t.Fatal(err)
		}
		return repo, service
	}
	repo, service := open()
	base, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := agentconfiguration.CreatePersonalSkillInput{Name: "My style", Prompt: "soft light", RecommendedSize: "1536x1024"}
	first, err := service.CreatePersonalSkill(ctx, "owner-a", input)
	if err != nil {
		t.Fatal(err)
	}
	ownerA, err := service.SnapshotForOwner(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	// The Schema's system Skill (three-view) always comes first, then the owner's.
	if !strings.HasPrefix(ownerA.Revision, base.Revision+":system:") || !strings.Contains(ownerA.Revision, ":personal:") || len(ownerA.Configuration.Skills) != 2 || ownerA.Configuration.Skills[0].Tool != "create_three_view" || ownerA.Configuration.Skills[1].ID != first.ID || first.Category != "" || ownerA.Configuration.Skills[1].Category != "" {
		t.Fatalf("owner snapshot: %#v", ownerA)
	}
	ownerB, err := service.SnapshotForOwner(ctx, "owner-b")
	if err != nil || strings.Contains(ownerB.Revision, ":personal:") || len(ownerB.Configuration.Skills) != 1 || ownerB.Configuration.Skills[0].Tool == "" {
		t.Fatalf("cross-owner snapshot: %#v %v", ownerB, err)
	}
	shared, err := service.Snapshot(ctx)
	if err != nil || shared.Revision != base.Revision || len(shared.Configuration.Skills) != 0 {
		t.Fatalf("personal Skill changed shared configuration: %#v %v", shared, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, service = open()
	defer repo.Close()
	reopened, err := service.SnapshotForOwner(ctx, "owner-a")
	if err != nil || reopened.Revision != ownerA.Revision || len(reopened.Configuration.Skills) != 2 || reopened.Configuration.Skills[1].Category != "" {
		t.Fatalf("database reopen lost personal Skill: %#v %v", reopened, err)
	}
	legacy := input
	legacy.Category = "anime"
	_, err = service.CreatePersonalSkill(ctx, "owner-a", legacy)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := service.SnapshotForOwner(ctx, "owner-a")
	if err != nil || latest.Revision == reopened.Revision || len(latest.Configuration.Skills) != 3 {
		t.Fatalf("creation did not change effective revision: %#v %v", latest, err)
	}
}

func TestPersonalSkillUpdateKeepsIdentityAndOnlyNewSnapshotsSeeIt(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "personal-update.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentconfiguration.New(repo, product.Application(), defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.CreatePersonalSkill(ctx, "owner-a", agentconfiguration.CreatePersonalSkillInput{Name: "My style", Prompt: "soft light", RecommendedSize: "1536x1024"})
	if err != nil {
		t.Fatal(err)
	}
	// A turn that already froze keeps the snapshot it captured, so the update cannot rewrite it.
	frozen, err := service.SnapshotForOwner(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdatePersonalSkill(ctx, "owner-a", first.ID, agentconfiguration.CreatePersonalSkillInput{Name: "Renamed style", Prompt: "warm light and blue ink", RecommendedSize: "1536x1024"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != first.ID || updated.Name != "Renamed style" || updated.Prompt != "warm light and blue ink" || updated.Category != "" {
		t.Fatalf("update changed identity or content: %#v", updated)
	}
	list, err := service.PersonalSkills(ctx, "owner-a")
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], updated) {
		t.Fatalf("updated Skill not listed: %#v %v", list, err)
	}
	after, err := service.SnapshotForOwner(ctx, "owner-a")
	if err != nil || after.Revision == frozen.Revision {
		t.Fatalf("update did not change the effective revision: %#v %v", after, err)
	}
	if frozen.Configuration.Skills[len(frozen.Configuration.Skills)-1].Prompt != "soft light" {
		t.Fatalf("a frozen snapshot was rewritten: %#v", frozen.Configuration.Skills)
	}
	last := after.Configuration.Skills[len(after.Configuration.Skills)-1]
	if last.ID != first.ID || last.Prompt != "warm light and blue ink" {
		t.Fatalf("new snapshot lacks the update: %#v", last)
	}
	// Owner scoping and unknown IDs are indistinguishable, and invalid content leaves the row intact.
	if _, err := service.UpdatePersonalSkill(ctx, "owner-b", first.ID, agentconfiguration.CreatePersonalSkillInput{Name: "Hijack", Prompt: "p", RecommendedSize: "1536x1024"}); !errors.Is(err, agentconfiguration.ErrPersonalSkillNotFound) {
		t.Fatalf("cross-owner update error = %v", err)
	}
	if _, err := service.UpdatePersonalSkill(ctx, "owner-a", "personal-unknown", agentconfiguration.CreatePersonalSkillInput{Name: "X", Prompt: "p", RecommendedSize: "1536x1024"}); !errors.Is(err, agentconfiguration.ErrPersonalSkillNotFound) {
		t.Fatalf("unknown update error = %v", err)
	}
	if _, err := service.UpdatePersonalSkill(ctx, "owner-a", first.ID, agentconfiguration.CreatePersonalSkillInput{Name: "", Prompt: "p", RecommendedSize: "1536x1024"}); !errors.Is(err, agentconfiguration.ErrInvalidPersonalSkill) {
		t.Fatalf("invalid update error = %v", err)
	}
	unchanged, err := service.PersonalSkills(ctx, "owner-a")
	if err != nil || len(unchanged) != 1 || !reflect.DeepEqual(unchanged[0], updated) {
		t.Fatalf("invalid update changed the row: %#v %v", unchanged, err)
	}
}

func TestPersonalSkillStoreUpdateIsOwnerScopedAndKeepsOrder(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "personal-update-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	one := catalog.SkillSchema{ID: "personal-one", Name: "One", RecommendedSize: "1536x1024", Prompt: "one"}
	two := catalog.SkillSchema{ID: "personal-two", Name: "Two", RecommendedSize: "1536x1024", Prompt: "two"}
	if err := repo.InsertPersonalSkill(ctx, "owner-a", one, 10); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertPersonalSkill(ctx, "owner-a", two, 10); err != nil {
		t.Fatal(err)
	}
	renamed := catalog.SkillSchema{ID: "personal-one", Name: "Renamed", RecommendedSize: "1536x1024", Prompt: "changed"}
	if err := repo.UpdatePersonalSkill(ctx, "owner-b", "personal-one", renamed); !errors.Is(err, agentconfiguration.ErrPersonalSkillNotFound) {
		t.Fatalf("cross-owner update error = %v", err)
	}
	missing := renamed
	missing.ID = "personal-missing"
	if err := repo.UpdatePersonalSkill(ctx, "owner-a", "personal-missing", missing); !errors.Is(err, agentconfiguration.ErrPersonalSkillNotFound) {
		t.Fatalf("unknown update error = %v", err)
	}
	if err := repo.UpdatePersonalSkill(ctx, "owner-a", "personal-one", renamed); err != nil {
		t.Fatal(err)
	}
	mine, err := repo.ListPersonalSkills(ctx, "owner-a")
	if err != nil || len(mine) != 2 || mine[0].ID != "personal-one" || mine[0].Name != "Renamed" || mine[1].ID != "personal-two" {
		t.Fatalf("owner rows after update: %#v %v", mine, err)
	}
	other, err := repo.ListPersonalSkills(ctx, "owner-b")
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-owner rows changed: %#v %v", other, err)
	}
	// An update does not consume the per-owner creation quota.
	limited := catalog.SkillSchema{ID: "personal-limited", Name: "Limited", RecommendedSize: "1536x1024", Prompt: "p"}
	if err := repo.InsertPersonalSkill(ctx, "owner-limit", limited, 1); err != nil {
		t.Fatal(err)
	}
	limited.Name = "Limited renamed"
	if err := repo.UpdatePersonalSkill(ctx, "owner-limit", "personal-limited", limited); err != nil {
		t.Fatal(err)
	}
	second := limited
	second.ID = "personal-second"
	if err := repo.InsertPersonalSkill(ctx, "owner-limit", second, 1); !errors.Is(err, agentconfiguration.ErrPersonalSkillLimit) {
		t.Fatalf("update changed the creation quota: %v", err)
	}
}

func TestPersonalSkillStoreEnforcesConcurrentOwnerLimit(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "limit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	skill := catalog.SkillSchema{ID: "personal-one", Name: "One", Category: "anime", RecommendedSize: "1536x1024", Prompt: "one"}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"personal-one", "personal-two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			candidate := skill
			candidate.ID = id
			results <- repo.InsertPersonalSkill(ctx, "owner", candidate, 1)
		}(id)
	}
	wg.Wait()
	close(results)
	success, limited := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, agentconfiguration.ErrPersonalSkillLimit):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || limited != 1 {
		t.Fatalf("concurrent inserts = success %d, limit %d", success, limited)
	}
	other := skill
	other.ID = "personal-other"
	if err := repo.InsertPersonalSkill(ctx, "other-owner", other, 1); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedConfigurationSurvivesRestartAndRejectsStaleOrInvalidWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	create := func(repository *sqlite.Repository) *agentconfiguration.Service {
		s, err := agentconfiguration.New(repository, product.Application(), skills, product.List(""))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	service := create(repo)
	base, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	draft := base.Configuration
	draft.SystemPrompt = "custom system instructions"
	draft.EnabledToolNames = nil
	draft.Skills = []catalog.SkillSchema{customSkill(t)}
	published, err := service.Publish(ctx, base.Revision, "admin", "publish", draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(ctx, base.Revision, "admin", "publish", draft); !errors.Is(err, agentconfiguration.ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	bad := draft
	bad.EnabledToolNames = []string{"arbitrary_paid_action"}
	if _, err := service.Publish(ctx, published.Revision, "admin", "publish", bad); err == nil {
		t.Fatal("unknown execution must fail")
	}
	bad = draft
	bad.Skills = append([]catalog.SkillSchema(nil), draft.Skills...)
	bad.Skills[0].RawImageURL = "https://upstream.test/private?token=secret"
	if _, err := service.Publish(ctx, published.Revision, "admin", "publish", bad); err == nil {
		t.Fatal("remote asset must fail")
	}
	current, err := service.Snapshot(ctx)
	if err != nil || current.Revision != published.Revision {
		t.Fatalf("invalid write changed active configuration: %#v %v", current, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service = create(repo)
	current, err = service.Snapshot(ctx)
	if err != nil || current.Configuration.SystemPrompt != draft.SystemPrompt {
		t.Fatalf("restart lost configuration: %#v %v", current, err)
	}
	restored, err := service.Restore(ctx, current.Revision, "0", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision == current.Revision || restored.Configuration.SystemPrompt != base.Configuration.SystemPrompt {
		t.Fatal("reset must publish a new version")
	}
	replay, err := service.Restore(ctx, restored.Revision, current.Revision, "admin")
	if err != nil || replay.Configuration.SystemPrompt != draft.SystemPrompt {
		t.Fatalf("history restoration: %v", err)
	}
}

func customSkill(t *testing.T) catalog.SkillSchema {
	t.Helper()
	retired, err := catalog.RetiredBuiltInSkills()
	if err != nil {
		t.Fatal(err)
	}
	path := retired[0].ThumbnailPath
	return catalog.SkillSchema{ID: "own-skill", Name: "Own Skill", Category: "anime",
		RecommendedSize: "1536x1024", ThumbnailPath: path, RawImageURL: path,
		Prompt: "User-created style"}
}

func legacyBuiltInSkill(t *testing.T, name string) catalog.SkillSchema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var skill catalog.SkillSchema
	if err := json.Unmarshal(b, &skill); err != nil {
		t.Fatal(err)
	}
	return skill
}

func TestRetireBuiltInSkillsPreservesEditedAndCustomSkillsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaultSkills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	create := func(repository *sqlite.Repository) *agentconfiguration.Service {
		s, err := agentconfiguration.New(repository, product.Application(), defaultSkills, product.List(""), agentconfiguration.WithRetiredBuiltInSkills())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	service := create(repo)
	base, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if base.Configuration.Skills == nil || len(base.Configuration.Skills) != 0 {
		t.Fatalf("empty defaults must serialize as []: %#v", base.Configuration.Skills)
	}
	old := legacyBuiltInSkill(t, "legacy_builtin_skill.json")
	retired, err := catalog.RetiredBuiltInSkills()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := catalog.RetiredSkillFingerprint(old)
	if err != nil || old.ID != retired[0].ID || fingerprint != retired[0].Fingerprint {
		t.Fatalf("legacy fixture does not match packaged retirement identity: %v", err)
	}
	edited := legacyBuiltInSkill(t, "legacy_builtin_skill_2.json")
	if edited.ID != retired[1].ID {
		t.Fatal("second legacy fixture has unexpected ID")
	}
	edited.Prompt += " User-edited text."
	own := customSkill(t)
	cloned := old
	cloned.ID = "cloned-old-style"
	legacy := base.Configuration
	legacy.Skills = []catalog.SkillSchema{old, own, edited, cloned}
	first, err := service.Publish(ctx, base.Revision, "admin", "publish", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileRetiredBuiltInSkills(ctx); err != nil {
		t.Fatal(err)
	}
	active, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.Revision == first.Revision || len(active.Configuration.Skills) != 3 ||
		active.Configuration.Skills[0].ID != own.ID ||
		active.Configuration.Skills[1].Prompt != edited.Prompt ||
		active.Configuration.Skills[2].ID != cloned.ID {
		t.Fatalf("retirement changed user content: %#v", active)
	}
	if _, err := service.Publish(ctx, first.Revision, "admin", "publish", legacy); !errors.Is(err, agentconfiguration.ErrConflict) {
		t.Fatalf("retirement must preserve CAS: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service = create(repo)
	if err := service.ReconcileRetiredBuiltInSkills(ctx); err != nil {
		t.Fatal(err)
	}
	afterRestart, err := service.Snapshot(ctx)
	if err != nil || afterRestart.Revision != active.Revision {
		t.Fatalf("retirement was not idempotent after restart: %#v %v", afterRestart, err)
	}
	restored, err := service.Restore(ctx, active.Revision, first.Revision, "admin")
	if err != nil || len(restored.Configuration.Skills) != 3 {
		t.Fatalf("historical restore revived packaged Skill: %#v %v", restored, err)
	}
	reset, err := service.Restore(ctx, restored.Revision, "0", "admin")
	if err != nil || reset.Configuration.Skills == nil || len(reset.Configuration.Skills) != 0 {
		t.Fatalf("reset must produce an empty Skill list: %#v %v", reset, err)
	}
}

func TestExternalSkillCatalogDoesNotRetireMatchingSavedContent(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "external.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	old := legacyBuiltInSkill(t, "legacy_builtin_skill.json")
	external := catalog.SkillCatalogSchema{Revision: "external", Skills: []catalog.SkillSchema{old}}
	service, err := agentconfiguration.New(repo, product.Application(), external, product.List(""))
	if err != nil {
		t.Fatal(err)
	}
	base, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Publish(ctx, base.Revision, "admin", "publish", base.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileRetiredBuiltInSkills(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := service.Snapshot(ctx)
	if err != nil || current.Revision != first.Revision || len(current.Configuration.Skills) != 1 {
		t.Fatalf("external catalog content was retired: %#v %v", current, err)
	}
}

func TestSystemSkillsAreNotPublishableOrPersonal(t *testing.T) {
	ctx := context.Background()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "system.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := agentconfiguration.New(repo, product.Application(), defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	system := product.Application().Chat.Skills.SystemSkills
	if len(system) == 0 {
		t.Fatal("default Schema declares no system Skill")
	}
	for name, skill := range map[string]catalog.SkillSchema{
		"binds a tool":      {ID: "mine", Name: "Mine", RecommendedSize: "1536x1024", Prompt: "p", Tool: "create_three_view"},
		"reuses system id":  {ID: system[0].ID, Name: "Mine", RecommendedSize: "1536x1024", Prompt: "p"},
		"claims input need": {ID: "mine", Name: "Mine", RecommendedSize: "1536x1024", Prompt: "p", RequiresImage: true},
	} {
		candidate := snap.Configuration
		candidate.Skills = []catalog.SkillSchema{skill}
		if err := service.Validate(candidate); err == nil {
			t.Fatalf("%s: Validate accepted a published Skill", name)
		}
	}
}
