package catalog_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestCatalogValidatesFidelityCheckerContract(t *testing.T) {
	for _, tc := range []struct {
		level, media string
		valid        bool
	}{
		{"skeleton", `["model/gltf-binary"]`, true},
		{"animation", `["model/gltf+json"]`, true},
		{"animation", `["model/gltf+json","model/gltf-binary"]`, true},
		{"unknown", `["model/gltf-binary"]`, false},
		{"skeleton", `[]`, false},
		{"skeleton", `["application/octet-stream"]`, false},
		{"animation", `["model/gltf-binary","application/octet-stream"]`, false},
	} {
		t.Run(tc.level+tc.media, func(t *testing.T) {
			outputs := fmt.Sprintf(`[{"role":"model","variant":"glb","delivery":"required","minimum":1,"fidelity":%q,"media_types":%s}]`, tc.level, tc.media)
			c, err := loadResourceContractCatalog(t, outputs)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				o, _ := c.Get("one")
				if o.Outputs[0].Fidelity != tc.level {
					t.Fatal("fidelity lost in catalog")
				}
			} else if err == nil || !strings.Contains(err.Error(), "fidelity") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCatalogFBXFidelityRequiresClosedBinaryProfile(t *testing.T) {
	for _, tc := range []struct {
		variant, media, profile string
		valid                   bool
	}{
		{"fbx", "model/fbx", "binary-fbx-v1", true},
		{"fbx", "model/fbx", "", false},
		{"fbx", "application/octet-stream", "binary-fbx-v1", false},
		{"converted.fbx", "model/fbx", "binary-fbx-v1", false},
	} {
		outputs := fmt.Sprintf(`[{"role":"model","variant":%q,"delivery":"required","minimum":1,"fidelity":"skeleton","media_types":[%q],"delivery_profile":%q,"max_bytes":1073741824}]`, tc.variant, tc.media, tc.profile)
		_, err := loadResourceContractCatalog(t, outputs)
		if (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}
