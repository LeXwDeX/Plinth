package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestJimengOfferingsDeclareUpstreamAssetHosts(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const want = "ark-content-generation-v2-cn-beijing.tos-cn-beijing.volces.com"
	for _, capability := range []string{"text-to-image", "image-to-image"} {
		o, ok := c.Get(generation.OfferingID("jimeng/seedream-4-5/" + capability))
		if !ok {
			t.Fatal(capability)
		}
		found := false
		for _, host := range o.AssetHosts {
			if host == want {
				found = true
			}
		}
		if !found {
			t.Fatalf(
				"%s asset_hosts missing %s: %v",
				capability,
				want,
				o.AssetHosts,
			)
		}
	}
}
