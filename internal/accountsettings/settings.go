// Package accountsettings stores the settings a person keeps for their own
// account. The first one is the 3D step mode (#330): how many 3D steps an
// asset list or an external MCP session may use. The package validates a
// change against the Product Schema's modes and cap range and enforces the
// optimistic version; it never decides a grant (internal/executiongrant reads
// the stored values inside its own transaction).
package accountsettings

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

var (
	// ErrInvalid means the owner, the version or a setting is not acceptable:
	// an unknown mode or a cap outside the Schema's range.
	ErrInvalid = errors.New("account settings: invalid")
	// ErrConflict means the stored version is not the version the caller read.
	ErrConflict = errors.New("account settings: version conflict")
)

// Settings is one person's settings. Version is 0 while the person never saved
// anything (the Schema defaults apply) and counts the saves afterwards.
type Settings struct {
	Version   int64
	List      approvalpolicy.StepSetting
	MCP       approvalpolicy.StepSetting
	UpdatedAt time.Time
}

// Entry returns the setting of one entry; the second result is false for an
// entry that does not exist.
func (s Settings) Entry(entry string) (approvalpolicy.StepSetting, bool) {
	switch entry {
	case approvalpolicy.StepEntryList:
		return s.List, true
	case approvalpolicy.StepEntryMCP:
		return s.MCP, true
	}
	return approvalpolicy.StepSetting{}, false
}

// SameValues reports whether two settings hold the same modes and caps,
// whatever their version.
func (s Settings) SameValues(other Settings) bool {
	return s.List == other.List && s.MCP == other.MCP
}

// Store keeps the rows. The sqlite repository implements it.
type Store interface {
	// AccountSettings returns the person's stored settings; found is false
	// when none exist.
	AccountSettings(ctx context.Context, ownerID string) (Settings, bool, error)
	// PutAccountSettings stores next as the person's settings, but only when
	// the stored version is expected (0 means no row yet). It returns the
	// stored settings and ErrConflict when the version moved.
	PutAccountSettings(ctx context.Context, ownerID string, expected int64, next Settings, now time.Time) (Settings, error)
}

// Config is the Product Schema's side of the settings.
type Config struct {
	// Modes are the modes a person may pick.
	Modes []string
	// Steps holds the settings of a person who never saved one.
	Steps approvalpolicy.StepsPolicy
	// CapMinimum and CapMaximum bound the cap a person may save.
	CapMinimum, CapMaximum int
}

// Service reads and changes a person's settings.
type Service struct {
	store  Store
	config Config
	clock  func() time.Time
}

// New builds the service. The defaults must themselves be valid settings, so a
// Schema that declares an unusable default never starts.
func New(store Store, config Config) (*Service, error) {
	if store == nil || config.CapMinimum < 1 || config.CapMaximum < config.CapMinimum {
		return nil, ErrInvalid
	}
	config.Modes = slices.Clone(config.Modes)
	service := &Service{store: store, config: config, clock: time.Now}
	defaults := service.Defaults()
	if service.validate(defaults.List) != nil || service.validate(defaults.MCP) != nil {
		return nil, ErrInvalid
	}
	return service, nil
}

// SetClock replaces the time source; tests only.
func (s *Service) SetClock(clock func() time.Time) { s.clock = clock }

// Modes lists the modes a person may pick, in Schema order.
func (s *Service) Modes() []string { return slices.Clone(s.config.Modes) }

// CapRange is the smallest and largest cap a person may save.
func (s *Service) CapRange() (minimum, maximum int) {
	return s.config.CapMinimum, s.config.CapMaximum
}

// Defaults is what a person who never saved anything has (version 0).
func (s *Service) Defaults() Settings {
	return Settings{List: s.config.Steps.DefaultList, MCP: s.config.Steps.DefaultMCP}
}

// Get returns the person's settings, or the Schema defaults at version 0.
func (s *Service) Get(ctx context.Context, ownerID string) (Settings, error) {
	if strings.TrimSpace(ownerID) == "" {
		return Settings{}, ErrInvalid
	}
	stored, found, err := s.store.AccountSettings(ctx, ownerID)
	if err != nil {
		return Settings{}, err
	}
	if !found {
		return s.Defaults(), nil
	}
	return stored, nil
}

// Change is the outcome of Put. Previous is what the person had before (the
// defaults when nothing was saved); Changed is false when the request held the
// values already in force, in which case nothing was written.
type Change struct {
	Settings Settings
	Previous Settings
	Changed  bool
}

// Put saves both settings when expected is the version the caller read: 0 when
// the person never saved anything. A request that holds the values already in
// force writes nothing and keeps the version. An unknown mode or a cap outside
// the Schema's range is ErrInvalid; a moved version is ErrConflict.
func (s *Service) Put(ctx context.Context, ownerID string, expected int64, list, mcp approvalpolicy.StepSetting) (Change, error) {
	if strings.TrimSpace(ownerID) == "" || expected < 0 || s.validate(list) != nil || s.validate(mcp) != nil {
		return Change{}, ErrInvalid
	}
	current, err := s.Get(ctx, ownerID)
	if err != nil {
		return Change{}, err
	}
	if current.Version != expected {
		return Change{}, ErrConflict
	}
	next := Settings{List: list, MCP: mcp}
	if current.SameValues(next) {
		return Change{Settings: current, Previous: current}, nil
	}
	stored, err := s.store.PutAccountSettings(ctx, ownerID, expected, next, s.clock().UTC())
	if err != nil {
		return Change{}, err
	}
	return Change{Settings: stored, Previous: current, Changed: true}, nil
}

func (s *Service) validate(setting approvalpolicy.StepSetting) error {
	if !slices.Contains(s.config.Modes, setting.Mode) ||
		setting.Cap < s.config.CapMinimum || setting.Cap > s.config.CapMaximum {
		return ErrInvalid
	}
	return nil
}
