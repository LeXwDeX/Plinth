package catalog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func sourceConstraint(t *testing.T, offering schema.Offering) *schema.UploadedModel {
	t.Helper()
	for _, p := range offering.Parameters {
		if p.UploadedModel != nil {
			return p.UploadedModel
		}
	}
	t.Fatal("missing uploaded source")
	return nil
}
func TestModelSourceProfilesAreFrozenAndLookupCopiesAreIndependent(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	profile := c.Application().Runtime.InputAssets.ModelProfile
	for _, o := range c.List("") {
		for _, p := range o.Parameters {
			if p.UploadedModel != nil {
				u := p.UploadedModel
				if u.ProfileRevision != profile.Revision || u.ProfileLimits == nil || *u.ProfileLimits != profile.Limits {
					t.Fatalf("%s missing frozen parameter profile: %+v", o.ID, u)
				}
			}
		}
	}
	const id schema.OfferingID = "local/static-glb/to-gltf"
	assertFresh := func() {
		t.Helper()
		o, _ := c.Get(id)
		u := sourceConstraint(t, o)
		if u.ProfileLimits == nil || u.ProfileLimits.MaxBytes != profile.Limits.MaxBytes || u.ProfileRevision != profile.Revision {
			t.Fatal("Catalog was mutated by returned value")
		}
	}
	o, _ := c.Get(id)
	sourceConstraint(t, o).ProfileLimits.MaxBytes = 1
	sourceConstraint(t, o).ProfileRevision = "changed"
	assertFresh()
	for _, o = range c.List(schema.FormatConversion) {
		if o.ID == id {
			sourceConstraint(t, o).ProfileLimits.MaxBytes = 2
		}
	}
	assertFresh()
	o, _, err = c.Normalize(schema.FormatConversion, id, json.RawMessage(`{"source_asset":"owned-model"}`))
	if err != nil {
		t.Fatal(err)
	}
	sourceConstraint(t, o).ProfileLimits.MaxBytes = 3
	assertFresh()
}
func TestAuthoredParameterProfileSnapshotsAreRejected(t *testing.T) {
	for _, id := range []string{"meshy/processing/remesh", "local/static-glb/to-gltf", "tripo/format-conversion/uploaded-glb-to-stl"} {
		for _, field := range []string{"profile_revision", "profile_limits"} {
			t.Run(id+"/"+field, func(t *testing.T) {
				var doc map[string]any
				if err := json.Unmarshal(defaultCatalog, &doc); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, raw := range doc["offerings"].([]any) {
					o := raw.(map[string]any)
					if o["id"] != id {
						continue
					}
					for _, rawP := range o["parameters"].([]any) {
						p := rawP.(map[string]any)
						if u, ok := p["uploaded_model"].(map[string]any); ok {
							found = true
							if field == "profile_revision" {
								u[field] = "1"
							} else {
								u[field] = map[string]any{"max_bytes": 100}
							}
						}
					}
				}
				if !found {
					t.Fatal("fixture missing source")
				}
				raw, _ := json.Marshal(doc)
				if _, err := Load(bytes.NewReader(raw)); err == nil {
					t.Fatal("authored frozen snapshot accepted")
				}
			})
		}
	}
}
func TestMeshyModelProfileFreezesExactReference(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "wrong revision", "unknown profile", "authored limits", "foreign protocol", "unknown parameter", "missing uploaded route", "unknown field"} {
		t.Run(name, func(t *testing.T) {
			app := c.Application()
			p := app.Runtime.InputAssets.ModelProfile
			opts := map[string]any{"parameter": "source_asset", "profile_id": p.ID, "profile_revision": p.Revision, "max_instanced_triangles": 40000}
			o := schema.Offering{AdapterKey: "meshy.v2", Parameters: []schema.Parameter{{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, MaxBytes: p.Limits.MaxBytes, UploadedModel: &schema.UploadedModel{ProfileID: p.ID}}}}
			switch name {
			case "wrong revision":
				opts["profile_revision"] = "2"
			case "unknown profile":
				opts["profile_id"] = "foreign"
			case "authored limits":
				opts["profile_limits"] = p.Limits
			case "foreign protocol":
				o.AdapterKey = "tripo.v3"
			case "unknown parameter":
				opts["parameter"] = "missing"
			case "missing uploaded route":
				o.Parameters[0].UploadedModel = nil
			case "unknown field":
				opts["remote_url"] = "x"
			}
			raw, _ := json.Marshal(map[string]any{"model_input_profile": opts})
			o.ProviderOptions = raw
			err := freezeOfferingModelProfiles(&o, app)
			if name != "valid" {
				if err == nil {
					t.Fatal("invalid profile contract accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var frozen struct {
				Profile schema.ModelInputProfileOptions `json:"model_input_profile"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &frozen); err != nil {
				t.Fatal(err)
			}
			if frozen.Profile.ProfileLimits == nil || *frozen.Profile.ProfileLimits != p.Limits || sourceConstraint(t, o).ProfileLimits == nil {
				t.Fatal("reference not frozen")
			}
			sourceConstraint(t, o).ProfileLimits.MaxBytes = 1
			if frozen.Profile.ProfileLimits.MaxBytes != p.Limits.MaxBytes || c.Application().Runtime.InputAssets.ModelProfile.Limits.MaxBytes != p.Limits.MaxBytes {
				t.Fatal("profile snapshots alias")
			}
		})
	}
}
