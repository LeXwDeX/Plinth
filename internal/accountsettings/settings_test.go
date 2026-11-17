package accountsettings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

// memoryStore is the version rules of the data layer, in memory.
type memoryStore struct {
	rows   map[string]Settings
	writes int
}

func (m *memoryStore) AccountSettings(_ context.Context, owner string) (Settings, bool, error) {
	row, found := m.rows[owner]
	return row, found, nil
}

func (m *memoryStore) PutAccountSettings(_ context.Context, owner string, expected int64, next Settings, now time.Time) (Settings, error) {
	row, found := m.rows[owner]
	if (!found && expected != 0) || (found && row.Version != expected) {
		return Settings{}, ErrConflict
	}
	next.Version, next.UpdatedAt = expected+1, now
	if m.rows == nil {
		m.rows = map[string]Settings{}
	}
	m.rows[owner] = next
	m.writes++
	return next, nil
}

func testService(t *testing.T) (*Service, *memoryStore) {
	t.Helper()
	store := &memoryStore{}
	service, err := New(store, Config{
		Modes: []string{approvalpolicy.StepModeAgent, approvalpolicy.StepModeUnlimited, approvalpolicy.StepModeCap},
		Steps: approvalpolicy.StepsPolicy{
			DefaultList: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
			DefaultMCP:  approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
		},
		CapMinimum: 1, CapMaximum: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func agentSetting(limit int) approvalpolicy.StepSetting {
	return approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: limit}
}

func capSetting(limit int) approvalpolicy.StepSetting {
	return approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: limit}
}

func TestNewRefusesAnUnusableConfiguration(t *testing.T) {
	good := Config{
		Modes:      []string{approvalpolicy.StepModeAgent},
		Steps:      approvalpolicy.StepsPolicy{DefaultList: agentSetting(20), DefaultMCP: agentSetting(20)},
		CapMinimum: 1, CapMaximum: 200,
	}
	if _, err := New(&memoryStore{}, good); err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, good); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no store: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"minimum zero":          func(c *Config) { c.CapMinimum = 0 },
		"maximum below minimum": func(c *Config) { c.CapMinimum, c.CapMaximum = 10, 5 },
		"default mode unknown":  func(c *Config) { c.Steps.DefaultList.Mode = "turbo" },
		"default mode missing":  func(c *Config) { c.Steps.DefaultMCP.Mode = "" },
		"default cap too big":   func(c *Config) { c.Steps.DefaultList.Cap = 201 },
		"default cap too small": func(c *Config) { c.Steps.DefaultMCP.Cap = 0 },
		"no modes":              func(c *Config) { c.Modes = nil },
	} {
		config := good
		config.Steps = good.Steps
		mutate(&config)
		if _, err := New(&memoryStore{}, config); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestGetShowsTheDefaultsAtVersionZeroUntilSaved(t *testing.T) {
	service, store := testService(t)
	got, err := service.Get(context.Background(), "owner-a")
	if err != nil || got.Version != 0 || got.List != agentSetting(20) || got.MCP != agentSetting(20) {
		t.Fatalf("defaults = %+v err=%v", got, err)
	}
	if len(store.rows) != 0 {
		t.Fatal("reading wrote a row")
	}
	if _, err := service.Get(context.Background(), " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank owner: %v", err)
	}
}

func TestPutSavesAndBumpsTheVersion(t *testing.T) {
	service, _ := testService(t)
	ctx := context.Background()
	first, err := service.Put(ctx, "owner-a", 0, capSetting(30), agentSetting(20))
	if err != nil || !first.Changed || first.Settings.Version != 1 || first.Previous.Version != 0 ||
		first.Previous.List != agentSetting(20) || first.Settings.List != capSetting(30) {
		t.Fatalf("first save = %+v err=%v", first, err)
	}
	second, err := service.Put(ctx, "owner-a", 1, capSetting(30), approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeUnlimited, Cap: 20})
	if err != nil || !second.Changed || second.Settings.Version != 2 || second.Previous.MCP != agentSetting(20) {
		t.Fatalf("second save = %+v err=%v", second, err)
	}
	read, err := service.Get(ctx, "owner-a")
	if err != nil || read.Version != 2 || read.MCP.Mode != approvalpolicy.StepModeUnlimited {
		t.Fatalf("read = %+v err=%v", read, err)
	}
}

