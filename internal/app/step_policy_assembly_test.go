package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #330: the assembly turns application.step_policy into the internal steps
// mode and the person's settings service. The steps mode is enabled but never
// offered, so no session can be put into it by a person and the three Schema
// modes behave exactly as before.
func TestDefaultSchemaEnablesTheInternalStepsModeWithoutOfferingIt(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	policy := approvalPolicyFromSchema(application)
	if !policy.StepsEnabled() || policy.NormalizeMode(approvalpolicy.ModeSteps) != approvalpolicy.ModeSteps {
		t.Fatal("the shipped Schema declares a step policy but the steps mode is not enabled")
	}
	if policy.Allows(approvalpolicy.ModeSteps) || slices.Contains(policy.Modes, approvalpolicy.ModeSteps) {
		t.Fatalf("the steps mode is offered to people: %v", policy.Modes)
	}
	if want := []string{approvalpolicy.ModeAuto, approvalpolicy.ModeBudget, approvalpolicy.ModeManual}; !slices.Equal(
		slices.Sorted(slices.Values(policy.Modes)), want) {
		t.Fatalf("offered modes = %v", policy.Modes)
	}
	// Counted capabilities are exactly the declared 3D ones; no 2D capability is.
	for _, counted := range []string{"text-to-3d", "image-to-3d", "retexture", "model-split"} {
		if !policy.Steps.Counts(counted) {
			t.Errorf("%s is not counted", counted)
		}
	}
	for _, uncounted := range []string{"text-to-image", "image-to-image", "background-removal", "image-to-multiview"} {
		if policy.Steps.Counts(uncounted) {
			t.Errorf("%s is counted", uncounted)
		}
	}
	if list, _ := policy.Steps.Default(approvalpolicy.StepEntryList); list.Mode != approvalpolicy.StepModeAgent || list.Cap != 20 {
		t.Fatalf("list default = %+v", list)
	}
	if mcp, _ := policy.Steps.Default(approvalpolicy.StepEntryMCP); mcp.Mode != approvalpolicy.StepModeAgent || mcp.Cap != 20 {
		t.Fatalf("mcp default = %+v", mcp)
	}
	// A stored or requested "steps" can never have come from a person; the
	// existing modes decide as before.
	if !approvalpolicy.RequiresHuman(policy, approvalpolicy.ModeManual, "image-to-3d") ||
		approvalpolicy.RequiresHuman(policy, approvalpolicy.ModeAuto, "image-to-3d") {
		t.Fatal("enabling the steps mode changed manual or auto")
	}

	// A Catalog without the section has no steps mode and no settings service.
	application.StepPolicy = nil
	if approvalPolicyFromSchema(application).StepsEnabled() {
		t.Fatal("a Catalog without a step policy enabled the steps mode")
	}
	service, err := accountSettingsFromSchema(nil, nil)
	if err != nil || service != nil {
		t.Fatalf("settings service without a step policy = %v err=%v", service, err)
	}
}

type memoryAccountStore struct {
	rows map[string]accountsettings.Settings
}

func (m *memoryAccountStore) AccountSettings(_ context.Context, owner string) (accountsettings.Settings, bool, error) {
	row, found := m.rows[owner]
	return row, found, nil
}

func (m *memoryAccountStore) PutAccountSettings(_ context.Context, owner string, expected int64, next accountsettings.Settings, now time.Time) (accountsettings.Settings, error) {
	if row, found := m.rows[owner]; (!found && expected != 0) || (found && row.Version != expected) {
		return accountsettings.Settings{}, accountsettings.ErrConflict
	}
	next.Version, next.UpdatedAt = expected+1, now
	if m.rows == nil {
		m.rows = map[string]accountsettings.Settings{}
	}
	m.rows[owner] = next
	return next, nil
}

func TestAccountSettingsServiceFollowsTheDeclaredModesAndCapRange(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	declared := product.Application().StepPolicy
	service, err := accountSettingsFromSchema(&memoryAccountStore{}, declared)
	if err != nil || service == nil {
		t.Fatalf("service = %v err=%v", service, err)
	}
	if minimum, maximum := service.CapRange(); minimum != declared.Cap.Minimum || maximum != declared.Cap.Maximum {
		t.Fatalf("cap range = %d..%d", minimum, maximum)
	}
	if !slices.Equal(service.Modes(), []string{"agent", "unlimited", "cap"}) {
		t.Fatalf("modes = %v", service.Modes())
	}
	got, err := service.Get(context.Background(), "owner-a")
	if err != nil || got.Version != 0 || got.List.Mode != "agent" || got.MCP.Mode != "agent" || got.List.Cap != 20 {
		t.Fatalf("defaults = %+v err=%v", got, err)
	}
	// A declared default the service cannot hold fails the assembly.
	broken := *declared
	broken.DefaultMode.List = "turbo"
	if _, err := accountSettingsFromSchema(&memoryAccountStore{}, &broken); !errors.Is(err, accountsettings.ErrInvalid) {
		t.Fatalf("unusable default: %v", err)
	}
}
