package mcppublic

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// fakeSessions builds sessions like the chat service: a title, an owner and a
// project that must be the owner's.
type fakeSessions struct {
	mu       sync.Mutex
	next     int
	projects map[string]string // project ID to owner
	stored   map[string]chat.Session
}

func (f *fakeSessions) NewSessionInProject(_ context.Context, owner, name, title, project string) (chat.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if project != "" && f.projects[project] != owner {
		return chat.Session{}, chat.ErrProjectNotFound
	}
	f.next++
	return chat.Session{ID: "session-" + string(rune('0'+f.next)), OwnerID: owner, OwnerName: name, Title: title, ProjectID: project,
		CreatedAt: time.Date(2026, 11, 17, 0, 0, f.next, 0, time.UTC)}, nil
}

func (f *fakeSessions) Session(_ context.Context, owner, id string) (chat.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, found := f.stored[id]
	return session, found && session.OwnerID == owner, nil
}

// fakeStore keeps the mapping and stores the session with it, as the
// repository does in one transaction.
type fakeStore struct {
	sessions *fakeSessions
	mu       sync.Mutex
	assets   map[string]chat.MCPAsset
}

func (s *fakeStore) FindMCPAsset(_ context.Context, owner, key string) (chat.MCPAsset, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	asset, found := s.assets[owner+"\x00"+key]
	return asset, found, nil
}

func (s *fakeStore) CreateMCPAsset(_ context.Context, session chat.Session, key, name string) (chat.MCPAsset, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.assets[session.OwnerID+"\x00"+key]; found {
		return existing, false, nil
	}
	s.sessions.mu.Lock()
	s.sessions.stored[session.ID] = session
	s.sessions.mu.Unlock()
	asset := chat.MCPAsset{OwnerID: session.OwnerID, NameKey: key, Name: name, SessionID: session.ID, CreatedAt: session.CreatedAt}
	s.assets[session.OwnerID+"\x00"+key] = asset
	return asset, true, nil
}

func (s *fakeStore) MCPAssetsOfSessions(_ context.Context, owner string, ids []string) (map[string]chat.MCPAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := map[string]chat.MCPAsset{}
	for _, asset := range s.assets {
		for _, id := range ids {
			if asset.OwnerID == owner && asset.SessionID == id {
				found[id] = asset
			}
		}
	}
	return found, nil
}

func newTestAssets(t *testing.T) (*Assets, *fakeStore) {
	t.Helper()
	sessions := &fakeSessions{projects: map[string]string{"project-a": "owner-a"}, stored: map[string]chat.Session{}}
	store := &fakeStore{sessions: sessions, assets: map[string]chat.MCPAsset{}}
	assets, err := NewAssets(AssetsConfig{
		Schema:   catalog.MCPPublicAssets{NameMaxLength: 10, TitleTemplate: "MCP · {name}", DefaultTitle: "MCP · 默认"},
		Sessions: sessions, Store: store,
		Localize: func(locale, text string) string {
			if locale == "en" && text == "MCP · 默认" {
				return "MCP · Default"
			}
			return text
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return assets, store
}

func TestNormalizeAssetName(t *testing.T) {
	for name, test := range map[string]struct {
		input, key, display string
		valid               bool
	}{
		"plain":                {"Barrel", "barrel", "Barrel", true},
		"spaces folded":        {"  Big \t Barrel\n", "big barrel", "Big Barrel", true},
		"wide space":           {"木桶　二号", "木桶 二号", "木桶 二号", true},
		"case":                 {"BARREL", "barrel", "BARREL", true},
		"empty":                {"", "", "", false},
		"blank":                {" \t\n", "", "", false},
		"too long":             {"12345678901", "", "", false},
		"exactly the bound":    {"1234567890", "1234567890", "1234567890", true},
		"counted in letters":   {"一二三四五六七八九十", "一二三四五六七八九十", "一二三四五六七八九十", true},
		"control character":    {"a\x07b", "", "", false},
		"null":                 {"a\x00b", "", "", false},
		"not UTF-8":            {"a\xffb", "", "", false},
		"zero width is a name": {"a​b", "a​b", "a​b", true},
	} {
		key, display, err := NormalizeAssetName(test.input, 10)
		if (err == nil) != test.valid || key != test.key || display != test.display {
			t.Errorf("%s: key=%q display=%q err=%v", name, key, display, err)
		}
		if err != nil && !errors.Is(err, ErrInvalidAssetName) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestResolveMakesTheAssetOnceAndReusesIt(t *testing.T) {
	assets, store := newTestAssets(t)
	ctx := context.Background()
	first, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", OwnerName: "A", Name: " Barrel "})
	if err != nil || !first.Created || first.Name != "Barrel" || first.Title != "MCP · Barrel" {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	again, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "BARREL"})
	if err != nil || again.Created || again.SessionID != first.SessionID || again.Title != "MCP · Barrel" {
		t.Fatalf("again = %+v err=%v", again, err)
	}
	// The default asset has the Schema's title, in the request's language, and one per person.
	english, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Locale: "en"})
	if err != nil || !english.Created || english.Name != "" || english.Title != "MCP · Default" {
		t.Fatalf("default = %+v err=%v", english, err)
	}
	if reused, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "  "}); err != nil || reused.SessionID != english.SessionID {
		t.Fatalf("a blank name is the default asset: %+v err=%v", reused, err)
	}
	if other, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-b", Name: "Barrel"}); err != nil || other.SessionID == first.SessionID {
		t.Fatalf("another person's = %+v err=%v", other, err)
	}
	if len(store.assets) != 3 {
		t.Fatalf("mappings = %d", len(store.assets))
	}
	if _, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: strings.Repeat("x", 11)}); !errors.Is(err, ErrInvalidAssetName) {
		t.Fatalf("a long name: %v", err)
	}
	// Describe and Marked read the marker.
	described, found, err := assets.Describe(ctx, "owner-a", first.SessionID)
	if err != nil || !found || described.Name != "Barrel" || described.Title != "MCP · Barrel" {
		t.Fatalf("described = %+v %v %v", described, found, err)
	}
	if _, found, _ := assets.Describe(ctx, "owner-b", first.SessionID); found {
		t.Fatal("another person's asset was described")
	}
	marked, err := assets.Marked(ctx, "owner-a", []string{first.SessionID, "session-none"})
	if err != nil || !marked[first.SessionID] || marked["session-none"] {
		t.Fatalf("marked = %v err=%v", marked, err)
	}
}