func TestPutWithTheValuesAlreadyInForceWritesNothing(t *testing.T) {
	service, store := testService(t)
	ctx := context.Background()
	// The defaults are in force before anything is saved.
	unchanged, err := service.Put(ctx, "owner-a", 0, agentSetting(20), agentSetting(20))
	if err != nil || unchanged.Changed || unchanged.Settings.Version != 0 || store.writes != 0 {
		t.Fatalf("defaults = %+v writes=%d err=%v", unchanged, store.writes, err)
	}
	if _, err := service.Put(ctx, "owner-a", 0, capSetting(5), agentSetting(20)); err != nil {
		t.Fatal(err)
	}
	same, err := service.Put(ctx, "owner-a", 1, capSetting(5), agentSetting(20))
	if err != nil || same.Changed || same.Settings.Version != 1 || store.writes != 1 {
		t.Fatalf("same values = %+v writes=%d err=%v", same, store.writes, err)
	}
}

func TestPutRefusesAStaleVersion(t *testing.T) {
	service, store := testService(t)
	ctx := context.Background()
	if _, err := service.Put(ctx, "owner-a", 0, capSetting(5), agentSetting(20)); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []int64{0, 2, 9} {
		if _, err := service.Put(ctx, "owner-a", expected, capSetting(6), agentSetting(20)); !errors.Is(err, ErrConflict) {
			t.Fatalf("expected %d: %v", expected, err)
		}
	}
	// A request that holds the values in force still needs the version the caller read.
	if _, err := service.Put(ctx, "owner-a", 0, capSetting(5), agentSetting(20)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale same values: %v", err)
	}
	if store.writes != 1 {
		t.Fatalf("a refused save wrote: %d", store.writes)
	}
}

func TestPutRefusesUnknownModesAndCapsOutsideTheRange(t *testing.T) {
	service, store := testService(t)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"unknown mode": func() error {
			_, err := service.Put(ctx, "owner-a", 0, approvalpolicy.StepSetting{Mode: "turbo", Cap: 5}, agentSetting(20))
			return err
		},
		"empty mode": func() error {
			_, err := service.Put(ctx, "owner-a", 0, agentSetting(20), approvalpolicy.StepSetting{Cap: 5})
			return err
		},
		"cap too small": func() error { _, err := service.Put(ctx, "owner-a", 0, capSetting(0), agentSetting(20)); return err },
		"cap too big":   func() error { _, err := service.Put(ctx, "owner-a", 0, agentSetting(20), capSetting(201)); return err },
		// The cap is stored whatever the mode, so it is bounded whatever the mode.
		"cap too big for the agent mode": func() error {
			_, err := service.Put(ctx, "owner-a", 0, agentSetting(500), agentSetting(20))
			return err
		},
		"negative version": func() error {
			_, err := service.Put(ctx, "owner-a", -1, agentSetting(20), agentSetting(20))
			return err
		},
		"blank owner": func() error { _, err := service.Put(ctx, "", 0, agentSetting(20), agentSetting(20)); return err },
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if store.writes != 0 {
		t.Fatalf("an invalid save wrote: %d", store.writes)
	}
	// The bounds themselves are allowed.
	if _, err := service.Put(ctx, "owner-a", 0, capSetting(1), capSetting(200)); err != nil {
		t.Fatalf("bounds: %v", err)
	}
}

func TestSettingsAreKeptPerOwner(t *testing.T) {
	service, _ := testService(t)
	ctx := context.Background()
	if _, err := service.Put(ctx, "owner-a", 0, capSetting(5), capSetting(6)); err != nil {
		t.Fatal(err)
	}
	other, err := service.Get(ctx, "owner-b")
	if err != nil || other.Version != 0 || other.List != agentSetting(20) {
		t.Fatalf("another owner reads %+v err=%v", other, err)
	}
	// Another owner's first save is not in conflict with a stranger's row.
	if _, err := service.Put(ctx, "owner-b", 0, agentSetting(7), agentSetting(8)); err != nil {
		t.Fatal(err)
	}
}

func TestEntryReadsTheNamedSetting(t *testing.T) {
	settings := Settings{List: capSetting(3), MCP: agentSetting(4)}
	if list, ok := settings.Entry(approvalpolicy.StepEntryList); !ok || list != capSetting(3) {
		t.Fatalf("list = %+v %v", list, ok)
	}
	if mcp, ok := settings.Entry(approvalpolicy.StepEntryMCP); !ok || mcp != agentSetting(4) {
		t.Fatalf("mcp = %+v %v", mcp, ok)
	}
	if _, ok := settings.Entry("web"); ok {
		t.Fatal("an unknown entry has a setting")
	}
}
