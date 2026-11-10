package catalog_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #247: the default Catalog declares the purpose-based vision budget, and the
// loader refuses a broken one instead of letting it reach the orchestrator.
func TestDefaultCatalogDeclaresTheVisionBudget(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.Load(bytes.NewReader(source))
	if err != nil {
		t.Fatalf("default catalog: %v", err)
	}
	vision := product.Application().Chat.Context.Vision
	if vision == nil {
		t.Fatal("default catalog must declare context.vision")
	}
	if vision.MaximumImages <= 0 || vision.MaximumTokens <= 0 || vision.MaximumBytes <= 0 ||
		len(vision.Roles) == 0 || !vision.ContactSheet.Enabled || !vision.Crop.Enabled {
		t.Fatalf("vision = %+v", vision)
	}
	sources := map[string]bool{}
	for _, role := range vision.Roles {
		if role.Detail == "" || role.TokenCost <= 0 || role.MaximumImages <= 0 {
			t.Fatalf("role = %+v", role)
		}
		for _, source := range role.Sources {
			sources[source] = true
		}
	}
	for _, want := range []string{"canvas", "turn", "style", "render", "generated", "history"} {
		if !sources[want] {
			t.Fatalf("role source %q missing: %+v", want, vision.Roles)
		}
	}
	if _, err := os.Stat("default_catalog.json"); err != nil {
		t.Fatal(err)
	}
}

func TestVisionBudgetRejectsBrokenDeclarations(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]struct {
		from, to, want string
	}{
		"duplicate source": {
			`"id": "history", "sources": ["history"]`, `"id": "history", "sources": ["generated"]`,
			"source",
		},
		"unknown source": {
			`"id": "history", "sources": ["history"]`, `"id": "history", "sources": ["nonsense"]`,
			"source",
		},
		"unknown detail": {
			`"detail": "low", "token_cost": 85`, `"detail": "medium", "token_cost": 85`,
			"detail",
		},
		"zero token bound": {
			`"maximum_tokens": 12000`, `"maximum_tokens": 0`,
			"maximum_tokens",
		},
		"zero byte bound": {
			`"maximum_bytes": 16777216`, `"maximum_bytes": 0`,
			"maximum_bytes",
		},
		"too many role places": {
			`"maximum_images": 1, "detail": "high", "token_cost": 1105`, `"maximum_images": 0, "detail": "high", "token_cost": 1105`,
			"maximum_images",
		},
		"broken overflow template": {
			`{handles}。需要看画面时`, `需要看画面时`,
			"overflow_template",
		},
		"broken contact label": {
			`"label_template": "[{handle}]"`, `"label_template": "[tile]"`,
			"label_template",
		},
		"broken crop label": {
			`{handle} 的局部裁剪`, `局部裁剪`,
			"picture_label_template",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(string(source), mutate.from) {
				t.Fatalf("fixture text missing: %s", mutate.from)
			}
			changed := bytes.Replace(source, []byte(mutate.from), []byte(mutate.to), 1)
			_, err := catalog.Load(bytes.NewReader(changed))
			if err == nil || !strings.Contains(err.Error(), mutate.want) {
				t.Fatalf("error = %v; want one naming %q", err, mutate.want)
			}
		})
	}
}

// A Catalog without context.vision keeps loading and keeps the legacy keys.
func TestCatalogWithoutVisionBudgetStaysValid(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(source, []byte(`        "vision": {`))
	if start < 0 {
		t.Fatal("vision block missing")
	}
	end := bytes.Index(source[start:], []byte(`        "user_image_label_template"`))
	if end < 0 {
		t.Fatal("vision block end missing")
	}
	changed := append([]byte(nil), source[:start]...)
	changed = append(changed, source[start+end:]...)
	product, err := catalog.Load(bytes.NewReader(changed))
	if err != nil {
		t.Fatalf("legacy catalog: %v", err)
	}
	if product.Application().Chat.Context.Vision != nil {
		t.Fatal("removed vision block still reads as declared")
	}
	if product.Application().Chat.Context.ImageDetail == "" {
		t.Fatal("legacy image_detail must stay declared")
	}
}
