package generation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// #467: the duplicate paid-conversion guard must not fail open. Two things
// used to disable it silently: a Module assembled without a conversion rule
// (the kernel then "knew" no Task was a conversion) and an admission that
// trusted the caller's Task.Conversion instead of the frozen Offering and
// input. The conversion rule is now a required dependency, and createTask
// names the declared conversion itself.

// conversionGuard467Rule recognizes exactly one Offering as a declared
// conversion, so a Task of any other Offering is an ordinary Task.
type conversionGuard467Rule struct {
	offering OfferingID
	source   TaskID
	formats  []string
}

func (r conversionGuard467Rule) IsConversion(id OfferingID, _ json.RawMessage) bool {
	return id == r.offering
}

func (r conversionGuard467Rule) ConversionIntent(offering Offering, _ json.RawMessage) (ConversionIntent, bool) {
	if offering.ID != r.offering {
		return ConversionIntent{}, false
	}
	return ConversionIntent{SourceTaskID: r.source, Formats: r.formats}, true
}

func (r conversionGuard467Rule) PendingFormats(Task) []string { return nil }

// conversionGuard467Repo is a Repository that only counts admissions. It does
// not implement ConversionGuardedRepository, so an admission that reaches the
// guard cannot be created at all.
type conversionGuard467Repo struct {
	Repository
	created int
}

func (r *conversionGuard467Repo) Create(_ context.Context, task Task, _ Event, _ int) (Task, bool, error) {
	r.created++
	return task, false, nil
}

// conversionGuard467GuardedRepo adds the transactional guard, so an admission
// that reaches it is recorded separately from a plain Create.
type conversionGuard467GuardedRepo struct {
	conversionGuard467Repo
	guarded int
}

func (r *conversionGuard467GuardedRepo) CreateConversionGuarded(
	_ context.Context, task Task, _ Event, _ int, _ ConversionConflictChecker,
) (Task, bool, error) {
	r.guarded++
	return task, false, nil
}

func conversionGuard467Dependencies(repository Repository, rule DeliveryConversionRule) Dependencies {
	return Dependencies{
		Repository:  repository,
		Catalog:     struct{ Catalog }{},
		Providers:   struct{ ProviderRegistry }{},
		Assets:      struct{ AssetStore }{},
		Conversions: rule,
	}
}

func conversionGuard467Task(offering OfferingID) Task {
	return Task{
		ID:       "task-1",
		OwnerID:  "owner-a",
		Offering: Offering{ID: offering},
		Input:    json.RawMessage(`{"source_task":"source-1"}`),
		State:    TaskQueued,
	}
}

// TestNewRequiresConversionRule467: a Module built without a conversion rule
// cannot tell a paid conversion from an ordinary Task, so construction fails
// instead of silently disabling the guard.
func TestNewRequiresConversionRule467(t *testing.T) {
	deps := conversionGuard467Dependencies(&conversionGuard467Repo{}, conversionGuard467Rule{})
	deps.Conversions = nil
	module, err := New(deps)
	if err == nil {
		t.Fatalf("New accepted a missing conversion rule: module=%v", module)
	}
	if !strings.Contains(err.Error(), "Conversions") {
		t.Fatalf("New error = %v, want it to name the missing conversion rule", err)
	}
}

// TestNewAcceptsNoConversionsRule467: a Product Schema that declares no format
// conversion says so explicitly with NoConversions, and stays admissible.
func TestNewAcceptsNoConversionsRule467(t *testing.T) {
	module, err := New(conversionGuard467Dependencies(&conversionGuard467Repo{}, NoConversions))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if intent, ok := module.declaredConversion(Offering{ID: "meshy/processing/convert"}, nil); ok {
		t.Fatalf("NoConversions recognized a conversion: %+v", intent)
	}
}

// TestCreateTaskDerivesDeclaredConversion467: the caller left Task.Conversion
// nil, but the frozen Offering and input are a declared conversion, so the
// admission must still go through the transactional guard.
func TestCreateTaskDerivesDeclaredConversion467(t *testing.T) {
	repo := &conversionGuard467GuardedRepo{}
	module, err := New(conversionGuard467Dependencies(repo, conversionGuard467Rule{
		offering: "tripo/tripo-v3.1/format-conversion",
		source:   "source-1",
		formats:  []string{"glb"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := module.createTask(context.Background(), conversionGuard467Task("tripo/tripo-v3.1/format-conversion"), Event{}); err != nil {
		t.Fatalf("createTask: %v", err)
	}
	if repo.guarded != 1 || repo.created != 0 {
		t.Fatalf("guarded=%d created=%d, want the declared conversion to reach the guard only", repo.guarded, repo.created)
	}
}

// TestCreateTaskRefusesUnguardedRepositoryForDerivedConversion467: the same
// admission against a repository without the guard must fail, because an
// unguarded first check would race another submission.
func TestCreateTaskRefusesUnguardedRepositoryForDerivedConversion467(t *testing.T) {
	repo := &conversionGuard467Repo{}
	module, err := New(conversionGuard467Dependencies(repo, conversionGuard467Rule{
		offering: "tripo/tripo-v3.1/format-conversion",
		source:   "source-1",
		formats:  []string{"glb"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := module.createTask(context.Background(), conversionGuard467Task("tripo/tripo-v3.1/format-conversion"), Event{}); err == nil {
		t.Fatal("createTask admitted a declared conversion through an unguarded repository")
	}
	if repo.created != 0 {
		t.Fatalf("created=%d, want the conversion refused before Create", repo.created)
	}
}

// TestCreateTaskKeepsCallerDeclaredConversionGuarded467: the conservative side
// of the union. The rule no longer recognizes the Offering, but the caller
// declared a conversion, so the guard still applies.
func TestCreateTaskKeepsCallerDeclaredConversionGuarded467(t *testing.T) {
	repo := &conversionGuard467GuardedRepo{}
	module, err := New(conversionGuard467Dependencies(repo, conversionGuard467Rule{}))
	if err != nil {
		t.Fatal(err)
	}
	task := conversionGuard467Task("tripo/tripo-v3.1/format-conversion")
	task.Conversion = &ConversionIntent{SourceTaskID: "source-1", Formats: []string{"glb"}}
	if _, _, err := module.createTask(context.Background(), task, Event{}); err != nil {
		t.Fatalf("createTask: %v", err)
	}
	if repo.guarded != 1 || repo.created != 0 {
		t.Fatalf("guarded=%d created=%d, want the caller-declared conversion to reach the guard only", repo.guarded, repo.created)
	}
}

// TestCreateTaskLeavesOrdinaryTaskOnCreatePath467: a Task no rule recognizes
// and whose caller declared no conversion stays on the plain Create path, so
// the fix does not turn every admission into a guarded one.
func TestCreateTaskLeavesOrdinaryTaskOnCreatePath467(t *testing.T) {
	repo := &conversionGuard467GuardedRepo{}
	module, err := New(conversionGuard467Dependencies(repo, conversionGuard467Rule{
		offering: "tripo/tripo-v3.1/format-conversion",
		source:   "source-1",
		formats:  []string{"glb"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := module.createTask(context.Background(), conversionGuard467Task("tripo/tripo-v3.1/text-to-3d"), Event{}); err != nil {
		t.Fatalf("createTask: %v", err)
	}
	if repo.created != 1 || repo.guarded != 0 {
		t.Fatalf("created=%d guarded=%d, want the ordinary Task on the plain Create path", repo.created, repo.guarded)
	}
}
