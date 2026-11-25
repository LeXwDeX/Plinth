package meshy_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// #581: Meshy's single-image image-to-3d answers 400 ("enable_pbr is only
// supported when should_texture is true") to enable_pbr=true with
// should_texture=false; a real probe on 2026-11-25 confirmed it at no charge.
// The default Catalog forces enable_pbr to false and leaves it off the wire
// once texturing is off, whether the true came from the Offering's default or
// from the caller: the frozen input and the request then say the same thing.
// meshy-7 carries the same declaration but is retired and starts nothing.
func TestMeshy581UntexturedImageTo3DLeavesEnablePBROut(t *testing.T) {
	for _, id := range []generation.OfferingID{
		"meshy/meshy-7.1/image-to-3d", "meshy/meshy-t2.5/image-to-3d",
	} {
		for _, tc := range []struct{ name, input string }{
			{"default", `{"reference_assets":["asset-input"],"should_texture":false}`},
			{"explicit", `{"reference_assets":["asset-input"],"should_texture":false,"enable_pbr":true}`},
		} {
			t.Run(string(id)+"/"+tc.name, func(t *testing.T) {
				upstream := newUpgradeUpstream(t)
				taskCatalog, offering := upgradeOffering(t, id, upstream.server)
				_, input, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(tc.input))
				if err != nil {
					t.Fatal(err)
				}
				var frozen map[string]any
				if err := json.Unmarshal(input, &frozen); err != nil {
					t.Fatal(err)
				}
				if frozen["should_texture"] != false || frozen["enable_pbr"] != false {
					t.Fatalf("frozen input = %s, want should_texture=false and enable_pbr=false", input)
				}
				adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: upstream.server.Client(),
					InputAssets: memoryInputAssets{content: []byte("\x89PNG\r\n\x1a\nreference")}})
				if _, err := adapter.Start(context.Background(), generation.StartRequest{
					Offering: offering, Input: input,
					InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-input", MediaType: "image/png"}}},
				}); err != nil {
					t.Fatal(err)
				}
				body := upstream.bodies("/openapi/v1/image-to-3d")[0]
				if body["should_texture"] != false {
					t.Fatalf("image to 3d body = %#v, want should_texture=false", body)
				}
				assertAbsent(t, "untextured image to 3D", body, "enable_pbr")
			})
		}
	}
}

// Texturing on keeps PBR as the Offering declares it.
func TestMeshy581TexturedImageTo3DKeepsEnablePBR(t *testing.T) {
	for _, id := range []generation.OfferingID{
		"meshy/meshy-7.1/image-to-3d", "meshy/meshy-t2.5/image-to-3d",
	} {
		t.Run(string(id), func(t *testing.T) {
			upstream := newUpgradeUpstream(t)
			taskCatalog, offering := upgradeOffering(t, id, upstream.server)
			_, input, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"reference_assets":["asset-input"]}`))
			if err != nil {
				t.Fatal(err)
			}
			adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: upstream.server.Client(),
				InputAssets: memoryInputAssets{content: []byte("\x89PNG\r\n\x1a\nreference")}})
			if _, err := adapter.Start(context.Background(), generation.StartRequest{
				Offering: offering, Input: input,
				InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-input", MediaType: "image/png"}}},
			}); err != nil {
				t.Fatal(err)
			}
			body := upstream.bodies("/openapi/v1/image-to-3d")[0]
			if body["should_texture"] != true || body["enable_pbr"] != true {
				t.Fatalf("image to 3d body = %#v, want should_texture=true and enable_pbr=true", body)
			}
		})
	}
}
