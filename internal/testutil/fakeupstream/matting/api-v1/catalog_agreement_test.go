package mattingapiv1

import (
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// I23: the fake and the product Schema hold the same bounds. The Offering's
// parameters are what the Adapter sends and what the UI offers; the fake's
// PipelineLimits and UploadLimits are what the service is held to. A bound moved
// on one side without the other fails here, before a real batch is spent.
func TestFakeLimitsAreTheShippedOfferingsDeclaredBounds(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := loaded.Get("matting/default/background-removal")
	if !ok {
		t.Fatal("background-removal offering missing")
	}
	parameters := map[string]schema.Parameter{}
	for _, parameter := range offering.Parameters {
		parameters[parameter.Name] = parameter
	}
	for _, limit := range PipelineLimits() {
		parameter, ok := parameters[limit.Field]
		if !ok {
			t.Errorf("the Offering declares no %s", limit.Field)
			continue
		}
		if limit.Kind == KindBoolean {
			if parameter.Type != "boolean" {
				t.Errorf("%s: the Offering declares type %q, the fake a boolean", limit.Field, parameter.Type)
			}
			continue
		}
		if parameter.Type != "number" || parameter.Minimum == nil || parameter.Maximum == nil ||
			*parameter.Minimum != limit.Min || *parameter.Maximum != limit.Max || parameter.Integer != limit.Integer {
			t.Errorf("%s: Offering %v %v..%v integer=%v, fake %v..%v integer=%v", limit.Field, parameter.Type,
				parameter.Minimum, parameter.Maximum, parameter.Integer, limit.Min, limit.Max, limit.Integer)
		}
	}
	for name := range parameters {
		if name == "source_assets" {
			continue
		}
		found := false
		for _, limit := range PipelineLimits() {
			found = found || limit.Field == name
		}
		if !found {
			t.Errorf("the Offering declares %s and the fake has no limit for it", name)
		}
	}
	files, ok := parameters["source_assets"]
	if !ok {
		t.Fatal("the Offering declares no source_assets")
	}
	limits := UploadLimits()
	if files.MaxItems != limits.MaxFiles || files.MaxBytes != limits.MaxFileBytes {
		t.Errorf("source_assets: %d files of %d bytes, the fake %d of %d", files.MaxItems, files.MaxBytes, limits.MaxFiles, limits.MaxFileBytes)
	}
	if !slices.Equal(files.Extensions, limits.Extensions) {
		t.Errorf("source_assets extensions %v, the fake %v", files.Extensions, limits.Extensions)
	}
}
