package catalog

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestLocalConversionFreezesReferencedProfile(t *testing.T) {
	application := ApplicationSchema{Runtime: RuntimeSchema{ProviderConnections: []ProviderConnectionSchema{{Protocol: "local-model", AdapterKey: "local.model"}}, InputAssets: InputAssetRuntimeSchema{ModelProfile: &ModelInputProfile{ID: "static-glb-v1", Revision: "1", Limits: ModelInputLimits{MaxBytes: 10 << 20, MaxWork: 128 << 20}}}}}
	for _, test := range []string{"valid", "foreign protocol", "missing profile", "wrong profile", "wrong revision", "override limits", "unknown direction", "zero work", "excess work", "wrong capability", "metered", "paid", "unknown field", "trailing JSON"} {
		t.Run(test, func(t *testing.T) {
			app := cloneApplication(application)
			options := map[string]any{"direction": "glb-to-gltf", "profile_id": "static-glb-v1", "profile_revision": "1", "max_work": 256 << 20}
			offering := schema.Offering{AdapterKey: "local.model", Capability: schema.FormatConversion}
			switch test {
			case "foreign protocol":
				offering.AdapterKey = "another"
			case "missing profile":
				app.Runtime.InputAssets.ModelProfile = nil
			case "wrong profile":
				options["profile_id"] = "other"
			case "wrong revision":
				options["profile_revision"] = "2"
			case "override limits":
				options["profile_limits"] = map[string]any{"max_bytes": 100}
			case "unknown direction":
				options["direction"] = "arbitrary"
			case "zero work":
				options["max_work"] = 0
			case "excess work":
				options["max_work"] = 1 << 33
			case "wrong capability":
				offering.Capability = schema.TextTo3D
			case "metered":
				offering.CreditsMetered = true
			case "paid":
				offering.EstimatedCredits = 1
			case "unknown field":
				options["url"] = "https://invalid.example/"
			}
			raw, _ := json.Marshal(options)
			offering.ProviderOptions = raw
			if test == "trailing JSON" {
				offering.ProviderOptions = append(offering.ProviderOptions, []byte(` {}`)...)
			}
			err := freezeLocalConversionProfile(&offering, app)
			if test != "valid" && test != "foreign protocol" {
				if err == nil {
					t.Fatal("unsafe contract accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test == "foreign protocol" {
				if string(offering.ProviderOptions) != string(raw) {
					t.Fatal("unrelated protocol changed")
				}
				return
			}
			app.Runtime.InputAssets.ModelProfile.Limits.MaxBytes = 1
			var frozen schema.ModelConversionOptions
			if err = json.Unmarshal(offering.ProviderOptions, &frozen); err != nil {
				t.Fatal(err)
			}
			if frozen.ProfileLimits == nil || frozen.ProfileLimits.MaxBytes != 10<<20 || frozen.ProfileLimits.MaxWork != 128<<20 || frozen.MaxWork != 256<<20 {
				t.Fatalf("profile not independently frozen: %+v", frozen)
			}
		})
	}
}
