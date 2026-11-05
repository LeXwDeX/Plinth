package catalog

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestLocalModelPartsConcurrencyBound(t *testing.T) {
	if MaxLocalModelPartsConcurrency != 8 {
		t.Fatalf("MaxLocalModelPartsConcurrency = %d, want 8", MaxLocalModelPartsConcurrency)
	}
	application := ApplicationSchema{Runtime: RuntimeSchema{
		ProviderConnections: []ProviderConnectionSchema{{Protocol: "local-model-parts", AdapterKey: "local.parts"}},
		InputAssets: InputAssetRuntimeSchema{GeneratedModelProfile: &GeneratedModelProfile{
			ID: "generated-glb-v1", Revision: "1", Limits: ModelInputLimits{MaxBytes: 128 << 20, MaxWork: 1 << 31},
		}},
	}}
	for _, test := range []struct {
		concurrent int
		valid      bool
	}{
		{concurrent: MaxLocalModelPartsConcurrency, valid: true},
		{concurrent: MaxLocalModelPartsConcurrency + 1, valid: false},
	} {
		raw, _ := json.Marshal(map[string]any{
			"profile_id": "generated-glb-v1", "profile_revision": "1",
			"max_work": 1 << 31, "max_concurrent": test.concurrent,
		})
		offering := schema.Offering{AdapterKey: "local.parts", Capability: schema.ModelSplit, ProviderOptions: raw}
		err := freezeLocalPartsProfile(&offering, application)
		if (err == nil) != test.valid {
			t.Fatalf("max_concurrent=%d error = %v, want valid=%v", test.concurrent, err, test.valid)
		}
	}
}

func TestLocalModelPartsFreezesTheGeneratedProfile(t *testing.T) {
	application := ApplicationSchema{Runtime: RuntimeSchema{
		ProviderConnections: []ProviderConnectionSchema{{Protocol: "local-model-parts", AdapterKey: "local.parts"}},
		InputAssets: InputAssetRuntimeSchema{GeneratedModelProfile: &GeneratedModelProfile{
			ID: "generated-glb-v1", Revision: "1", Limits: ModelInputLimits{MaxBytes: 128 << 20, MaxWork: 1 << 31},
		}},
	}}
	for _, test := range []string{
		"valid", "merge valid", "foreign protocol", "missing profile", "wrong profile", "wrong revision",
		"override limits", "wrong capability", "metered", "paid", "zero work", "excess work",
		"zero concurrency", "excess concurrency", "unknown field", "trailing JSON",
	} {
		t.Run(test, func(t *testing.T) {
			app := cloneApplication(application)
			options := map[string]any{"profile_id": "generated-glb-v1", "profile_revision": "1", "max_work": 1 << 31, "max_concurrent": 1}
			offering := schema.Offering{AdapterKey: "local.parts", Capability: schema.ModelSplit}
			switch test {
			case "merge valid":
				offering.Capability = schema.ModelMerge
			case "foreign protocol":
				offering.AdapterKey = "another"
			case "missing profile":
				app.Runtime.InputAssets.GeneratedModelProfile = nil
			case "wrong profile":
				options["profile_id"] = "static-glb-v1"
			case "wrong revision":
				options["profile_revision"] = "2"
			case "override limits":
				options["profile_limits"] = map[string]any{"max_bytes": 100}
			case "wrong capability":
				offering.Capability = schema.TextTo3D
			case "metered":
				offering.CreditsMetered = true
			case "paid":
				offering.EstimatedCredits = 1
			case "zero work":
				options["max_work"] = 0
			case "excess work":
				options["max_work"] = 1 << 33
			case "zero concurrency":
				options["max_concurrent"] = 0
			case "excess concurrency":
				options["max_concurrent"] = 17
			case "unknown field":
				options["url"] = "https://invalid.example/"
			}
			raw, _ := json.Marshal(options)
			offering.ProviderOptions = raw
			if test == "trailing JSON" {
				offering.ProviderOptions = append(offering.ProviderOptions, []byte(` {}`)...)
			}
			err := freezeLocalPartsProfile(&offering, app)
			if test != "valid" && test != "merge valid" && test != "foreign protocol" {
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
			app.Runtime.InputAssets.GeneratedModelProfile.Limits.MaxBytes = 1
			var frozen schema.ModelPartsOptions
			if err = json.Unmarshal(offering.ProviderOptions, &frozen); err != nil {
				t.Fatal(err)
			}
			if frozen.ProfileLimits == nil || frozen.ProfileLimits.MaxBytes != 128<<20 ||
				frozen.ProfileLimits.MaxWork != 1<<31 || frozen.MaxWork != 1<<31 || frozen.MaxConcurrent != 1 {
				t.Fatalf("profile not independently frozen: %+v", frozen)
			}
		})
	}
}
