package catalog

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

func TestDeliveryProfileOutputContractsAreExactAndOptIn(t *testing.T) {
	for _, tc := range []struct {
		profile, variant, mime string
		max                    int64
		valid                  bool
	}{
		{schema.DeliveryProfileBinaryFBXV1, "fbx", "model/fbx", 100, true}, {schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", 100, true},
		{"", "legacy", "application/octet-stream", 0, true}, {"unknown", "fbx", "model/fbx", 100, false},
		{schema.DeliveryProfileBinaryFBXV1, "fbx", "application/octet-stream", 100, false}, {schema.DeliveryProfile3MFPackageV1, "zip", "model/3mf", 100, false}, {schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", 0, false},
	} {
		o := schema.Offering{ID: "test", Outputs: []schema.AssetRequirement{{Role: "arbitrary", Variant: tc.variant, MediaTypes: []string{tc.mime}, MaxBytes: tc.max, DeliveryProfile: tc.profile}}}
		if err := validateOfferingOutputs(o); (err == nil) != tc.valid {
			t.Fatalf("contract=%+v valid=%v err=%v", o.Outputs[0], tc.valid, err)
		}
	}
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[schema.OfferingID]string{"meshy/prime/text-to-motion": schema.DeliveryProfileBinaryFBXV1, "meshy/processing/multi-color-print": schema.DeliveryProfile3MFPackageV1} {
		o, _ := c.Get(id)
		if !o.Enabled || len(o.Outputs) != 1 || o.Outputs[0].DeliveryProfile != want {
			t.Fatalf("qualified profile missing: %+v", o.Outputs)
		}
		raw, _ := json.Marshal(o.Outputs[0])
		var restored schema.AssetRequirement
		if err = json.Unmarshal(raw, &restored); err != nil || restored.DeliveryProfile != want {
			t.Fatal("profile not serializable")
		}
	}
}
