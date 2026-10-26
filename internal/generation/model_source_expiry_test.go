package generation

import (
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #325: a Provider keeps its own job only for a while (Meshy: at most three
// days). Past the declared age the upstream reference is treated as gone, so
// the binder takes the saved model file when the Offering accepts one, and
// otherwise refuses with the expiry as the reason.
const twoDays = 2 * 24 * 60 * 60

func expiringTaskParameter() Parameter {
	return Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask,
		SameProvider: true, TaskCapabilities: []Capability{"text-to-3d"}, UpstreamJobMaxAgeSeconds: twoDays}
}

func agedSource(age time.Duration, now time.Time) Task {
	task := modelSourceTask("vendor", "text-to-3d", savedModel("a1", "glb"))
	task.CreatedAt = now.Add(-age)
	return task
}

func TestBindModelSourceKeepsTheTaskRouteWhileTheUpstreamJobIsFresh(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	offering := modelSourceOffering(expiringTaskParameter(),
		Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset})
	binding, err := BindModelSourceAt(offering, agedSource(47*time.Hour, now), "", nil, now)
	if err != nil || binding.Parameter != "source_task" || binding.Value != "task-1" {
		t.Fatalf("fresh binding = %+v, %v", binding, err)
	}
}

func TestBindModelSourceUsesTheSavedFileOnceTheUpstreamJobExpired(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	offering := modelSourceOffering(expiringTaskParameter(),
		Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset})
	for _, age := range []time.Duration{48 * time.Hour, 9 * 24 * time.Hour} {
		binding, err := BindModelSourceAt(offering, agedSource(age, now), "", nil, now)
		if err != nil || binding.Parameter != "source_asset" || binding.Value != "a1" || binding.AssetID != "a1" {
			t.Fatalf("age %s: binding = %+v, %v", age, binding, err)
		}
	}
}

func TestBindModelSourceRefusesAnExpiredJobWithoutAFileRoute(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	offering := modelSourceOffering(expiringTaskParameter())
	_, err := BindModelSourceAt(offering, agedSource(72*time.Hour, now), "", nil, now)
	var domain *Error
	if !errors.As(err, &domain) || domain.Code != ErrInvalidInput || !errors.Is(err, ErrSourceExpired) {
		t.Fatalf("err = %v; want ErrInvalidInput wrapping ErrSourceExpired", err)
	}
	// Any other refusal is not reported as an expiry.
	foreign := agedSource(72*time.Hour, now)
	foreign.Offering.ProviderID = "other"
	if _, err := BindModelSourceAt(offering, foreign, "", nil, now); err == nil || errors.Is(err, ErrSourceExpired) {
		t.Fatalf("foreign provider err = %v; want a non-expiry refusal", err)
	}
}

func TestUpstreamJobExpiredOnlyWhenDeclared(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	undeclared := expiringTaskParameter()
	undeclared.UpstreamJobMaxAgeSeconds = 0
	if UpstreamJobExpired(undeclared, agedSource(30*24*time.Hour, now), now) {
		t.Fatal("a parameter without a declared age expired")
	}
	assets := expiringTaskParameter()
	assets.TaskSourceKind = schema.TaskSourceAssets
	if UpstreamJobExpired(assets, agedSource(30*24*time.Hour, now), now) {
		t.Fatal("a stored-bytes dependency expired")
	}
	if UpstreamJobExpired(expiringTaskParameter(), Task{ID: "t"}, now) {
		t.Fatal("a Task without a creation time expired")
	}
	if !UpstreamJobExpired(expiringTaskParameter(), agedSource(48*time.Hour, now), now) {
		t.Fatal("a job at the declared age did not expire")
	}
}
