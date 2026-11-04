package localparts

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestMaxConcurrencyMatchesCatalogBound(t *testing.T) {
	if catalog.MaxLocalModelPartsConcurrency != 8 {
		t.Fatalf("catalog.MaxLocalModelPartsConcurrency = %d, want 8",
			catalog.MaxLocalModelPartsConcurrency)
	}
	offering := loadTestOffering(t, "local/model-parts/split")
	var options map[string]any
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatalf("decode frozen options: %v", err)
	}
	for _, test := range []struct {
		concurrent int
		valid      bool
	}{
		{concurrent: catalog.MaxLocalModelPartsConcurrency, valid: true},
		{concurrent: catalog.MaxLocalModelPartsConcurrency + 1, valid: false},
	} {
		options["max_concurrent"] = test.concurrent
		encoded, err := json.Marshal(options)
		if err != nil {
			t.Fatal(err)
		}
		offering.ProviderOptions = encoded
		_, err = partsContract(offering)
		if (err == nil) != test.valid {
			t.Fatalf("max_concurrent=%d contract error = %v, want valid=%v",
				test.concurrent, err, test.valid)
		}
	}
}
