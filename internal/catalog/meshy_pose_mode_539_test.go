package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #539: Meshy's pose_mode is "a-pose or t-pose" and to get no pose the request
// leaves it out (https://docs.meshy.ai/en/api/image-to-3d, read 2026-11-24: "To
// get no pose, leave out pose_mode"); an explicit empty string is undocumented and
// the fake refuses it. Every Meshy generation Offering offers "no pose" as the
// empty option, so each must omit the parameter from the wire when it is empty.
// meshy-t2.5 image-to-3d did not, and a default request to it came back 400
// (found by the L5 endpoint script against the fake).
func TestEveryMeshyGenerationOfferingOmitsAnEmptyPoseMode(t *testing.T) {
	product := shippedProduct(t)
	checked := 0
	for _, capability := range []generation.Capability{generation.TextTo3D, generation.ImageTo3D} {
		for _, offering := range product.List(capability) {
			if offering.ProviderID != "meshy" {
				continue
			}
			for _, parameter := range offering.Parameters {
				if parameter.Name != "pose_mode" {
					continue
				}
				checked++
				if !schema.ParameterOmitted(parameter, map[string]any{"pose_mode": ""}) {
					t.Errorf("%s sends an empty pose_mode: declare omit_when pose_mode equals \"\"", offering.ID)
				}
				if schema.ParameterOmitted(parameter, map[string]any{"pose_mode": "t-pose"}) {
					t.Errorf("%s omits a chosen pose_mode", offering.ID)
				}
			}
		}
	}
	if checked < 5 {
		t.Fatalf("checked %d Meshy pose_mode parameters, want at least the five enabled generation Offerings", checked)
	}
}