func TestResolveProjectRules(t *testing.T) {
	assets, _ := newTestAssets(t)
	ctx := context.Background()
	inProject, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "Crate", ProjectID: "project-a"})
	if err != nil || inProject.ProjectID != "project-a" {
		t.Fatalf("in a project = %+v err=%v", inProject, err)
	}
	for _, project := range []string{"", "project-a", " project-a "} {
		if again, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "Crate", ProjectID: project}); err != nil || again.SessionID != inProject.SessionID {
			t.Errorf("project %q: %+v err=%v", project, again, err)
		}
	}
	if _, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "Crate", ProjectID: "project-b"}); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("another project: %v", err)
	}
	// The default asset is one per project, and one outside any project.
	plainDefault, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a"})
	if err != nil || plainDefault.ProjectID != "" {
		t.Fatalf("default outside a project = %+v err=%v", plainDefault, err)
	}
	projectDefault, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", ProjectID: "project-a"})
	if err != nil || !projectDefault.Created || projectDefault.ProjectID != "project-a" || projectDefault.SessionID == plainDefault.SessionID {
		t.Fatalf("default of a project = %+v err=%v", projectDefault, err)
	}
	if again, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", ProjectID: " project-a "}); err != nil || again.SessionID != projectDefault.SessionID {
		t.Fatalf("default of the project again = %+v err=%v", again, err)
	}
	if again, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a"}); err != nil || again.SessionID != plainDefault.SessionID {
		t.Fatalf("default outside a project again = %+v err=%v", again, err)
	}
	if _, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "Plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "Plain", ProjectID: "project-a"}); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("a project for an asset that has none: %v", err)
	}
	if _, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-b", Name: "Theirs", ProjectID: "project-a"}); !errors.Is(err, chat.ErrProjectNotFound) {
		t.Fatalf("a foreign project: %v", err)
	}
	if _, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "Missing", ProjectID: "project-none"}); !errors.Is(err, chat.ErrProjectNotFound) {
		t.Fatalf("an unknown project: %v", err)
	}
}

func TestResolveFindOnlyMakesNothing(t *testing.T) {
	assets, store := newTestAssets(t)
	ctx := context.Background()
	if _, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "Barrel", FindOnly: true}); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("a missing asset: %v", err)
	}
	if len(store.assets) != 0 {
		t.Fatal("a lookup made an asset")
	}
	made, _ := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "Barrel"})
	if found, err := assets.Resolve(ctx, AssetRequest{OwnerID: "owner-a", Name: "barrel", FindOnly: true}); err != nil || found.SessionID != made.SessionID || found.Created {
		t.Fatalf("found = %+v err=%v", found, err)
	}
}

func TestNewAssetsNeedsItsParts(t *testing.T) {
	schema := catalog.MCPPublicAssets{NameMaxLength: 5, TitleTemplate: "MCP · {name}", DefaultTitle: "default"}
	sessions := &fakeSessions{}
	store := &fakeStore{}
	for name, config := range map[string]AssetsConfig{
		"no sessions":      {Schema: schema, Store: store},
		"no store":         {Schema: schema, Sessions: sessions},
		"no bound":         {Schema: catalog.MCPPublicAssets{TitleTemplate: "{name}", DefaultTitle: "d"}, Sessions: sessions, Store: store},
		"title lacks name": {Schema: catalog.MCPPublicAssets{NameMaxLength: 5, TitleTemplate: "MCP", DefaultTitle: "d"}, Sessions: sessions, Store: store},
		"no default title": {Schema: catalog.MCPPublicAssets{NameMaxLength: 5, TitleTemplate: "{name}"}, Sessions: sessions, Store: store},
	} {
		if _, err := NewAssets(config); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
